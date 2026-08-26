package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/store"
)

var (
	errNotFound = errors.New("not found")
	errInvalid  = errors.New("invalid input")
	errConflict = errors.New("version conflict")
)

type server struct {
	store               *store.SQLiteStore
	uploadPath          string
	objectStore         objectstore.Store
	staticPath          string
	authDisabled        bool
	mailer              *smtpMailer
	authLimiter         authRateLimitBackend
	realtime            *realtimeHub
	coordinator         *coordination.Redis
	coordinationStarted atomic.Bool
	externalAuth        *externalAuth
	agent               appconfig.AgentConfig
	agentClient         *http.Client
	allowedOrigin       string
	mcpUploadMu         sync.Mutex
	mcpUploads          map[string]*mcpPendingUpload
}

func main() {
	applicationConfig, err := appconfig.Load()
	if err != nil {
		log.Fatal(err)
	}
	repository, err := store.OpenDatabase(applicationConfig.Database)
	if err != nil {
		log.Fatal(err)
	}
	defer repository.Close()
	objects, err := objectstore.Open(context.Background(), applicationConfig.Storage)
	if err != nil {
		log.Fatal(err)
	}
	redisCoordinator, err := coordination.Open(context.Background(), applicationConfig.Redis)
	if err != nil {
		log.Fatal(err)
	}
	if redisCoordinator != nil {
		defer redisCoordinator.Close()
		repository.SetWorkspaceCoordinator(redisCoordinator)
	}
	external, err := newExternalAuth(context.Background(), applicationConfig.Auth, applicationConfig.AppURL)
	if err != nil {
		log.Fatal(err)
	}
	shutdownTelemetry, err := configureTelemetry(context.Background(), applicationConfig.Telemetry)
	if err != nil {
		log.Fatal(err)
	}
	defer shutdownTelemetry(context.Background())
	s := &server{
		store:         repository,
		uploadPath:    applicationConfig.Storage.LocalPath,
		objectStore:   objects,
		staticPath:    applicationConfig.StaticPath,
		authDisabled:  applicationConfig.AuthDisabled,
		coordinator:   redisCoordinator,
		mailer:        smtpMailerFromEnv(),
		externalAuth:  external,
		agent:         applicationConfig.Agent,
		agentClient:   &http.Client{Timeout: applicationConfig.Agent.Timeout},
		allowedOrigin: applicationConfig.AppURL,
	}

	httpServer := &http.Server{Addr: applicationConfig.HTTPAddr, Handler: telemetryHandler(newHandler(s), applicationConfig.Telemetry.Enabled), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("Flow API listening on %s using database=%s storage=%s redis=%s", httpServer.Addr, applicationConfig.Database.Driver, applicationConfig.Storage.Driver, applicationConfig.Redis.Mode)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newHandler(s *server) http.Handler {
	if s.authLimiter == nil {
		if s.coordinator != nil {
			s.authLimiter = s.coordinator
		} else {
			s.authLimiter = newAuthRateLimiter()
		}
	}
	if s.realtime == nil {
		s.realtime = newRealtimeHub()
	}
	s.store.SetRealtimeSink(s.publishRealtime)
	s.startCoordination()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		response := map[string]string{"status": "ok", "redis": "disabled"}
		if s.coordinator != nil {
			response["redis"] = s.coordinator.Mode()
			if err := s.coordinator.Ping(r.Context()); err != nil {
				response["status"] = "degraded"
				writeJSON(w, http.StatusServiceUnavailable, response)
				return
			}
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.oauthProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.oauthProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp/readonly", s.oauthProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.oauthAuthorizationServer)
	mux.HandleFunc("POST /oauth/register", s.registerOAuthClient)
	mux.HandleFunc("POST /oauth/token", s.exchangeMCPToken)
	mux.HandleFunc("POST /oauth/revoke", s.revokeOAuthToken)
	mux.HandleFunc("POST /mcp", s.mcpHTTP(false))
	mux.HandleFunc("POST /mcp/readonly", s.mcpHTTP(true))
	mux.HandleFunc("PUT /api/mcp/uploads/{token}", s.putMCPUpload)
	mux.HandleFunc("GET /api/oauth/authorization-request", s.getOAuthAuthorizationRequest)
	mux.HandleFunc("POST /api/oauth/authorization-request", s.decideOAuthAuthorization)
	mux.HandleFunc("DELETE /api/oauth/authorizations/{id}", s.revokeOAuthAuthorization)
	mux.Handle("POST /api/auth/register", s.requireEmailAuth(s.limitAuth("register", 5, 15*time.Minute, http.HandlerFunc(s.register))))
	mux.Handle("POST /api/auth/verify-email", s.requireEmailAuth(s.limitAuth("verify-email", 10, 15*time.Minute, http.HandlerFunc(s.verifyEmail))))
	mux.Handle("POST /api/auth/resend-verification", s.requireEmailAuth(s.limitAuth("resend-verification", 5, 15*time.Minute, http.HandlerFunc(s.resendVerification))))
	mux.Handle("POST /api/auth/login", s.requireEmailAuth(s.limitAuth("login", 8, 15*time.Minute, http.HandlerFunc(s.login))))
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/session", s.authSession)
	mux.Handle("POST /api/auth/forgot-password", s.requireEmailAuth(s.limitAuth("forgot-password", 5, 15*time.Minute, http.HandlerFunc(s.forgotPassword))))
	mux.Handle("POST /api/auth/reset-password", s.requireEmailAuth(s.limitAuth("reset-password", 8, 15*time.Minute, http.HandlerFunc(s.resetPassword))))
	mux.HandleFunc("GET /api/auth/providers", s.authProviders)
	mux.HandleFunc("GET /api/auth/{provider}/start", s.startOIDC)
	mux.HandleFunc("GET /api/auth/{provider}/callback", s.finishOIDC)
	mux.HandleFunc("GET /api/auth/saml/start", s.startSAML)
	if s.externalAuth != nil && s.externalAuth.saml != nil {
		mux.Handle("GET /api/auth/saml/metadata", s.externalAuth.saml)
		mux.Handle("POST /api/auth/saml/acs", s.externalAuth.saml)
		mux.Handle("GET /api/auth/saml/complete", s.externalAuth.saml.RequireAccount(http.HandlerFunc(s.finishSAML)))
	}
	mux.HandleFunc("POST /api/invitations/accept", s.acceptInvitation)
	mux.HandleFunc("GET /api/invitations/preview/{token}", s.invitationPreview)
	mux.HandleFunc("GET /api/account/bootstrap", s.accountBootstrap)
	mux.HandleFunc("PUT /api/account/last-workspace", s.setLastWorkspace)
	mux.HandleFunc("GET /api/account/settings", s.getUserSettings)
	mux.HandleFunc("PATCH /api/account/settings", s.updateUserSettings)
	mux.HandleFunc("PATCH /api/account/profile", s.updateAccountProfile)
	mux.HandleFunc("GET /api/account/sessions", s.listAccountSessions)
	mux.HandleFunc("DELETE /api/account/sessions/others", s.revokeOtherSessions)
	mux.HandleFunc("POST /api/account/change-password", s.changeAccountPassword)
	mux.HandleFunc("GET /api/realtime/events", s.realtimeEvents)
	mux.HandleFunc("POST /api/realtime/presence", s.updatePresence)
	mux.HandleFunc("GET /api/search", s.searchWorkspace)
	mux.HandleFunc("DELETE /api/search/history", s.clearSearchHistory)
	mux.HandleFunc("POST /api/recent", s.recordRecentResource)
	mux.HandleFunc("GET /api/agent/status", s.agentStatus)
	mux.HandleFunc("POST /api/agent/chat", s.agentChat)
	mux.HandleFunc("GET /api/agent/sessions", s.listAgentSessions)
	mux.HandleFunc("POST /api/agent/sessions", s.createAgentSession)
	mux.HandleFunc("GET /api/agent/sessions/{id}", s.getAgentSession)
	mux.HandleFunc("PATCH /api/agent/sessions/{id}", s.updateAgentSession)
	mux.HandleFunc("DELETE /api/agent/sessions/{id}", s.deleteAgentSession)
	mux.HandleFunc("POST /api/agent/sessions/{id}/messages", s.createAgentSessionMessage)
	mux.HandleFunc("PATCH /api/agent/sessions/{id}/messages/{messageId}", s.updateAgentSessionMessage)
	mux.HandleFunc("GET /api/agent/skills", s.listAgentSkillsHTTP)
	mux.HandleFunc("POST /api/agent/skills", s.createAgentSkill)
	mux.HandleFunc("PATCH /api/agent/skills/{id}", s.updateAgentSkill)
	mux.HandleFunc("DELETE /api/agent/skills/{id}", s.deleteAgentSkill)
	mux.HandleFunc("POST /api/workspaces", s.createWorkspace)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceKey}", s.updateWorkspace)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceKey}", s.deleteWorkspace)
	mux.HandleFunc("POST /api/workspaces/{workspaceKey}/teams", s.createTeam)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceKey}/teams/{teamId}", s.updateTeam)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceKey}/teams/{teamId}", s.deleteTeam)
	mux.HandleFunc("POST /api/workspaces/{workspaceKey}/invitations", s.createInvitation)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceKey}/invitations/{invitationId}", s.revokeInvitation)
	mux.HandleFunc("POST /api/workspaces/{workspaceKey}/invitations/{invitationId}/resend", s.resendInvitation)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceKey}/members/{userId}", s.updateMemberRole)
	mux.HandleFunc("POST /api/workspaces/{workspaceKey}/members/{userId}/suspend", s.suspendMember)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceKey}/members/{userId}", s.removeMember)
	mux.HandleFunc("PUT /api/workspaces/{workspaceKey}/teams/{teamId}/members/{userId}", s.updateTeamMember)
	mux.HandleFunc("POST /api/customers", s.createCustomer)
	mux.HandleFunc("PATCH /api/customers/{id}", s.updateCustomer)
	mux.HandleFunc("DELETE /api/customers/{id}", s.deleteCustomer)
	mux.HandleFunc("POST /api/customer-requests", s.createCustomerRequest)
	mux.HandleFunc("PATCH /api/customer-requests/{id}", s.updateCustomerRequest)
	mux.HandleFunc("DELETE /api/customer-requests/{id}", s.deleteCustomerRequest)
	mux.HandleFunc("POST /api/customer-requests/{id}/attachments", s.createCustomerRequestAttachment)
	mux.HandleFunc("DELETE /api/customer-requests/{id}/attachments/{attachmentId}", s.deleteCustomerRequestAttachment)
	mux.HandleFunc("POST /api/documents", s.createDocument)
	mux.HandleFunc("PATCH /api/documents/{id}", s.updateDocument)
	mux.HandleFunc("DELETE /api/documents/{id}", s.deleteDocument)
	mux.HandleFunc("POST /api/documents/{id}/restore/{revisionId}", s.restoreDocumentRevision)
	mux.HandleFunc("POST /api/releases", s.createRelease)
	mux.HandleFunc("GET /api/releases", s.listReleases)
	mux.HandleFunc("GET /api/releases/{id}", s.getRelease)
	mux.HandleFunc("POST /api/releases/reorder", s.reorderReleases)
	mux.HandleFunc("PATCH /api/releases/{id}", s.updateRelease)
	mux.HandleFunc("DELETE /api/releases/{id}", s.deleteRelease)
	mux.HandleFunc("POST /api/release-pipelines", s.createReleasePipeline)
	mux.HandleFunc("GET /api/release-pipelines", s.listReleasePipelines)
	mux.HandleFunc("GET /api/release-pipelines/{id}", s.getReleasePipeline)
	mux.HandleFunc("POST /api/release-pipelines/reorder", s.reorderReleasePipelines)
	mux.HandleFunc("POST /api/release-pipelines/{id}/access-key", s.rotateReleasePipelineAccessKey)
	mux.HandleFunc("PATCH /api/release-pipelines/{id}", s.updateReleasePipeline)
	mux.HandleFunc("DELETE /api/release-pipelines/{id}", s.deleteReleasePipeline)
	mux.HandleFunc("POST /api/custom-emojis", s.createCustomEmoji)
	mux.HandleFunc("PATCH /api/custom-emojis/{id}", s.updateCustomEmoji)
	mux.HandleFunc("POST /api/asks", s.createAsk)
	mux.HandleFunc("PATCH /api/asks/{id}", s.updateAsk)
	mux.HandleFunc("POST /api/asks/{id}/decision", s.decideAsk)
	mux.HandleFunc("DELETE /api/asks/{id}", s.deleteAsk)
	mux.HandleFunc("GET /api/project-templates", s.listProjectTemplates)
	mux.HandleFunc("POST /api/project-templates", s.createProjectTemplate)
	mux.HandleFunc("PATCH /api/project-templates/{id}", s.updateProjectTemplate)
	mux.HandleFunc("DELETE /api/project-templates/{id}", s.deleteProjectTemplate)
	mux.HandleFunc("GET /api/issue-templates", s.listWorkspaceIssueTemplates)
	mux.HandleFunc("POST /api/issue-templates", s.createWorkspaceIssueTemplate)
	mux.HandleFunc("PATCH /api/issue-templates/{id}", s.updateWorkspaceIssueTemplate)
	mux.HandleFunc("DELETE /api/issue-templates/{id}", s.deleteWorkspaceIssueTemplate)
	mux.HandleFunc("POST /api/document-templates", s.createDocumentTemplate)
	mux.HandleFunc("PATCH /api/document-templates/{id}", s.updateDocumentTemplate)
	mux.HandleFunc("DELETE /api/document-templates/{id}", s.deleteDocumentTemplate)
	mux.HandleFunc("GET /api/labels", s.listWorkspaceLabels)
	mux.HandleFunc("POST /api/labels", s.createWorkspaceLabel)
	mux.HandleFunc("PATCH /api/labels/{id}", s.updateWorkspaceLabel)
	mux.HandleFunc("POST /api/labels/{id}/move-to-teams", s.moveWorkspaceLabelToTeams)
	mux.HandleFunc("DELETE /api/labels/{id}", s.deleteWorkspaceLabel)
	mux.HandleFunc("POST /api/label-groups", s.createLabelGroup)
	mux.HandleFunc("PATCH /api/label-groups/{id}", s.updateLabelGroup)
	mux.HandleFunc("DELETE /api/label-groups/{id}", s.deleteLabelGroup)
	mux.HandleFunc("POST /api/project-statuses", s.createProjectStatus)
	mux.HandleFunc("PATCH /api/project-statuses/{id}", s.updateProjectStatus)
	mux.HandleFunc("DELETE /api/project-statuses/{id}", s.deleteProjectStatus)
	mux.HandleFunc("POST /api/project-statuses/reorder", s.reorderProjectStatuses)
	mux.HandleFunc("GET /api/workspace/preferences", s.getWorkspacePreferences)
	mux.HandleFunc("PATCH /api/workspace/preferences", s.updateWorkspacePreferences)
	mux.HandleFunc("GET /api/api-keys", s.listAPIKeys)
	mux.HandleFunc("POST /api/api-keys", s.createAPIKey)
	mux.HandleFunc("DELETE /api/api-keys/{id}", s.revokeAPIKey)
	mux.HandleFunc("GET /api/oauth-applications", s.listOAuthApplications)
	mux.HandleFunc("POST /api/oauth-applications", s.createOAuthApplication)
	mux.HandleFunc("PATCH /api/oauth-applications/{id}", s.updateOAuthApplication)
	mux.HandleFunc("DELETE /api/oauth-applications/{id}", s.deleteOAuthApplication)
	mux.HandleFunc("GET /api/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /api/webhooks", s.createWebhook)
	mux.HandleFunc("PATCH /api/webhooks/{id}", s.updateWebhook)
	mux.HandleFunc("DELETE /api/webhooks/{id}", s.deleteWebhook)
	mux.HandleFunc("POST /api/oauth/token", s.exchangeOAuthToken)
	mux.HandleFunc("GET /api/integrations", s.listIntegrations)
	mux.HandleFunc("PUT /api/integrations/{provider}", s.connectIntegration)
	mux.HandleFunc("DELETE /api/integrations/{provider}", s.disconnectIntegration)
	mux.HandleFunc("PATCH /api/integrations/{provider}/{id}", s.updateIntegration)
	mux.HandleFunc("DELETE /api/integrations/{provider}/{id}", s.disconnectIntegrationConnection)
	mux.HandleFunc("GET /api/reviews", s.listReviews)
	mux.HandleFunc("GET /api/reviews/{id}", s.getReview)
	mux.HandleFunc("PATCH /api/reviews/{id}", s.updateReview)
	mux.HandleFunc("POST /api/reviews/{id}/submit", s.submitReview)
	mux.HandleFunc("POST /api/reviews/{id}/comments", s.commentOnReview)
	mux.HandleFunc("GET /api/usage", s.getWorkspaceUsage)
	mux.HandleFunc("POST /api/sla-rules", s.createSLARule)
	mux.HandleFunc("PATCH /api/sla-rules/{id}", s.updateSLARule)
	mux.HandleFunc("DELETE /api/sla-rules/{id}", s.deleteSLARule)
	mux.HandleFunc("PUT /api/sla-settings", s.updateSLASettings)
	mux.HandleFunc("PUT /api/project-update-settings", s.updateProjectUpdateSettings)
	mux.HandleFunc("POST /api/drafts", s.createDraft)
	mux.HandleFunc("DELETE /api/drafts", s.deleteAllDrafts)
	mux.HandleFunc("PATCH /api/drafts/{id}", s.updateDraft)
	mux.HandleFunc("DELETE /api/drafts/{id}", s.deleteDraft)
	mux.HandleFunc("PUT /api/favorites/{type}/{id}", s.addFavorite)
	mux.HandleFunc("DELETE /api/favorites/{type}/{id}", s.removeFavorite)
	mux.HandleFunc("PUT /api/subscriptions/{type}/{id}", s.addSubscription)
	mux.HandleFunc("DELETE /api/subscriptions/{type}/{id}", s.removeSubscription)
	mux.HandleFunc("POST /api/trash/{id}/restore", s.restoreTrashEntry)
	mux.HandleFunc("DELETE /api/trash/{id}", s.purgeTrashEntry)
	mux.HandleFunc("POST /api/imports/preview", s.previewImport)
	mux.HandleFunc("POST /api/imports/{id}/commit", s.commitImport)
	mux.HandleFunc("POST /api/exports", s.createExport)
	mux.HandleFunc("GET /api/exports/{id}/download", s.downloadExport)
	mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	mux.HandleFunc("PUT /api/workspace/project-display-default", s.updateProjectDisplayDefault)
	mux.HandleFunc("PUT /api/workspace/settings", s.updateWorkspaceSettings)
	mux.HandleFunc("GET /api/notifications", s.listNotifications)
	mux.HandleFunc("PATCH /api/notifications/{id}", s.updateNotification)
	mux.HandleFunc("POST /api/notifications/batch", s.batchNotifications)
	mux.HandleFunc("GET /api/notification-preferences", s.getNotificationPreferences)
	mux.HandleFunc("PATCH /api/notification-preferences", s.updateNotificationPreferences)
	mux.HandleFunc("GET /api/notification-deliveries", s.listNotificationDeliveries)
	mux.HandleFunc("POST /api/notification-deliveries/{id}/retry", s.retryNotificationDelivery)
	mux.HandleFunc("POST /api/desktop-notifications/ack", s.acknowledgeDesktopNotifications)
	mux.HandleFunc("POST /api/views", s.createSavedView)
	mux.HandleFunc("PATCH /api/views/{id}", s.updateSavedView)
	mux.HandleFunc("DELETE /api/views/{id}", s.deleteSavedView)
	mux.HandleFunc("POST /api/issues", s.createIssue)
	mux.HandleFunc("PATCH /api/teams/{id}/cycle-settings", s.updateCycleSettings)
	mux.HandleFunc("GET /api/teams/{id}/states", s.listWorkflowStates)
	mux.HandleFunc("POST /api/teams/{id}/states", s.createWorkflowState)
	mux.HandleFunc("PATCH /api/teams/{id}/states/{stateId}", s.updateWorkflowState)
	mux.HandleFunc("DELETE /api/teams/{id}/states/{stateId}", s.deleteWorkflowState)
	mux.HandleFunc("POST /api/teams/{id}/states/reorder", s.reorderWorkflowStates)
	mux.HandleFunc("GET /api/teams/{id}/settings", s.getTeamSettings)
	mux.HandleFunc("PATCH /api/teams/{id}/settings", s.updateStructuredTeamSettings)
	mux.HandleFunc("GET /api/teams/{id}/templates", s.listIssueTemplates)
	mux.HandleFunc("POST /api/teams/{id}/templates", s.createIssueTemplate)
	mux.HandleFunc("PATCH /api/teams/{id}/templates/{templateId}", s.updateIssueTemplate)
	mux.HandleFunc("DELETE /api/teams/{id}/templates/{templateId}", s.deleteIssueTemplate)
	mux.HandleFunc("GET /api/teams/{id}/labels", s.listTeamLabels)
	mux.HandleFunc("POST /api/teams/{id}/labels", s.createTeamLabel)
	mux.HandleFunc("PATCH /api/teams/{id}/labels/{labelId}", s.updateTeamLabel)
	mux.HandleFunc("DELETE /api/teams/{id}/labels/{labelId}", s.deleteTeamLabel)
	mux.HandleFunc("PATCH /api/cycles/{id}", s.updateCycle)
	mux.HandleFunc("POST /api/cycles/{id}/start", s.startCycle)
	mux.HandleFunc("POST /api/cycles/{id}/complete", s.completeCycle)
	mux.HandleFunc("POST /api/cycles/{id}/resources", s.createCycleResource)
	mux.HandleFunc("DELETE /api/cycles/{id}/resources/{resourceId}", s.deleteCycleResource)
	mux.HandleFunc("POST /api/cycles/{id}/calendar-token", s.cycleCalendarToken)
	mux.HandleFunc("GET /api/calendar/cycles/{id}", s.cycleCalendar)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.updateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)
	mux.HandleFunc("POST /api/projects/{id}/reminders", s.createProjectReminder)
	mux.HandleFunc("POST /api/projects/{id}/resources", s.createProjectResource)
	mux.HandleFunc("PATCH /api/projects/{id}/resources/{resourceId}", s.updateProjectResource)
	mux.HandleFunc("DELETE /api/projects/{id}/resources/{resourceId}", s.deleteProjectResource)
	mux.HandleFunc("POST /api/projects/{id}/milestones", s.createProjectMilestone)
	mux.HandleFunc("POST /api/projects/{id}/milestones/reorder", s.reorderProjectMilestones)
	mux.HandleFunc("PATCH /api/projects/{id}/milestones/{milestoneId}", s.updateProjectMilestone)
	mux.HandleFunc("DELETE /api/projects/{id}/milestones/{milestoneId}", s.deleteProjectMilestone)
	mux.HandleFunc("POST /api/projects/{id}/comments", s.createProjectComment)
	mux.HandleFunc("POST /api/projects/{id}/updates", s.createProjectUpdate)
	mux.HandleFunc("PATCH /api/projects/{id}/updates/{updateId}", s.updateProjectUpdate)
	mux.HandleFunc("DELETE /api/projects/{id}/updates/{updateId}", s.deleteProjectUpdate)
	mux.HandleFunc("POST /api/projects/{id}/updates/{updateId}/comments", s.createProjectUpdateComment)
	mux.HandleFunc("POST /api/projects/{id}/updates/{updateId}/reactions", s.toggleProjectUpdateReaction)
	mux.HandleFunc("POST /api/initiatives", s.createInitiative)
	mux.HandleFunc("PATCH /api/initiatives/{id}", s.updateInitiative)
	mux.HandleFunc("DELETE /api/initiatives/{id}", s.deleteInitiative)
	mux.HandleFunc("POST /api/initiatives/{id}/reminders", s.createInitiativeReminder)
	mux.HandleFunc("POST /api/initiatives/{id}/resources", s.createInitiativeResource)
	mux.HandleFunc("PATCH /api/initiatives/{id}/resources/{resourceId}", s.updateInitiativeResource)
	mux.HandleFunc("DELETE /api/initiatives/{id}/resources/{resourceId}", s.deleteInitiativeResource)
	mux.HandleFunc("POST /api/initiatives/{id}/comments", s.createInitiativeComment)
	mux.HandleFunc("PATCH /api/initiatives/{id}/comments/{commentId}", s.updateInitiativeComment)
	mux.HandleFunc("DELETE /api/initiatives/{id}/comments/{commentId}", s.deleteInitiativeComment)
	mux.HandleFunc("POST /api/initiatives/{id}/comments/{commentId}/reactions", s.toggleInitiativeCommentReaction)
	mux.HandleFunc("POST /api/initiatives/{id}/updates", s.createInitiativeUpdate)
	mux.HandleFunc("PATCH /api/initiatives/{id}/updates/{updateId}", s.updateInitiativeUpdate)
	mux.HandleFunc("DELETE /api/initiatives/{id}/updates/{updateId}", s.deleteInitiativeUpdate)
	mux.HandleFunc("POST /api/initiatives/{id}/updates/{updateId}/comments", s.createInitiativeUpdateComment)
	mux.HandleFunc("POST /api/initiatives/{id}/updates/{updateId}/reactions", s.toggleInitiativeUpdateReaction)
	mux.HandleFunc("PATCH /api/issues/{id}", s.updateIssue)
	mux.HandleFunc("PUT /api/issues/{id}/releases", s.setIssueReleases)
	mux.HandleFunc("DELETE /api/issues/{id}", s.deleteIssue)
	mux.HandleFunc("POST /api/issues/{id}/reactions", s.toggleIssueReaction)
	mux.HandleFunc("POST /api/issues/batch", s.batchUpdate)
	mux.HandleFunc("POST /api/issues/{id}/comments", s.createComment)
	mux.HandleFunc("PATCH /api/issues/{id}/comments/{commentId}", s.updateComment)
	mux.HandleFunc("DELETE /api/issues/{id}/comments/{commentId}", s.deleteComment)
	mux.HandleFunc("POST /api/issues/{id}/comments/{commentId}/reactions", s.toggleCommentReaction)
	mux.HandleFunc("POST /api/issues/{id}/relations", s.createRelation)
	mux.HandleFunc("DELETE /api/issues/{id}/relations/{relationId}", s.deleteRelation)
	mux.HandleFunc("POST /api/issues/{id}/attachments", s.createAttachment)
	mux.HandleFunc("POST /api/issues/{id}/links", s.createIssueLink)
	mux.HandleFunc("POST /api/issues/{id}/reminders", s.createIssueReminder)
	mux.HandleFunc("POST /api/issues/{id}/loop-runs", s.createIssueLoopRun)
	mux.HandleFunc("DELETE /api/issues/{id}/attachments/{attachmentId}", s.deleteAttachment)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /uploads/{name}", s.serveUpload)

	handler := s.withStaticFiles(s.authenticate(mux))
	return requestLog(s.cors(handler))
}

func (s *server) withStaticFiles(next http.Handler) http.Handler {
	if s.staticPath == "" {
		return next
	}
	files := http.FileServer(http.Dir(s.staticPath))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/uploads/") || strings.HasPrefix(r.URL.Path, "/.well-known/oauth-") || r.URL.Path == "/mcp" || r.URL.Path == "/mcp/readonly" || r.URL.Path == "/oauth/register" || r.URL.Path == "/oauth/token" || r.URL.Path == "/oauth/revoke" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		path := strings.TrimPrefix(filepath.Clean("/"+strings.TrimPrefix(r.URL.Path, "/")), string(filepath.Separator))
		if path == "" {
			http.ServeFile(w, r, filepath.Join(s.staticPath, "index.html"))
			return
		}
		if _, err := os.Stat(filepath.Join(s.staticPath, path)); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.staticPath, "index.html"))
	})
}

func (s *server) accountBootstrap(w http.ResponseWriter, r *http.Request) {
	if s.authDisabled {
		writeJSON(w, http.StatusOK, s.store.Account())
		return
	}
	account, err := s.store.AccountForUser(r.Context(), authUser(r).ID)
	respondMutation(w, err, http.StatusOK, account)
}

func (s *server) bootstrap(w http.ResponseWriter, r *http.Request) {
	s.maintainCycleSchedule(r.Context(), workspaceKey(r))
	s.maintainAdvancedSchedules(r.Context(), workspaceKey(r))
	if !s.authDisabled {
		data, ok, err := s.store.BootstrapForUser(r.Context(), workspaceKey(r), authUser(r).ID)
		if err != nil {
			writeError(w, http.StatusForbidden, "You don't have access to this workspace")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		filterBootstrapForAPIKey(&data, r)
		sanitizeBootstrap(&data)
		writeJSON(w, http.StatusOK, data)
		return
	}
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	data.ViewerRole = "admin"
	sanitizeBootstrap(&data)
	writeJSON(w, http.StatusOK, data)
}

func sanitizeBootstrap(data *domain.Bootstrap) {
	data.AgentSessions = slices.DeleteFunc(data.AgentSessions, func(item domain.AgentSession) bool { return item.UserID != data.Viewer.ID })
	data.AgentSkills = slices.DeleteFunc(data.AgentSkills, func(item domain.PersonalAgentSkill) bool { return item.UserID != data.Viewer.ID })
	for index := range data.Cycles {
		data.Cycles[index].CalendarToken = ""
	}
	for index := range data.APIKeys {
		data.APIKeys[index].SecretHash = ""
	}
	for index := range data.ReleasePipelines {
		data.ReleasePipelines[index].AccessKeyHash = ""
	}
	for index := range data.OAuthApplications {
		data.OAuthApplications[index].ClientSecret = ""
	}
}

func filterBootstrapForAPIKey(data *domain.Bootstrap, r *http.Request) {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok || len(key.TeamIDs) == 0 {
		return
	}
	allowed := func(id string) bool { return slices.Contains(key.TeamIDs, id) }
	data.Teams = slices.DeleteFunc(data.Teams, func(item domain.Team) bool { return !allowed(item.ID) })
	data.States = slices.DeleteFunc(data.States, func(item domain.WorkflowState) bool { return item.TeamID != "" && !allowed(item.TeamID) })
	data.Labels = slices.DeleteFunc(data.Labels, func(item domain.IssueLabel) bool { return item.Scope != "" && !allowed(item.Scope) })
	data.Issues = slices.DeleteFunc(data.Issues, func(item domain.Issue) bool { return !allowed(item.Team.ID) })
	visibleIssue := func(id string) bool {
		return slices.ContainsFunc(data.Issues, func(item domain.Issue) bool { return item.ID == id })
	}
	data.Cycles = slices.DeleteFunc(data.Cycles, func(item domain.Cycle) bool { return !allowed(item.TeamID) })
	data.Projects = slices.DeleteFunc(data.Projects, func(item domain.Project) bool { return !slices.ContainsFunc(item.TeamIDs, allowed) })
	visibleProject := func(id string) bool {
		return slices.ContainsFunc(data.Projects, func(item domain.Project) bool { return item.ID == id })
	}
	data.IssueTemplates = slices.DeleteFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.TeamID != "" && !allowed(item.TeamID) })
	data.ProjectTemplates = slices.DeleteFunc(data.ProjectTemplates, func(item domain.ProjectTemplate) bool {
		return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, allowed)
	})
	data.DocumentTemplates = slices.DeleteFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return !allowed(item.TeamID) })
	data.Documents = slices.DeleteFunc(data.Documents, func(item domain.Document) bool { return !slices.ContainsFunc(item.TeamIDs, allowed) })
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(item domain.TeamMember) bool { return !allowed(item.TeamID) })
	for id := range data.TeamSettings {
		if !allowed(id) {
			delete(data.TeamSettings, id)
		}
	}
	for id := range data.CycleSettings {
		if !allowed(id) {
			delete(data.CycleSettings, id)
		}
	}
	data.CustomerRequests = slices.DeleteFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool {
		return item.IssueID != "" && !visibleIssue(item.IssueID) || item.ProjectID != "" && !visibleProject(item.ProjectID)
	})
	data.Releases = slices.DeleteFunc(data.Releases, func(item domain.Release) bool {
		return !slices.ContainsFunc(item.ProjectIDs, visibleProject) && !slices.ContainsFunc(item.IssueIDs, visibleIssue)
	})
	data.Asks = slices.DeleteFunc(data.Asks, func(item domain.Ask) bool { return item.TeamID != "" && !allowed(item.TeamID) })
	data.Initiatives = slices.DeleteFunc(data.Initiatives, func(item domain.Initiative) bool { return !slices.ContainsFunc(item.ProjectIDs, visibleProject) })
}

func (s *server) updateWorkspaceSettings(w http.ResponseWriter, r *http.Request) {
	var input map[string]any
	if !decodeJSON(w, r, &input) {
		return
	}
	if input == nil {
		input = map[string]any{}
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workspace.settings_updated", "workspace", input, func(data *domain.Bootstrap) error {
		data.Settings = input
		return nil
	})
	respondMutation(w, err, http.StatusOK, input)
}

func (s *server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name   string `json:"name"`
		URLKey string `json:"urlKey"`
		Region string `json:"region"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.URLKey = normalizeWorkspaceKey(input.URLKey)
	if input.Name == "" || input.URLKey == "" {
		writeError(w, http.StatusBadRequest, "name and URL are required")
		return
	}
	if input.Region == "" {
		input.Region = "us"
	}
	data, err := s.store.CreateWorkspace(r.Context(), input.Name, input.URLKey, input.Region)
	respondMutation(w, err, http.StatusCreated, data)
}

func (s *server) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("workspaceKey")
	data, ok := s.store.BootstrapFor(key)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	var input struct {
		Name   *string `json:"name"`
		URLKey *string `json:"urlKey"`
		Icon   *string `json:"icon"`
		Color  *string `json:"color"`
		Region *string `json:"region"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	workspace := data.Workspace
	if input.Name != nil {
		workspace.Name = strings.TrimSpace(*input.Name)
	}
	if input.URLKey != nil {
		workspace.URLKey = normalizeWorkspaceKey(*input.URLKey)
	}
	if input.Icon != nil {
		workspace.Icon = *input.Icon
	}
	if input.Color != nil {
		workspace.Color = *input.Color
	}
	if input.Region != nil {
		workspace.Region = *input.Region
	}
	if workspace.Name == "" || workspace.URLKey == "" {
		writeError(w, http.StatusBadRequest, "name and URL are required")
		return
	}
	updated, err := s.store.UpdateWorkspace(r.Context(), key, workspace)
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteWorkspace(r.Context(), r.PathValue("workspaceKey"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) createTeam(w http.ResponseWriter, r *http.Request) {
	workspaceKey := r.PathValue("workspaceKey")
	var input struct {
		Name, Key, Color, Icon string
		Private                bool `json:"private"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Key = strings.ToUpper(strings.TrimSpace(input.Key))
	if input.Name == "" || input.Key == "" {
		writeError(w, http.StatusBadRequest, "name and key are required")
		return
	}
	if input.Color == "" {
		input.Color = "#5E6AD2"
	}
	team := domain.Team{ID: fmt.Sprintf("team_%d", time.Now().UnixNano()), Name: input.Name, Key: input.Key, Color: input.Color, Icon: input.Icon, Private: input.Private}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey, "team.created", team.ID, input, func(data *domain.Bootstrap) error {
		for _, existing := range data.Teams {
			if strings.EqualFold(existing.Key, team.Key) {
				return errInvalid
			}
		}
		data.Teams = append(data.Teams, team)
		if data.TeamSettings == nil {
			data.TeamSettings = map[string]domain.TeamSettings{}
		}
		data.TeamSettings[team.ID] = domain.TeamSettings{TeamID: team.ID, Timezone: "Etc/UTC", EstimateType: "notUsed", DefaultStateID: "state_backlog", Access: "public", MembershipRestriction: "open", SettingsPermission: "allMembers", LabelPermission: "allMembers", TemplatePermission: "allMembers", AgentSkillPermission: "allMembers", LoopPermission: "allMembers", MemberPermission: "allMembers", SlackNotifications: map[string]bool{}, PRAutomations: map[string]string{}, StaleMonths: 6, AutoArchiveMonths: 6, ProgressOrder: "first", TriageAction: "none", ReleaseAutomations: []domain.TeamAutomationRule{}, TriageRules: []domain.TeamAutomationRule{}, AgentSkills: []domain.TeamAgentSkill{}, ResolvedSummaries: true, ShowInitiatives: true}
		if data.CycleSettings == nil {
			data.CycleSettings = map[string]domain.CycleSettings{}
		}
		data.CycleSettings[team.ID] = domain.CycleSettings{Enabled: false, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4, AutoCreate: true, AutoMigrate: true}
		return nil
	})
	if err == nil && !s.authDisabled {
		data, _ := s.store.BootstrapFor(workspaceKey)
		err = s.store.SetTeamMembership(r.Context(), data.Workspace.ID, team.ID, authUser(r).ID, "owner", true)
	}
	respondMutation(w, err, http.StatusCreated, team)
}

func (s *server) updateTeam(w http.ResponseWriter, r *http.Request) {
	workspaceKey, teamID := r.PathValue("workspaceKey"), r.PathValue("teamId")
	var input struct {
		Name, Key, Color, Icon *string
		Private                *bool `json:"private"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.Team
	err := s.store.MutateWorkspace(r.Context(), workspaceKey, "team.updated", teamID, input, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID != teamID {
				continue
			}
			if input.Name != nil {
				data.Teams[index].Name = strings.TrimSpace(*input.Name)
			}
			if input.Key != nil {
				key := strings.ToUpper(strings.TrimSpace(*input.Key))
				if !teamIdentifierPattern.MatchString(key) || slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID != teamID && strings.EqualFold(team.Key, key) }) {
					return errInvalid
				}
				data.Teams[index].Key = key
			}
			if input.Color != nil {
				data.Teams[index].Color = *input.Color
			}
			if input.Icon != nil {
				data.Teams[index].Icon = *input.Icon
			}
			if input.Private != nil {
				data.Teams[index].Private = *input.Private
			}
			updated = data.Teams[index]
			return nil
		}
		return errNotFound
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	workspaceKey, teamID := r.PathValue("workspaceKey"), r.PathValue("teamId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey, "team.deleted", teamID, nil, func(data *domain.Bootstrap) error {
		if len(data.Teams) <= 1 {
			return fmt.Errorf("a workspace needs at least one team")
		}
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
		if index < 0 {
			return errNotFound
		}
		data.Teams = slices.Delete(data.Teams, index, index+1)
		delete(data.TeamSettings, teamID)
		delete(data.CycleSettings, teamID)
		data.States = slices.DeleteFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID })
		data.IssueTemplates = slices.DeleteFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.TeamID == teamID })
		return nil
	})
	if err == nil && !s.authDisabled {
		data, _ := s.store.BootstrapFor(workspaceKey)
		err = s.store.DeleteTeamMemberships(r.Context(), data.Workspace.ID, teamID)
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}

type customerInput struct {
	Name          *string   `json:"name"`
	LogoURL       *string   `json:"logoUrl"`
	OwnerID       *string   `json:"ownerId"`
	Status        *string   `json:"status"`
	Tier          *string   `json:"tier"`
	AnnualRevenue *float64  `json:"annualRevenue"`
	Size          *int      `json:"size"`
	Domains       *[]string `json:"domains"`
}

func (s *server) createCustomer(w http.ResponseWriter, r *http.Request) {
	var input customerInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	now := time.Now().UTC()
	customer := domain.Customer{ID: fmt.Sprintf("customer_%d", now.UnixNano()), Name: strings.TrimSpace(*input.Name), Status: "active", Domains: []string{}, CreatedAt: now, UpdatedAt: now}
	applyCustomerInput(&customer, input)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer.created", customer.ID, input, func(data *domain.Bootstrap) error {
		data.Customers = append(data.Customers, customer)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, customer)
}

func (s *server) updateCustomer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input customerInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.Customer
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Customers, func(customer domain.Customer) bool { return customer.ID == id })
		if index < 0 {
			return errNotFound
		}
		applyCustomerInput(&data.Customers[index], input)
		if strings.TrimSpace(data.Customers[index].Name) == "" {
			return errInvalid
		}
		data.Customers[index].UpdatedAt = time.Now().UTC()
		updated = data.Customers[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Customers, func(customer domain.Customer) bool { return customer.ID == id })
		if index < 0 {
			return errNotFound
		}
		removed := data.Customers[index]
		if err := appendTrash(data, "customer", removed.ID, removed.Name, removed); err != nil {
			return err
		}
		data.Customers = slices.Delete(data.Customers, index, index+1)
		data.CustomerRequests = slices.DeleteFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.CustomerID == id })
		for projectIndex := range data.Projects {
			data.Projects[projectIndex].Customers = slices.DeleteFunc(data.Projects[projectIndex].Customers, func(value string) bool { return value == id })
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func applyCustomerInput(customer *domain.Customer, input customerInput) {
	if input.Name != nil {
		customer.Name = strings.TrimSpace(*input.Name)
	}
	if input.LogoURL != nil {
		customer.LogoURL = strings.TrimSpace(*input.LogoURL)
	}
	if input.OwnerID != nil {
		customer.OwnerID = strings.TrimSpace(*input.OwnerID)
	}
	if input.Status != nil && (*input.Status == "active" || *input.Status == "inactive") {
		customer.Status = *input.Status
	}
	if input.Tier != nil {
		customer.Tier = strings.TrimSpace(*input.Tier)
	}
	if input.AnnualRevenue != nil {
		customer.AnnualRevenue = *input.AnnualRevenue
	}
	if input.Size != nil {
		customer.Size = *input.Size
	}
	if input.Domains != nil {
		customer.Domains = normalizedStrings(*input.Domains)
	}
}

func normalizeWorkspaceKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			continue
		}
		if (r == '-' || r == '_' || r == ' ') && builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-") {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func workspaceKey(r *http.Request) string {
	if key, ok := r.Context().Value(workspaceKeyContextKey{}).(string); ok && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	if key := strings.TrimSpace(r.Header.Get("X-Workspace-Key")); key != "" {
		return key
	}
	return strings.TrimSpace(r.URL.Query().Get("workspace"))
}

func (s *server) workspaceData(r *http.Request) domain.Bootstrap {
	if !s.authDisabled {
		data, _, _ := s.store.BootstrapForUser(r.Context(), workspaceKey(r), authUser(r).ID)
		return data
	}
	data, _ := s.store.BootstrapFor(workspaceKey(r))
	return data
}

func (s *server) updateProjectDisplayDefault(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Display json.RawMessage `json:"display"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Display) == 0 || !json.Valid(input.Display) {
		writeError(w, http.StatusBadRequest, "display must be valid JSON")
		return
	}
	var object map[string]any
	if err := json.Unmarshal(input.Display, &object); err != nil || object == nil {
		writeError(w, http.StatusBadRequest, "display must be an object")
		return
	}

	var updated json.RawMessage
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_display_default.updated", "workspace", object, func(data *domain.Bootstrap) error {
		data.ProjectDisplayDefault = append(json.RawMessage(nil), input.Display...)
		updated = append(json.RawMessage(nil), data.ProjectDisplayDefault...)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

// listNotifications returns Inbox-visible notifications by default. Archived,
// deleted, and future-snoozed records remain available through explicit query
// flags so all lifecycle state is preserved without leaking into the inbox.
func (s *server) listNotifications(w http.ResponseWriter, r *http.Request) {
	includeArchived, ok := queryBool(w, r, "includeArchived")
	if !ok {
		return
	}
	includeDeleted, ok := queryBool(w, r, "includeDeleted")
	if !ok {
		return
	}
	includeSnoozed, ok := queryBool(w, r, "includeSnoozed")
	if !ok {
		return
	}
	read, ok := optionalQueryBool(w, r, "read")
	if !ok {
		return
	}

	now := time.Now().UTC()
	data := s.workspaceData(r)
	result := domain.NotificationList{Notifications: []domain.Notification{}}
	for _, notification := range data.Notifications {
		if notification.RecipientID != data.Viewer.ID {
			continue
		}
		if !includeArchived && notification.ArchivedAt != nil {
			continue
		}
		if !includeDeleted && notification.DeletedAt != nil {
			continue
		}
		if !includeSnoozed && notification.SnoozedUntil != nil && notification.SnoozedUntil.After(now) {
			continue
		}
		if notification.ReadAt == nil {
			result.UnreadCount++
		}
		if read != nil && (notification.ReadAt != nil) != *read {
			continue
		}
		result.Notifications = append(result.Notifications, notification)
	}
	slices.SortFunc(result.Notifications, func(left, right domain.Notification) int {
		return right.CreatedAt.Compare(left.CreatedAt)
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *server) updateNotification(w http.ResponseWriter, r *http.Request) {
	var input domain.NotificationMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !hasNotificationMutation(input) {
		writeError(w, http.StatusBadRequest, "notification update is required")
		return
	}
	snoozeProvided, snoozedUntil, err := parseSnoozedUntil(input.SnoozedUntil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "snoozedUntil must be an RFC3339 timestamp or null")
		return
	}

	id := r.PathValue("id")
	var updated domain.Notification
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), notificationEventType(input), id, input, func(data *domain.Bootstrap) error {
		notification, err := notificationByID(data, id)
		if err != nil {
			return err
		}
		applyNotificationUpdate(notification, input, snoozeProvided, snoozedUntil)
		updated = *notification
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createSavedView(w http.ResponseWriter, r *http.Request) {
	var input domain.SavedViewMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.SavedView
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "view.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.SavedView{ID: fmt.Sprintf("view_%d", time.Now().UnixNano()), CreatedAt: now, UpdatedAt: now}
		if err := applySavedViewUpdate(data, &created, input); err != nil {
			return "", err
		}
		data.SavedViews = append(data.SavedViews, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateSavedView(w http.ResponseWriter, r *http.Request) {
	var input domain.SavedViewMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.SavedView
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "view.updated", id, input, func(data *domain.Bootstrap) error {
		view, err := savedViewByID(data, id)
		if err != nil {
			return err
		}
		if err := applySavedViewUpdate(data, view, input); err != nil {
			return err
		}
		view.UpdatedAt = time.Now().UTC()
		updated = *view
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteSavedView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "view.deleted", id, map[string]string{"id": id}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.SavedViews, func(view domain.SavedView) bool { return view.ID == id })
		if index < 0 {
			return errNotFound
		}
		data.SavedViews = slices.Delete(data.SavedViews, index, index+1)
		data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.ResourceType == "view" && item.ResourceID == id
		})
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
			return item.ResourceType == "view" && item.ResourceID == id
		})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) updateCycleSettings(w http.ResponseWriter, r *http.Request) {
	var input domain.CycleSettingsMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID := r.PathValue("id")
	var updated domain.CycleSettings
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.settings_updated", teamID, input, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
			return errNotFound
		}
		settings := data.CycleSettings[teamID]
		if settings.DurationWeeks == 0 {
			settings = domain.CycleSettings{Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2}
		}
		if input.Enabled != nil {
			settings.Enabled = *input.Enabled
		}
		if input.DurationWeeks != nil {
			settings.DurationWeeks = *input.DurationWeeks
		}
		if input.CooldownWeeks != nil {
			settings.CooldownWeeks = *input.CooldownWeeks
		}
		if input.StartsOn != nil {
			settings.StartsOn = *input.StartsOn
		}
		if input.UpcomingCount != nil {
			settings.UpcomingCount = *input.UpcomingCount
		}
		if input.AutoAddStarted != nil {
			settings.AutoAddStarted = *input.AutoAddStarted
		}
		if input.AutoAddCompleted != nil {
			settings.AutoAddCompleted = *input.AutoAddCompleted
		}
		if input.Capacity != nil {
			settings.Capacity = *input.Capacity
		}
		if input.AutoCreate != nil {
			settings.AutoCreate = *input.AutoCreate
		}
		if input.AutoAddActive != nil {
			settings.AutoAddActive = *input.AutoAddActive
		}
		if input.AutoAddDueDate != nil {
			settings.AutoAddDueDate = *input.AutoAddDueDate
		}
		if input.AutoMigrate != nil {
			settings.AutoMigrate = *input.AutoMigrate
		}
		if input.FavoriteView != nil {
			settings.FavoriteView = *input.FavoriteView
		}
		if settings.DurationWeeks < 1 || settings.DurationWeeks > 8 || settings.CooldownWeeks < 0 || settings.CooldownWeeks > 8 || settings.StartsOn < 0 || settings.StartsOn > 6 || settings.UpcomingCount < 1 || settings.UpcomingCount > 15 || settings.Capacity < 0 || settings.Capacity > 10000 {
			return errInvalid
		}
		if data.CycleSettings == nil {
			data.CycleSettings = map[string]domain.CycleSettings{}
		}
		data.CycleSettings[teamID] = settings
		if settings.Enabled && settings.AutoCreate {
			current := currentCycle(data, teamID)
			if current != nil {
				ensureUpcomingCycles(data, current)
			} else if latest := latestCycle(data, teamID); latest != nil {
				for countUpcomingCycles(data, teamID) < settings.UpcomingCount {
					appendFutureCycle(data, teamID, latestCycle(data, teamID))
				}
			} else {
				first := appendFutureCycle(data, teamID, nil)
				if err := transitionToCycle(data, first, time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		updated = settings
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) updateCycle(w http.ResponseWriter, r *http.Request) {
	var input domain.CycleMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Cycle
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.updated", id, input, func(data *domain.Bootstrap) error {
		cycle, err := cycleByID(data, id)
		if err != nil {
			return err
		}
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" || len(name) > 100 {
				return errInvalid
			}
			cycle.Name = name
		}
		if input.Description != nil {
			cycle.Description = strings.TrimSpace(*input.Description)
		}
		if input.StartsAt != nil {
			parsed, err := parseCycleDate(*input.StartsAt)
			if err != nil {
				return errInvalid
			}
			cycle.StartsAt = parsed
		}
		if input.EndsAt != nil {
			parsed, err := parseCycleDate(*input.EndsAt)
			if err != nil {
				return errInvalid
			}
			cycle.EndsAt = parsed
		}
		if !cycle.EndsAt.After(cycle.StartsAt) {
			return errInvalid
		}
		if input.Capacity != nil {
			if *input.Capacity < 0 || *input.Capacity > 10000 {
				return errInvalid
			}
			cycle.Capacity = *input.Capacity
		}
		if input.Favorite != nil {
			cycle.Favorite = *input.Favorite
		}
		if input.Insight != nil {
			measure, slice, segment := input.Insight["measure"], input.Insight["slice"], input.Insight["segment"]
			if !slices.Contains([]string{"Issue count", "Estimate"}, measure) || !slices.Contains([]string{"Status", "Assignee"}, slice) || !slices.Contains([]string{"Priority", "Project"}, segment) {
				return errInvalid
			}
			cycle.Insight = map[string]string{"measure": measure, "slice": slice, "segment": segment}
		}
		cycle.UpdatedAt = time.Now().UTC()
		updated = *cycle
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) startCycle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var started domain.Cycle
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.started", id, map[string]string{"id": id}, func(data *domain.Bootstrap) error {
		cycle, err := cycleByID(data, id)
		if err != nil {
			return err
		}
		if cycle.Status != "upcoming" {
			return errInvalid
		}
		if err := transitionToCycle(data, cycle, time.Now().UTC()); err != nil {
			return err
		}
		started = *cycle
		return nil
	})
	respondMutation(w, err, http.StatusOK, started)
}

func (s *server) completeCycle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var completed domain.Cycle
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.completed", id, map[string]string{"id": id}, func(data *domain.Bootstrap) error {
		cycle, err := cycleByID(data, id)
		if err != nil {
			return err
		}
		if cycle.Status != "current" {
			return errInvalid
		}
		completed = *cycle
		next := earliestUpcomingCycle(data, cycle.TeamID)
		if next == nil {
			next = appendFutureCycle(data, cycle.TeamID, cycle)
		}
		if err := transitionToCycle(data, next, time.Now().UTC()); err != nil {
			return err
		}
		completed.Status = "completed"
		completed.UpdatedAt = time.Now().UTC()
		return nil
	})
	respondMutation(w, err, http.StatusOK, completed)
}

func transitionToCycle(data *domain.Bootstrap, target *domain.Cycle, now time.Time) error {
	var previous *domain.Cycle
	for i := range data.Cycles {
		if data.Cycles[i].TeamID == target.TeamID && data.Cycles[i].Status == "current" {
			previous = &data.Cycles[i]
			break
		}
	}
	if previous != nil && previous.ID != target.ID {
		previous.Status = "completed"
		previous.UpdatedAt = now
		settings := data.CycleSettings[target.TeamID]
		for i := range data.Issues {
			issue := &data.Issues[i]
			if settings.AutoMigrate && issue.CycleID != nil && *issue.CycleID == previous.ID && issue.State.Type != "completed" && issue.State.Type != "canceled" && issue.State.Type != "backlog" {
				issue.CycleID = stringPointer(target.ID)
				issue.UpdatedAt = now
				appendActivity(data, issue.ID, "issue.updated", data.Viewer, map[string]string{"cycle": target.ID})
			}
		}
	}
	duration := target.EndsAt.Sub(target.StartsAt)
	if duration < 24*time.Hour {
		duration = 13 * 24 * time.Hour
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	target.StartsAt = start
	target.EndsAt = start.Add(duration)
	target.Status = "current"
	target.UpdatedAt = now
	ensureUpcomingCycles(data, target)
	return nil
}

func ensureUpcomingCycles(data *domain.Bootstrap, current *domain.Cycle) {
	teamID := current.TeamID
	settings := data.CycleSettings[teamID]
	wanted := settings.UpcomingCount
	if wanted < 1 {
		wanted = 2
	}
	for countUpcomingCycles(data, teamID) < wanted {
		appendFutureCycle(data, teamID, latestCycle(data, teamID))
	}
}

func appendFutureCycle(data *domain.Bootstrap, teamID string, previous *domain.Cycle) *domain.Cycle {
	settings := data.CycleSettings[teamID]
	weeks := settings.DurationWeeks
	if weeks < 1 {
		weeks = 2
	}
	cooldown := settings.CooldownWeeks
	start := cycleWeekStart(time.Now().UTC())
	number := 1
	if previous != nil {
		start = previous.EndsAt.AddDate(0, 0, 1+cooldown*7)
		number = previous.Number + 1
	}
	now := time.Now().UTC()
	capacity := settings.Capacity
	if capacity < 1 {
		capacity = 4
	}
	cycle := domain.Cycle{ID: fmt.Sprintf("cycle_%d", time.Now().UnixNano()), Number: number, Name: fmt.Sprintf("Cycle %d", number), TeamID: teamID, StartsAt: start, EndsAt: start.AddDate(0, 0, weeks*7-1), Status: "upcoming", Capacity: capacity, CreatedAt: now, UpdatedAt: now}
	data.Cycles = append(data.Cycles, cycle)
	return &data.Cycles[len(data.Cycles)-1]
}

func countUpcomingCycles(data *domain.Bootstrap, teamID string) int {
	count := 0
	for _, cycle := range data.Cycles {
		if cycle.TeamID == teamID && cycle.Status == "upcoming" {
			count++
		}
	}
	return count
}

func latestCycle(data *domain.Bootstrap, teamID string) *domain.Cycle {
	var latest *domain.Cycle
	for i := range data.Cycles {
		cycle := &data.Cycles[i]
		if cycle.TeamID == teamID && (latest == nil || cycle.EndsAt.After(latest.EndsAt)) {
			latest = cycle
		}
	}
	return latest
}

func earliestUpcomingCycle(data *domain.Bootstrap, teamID string) *domain.Cycle {
	var earliest *domain.Cycle
	for i := range data.Cycles {
		cycle := &data.Cycles[i]
		if cycle.TeamID == teamID && cycle.Status == "upcoming" && (earliest == nil || cycle.StartsAt.Before(earliest.StartsAt)) {
			earliest = cycle
		}
	}
	return earliest
}

func cycleByID(data *domain.Bootstrap, id string) (*domain.Cycle, error) {
	for i := range data.Cycles {
		if data.Cycles[i].ID == id {
			return &data.Cycles[i], nil
		}
	}
	return nil, errNotFound
}

func parseCycleDate(value string) (time.Time, error) {
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed.UTC(), err
}

func stringPointer(value string) *string { return &value }

func cycleWeekStart(value time.Time) time.Time {
	day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

func (s *server) createIssue(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueCreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Title) == "" && input.TemplateID == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	var created domain.Issue
	var templateSubIssues []domain.TemplateSubIssue
	if !s.authDisabled && input.TeamID == "" {
		projected, ok, err := s.store.BootstrapForUser(r.Context(), workspaceKey(r), authUser(r).ID)
		if err != nil || !ok || len(projected.Teams) == 0 {
			writeError(w, http.StatusForbidden, "Join a team before creating an issue")
			return
		}
		input.TeamID = projected.Teams[0].ID
	}
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "issue.created", input, func(data *domain.Bootstrap) (string, error) {
		if input.TemplateID != "" {
			index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == input.TemplateID })
			if index < 0 {
				return "", errNotFound
			}
			template := data.IssueTemplates[index]
			templateSubIssues = slices.Clone(template.SubIssues)
			if input.Title == "" {
				input.Title = template.Title
				if input.Title == "" {
					input.Title = template.Name
				}
			}
			if input.Description == "" {
				input.Description = template.Body
			}
			if input.StateID == nil && template.StateID != "" {
				input.StateID = &template.StateID
			}
			if input.Priority == nil {
				input.Priority = &template.Priority
			}
			if input.AssigneeID == nil && template.AssigneeID != "" {
				input.AssigneeID = &template.AssigneeID
			}
			if input.ProjectID == nil && template.ProjectID != "" {
				input.ProjectID = &template.ProjectID
			}
			if len(input.LabelIDs) == 0 {
				input.LabelIDs = slices.Clone(template.LabelIDs)
			}
			if input.TeamID == "" {
				input.TeamID = template.TeamID
			}
		}
		if strings.TrimSpace(input.Title) == "" {
			return "", errInvalid
		}
		number := nextIssueNumber(data.Issues)
		now := time.Now().UTC()
		team := data.Teams[0]
		if input.TeamID != "" {
			index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == input.TeamID })
			if index < 0 {
				return "", errInvalid
			}
			team = data.Teams[index]
		}
		settings := teamSettings(data, team.ID)
		defaultState := stateForTeam(data, team.ID, settings.DefaultStateID)
		if defaultState == nil {
			states := statesForTeam(data, team.ID)
			if len(states) == 0 {
				return "", errInvalid
			}
			defaultState = stateForTeam(data, team.ID, states[0].ID)
		}
		created = domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number, Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), Priority: settings.DefaultPriority, PriorityLabel: priorityLabel(settings.DefaultPriority), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: *defaultState, Creator: data.Viewer, Labels: []domain.IssueLabel{}, ParentID: input.ParentID, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}, TemplateID: input.TemplateID, SuggestedLabelIDs: []string{}}
		if preferences := data.UserSettings[data.Viewer.ID]; preferences.AutoAssign && input.AssigneeID == nil {
			input.AssigneeID = &data.Viewer.ID
		}
		createUpdate := domain.IssueUpdateInput{DescriptionState: input.DescriptionState, DescriptionData: input.DescriptionData, ContentState: input.ContentState, StateID: input.StateID, Priority: input.Priority, AssigneeID: input.AssigneeID, DelegateID: input.DelegateID, ProjectID: input.ProjectID, ProjectMilestoneID: input.ProjectMilestoneID, CycleID: input.CycleID, DueDate: input.DueDate, SLABreachesAt: input.SLABreachesAt, SLAType: input.SLAType}
		if len(input.LabelIDs) > 0 {
			createUpdate.LabelIDs = &input.LabelIDs
		}
		if _, err := applyUpdate(data, &created, createUpdate); err != nil {
			return "", err
		}
		applyCycleAutomation(data, &created)
		applySLARules(data, &created, now)
		if input.ParentID != nil {
			parent, err := issueByID(data, *input.ParentID)
			if err != nil {
				return "", err
			}
			parent.SubIssueIDs = appendUnique(parent.SubIssueIDs, created.ID)
		}
		children := make([]domain.Issue, 0, len(templateSubIssues))
		for childIndex, templateChild := range templateSubIssues {
			childTeam := team
			if templateChild.TeamID != "" {
				teamIndex := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == templateChild.TeamID })
				if teamIndex < 0 {
					return "", errInvalid
				}
				childTeam = data.Teams[teamIndex]
			}
			childSettings := teamSettings(data, childTeam.ID)
			childState := stateForTeam(data, childTeam.ID, childSettings.DefaultStateID)
			if childState == nil {
				states := statesForTeam(data, childTeam.ID)
				if len(states) == 0 {
					return "", errInvalid
				}
				childState = &states[0]
			}
			childNumber := number + childIndex + 1
			child := domain.Issue{ID: fmt.Sprintf("issue_%d", childNumber), Version: 1, Identifier: fmt.Sprintf("%s-%d", childTeam.Key, childNumber), Number: childNumber, Title: strings.TrimSpace(templateChild.Title), Description: strings.TrimSpace(templateChild.Description), Priority: templateChild.Priority, PriorityLabel: priorityLabel(templateChild.Priority), SortOrder: float64(childNumber), CreatedAt: now, UpdatedAt: now, Team: childTeam, State: *childState, Creator: data.Viewer, Labels: labelsByID(data, templateChild.LabelIDs), ParentID: &created.ID, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
			if templateChild.AssigneeID != "" {
				child.Assignee = userByID(data, templateChild.AssigneeID)
			}
			created.SubIssueIDs = append(created.SubIssueIDs, child.ID)
			children = append(children, child)
		}
		data.Issues = append([]domain.Issue{created}, append(children, data.Issues...)...)
		appendActivity(data, created.ID, "issue.created", data.Viewer, map[string]string{})
		for _, child := range children {
			appendActivity(data, child.ID, "issue.created", data.Viewer, map[string]string{})
		}
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if (input.Name == nil || strings.TrimSpace(*input.Name) == "") && input.TemplateID == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.Project
	if !s.authDisabled && len(input.TeamIDs) == 0 {
		projected, ok, err := s.store.BootstrapForUser(r.Context(), workspaceKey(r), authUser(r).ID)
		if err != nil || !ok || len(projected.Teams) == 0 {
			writeError(w, http.StatusForbidden, "Join a team before creating a project")
			return
		}
		input.TeamIDs = []string{projected.Teams[0].ID}
	}
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "project.created", input, func(data *domain.Bootstrap) (string, error) {
		if input.TemplateID != "" {
			index := slices.IndexFunc(data.ProjectTemplates, func(template domain.ProjectTemplate) bool { return template.ID == input.TemplateID })
			if index < 0 {
				return "", errNotFound
			}
			template := data.ProjectTemplates[index]
			if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
				input.Name = &template.ProjectName
				if strings.TrimSpace(*input.Name) == "" {
					input.Name = &template.Name
				}
			}
			if input.Summary == nil && template.Summary != "" {
				input.Summary = &template.Summary
			}
			if input.Description == nil && template.Description != "" {
				input.Description = &template.Description
			}
			if input.Icon == nil && template.Icon != "" {
				input.Icon = &template.Icon
			}
			if input.Color == nil && template.Color != "" {
				input.Color = &template.Color
			}
			if input.StatusID == nil && template.StatusID != "" {
				input.StatusID = &template.StatusID
			}
			if input.Priority == nil {
				input.Priority = &template.Priority
			}
			if len(input.TeamIDs) == 0 {
				input.TeamIDs = slices.Clone(template.TeamIDs)
			}
			if input.LabelIDs == nil {
				input.LabelIDs = slices.Clone(template.LabelIDs)
			}
			if input.LeadID == nil && template.LeadID != "" {
				input.LeadID = &template.LeadID
			}
			if len(input.MemberIDs) == 0 {
				input.MemberIDs = slices.Clone(template.MemberIDs)
			}
			if len(input.DependencyIDs) == 0 {
				input.DependencyIDs = slices.Clone(template.DependencyIDs)
			}
			if len(input.Initiatives) == 0 {
				input.Initiatives = slices.Clone(template.InitiativeIDs)
			}
		}
		if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
			return "", errInvalid
		}
		now := time.Now().UTC()
		id := fmt.Sprintf("project_%d", time.Now().UnixNano())
		status := defaultProjectStatus(data)
		teamIDs := input.TeamIDs
		if len(teamIDs) == 0 {
			teamIDs = []string{data.Teams[0].ID}
		}
		created = domain.Project{ID: id, Name: strings.TrimSpace(*input.Name), SlugID: slug(strings.TrimSpace(*input.Name)), Color: "#5e6ad2", PriorityLabel: "No priority", Health: "noUpdate", Status: status, MemberIDs: []string{}, TeamIDs: teamIDs, DependencyIDs: []string{}, Initiatives: []string{}, Customers: []string{}, Resources: []domain.ProjectResource{}, Milestones: []domain.ProjectMilestone{}, Comments: []domain.Comment{}, DescriptionRevisions: []domain.ProjectDescriptionRevision{}, UpdateCadence: "none", CreatedAt: now, UpdatedAt: now}
		if err := applyProjectUpdate(data, &created, input); err != nil {
			return "", err
		}
		if input.TemplateID != "" {
			if index := slices.IndexFunc(data.ProjectTemplates, func(template domain.ProjectTemplate) bool { return template.ID == input.TemplateID }); index >= 0 {
				for milestoneIndex, milestone := range data.ProjectTemplates[index].Milestones {
					created.Milestones = append(created.Milestones, domain.ProjectMilestone{
						ID: fmt.Sprintf("project_milestone_%d_%d", time.Now().UnixNano(), milestoneIndex), ProjectID: created.ID,
						Name: milestone.Name, Description: milestone.Description, CreatedAt: now, UpdatedAt: now,
					})
				}
			}
		}
		data.Projects = append([]domain.Project{created}, data.Projects...)
		if input.TemplateID != "" {
			if index := slices.IndexFunc(data.ProjectTemplates, func(template domain.ProjectTemplate) bool { return template.ID == input.TemplateID }); index >= 0 {
				for issueIndex := range data.Issues {
					if slices.Contains(data.ProjectTemplates[index].IssueIDs, data.Issues[issueIndex].ID) {
						data.Issues[issueIndex].Project = &domain.ProjectSummary{ID: created.ID, Name: created.Name, Icon: created.Icon, Color: created.Color}
					}
				}
			}
		}
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateProject(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Project
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.updated", id, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, id)
		if err != nil {
			return err
		}
		if input.Description != nil && *input.Description != project.Description {
			project.DescriptionRevisions = append([]domain.ProjectDescriptionRevision{{ID: fmt.Sprintf("project_description_revision_%d", time.Now().UnixNano()), ProjectID: id, Description: project.Description, Author: data.Viewer, CreatedAt: time.Now().UTC()}}, project.DescriptionRevisions...)
		}
		if err := applyProjectUpdate(data, project, input); err != nil {
			return err
		}
		project.UpdatedAt = time.Now().UTC()
		updated = *project
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.deleted", id, map[string]string{"id": id}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
		if index < 0 {
			return errNotFound
		}
		removed := data.Projects[index]
		if err := appendTrash(data, "project", removed.ID, removed.Name, deletedProjectPayload{Project: removed, Updates: data.ProjectUpdates[id]}); err != nil {
			return err
		}
		data.Projects = slices.Delete(data.Projects, index, index+1)
		for i := range data.Issues {
			if data.Issues[i].Project != nil && data.Issues[i].Project.ID == id {
				data.Issues[i].Project = nil
			}
		}
		delete(data.ProjectUpdates, id)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createInitiative(w http.ResponseWriter, r *http.Request) {
	var input domain.InitiativeMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.Initiative
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "initiative.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.Initiative{ID: fmt.Sprintf("initiative_%d", now.UnixNano()), Name: strings.TrimSpace(*input.Name), SlugID: slug(*input.Name), Icon: "Initiative", Color: "#d15f64", Status: "active", PriorityLabel: "No priority", Health: "noUpdate", Creator: data.Viewer, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}, ProjectIDs: []string{}, Resources: []domain.InitiativeResource{}, Comments: []domain.Comment{}, NotificationRules: domain.InitiativeNotificationRules{DescriptionChanges: true, NewUpdate: true}, UpdateSchedule: domain.InitiativeUpdateSchedule{Cadence: "none", Weekday: 1, TimeRange: "09:00-12:00"}, DescriptionHistory: []domain.InitiativeDescriptionRevision{}, CreatedAt: now, UpdatedAt: now}
		if err := applyInitiativeUpdate(data, &created, input); err != nil {
			return "", err
		}
		data.Initiatives = append([]domain.Initiative{created}, data.Initiatives...)
		if data.InitiativeUpdates == nil {
			data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
		}
		data.InitiativeUpdates[created.ID] = []domain.InitiativeUpdate{}
		syncInitiativeProjects(data, created.ID, nil, created.ProjectIDs)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateInitiative(w http.ResponseWriter, r *http.Request) {
	var input domain.InitiativeMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.Initiative
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.updated", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		before := slices.Clone(initiative.ProjectIDs)
		if err := applyInitiativeUpdate(data, initiative, input); err != nil {
			return err
		}
		initiative.UpdatedAt = time.Now().UTC()
		syncInitiativeProjects(data, id, before, initiative.ProjectIDs)
		updated = *initiative
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteInitiative(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.deleted", id, map[string]string{"id": id}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		removed := data.Initiatives[index]
		if err := appendTrash(data, "initiative", removed.ID, removed.Name, deletedInitiativePayload{Initiative: removed, Updates: data.InitiativeUpdates[id]}); err != nil {
			return err
		}
		for i := range data.Projects {
			data.Projects[i].Initiatives = removeString(data.Projects[i].Initiatives, id)
		}
		data.Initiatives = slices.Delete(data.Initiatives, index, index+1)
		delete(data.InitiativeUpdates, id)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createInitiativeReminder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RemindAt string `json:"remindAt"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	remindAt, err := time.Parse(time.RFC3339, strings.TrimSpace(input.RemindAt))
	if err != nil || !remindAt.After(time.Now().UTC()) {
		writeError(w, http.StatusBadRequest, "remindAt must be a future RFC3339 timestamp")
		return
	}
	id := r.PathValue("id")
	var reminder domain.Notification
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.reminder_created", id, input, func(data *domain.Bootstrap) error {
		initiative, initiativeErr := initiativeByID(data, id)
		if initiativeErr != nil {
			return initiativeErr
		}
		now := time.Now().UTC()
		reminder = domain.Notification{ID: fmt.Sprintf("notification_initiative_reminder_%d", now.UnixNano()), RecipientID: data.Viewer.ID, Type: "initiativeReminder", SourceType: "initiative", SourceID: id, Actor: data.Viewer, Category: "reminders", GroupKey: "initiative-reminder:" + id + ":" + strconv.FormatInt(remindAt.Unix(), 10), OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, SnoozedUntil: &remindAt, CreatedAt: now, UpdatedAt: now}
		data.Notifications = append([]domain.Notification{reminder}, data.Notifications...)
		appendActivity(data, id, "initiative.reminder_created", data.Viewer, map[string]string{"initiativeName": initiative.Name, "remindAt": remindAt.Format(time.RFC3339)})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, reminder)
}

func (s *server) createInitiativeResource(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectResourceMutationInput
	if !decodeJSON(w, r, &input) || input.URL == nil || strings.TrimSpace(*input.URL) == "" {
		writeError(w, http.StatusBadRequest, "resource URL is required")
		return
	}
	id := r.PathValue("id")
	var created domain.InitiativeResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.resource_created", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		resourceType := "link"
		if input.Type != nil && *input.Type != "" {
			resourceType = *input.Type
		}
		if !slices.Contains([]string{"link", "document"}, resourceType) {
			return errInvalid
		}
		title := strings.TrimSpace(*input.URL)
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			title = strings.TrimSpace(*input.Title)
		}
		created = domain.InitiativeResource{ID: fmt.Sprintf("initiative_resource_%d", time.Now().UnixNano()), InitiativeID: id, Type: resourceType, Title: title, URL: strings.TrimSpace(*input.URL), CreatedAt: time.Now().UTC()}
		initiative.Resources = append(initiative.Resources, created)
		initiative.UpdatedAt = created.CreatedAt
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateInitiativeResource(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectResourceMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	initiativeID, resourceID := r.PathValue("id"), r.PathValue("resourceId")
	var updated domain.InitiativeResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.resource_updated", initiativeID, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, initiativeID)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(initiative.Resources, func(resource domain.InitiativeResource) bool { return resource.ID == resourceID })
		if index < 0 {
			return errNotFound
		}
		resource := &initiative.Resources[index]
		if input.Type != nil {
			if !slices.Contains([]string{"link", "document"}, *input.Type) {
				return errInvalid
			}
			resource.Type = *input.Type
		}
		if input.Title != nil {
			resource.Title = strings.TrimSpace(*input.Title)
		}
		if input.URL != nil {
			if strings.TrimSpace(*input.URL) == "" {
				return errInvalid
			}
			resource.URL = strings.TrimSpace(*input.URL)
		}
		initiative.UpdatedAt = time.Now().UTC()
		updated = *resource
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteInitiativeResource(w http.ResponseWriter, r *http.Request) {
	initiativeID, resourceID := r.PathValue("id"), r.PathValue("resourceId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.resource_deleted", initiativeID, map[string]string{"id": resourceID}, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, initiativeID)
		if err != nil {
			return err
		}
		before := len(initiative.Resources)
		initiative.Resources = slices.DeleteFunc(initiative.Resources, func(resource domain.InitiativeResource) bool { return resource.ID == resourceID })
		if len(initiative.Resources) == before {
			return errNotFound
		}
		initiative.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createInitiativeComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	id := r.PathValue("id")
	var created domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.commented", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		created = domain.Comment{ID: fmt.Sprintf("initiative_comment_%d", time.Now().UnixNano()), Body: strings.TrimSpace(input.Body), BodyData: input.BodyData, Reactions: map[string][]string{}, CreatedAt: time.Now().UTC(), User: data.Viewer}
		initiative.Comments = append(initiative.Comments, created)
		initiative.UpdatedAt = created.CreatedAt
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateInitiativeComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentUpdateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.comment_updated", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(initiative.Comments, func(comment domain.Comment) bool { return comment.ID == commentID })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		initiative.Comments[index].Body = strings.TrimSpace(input.Body)
		initiative.Comments[index].BodyData = input.BodyData
		initiative.Comments[index].EditedAt = &now
		initiative.UpdatedAt = now
		updated = initiative.Comments[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteInitiativeComment(w http.ResponseWriter, r *http.Request) {
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.comment_deleted", id, map[string]string{"commentId": commentID}, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		before := len(initiative.Comments)
		initiative.Comments = slices.DeleteFunc(initiative.Comments, func(comment domain.Comment) bool { return comment.ID == commentID })
		if len(initiative.Comments) == before {
			return errNotFound
		}
		initiative.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) toggleInitiativeCommentReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Emoji) == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.comment_reaction_toggled", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(initiative.Comments, func(comment domain.Comment) bool { return comment.ID == commentID })
		if index < 0 {
			return errNotFound
		}
		comment := &initiative.Comments[index]
		if comment.Reactions == nil {
			comment.Reactions = map[string][]string{}
		}
		users := comment.Reactions[input.Emoji]
		if slices.Contains(users, data.Viewer.ID) {
			users = removeString(users, data.Viewer.ID)
		} else {
			users = append(users, data.Viewer.ID)
		}
		if len(users) == 0 {
			delete(comment.Reactions, input.Emoji)
		} else {
			comment.Reactions[input.Emoji] = users
		}
		comment.Version++
		updated = *comment
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createInitiativeUpdate(w http.ResponseWriter, r *http.Request) {
	var input domain.InitiativeUpdateCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "update body is required")
		return
	}
	if input.Health != "" && !slices.Contains([]string{"onTrack", "atRisk", "offTrack", "noUpdate"}, input.Health) {
		writeError(w, http.StatusBadRequest, "invalid health")
		return
	}
	id := r.PathValue("id")
	var created domain.InitiativeUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.update_created", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		health := input.Health
		if health == "" {
			health = initiative.Health
		}
		if health == "noUpdate" {
			health = "onTrack"
		}
		created = domain.InitiativeUpdate{ID: fmt.Sprintf("initiative_update_%d", time.Now().UnixNano()), InitiativeID: id, Body: strings.TrimSpace(input.Body), Health: health, CreatedAt: time.Now().UTC(), User: data.Viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{}}
		data.InitiativeUpdates[id] = append([]domain.InitiativeUpdate{created}, data.InitiativeUpdates[id]...)
		initiative.Health = health
		initiative.UpdatedAt = created.CreatedAt
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateInitiativeUpdate(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectUpdateMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id, updateID := r.PathValue("id"), r.PathValue("updateId")
	var updated domain.InitiativeUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.update_updated", id, input, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		updates := data.InitiativeUpdates[id]
		index := slices.IndexFunc(updates, func(item domain.InitiativeUpdate) bool { return item.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		if input.Body != nil {
			if strings.TrimSpace(*input.Body) == "" {
				return errInvalid
			}
			updates[index].Body = strings.TrimSpace(*input.Body)
		}
		if input.Health != nil {
			if !slices.Contains([]string{"onTrack", "atRisk", "offTrack"}, *input.Health) {
				return errInvalid
			}
			updates[index].Health = *input.Health
			if index == 0 {
				initiative.Health = *input.Health
			}
		}
		now := time.Now().UTC()
		updates[index].EditedAt = &now
		initiative.UpdatedAt = now
		data.InitiativeUpdates[id] = updates
		updated = updates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteInitiativeUpdate(w http.ResponseWriter, r *http.Request) {
	id, updateID := r.PathValue("id"), r.PathValue("updateId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.update_deleted", id, map[string]string{"id": updateID}, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		updates := data.InitiativeUpdates[id]
		index := slices.IndexFunc(updates, func(item domain.InitiativeUpdate) bool { return item.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		updates = slices.Delete(updates, index, index+1)
		data.InitiativeUpdates[id] = updates
		initiative.Health = "noUpdate"
		if len(updates) > 0 {
			initiative.Health = updates[0].Health
		}
		initiative.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createInitiativeUpdateComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	initiativeID, updateID := r.PathValue("id"), r.PathValue("updateId")
	var updated domain.InitiativeUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.update_commented", initiativeID, input, func(data *domain.Bootstrap) error {
		if _, err := initiativeByID(data, initiativeID); err != nil {
			return err
		}
		updates := data.InitiativeUpdates[initiativeID]
		index := slices.IndexFunc(updates, func(update domain.InitiativeUpdate) bool { return update.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		comment := domain.Comment{ID: fmt.Sprintf("initiative_update_comment_%d", time.Now().UnixNano()), Body: strings.TrimSpace(input.Body), Reactions: map[string][]string{}, CreatedAt: time.Now().UTC(), User: data.Viewer}
		updates[index].Comments = append(updates[index].Comments, comment)
		data.InitiativeUpdates[initiativeID] = updates
		updated = updates[index]
		return nil
	})
	respondMutation(w, err, http.StatusCreated, updated)
}

func (s *server) toggleInitiativeUpdateReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Emoji) == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	initiativeID, updateID := r.PathValue("id"), r.PathValue("updateId")
	var updated domain.InitiativeUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.update_reaction_toggled", initiativeID, input, func(data *domain.Bootstrap) error {
		if _, err := initiativeByID(data, initiativeID); err != nil {
			return err
		}
		updates := data.InitiativeUpdates[initiativeID]
		index := slices.IndexFunc(updates, func(update domain.InitiativeUpdate) bool { return update.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		if updates[index].Reactions == nil {
			updates[index].Reactions = map[string][]string{}
		}
		users := updates[index].Reactions[input.Emoji]
		viewerIndex := slices.Index(users, data.Viewer.ID)
		if viewerIndex >= 0 {
			users = slices.Delete(users, viewerIndex, viewerIndex+1)
		} else {
			users = append(users, data.Viewer.ID)
		}
		if len(users) == 0 {
			delete(updates[index].Reactions, input.Emoji)
		} else {
			updates[index].Reactions[input.Emoji] = users
		}
		data.InitiativeUpdates[initiativeID] = updates
		updated = updates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createProjectResource(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectResourceMutationInput
	if !decodeJSON(w, r, &input) || input.URL == nil || strings.TrimSpace(*input.URL) == "" {
		writeError(w, http.StatusBadRequest, "resource URL is required")
		return
	}
	projectID := r.PathValue("id")
	var created domain.ProjectResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.resource_created", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		resourceType := "link"
		if input.Type != nil && strings.TrimSpace(*input.Type) != "" {
			resourceType = strings.TrimSpace(*input.Type)
		}
		if !slices.Contains([]string{"link", "document"}, resourceType) {
			return errInvalid
		}
		title := strings.TrimSpace(*input.URL)
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			title = strings.TrimSpace(*input.Title)
		}
		created = domain.ProjectResource{ID: fmt.Sprintf("project_resource_%d", time.Now().UnixNano()), ProjectID: projectID, Type: resourceType, Title: title, URL: strings.TrimSpace(*input.URL), PinnedTeamIDs: []string{}, CreatedAt: time.Now().UTC()}
		project.Resources = append(project.Resources, created)
		project.UpdatedAt = created.CreatedAt
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateProjectResource(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectResourceMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	projectID, resourceID := r.PathValue("id"), r.PathValue("resourceId")
	var updated domain.ProjectResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.resource_updated", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(project.Resources, func(resource domain.ProjectResource) bool { return resource.ID == resourceID })
		if index < 0 {
			return errNotFound
		}
		resource := &project.Resources[index]
		if input.Type != nil {
			if !slices.Contains([]string{"link", "document"}, *input.Type) {
				return errInvalid
			}
			resource.Type = *input.Type
		}
		if input.Title != nil {
			resource.Title = strings.TrimSpace(*input.Title)
		}
		if input.URL != nil {
			if strings.TrimSpace(*input.URL) == "" {
				return errInvalid
			}
			resource.URL = strings.TrimSpace(*input.URL)
		}
		if input.PinnedTeamIDs != nil {
			teamIDs := normalizedStrings(*input.PinnedTeamIDs)
			if slices.ContainsFunc(teamIDs, func(id string) bool {
				return !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
			}) {
				return errInvalid
			}
			resource.PinnedTeamIDs = teamIDs
		}
		project.UpdatedAt = time.Now().UTC()
		updated = *resource
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteProjectResource(w http.ResponseWriter, r *http.Request) {
	projectID, resourceID := r.PathValue("id"), r.PathValue("resourceId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.resource_deleted", projectID, map[string]string{"id": resourceID}, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		before := len(project.Resources)
		project.Resources = slices.DeleteFunc(project.Resources, func(resource domain.ProjectResource) bool { return resource.ID == resourceID })
		if len(project.Resources) == before {
			return errNotFound
		}
		project.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createProjectMilestone(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectMilestoneMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "milestone name is required")
		return
	}
	projectID := r.PathValue("id")
	var created domain.ProjectMilestone
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.milestone_created", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created = domain.ProjectMilestone{ID: fmt.Sprintf("project_milestone_%d", time.Now().UnixNano()), ProjectID: projectID, Name: strings.TrimSpace(*input.Name), CreatedAt: now, UpdatedAt: now}
		if input.Description != nil {
			created.Description = strings.TrimSpace(*input.Description)
		}
		if input.TargetDate != nil {
			created.TargetDate = optionalString(*input.TargetDate)
		}
		project.Milestones = append(project.Milestones, created)
		project.UpdatedAt = now
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateProjectMilestone(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectMilestoneMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	projectID, milestoneID := r.PathValue("id"), r.PathValue("milestoneId")
	var updated domain.ProjectMilestone
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.milestone_updated", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(project.Milestones, func(milestone domain.ProjectMilestone) bool { return milestone.ID == milestoneID })
		if index < 0 {
			return errNotFound
		}
		milestone := &project.Milestones[index]
		if input.Name != nil {
			if strings.TrimSpace(*input.Name) == "" {
				return errInvalid
			}
			milestone.Name = strings.TrimSpace(*input.Name)
		}
		if input.TargetDate != nil {
			milestone.TargetDate = optionalString(*input.TargetDate)
		}
		if input.Description != nil {
			milestone.Description = strings.TrimSpace(*input.Description)
		}
		milestone.UpdatedAt = time.Now().UTC()
		project.UpdatedAt = milestone.UpdatedAt
		updated = *milestone
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) reorderProjectMilestones(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	projectID := r.PathValue("id")
	var updated []domain.ProjectMilestone
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.milestones_reordered", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		if len(input.IDs) != len(project.Milestones) {
			return errInvalid
		}
		ordered := make([]domain.ProjectMilestone, 0, len(input.IDs))
		seen := make(map[string]struct{}, len(input.IDs))
		for _, id := range input.IDs {
			if _, duplicate := seen[id]; duplicate {
				return errInvalid
			}
			index := slices.IndexFunc(project.Milestones, func(milestone domain.ProjectMilestone) bool { return milestone.ID == id })
			if index < 0 {
				return errInvalid
			}
			seen[id] = struct{}{}
			ordered = append(ordered, project.Milestones[index])
		}
		project.Milestones = ordered
		project.UpdatedAt = time.Now().UTC()
		updated = slices.Clone(ordered)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteProjectMilestone(w http.ResponseWriter, r *http.Request) {
	projectID, milestoneID := r.PathValue("id"), r.PathValue("milestoneId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.milestone_deleted", projectID, map[string]string{"id": milestoneID}, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		before := len(project.Milestones)
		project.Milestones = slices.DeleteFunc(project.Milestones, func(milestone domain.ProjectMilestone) bool { return milestone.ID == milestoneID })
		if len(project.Milestones) == before {
			return errNotFound
		}
		for index := range data.Issues {
			if data.Issues[index].ProjectMilestoneID != nil && *data.Issues[index].ProjectMilestoneID == milestoneID {
				data.Issues[index].ProjectMilestoneID = nil
				data.Issues[index].UpdatedAt = time.Now().UTC()
				data.Issues[index].Version++
			}
		}
		project.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createProjectComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	projectID := r.PathValue("id")
	var created domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.commented", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		created = domain.Comment{ID: fmt.Sprintf("project_comment_%d", time.Now().UnixNano()), Body: strings.TrimSpace(input.Body), BodyData: input.BodyData, Reactions: map[string][]string{}, CreatedAt: time.Now().UTC(), User: data.Viewer}
		project.Comments = append(project.Comments, created)
		project.UpdatedAt = created.CreatedAt
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) createProjectUpdate(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectUpdateCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "update body is required")
		return
	}
	if input.Health != "" && !slices.Contains([]string{"onTrack", "atRisk", "offTrack", "noUpdate"}, input.Health) {
		writeError(w, http.StatusBadRequest, "invalid health")
		return
	}
	id := r.PathValue("id")
	var created domain.ProjectUpdate
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "project.update_created", input, func(data *domain.Bootstrap) (string, error) {
		project, err := fullProjectByID(data, id)
		if err != nil {
			return "", err
		}
		now := time.Now().UTC()
		health := input.Health
		if health == "" {
			health = project.Health
			if health == "noUpdate" {
				health = "onTrack"
			}
		}
		created = domain.ProjectUpdate{ID: fmt.Sprintf("project_update_%d", time.Now().UnixNano()), ProjectID: id, Body: strings.TrimSpace(input.Body), Health: health, CreatedAt: now, User: data.Viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{}}
		if settings, ok := data.Settings["projectUpdates"].(map[string]any); ok {
			if cadence := intFromAny(settings["cadenceDays"]); cadence > 0 {
				dueAt := now.AddDate(0, 0, cadence)
				created.DueAt = &dueAt
			}
		}
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		data.ProjectUpdates[id] = append([]domain.ProjectUpdate{created}, data.ProjectUpdates[id]...)
		project.Health = health
		project.UpdatedAt = now
		for index := range data.Notifications {
			notification := &data.Notifications[index]
			if notification.ProjectID == id && strings.HasPrefix(notification.Type, "projectUpdate") && notification.ArchivedAt == nil {
				notification.ArchivedAt = &now
				notification.ReadAt = &now
				notification.UpdatedAt = now
			}
		}
		return id, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) deleteProjectUpdate(w http.ResponseWriter, r *http.Request) {
	projectID, updateID := r.PathValue("id"), r.PathValue("updateId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.update_deleted", projectID, map[string]string{"id": updateID}, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		updates := data.ProjectUpdates[projectID]
		index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		remaining := slices.Delete(updates, index, index+1)
		data.ProjectUpdates[projectID] = remaining
		if index == 0 {
			if len(remaining) > 0 {
				project.Health = remaining[0].Health
				project.UpdatedAt = remaining[0].CreatedAt
			} else {
				project.Health = "noUpdate"
				project.UpdatedAt = time.Now().UTC()
			}
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) updateProjectUpdate(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectUpdateMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Body != nil && strings.TrimSpace(*input.Body) == "" {
		writeError(w, http.StatusBadRequest, "update body is required")
		return
	}
	if input.Health != nil && !slices.Contains([]string{"onTrack", "atRisk", "offTrack", "noUpdate"}, *input.Health) {
		writeError(w, http.StatusBadRequest, "invalid health")
		return
	}
	projectID, updateID := r.PathValue("id"), r.PathValue("updateId")
	var updated domain.ProjectUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.update_updated", projectID, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		updates := data.ProjectUpdates[projectID]
		index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		if input.Body != nil {
			updates[index].Body = strings.TrimSpace(*input.Body)
		}
		if input.Health != nil {
			updates[index].Health = *input.Health
			if index == 0 {
				project.Health = *input.Health
			}
		}
		now := time.Now().UTC()
		updates[index].EditedAt = &now
		project.UpdatedAt = now
		data.ProjectUpdates[projectID] = updates
		updated = updates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createProjectUpdateComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	projectID, updateID := r.PathValue("id"), r.PathValue("updateId")
	var updated domain.ProjectUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.update_commented", projectID, input, func(data *domain.Bootstrap) error {
		updates := data.ProjectUpdates[projectID]
		index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		comment := domain.Comment{ID: fmt.Sprintf("project_update_comment_%d", time.Now().UnixNano()), Body: strings.TrimSpace(input.Body), Reactions: map[string][]string{}, CreatedAt: time.Now().UTC(), User: data.Viewer}
		updates[index].Comments = append(updates[index].Comments, comment)
		data.ProjectUpdates[projectID] = updates
		updated = updates[index]
		return nil
	})
	respondMutation(w, err, http.StatusCreated, updated)
}

func (s *server) toggleProjectUpdateReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Emoji) == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	projectID, updateID := r.PathValue("id"), r.PathValue("updateId")
	var updated domain.ProjectUpdate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.update_reaction_toggled", projectID, input, func(data *domain.Bootstrap) error {
		updates := data.ProjectUpdates[projectID]
		index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == updateID })
		if index < 0 {
			return errNotFound
		}
		if updates[index].Reactions == nil {
			updates[index].Reactions = map[string][]string{}
		}
		viewerID := data.Viewer.ID
		users := updates[index].Reactions[input.Emoji]
		viewerIndex := slices.Index(users, viewerID)
		if viewerIndex >= 0 {
			users = slices.Delete(users, viewerIndex, viewerIndex+1)
		} else {
			users = append(users, viewerID)
		}
		if len(users) == 0 {
			delete(updates[index].Reactions, input.Emoji)
		} else {
			updates[index].Reactions[input.Emoji] = users
		}
		data.ProjectUpdates[projectID] = updates
		updated = updates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) updateIssue(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated, current domain.Issue
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.updated", id, input, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		if input.ExpectedVersion != nil && issue.Version != *input.ExpectedVersion {
			current = *issue
			return errConflict
		}
		changes, err := applyUpdate(data, issue, input)
		if err != nil {
			return err
		}
		issue.UpdatedAt = time.Now().UTC()
		applySLARules(data, issue, issue.UpdatedAt)
		issue.Version++
		updated = *issue
		activity := appendActivity(data, id, "issue.updated", data.Viewer, changes)
		appendIssueNotifications(data, *issue, activity, nil)
		return nil
	})
	if errors.Is(err, errConflict) {
		writeVersionConflict(w, current)
		return
	}
	if err == nil {
		s.dispatchNotificationEmails(r.Context(), workspaceKey(r))
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteIssue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.deleted", id, map[string]string{"id": id}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == id })
		if index < 0 {
			return errNotFound
		}
		removed := data.Issues[index]
		if err := appendTrash(data, "issue", removed.ID, removed.Identifier+" "+removed.Title, deletedIssuePayload{Issue: removed, Comments: data.Comments[id], Activities: data.Activities[id]}); err != nil {
			return err
		}
		data.Issues = slices.Delete(data.Issues, index, index+1)
		delete(data.Comments, id)
		delete(data.Activities, id)
		removedNotificationIDs := map[string]bool{}
		for _, notification := range data.Notifications {
			if notification.IssueID == id || (notification.SourceType == "issue" && notification.SourceID == id) {
				removedNotificationIDs[notification.ID] = true
			}
		}
		data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool {
			return removedNotificationIDs[item.ID]
		})
		data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool {
			return removedNotificationIDs[item.NotificationID]
		})
		data.Asks = slices.DeleteFunc(data.Asks, func(item domain.Ask) bool { return item.IssueID == id })
		data.IssueSLAs = slices.DeleteFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == id })
		data.SLAEvents = slices.DeleteFunc(data.SLAEvents, func(item domain.SLAEvent) bool { return item.IssueID == id })
		data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool {
			return item.Type == "issue" && item.ResourceID == id
		})
		data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
			return item.ResourceType == "issue" && item.ResourceID == id
		})
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
			return item.ResourceType == "issue" && item.ResourceID == id
		})
		for i := range data.Releases {
			data.Releases[i].IssueIDs = removeString(data.Releases[i].IssueIDs, id)
		}
		for i := range data.ProjectTemplates {
			data.ProjectTemplates[i].IssueIDs = removeString(data.ProjectTemplates[i].IssueIDs, id)
		}
		for i := range data.CustomerRequests {
			if data.CustomerRequests[i].IssueID == id {
				data.CustomerRequests[i].IssueID = ""
			}
		}
		for i := range data.Documents {
			if data.Documents[i].IssueID == id {
				data.Documents[i].IssueID = ""
			}
		}
		for i := range data.Issues {
			data.Issues[i].SubIssueIDs = removeString(data.Issues[i].SubIssueIDs, id)
			data.Issues[i].Relations = slices.DeleteFunc(data.Issues[i].Relations, func(rel domain.IssueRelation) bool { return rel.RelatedIssueID == id || rel.IssueID == id })
			if data.Issues[i].ParentID != nil && *data.Issues[i].ParentID == id {
				data.Issues[i].ParentID = nil
			}
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) batchUpdate(w http.ResponseWriter, r *http.Request) {
	var input domain.BatchIssueUpdateInput
	if !decodeJSON(w, r, &input) || len(input.IssueIDs) == 0 {
		writeError(w, http.StatusBadRequest, "issueIds are required")
		return
	}
	var updated []domain.Issue
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.batch_updated", strings.Join(input.IssueIDs, ","), input, func(data *domain.Bootstrap) error {
		for _, id := range input.IssueIDs {
			issue, err := issueByID(data, id)
			if err != nil {
				return err
			}
			changes, err := applyUpdate(data, issue, input.Update)
			if err != nil {
				return err
			}
			issue.UpdatedAt = time.Now().UTC()
			applySLARules(data, issue, issue.UpdatedAt)
			issue.Version++
			updated = append(updated, *issue)
			activity := appendActivity(data, id, "issue.updated", data.Viewer, changes)
			appendIssueNotifications(data, *issue, activity, nil)
		}
		return nil
	})
	if err == nil {
		s.dispatchNotificationEmails(r.Context(), workspaceKey(r))
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentCreateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	id := r.PathValue("id")
	var comment domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "comment.created", id, input, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		if input.ParentID != nil {
			if slices.IndexFunc(data.Comments[id], func(comment domain.Comment) bool { return comment.ID == *input.ParentID }) < 0 {
				return errNotFound
			}
		}
		comment = domain.Comment{ID: fmt.Sprintf("comment_%d", time.Now().UnixNano()), Version: 1, Body: strings.TrimSpace(input.Body), BodyData: input.BodyData, ParentID: input.ParentID, Reactions: map[string][]string{}, CreatedAt: time.Now().UTC(), User: data.Viewer}
		data.Comments[id] = append(data.Comments[id], comment)
		activity := appendActivity(data, id, "comment.created", data.Viewer, map[string]string{"commentId": comment.ID})
		appendIssueNotifications(data, *issue, activity, &comment)
		return nil
	})
	if err == nil {
		s.dispatchNotificationEmails(r.Context(), workspaceKey(r))
	}
	respondMutation(w, err, http.StatusCreated, comment)
}

func (s *server) updateComment(w http.ResponseWriter, r *http.Request) {
	var input domain.CommentUpdateInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	issueID, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated, current domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "comment.updated", issueID, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Comments[issueID], func(comment domain.Comment) bool { return comment.ID == commentID })
		if index < 0 {
			return errNotFound
		}
		if input.ExpectedVersion != nil && data.Comments[issueID][index].Version != *input.ExpectedVersion {
			current = data.Comments[issueID][index]
			return errConflict
		}
		now := time.Now().UTC()
		data.Comments[issueID][index].Body = strings.TrimSpace(input.Body)
		data.Comments[issueID][index].BodyData = input.BodyData
		data.Comments[issueID][index].EditedAt = &now
		data.Comments[issueID][index].Version++
		updated = data.Comments[issueID][index]
		issue, issueErr := issueByID(data, issueID)
		if issueErr != nil {
			return issueErr
		}
		activity := appendActivity(data, issueID, "comment.updated", data.Viewer, map[string]string{"commentId": commentID})
		appendIssueNotifications(data, *issue, activity, &updated)
		return nil
	})
	if errors.Is(err, errConflict) {
		writeVersionConflict(w, current)
		return
	}
	if err == nil {
		s.dispatchNotificationEmails(r.Context(), workspaceKey(r))
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteComment(w http.ResponseWriter, r *http.Request) {
	issueID, commentID := r.PathValue("id"), r.PathValue("commentId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "comment.deleted", issueID, map[string]string{"commentId": commentID}, func(data *domain.Bootstrap) error {
		before := len(data.Comments[issueID])
		data.Comments[issueID] = slices.DeleteFunc(data.Comments[issueID], func(comment domain.Comment) bool {
			return comment.ID == commentID || (comment.ParentID != nil && *comment.ParentID == commentID)
		})
		if len(data.Comments[issueID]) == before {
			return errNotFound
		}
		appendActivity(data, issueID, "comment.deleted", data.Viewer, map[string]string{"commentId": commentID})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) toggleCommentReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Emoji) == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	issueID, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated domain.Comment
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "comment.reaction_toggled", issueID, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Comments[issueID], func(comment domain.Comment) bool { return comment.ID == commentID })
		if index < 0 {
			return errNotFound
		}
		comment := &data.Comments[issueID][index]
		if comment.Reactions == nil {
			comment.Reactions = map[string][]string{}
		}
		users := comment.Reactions[input.Emoji]
		if slices.Contains(users, data.Viewer.ID) {
			users = removeString(users, data.Viewer.ID)
		} else {
			users = append(users, data.Viewer.ID)
		}
		if len(users) == 0 {
			delete(comment.Reactions, input.Emoji)
		} else {
			comment.Reactions[input.Emoji] = users
		}
		updated = *comment
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) toggleIssueReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Emoji) == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	id := r.PathValue("id")
	var updated domain.Issue
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.reaction_toggled", id, input, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		if issue.Reactions == nil {
			issue.Reactions = map[string][]string{}
		}
		users := issue.Reactions[input.Emoji]
		if slices.Contains(users, data.Viewer.ID) {
			users = removeString(users, data.Viewer.ID)
		} else {
			users = append(users, data.Viewer.ID)
		}
		if len(users) == 0 {
			delete(issue.Reactions, input.Emoji)
		} else {
			issue.Reactions[input.Emoji] = users
		}
		issue.Version++
		issue.UpdatedAt = time.Now().UTC()
		updated = *issue
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createRelation(w http.ResponseWriter, r *http.Request) {
	var input domain.RelationCreateInput
	if !decodeJSON(w, r, &input) || !validRelation(input.Type) {
		writeError(w, http.StatusBadRequest, "invalid relation")
		return
	}
	id := r.PathValue("id")
	var relation domain.IssueRelation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.relation_added", id, input, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		target, err := issueByID(data, input.RelatedIssueID)
		if err != nil {
			return err
		}
		if id == input.RelatedIssueID {
			return errInvalid
		}
		if input.Type == "parent_of" && issueParentCreatesCycle(data, target.ID, issue.ID) || input.Type == "sub_issue_of" && issueParentCreatesCycle(data, issue.ID, target.ID) {
			return fmt.Errorf("%w: issue relation would create a parent cycle", errInvalid)
		}
		relation = domain.IssueRelation{ID: fmt.Sprintf("relation_%d", time.Now().UnixNano()), Type: input.Type, IssueID: id, RelatedIssueID: input.RelatedIssueID}
		issue.Relations = append(issue.Relations, relation)
		inverse := domain.IssueRelation{ID: relation.ID, Type: inverseRelation(input.Type), IssueID: input.RelatedIssueID, RelatedIssueID: id}
		target.Relations = append(target.Relations, inverse)
		if input.Type == "parent_of" {
			setParent(data, target, id)
		} else if input.Type == "sub_issue_of" {
			setParent(data, issue, input.RelatedIssueID)
		}
		appendActivity(data, id, "issue.relation_added", data.Viewer, map[string]string{"type": input.Type, "relatedIssueId": input.RelatedIssueID})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, relation)
}

func (s *server) deleteRelation(w http.ResponseWriter, r *http.Request) {
	id, relationID := r.PathValue("id"), r.PathValue("relationId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.relation_removed", id, map[string]string{"relationId": relationID}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		before := len(issue.Relations)
		var removed domain.IssueRelation
		for _, relation := range issue.Relations {
			if relation.ID == relationID {
				removed = relation
				break
			}
		}
		issue.Relations = slices.DeleteFunc(issue.Relations, func(rel domain.IssueRelation) bool { return rel.ID == relationID })
		if before == len(issue.Relations) {
			return errNotFound
		}
		if target, err := issueByID(data, removed.RelatedIssueID); err == nil {
			target.Relations = slices.DeleteFunc(target.Relations, func(rel domain.IssueRelation) bool { return rel.ID == relationID })
			if removed.Type == "parent_of" {
				setParent(data, target, "")
			} else if removed.Type == "sub_issue_of" {
				setParent(data, issue, "")
			}
		}
		appendActivity(data, id, "issue.relation_removed", data.Viewer, map[string]string{"relationId": relationID})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (20<<20)+(1<<20))
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	id := r.PathValue("id")
	attachmentID := fmt.Sprintf("attachment_%d", time.Now().UnixNano())
	safeName := attachmentID + "_" + filepath.Base(header.Filename)
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	size, copyErr := storage.Put(r.Context(), safeName, io.LimitReader(file, (20<<20)+1), header.Header.Get("Content-Type"))
	if copyErr != nil {
		_ = storage.Delete(r.Context(), safeName)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if size > 20<<20 {
		_ = storage.Delete(r.Context(), safeName)
		writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds 20 MB")
		return
	}
	var attachment domain.Attachment
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "attachment.created", id, map[string]string{"name": header.Filename}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		attachment = domain.Attachment{ID: attachmentID, IssueID: id, Title: header.Filename, URL: "/uploads/" + safeName, ContentType: header.Header.Get("Content-Type"), Size: size, CreatedAt: time.Now().UTC(), Creator: data.Viewer}
		issue.Attachments = append(issue.Attachments, attachment)
		appendActivity(data, id, "attachment.created", data.Viewer, map[string]string{"attachmentId": attachment.ID, "title": attachment.Title})
		return nil
	})
	if err != nil {
		_ = storage.Delete(r.Context(), safeName)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}

func (s *server) createIssueLink(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueLinkInput
	if !decodeJSON(w, r, &input) {
		return
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(input.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "a valid http or https URL is required")
		return
	}
	id := r.PathValue("id")
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = parsed.Host
	}
	var attachment domain.Attachment
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.link_created", id, input, func(data *domain.Bootstrap) error {
		issue, issueErr := issueByID(data, id)
		if issueErr != nil {
			return issueErr
		}
		now := time.Now().UTC()
		attachment = domain.Attachment{ID: fmt.Sprintf("link_%d", now.UnixNano()), IssueID: id, Title: title, URL: parsed.String(), ContentType: "text/uri-list", CreatedAt: now, Creator: data.Viewer}
		issue.Attachments = append(issue.Attachments, attachment)
		appendActivity(data, id, "issue.link_created", data.Viewer, map[string]string{"attachmentId": attachment.ID, "title": attachment.Title, "url": attachment.URL})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, attachment)
}

func (s *server) createIssueReminder(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueReminderInput
	if !decodeJSON(w, r, &input) {
		return
	}
	remindAt, err := time.Parse(time.RFC3339, strings.TrimSpace(input.RemindAt))
	if err != nil || !remindAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "remindAt must be a future RFC3339 timestamp")
		return
	}
	id := r.PathValue("id")
	var reminder domain.Notification
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.reminder_created", id, input, func(data *domain.Bootstrap) error {
		if _, issueErr := issueByID(data, id); issueErr != nil {
			return issueErr
		}
		now := time.Now().UTC()
		remindAt = remindAt.UTC()
		reminder = domain.Notification{
			ID: fmt.Sprintf("notification_%d", now.UnixNano()), RecipientID: data.Viewer.ID,
			Type: "issueReminder", SourceType: "issue", SourceID: id, IssueID: id,
			Actor: data.Viewer, Category: "reminders", GroupKey: "issue-reminder:" + id,
			OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, SnoozedUntil: &remindAt,
			CreatedAt: now, UpdatedAt: now,
		}
		data.Notifications = append([]domain.Notification{reminder}, data.Notifications...)
		appendActivity(data, id, "issue.reminder_created", data.Viewer, map[string]string{"remindAt": remindAt.Format(time.RFC3339)})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, reminder)
}

func (s *server) createProjectReminder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RemindAt string `json:"remindAt"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	remindAt, err := time.Parse(time.RFC3339, input.RemindAt)
	if err != nil || !remindAt.After(time.Now().UTC()) {
		writeError(w, http.StatusBadRequest, "remindAt must be a future RFC3339 timestamp")
		return
	}
	id := r.PathValue("id")
	var reminder domain.Notification
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.reminder_created", id, input, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		reminder = domain.Notification{ID: fmt.Sprintf("notification_project_reminder_%d", time.Now().UnixNano()), RecipientID: data.Viewer.ID, Type: "projectReminder", SourceType: "project", SourceID: id, ProjectID: id, Actor: data.Viewer, Category: "reminders", GroupKey: "project-reminder:" + id + ":" + strconv.FormatInt(remindAt.Unix(), 10), OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, SnoozedUntil: &remindAt, CreatedAt: now, UpdatedAt: now}
		data.Notifications = append([]domain.Notification{reminder}, data.Notifications...)
		appendActivity(data, id, "project.reminder_created", data.Viewer, map[string]string{"projectName": project.Name, "remindAt": remindAt.Format(time.RFC3339)})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, reminder)
}

func (s *server) createIssueLoopRun(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueLoopRunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var run domain.Ask
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.loop_run_created", id, input, func(data *domain.Bootstrap) error {
		issue, issueErr := issueByID(data, id)
		if issueErr != nil {
			return issueErr
		}
		now := time.Now().UTC()
		prompt := strings.TrimSpace(input.Prompt)
		if prompt == "" {
			prompt = issue.Description
		}
		run = domain.Ask{
			ID: fmt.Sprintf("loop_run_%d", now.UnixNano()), Title: "Run loop on " + issue.Identifier,
			Body: prompt, Source: "loop", Requester: data.Viewer, TeamID: issue.Team.ID,
			Status: "approved", IssueID: issue.ID, Approvals: []domain.AskApproval{}, CreatedAt: now, UpdatedAt: now,
		}
		data.Asks = append([]domain.Ask{run}, data.Asks...)
		appendActivity(data, id, "issue.loop_run_created", data.Viewer, map[string]string{"runId": run.ID})
		appendAudit(data, "loop_run_created", "issue", issue.ID, map[string]any{"runId": run.ID})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, run)
}

func (s *server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, attachmentID := r.PathValue("id"), r.PathValue("attachmentId")
	var path string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "attachment.deleted", id, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(issue.Attachments, func(a domain.Attachment) bool { return a.ID == attachmentID })
		if index < 0 {
			return errNotFound
		}
		path = issue.Attachments[index].URL
		issue.Attachments = slices.Delete(issue.Attachments, index, index+1)
		appendActivity(data, id, "attachment.deleted", data.Viewer, map[string]string{"attachmentId": attachmentID})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	if strings.HasPrefix(path, "/uploads/") {
		if storage, storageErr := s.storage(); storageErr == nil {
			_ = storage.Delete(r.Context(), filepath.Base(path))
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) serveUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || filepath.Base(name) != name {
		http.NotFound(w, r)
		return
	}
	if !s.authDisabled {
		user := authUser(r)
		account, err := s.store.AccountForUser(r.Context(), user.ID)
		if err != nil || !s.attachmentVisible(r.Context(), account, user.ID, "/uploads/"+name) {
			http.NotFound(w, r)
			return
		}
	}
	storage, err := s.storage()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	reader, contentType, size, err := storage.Open(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer reader.Close()
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, reader)
}

func (s *server) attachmentVisible(ctx context.Context, account domain.AccountBootstrap, userID, url string) bool {
	for _, membership := range account.Workspaces {
		data, ok, err := s.store.BootstrapForUser(ctx, membership.Workspace.URLKey, userID)
		if err != nil || !ok {
			continue
		}
		for _, issue := range data.Issues {
			if slices.ContainsFunc(issue.Attachments, func(attachment domain.Attachment) bool { return attachment.URL == url }) {
				return true
			}
		}
		for _, request := range data.CustomerRequests {
			if slices.ContainsFunc(request.Attachments, func(attachment domain.Attachment) bool { return attachment.URL == url }) {
				return true
			}
		}
	}
	return false
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.Events(r.Context(), r.URL.Query().Get("aggregateId"))
	respondMutation(w, err, http.StatusOK, events)
}

func hasNotificationMutation(input domain.NotificationMutationInput) bool {
	return input.Read != nil || input.Favorite != nil || input.Archived != nil || input.Deleted != nil || len(input.SnoozedUntil) > 0
}

func parseSnoozedUntil(raw json.RawMessage) (bool, *time.Time, error) {
	if len(raw) == 0 {
		return false, nil, nil
	}
	value := bytes.TrimSpace(raw)
	if bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte(`""`)) {
		return true, nil, nil
	}
	var encoded string
	if err := json.Unmarshal(value, &encoded); err != nil {
		return false, nil, err
	}
	parsed, err := time.Parse(time.RFC3339, encoded)
	if err != nil {
		return false, nil, err
	}
	parsed = parsed.UTC()
	return true, &parsed, nil
}

func notificationEventType(input domain.NotificationMutationInput) string {
	changes := 0
	eventType := "notification.updated"
	if input.Read != nil {
		changes++
		if *input.Read {
			eventType = "notification.read"
		} else {
			eventType = "notification.unread"
		}
	}
	if input.Favorite != nil {
		changes++
		if *input.Favorite {
			eventType = "notification.favorited"
		} else {
			eventType = "notification.unfavorited"
		}
	}
	if input.Archived != nil {
		changes++
		if *input.Archived {
			eventType = "notification.archived"
		} else {
			eventType = "notification.unarchived"
		}
	}
	if input.Deleted != nil {
		changes++
		if *input.Deleted {
			eventType = "notification.deleted"
		} else {
			eventType = "notification.restored"
		}
	}
	if len(input.SnoozedUntil) > 0 {
		changes++
		if bytes.Equal(bytes.TrimSpace(input.SnoozedUntil), []byte("null")) || bytes.Equal(bytes.TrimSpace(input.SnoozedUntil), []byte(`""`)) {
			eventType = "notification.unsnoozed"
		} else {
			eventType = "notification.snoozed"
		}
	}
	if changes != 1 {
		return "notification.updated"
	}
	return eventType
}

func applyNotificationUpdate(notification *domain.Notification, input domain.NotificationMutationInput, snoozeProvided bool, snoozedUntil *time.Time) {
	now := time.Now().UTC()
	if input.Read != nil {
		if *input.Read {
			if notification.ReadAt == nil {
				notification.ReadAt = &now
			}
		} else {
			notification.ReadAt = nil
		}
	}
	if input.Favorite != nil {
		notification.Favorite = *input.Favorite
		if *input.Favorite {
			if notification.FavoritedAt == nil {
				notification.FavoritedAt = &now
			}
		} else {
			notification.FavoritedAt = nil
		}
	}
	if input.Archived != nil {
		if *input.Archived {
			if notification.ArchivedAt == nil {
				notification.ArchivedAt = &now
			}
		} else {
			notification.ArchivedAt = nil
		}
	}
	if input.Deleted != nil {
		if *input.Deleted {
			if notification.DeletedAt == nil {
				notification.DeletedAt = &now
			}
		} else {
			notification.DeletedAt = nil
		}
	}
	if snoozeProvided {
		notification.SnoozedUntil = snoozedUntil
	}
	notification.UpdatedAt = now
}

func applyUpdate(data *domain.Bootstrap, issue *domain.Issue, input domain.IssueUpdateInput) (map[string]string, error) {
	changes := map[string]string{}
	if input.Title != nil {
		changes["title"] = *input.Title
		issue.Title = strings.TrimSpace(*input.Title)
	}
	if input.Description != nil {
		changes["description"] = "updated"
		changes["descriptionBefore"] = issue.Description
		changes["descriptionStateBefore"] = issue.DescriptionState
		issue.Description = *input.Description
		if input.DescriptionData == nil && issue.DocumentContent != nil {
			issue.DocumentContent.Content = *input.Description
			issue.DocumentContent.ContentData = nil
			if input.DescriptionState != nil {
				issue.DocumentContent.ContentState = *input.DescriptionState
			}
			issue.DocumentContent.UpdatedAt = time.Now().UTC()
		}
	}
	if input.DescriptionState != nil {
		issue.DescriptionState = *input.DescriptionState
	}
	if input.DescriptionData != nil {
		content := issue.Description
		if input.Description != nil {
			content = *input.Description
		}
		contentState := ""
		if input.ContentState != nil {
			contentState = *input.ContentState
		} else if issue.DocumentContent != nil {
			contentState = issue.DocumentContent.ContentState
		}
		now := time.Now().UTC()
		if issue.DocumentContent == nil {
			issue.DocumentContent = &domain.DocumentContent{ID: "document_content_" + issue.ID}
		}
		issue.DocumentContent.Content = content
		issue.DocumentContent.ContentData = input.DescriptionData
		issue.DocumentContent.ContentState = contentState
		issue.DocumentContent.UpdatedAt = now
		changes["documentContent"] = issue.DocumentContent.ID
	}
	if input.StateID != nil {
		value := stateForTeam(data, issue.Team.ID, *input.StateID)
		if value == nil {
			return nil, fmt.Errorf("%w: unknown state", errInvalid)
		}
		if value.ID != issue.State.ID {
			now := time.Now().UTC()
			changes["stateBefore"] = issue.State.Name
			changes["stateBeforeId"] = issue.State.ID
			changes["state"] = value.Name
			changes["stateId"] = value.ID
			issue.StatusChangedAt = &now
			if value.Type == "started" && issue.StartedAt == nil {
				issue.StartedAt = &now
			}
			if value.Type == "completed" {
				issue.CompletedAt = &now
				issue.CanceledAt = nil
			} else if value.Type == "canceled" {
				issue.CanceledAt = &now
				issue.CompletedAt = nil
			} else {
				issue.CompletedAt = nil
				issue.CanceledAt = nil
			}
			issue.State = *value
		}
		if value.Type == "started" && issue.Assignee == nil && data.UserSettings[data.Viewer.ID].AssignStarted {
			issue.Assignee = &data.Viewer
			changes["assignee"] = data.Viewer.ID
		}
	}
	if input.Priority != nil {
		issue.Priority = *input.Priority
		issue.PriorityLabel = priorityLabel(*input.Priority)
		changes["priority"] = issue.PriorityLabel
	}
	if input.AssigneeID != nil {
		if issue.Assignee != nil {
			changes["previousAssignee"] = issue.Assignee.ID
		}
		issue.Assignee = userByID(data, *input.AssigneeID)
		if *input.AssigneeID != "" && issue.Assignee == nil {
			return nil, fmt.Errorf("%w: unknown assignee", errInvalid)
		}
		changes["assignee"] = *input.AssigneeID
	}
	if input.DelegateID != nil {
		issue.Delegate = userByID(data, *input.DelegateID)
		if *input.DelegateID != "" && issue.Delegate == nil {
			return nil, fmt.Errorf("%w: unknown delegate", errInvalid)
		}
		changes["delegate"] = *input.DelegateID
	}
	if input.ProjectID != nil {
		issue.Project = projectByID(data, *input.ProjectID)
		if *input.ProjectID != "" && issue.Project == nil {
			return nil, fmt.Errorf("%w: unknown project", errInvalid)
		}
		changes["project"] = *input.ProjectID
		if issue.ProjectMilestoneID != nil {
			project, err := fullProjectByID(data, *input.ProjectID)
			if err != nil || !slices.ContainsFunc(project.Milestones, func(milestone domain.ProjectMilestone) bool { return milestone.ID == *issue.ProjectMilestoneID }) {
				issue.ProjectMilestoneID = nil
			}
		}
	}
	if input.ProjectMilestoneID != nil {
		if *input.ProjectMilestoneID == "" {
			issue.ProjectMilestoneID = nil
		} else {
			if issue.Project == nil {
				return nil, fmt.Errorf("%w: a project milestone requires a project", errInvalid)
			}
			project, err := fullProjectByID(data, issue.Project.ID)
			if err != nil || !slices.ContainsFunc(project.Milestones, func(milestone domain.ProjectMilestone) bool { return milestone.ID == *input.ProjectMilestoneID }) {
				return nil, fmt.Errorf("%w: unknown project milestone", errInvalid)
			}
			issue.ProjectMilestoneID = stringPointer(*input.ProjectMilestoneID)
		}
		changes["projectMilestone"] = *input.ProjectMilestoneID
	}
	if input.CycleID != nil {
		if *input.CycleID == "" {
			issue.CycleID = nil
		} else {
			cycle, err := cycleByID(data, *input.CycleID)
			if err != nil || cycle.TeamID != issue.Team.ID {
				return nil, fmt.Errorf("%w: unknown cycle", errInvalid)
			}
			issue.CycleID = stringPointer(*input.CycleID)
		}
		changes["cycle"] = *input.CycleID
	}
	if input.DueDate != nil {
		if *input.DueDate == "" {
			issue.DueDate = nil
		} else {
			issue.DueDate = input.DueDate
		}
		changes["dueDate"] = *input.DueDate
	}
	if input.SLABreachesAt != nil {
		if *input.SLABreachesAt == "" {
			issue.SLABreachesAt = nil
		} else {
			value, err := time.Parse(time.RFC3339, *input.SLABreachesAt)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid SLA breach time", errInvalid)
			}
			value = value.UTC()
			issue.SLABreachesAt = &value
		}
	}
	if input.SLAType != nil {
		if *input.SLAType != "" && !slices.Contains([]string{"all", "onlyBusinessDays"}, *input.SLAType) {
			return nil, fmt.Errorf("%w: invalid SLA type", errInvalid)
		}
		issue.SLAType = *input.SLAType
	}
	if input.Recurrence != nil {
		value := strings.TrimSpace(*input.Recurrence)
		if value != "" && !slices.Contains([]string{"daily", "weekly", "monthly"}, value) {
			return nil, fmt.Errorf("%w: unknown recurrence", errInvalid)
		}
		issue.Recurrence = value
		changes["recurrence"] = value
	}
	if input.NextOccurrenceAt != nil {
		if strings.TrimSpace(*input.NextOccurrenceAt) == "" {
			issue.NextOccurrenceAt = nil
		} else {
			parsed, err := time.Parse(time.RFC3339, *input.NextOccurrenceAt)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid next occurrence", errInvalid)
			}
			parsed = parsed.UTC()
			issue.NextOccurrenceAt = &parsed
		}
		changes["nextOccurrenceAt"] = *input.NextOccurrenceAt
	}
	if input.LabelIDs != nil {
		issue.Labels = labelsByIDForResource(data, *input.LabelIDs, "issue")
		if len(issue.Labels) != len(*input.LabelIDs) {
			return nil, fmt.Errorf("%w: unknown label", errInvalid)
		}
		changes["labels"] = strings.Join(*input.LabelIDs, ",")
	}
	if input.SubscriberIDs != nil {
		for _, id := range *input.SubscriberIDs {
			if userByID(data, id) == nil {
				return nil, fmt.Errorf("%w: unknown subscriber", errInvalid)
			}
		}
		issue.SubscriberIDs = slices.Clone(*input.SubscriberIDs)
		changes["subscribers"] = strings.Join(*input.SubscriberIDs, ",")
	}
	if input.Archived != nil {
		if *input.Archived {
			now := time.Now().UTC()
			issue.ArchivedAt = &now
		} else {
			issue.ArchivedAt = nil
		}
		changes["archived"] = strconv.FormatBool(*input.Archived)
	}
	if input.ParentID != nil {
		if *input.ParentID == issue.ID {
			return nil, fmt.Errorf("%w: issue cannot parent itself", errInvalid)
		}
		if *input.ParentID != "" {
			if _, err := issueByID(data, *input.ParentID); err != nil {
				return nil, fmt.Errorf("%w: unknown parent", errInvalid)
			}
			if issueParentCreatesCycle(data, issue.ID, *input.ParentID) {
				return nil, fmt.Errorf("%w: issue parent would create a cycle", errInvalid)
			}
		}
		setParent(data, issue, *input.ParentID)
		changes["parent"] = *input.ParentID
	}
	if input.SortOrder != nil {
		issue.SortOrder = *input.SortOrder
		changes["sortOrder"] = strconv.FormatFloat(*input.SortOrder, 'f', -1, 64)
	}
	applyCycleAutomation(data, issue)
	return changes, nil
}

func applySavedViewUpdate(data *domain.Bootstrap, view *domain.SavedView, input domain.SavedViewMutationInput) error {
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		view.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		view.Description = strings.TrimSpace(*input.Description)
	}
	if input.Icon != nil {
		icon := strings.TrimSpace(*input.Icon)
		if icon == "" || len(icon) > 64 {
			return errInvalid
		}
		view.Icon = icon
	}
	if input.Color != nil {
		color := strings.TrimSpace(*input.Color)
		if !validHexColor(color) {
			return errInvalid
		}
		view.Color = color
	}
	if input.Resource != nil {
		if !slices.Contains([]string{"issues", "projects", "initiativeProjects"}, *input.Resource) {
			return errInvalid
		}
		view.Resource = *input.Resource
	}
	if input.Scope != nil {
		if !slices.Contains([]string{"personal", "team", "workspace"}, *input.Scope) {
			return errInvalid
		}
		view.Scope = *input.Scope
	}
	if input.TeamID != nil {
		if *input.TeamID != "" && !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == *input.TeamID }) {
			return errInvalid
		}
		view.TeamID = *input.TeamID
	}
	if input.OwnerID != nil {
		if *input.OwnerID != "" && !slices.ContainsFunc(data.Users, func(user domain.User) bool { return user.ID == *input.OwnerID }) {
			return errInvalid
		}
		view.OwnerID = *input.OwnerID
	}
	if input.Favorite != nil {
		view.Favorite = *input.Favorite
	}
	if input.Subscribed != nil {
		view.Subscribed = *input.Subscribed
	}
	if input.View != nil {
		if !slices.Contains([]string{"active", "backlog", "all"}, *input.View) {
			return errInvalid
		}
		view.View = *input.View
	}
	if input.Filters != nil {
		view.Filters = slices.Clone(input.Filters)
	}
	if input.Display != nil {
		view.Display = slices.Clone(input.Display)
	}
	if input.Insights != nil {
		view.Insights = slices.Clone(input.Insights)
	}
	if view.Scope == "" {
		view.Scope = "workspace"
	}
	if view.OwnerID == "" {
		view.OwnerID = data.Viewer.ID
	}
	if view.Scope != "team" {
		view.TeamID = ""
	}
	if view.Resource == "" {
		view.Resource = "issues"
	}
	if view.Icon == "" {
		view.Icon = "CustomView"
	}
	if view.Color == "" {
		view.Color = "#bec2c8"
	}
	if view.View == "" {
		view.View = "all"
	}
	if view.Filters == nil {
		view.Filters = json.RawMessage("[]")
	}
	if view.Display == nil {
		view.Display = json.RawMessage("{}")
	}
	if view.Insights == nil {
		view.Insights = json.RawMessage("{}")
	}
	return nil
}

func validHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, character := range value[1:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func savedViewByID(data *domain.Bootstrap, id string) (*domain.SavedView, error) {
	for i := range data.SavedViews {
		if data.SavedViews[i].ID == id {
			return &data.SavedViews[i], nil
		}
	}
	return nil, errNotFound
}

func setParent(data *domain.Bootstrap, issue *domain.Issue, parentID string) {
	if issue.ParentID != nil {
		if oldParent, err := issueByID(data, *issue.ParentID); err == nil {
			oldParent.SubIssueIDs = removeString(oldParent.SubIssueIDs, issue.ID)
		}
	}
	if parentID == "" {
		issue.ParentID = nil
		return
	}
	issue.ParentID = &parentID
	if parent, err := issueByID(data, parentID); err == nil {
		parent.SubIssueIDs = appendUnique(parent.SubIssueIDs, issue.ID)
	}
}

func issueParentCreatesCycle(data *domain.Bootstrap, issueID, parentID string) bool {
	seen := map[string]bool{}
	for parentID != "" && !seen[parentID] {
		if parentID == issueID {
			return true
		}
		seen[parentID] = true
		parent, err := issueByID(data, parentID)
		if err != nil || parent.ParentID == nil {
			return false
		}
		parentID = *parent.ParentID
	}
	return parentID != ""
}

func issueByID(data *domain.Bootstrap, id string) (*domain.Issue, error) {
	for i := range data.Issues {
		if data.Issues[i].ID == id {
			return &data.Issues[i], nil
		}
	}
	return nil, errNotFound
}
func notificationByID(data *domain.Bootstrap, id string) (*domain.Notification, error) {
	for i := range data.Notifications {
		if data.Notifications[i].ID == id {
			return &data.Notifications[i], nil
		}
	}
	return nil, errNotFound
}
func stateByID(data *domain.Bootstrap, id string) *domain.WorkflowState {
	for i := range data.States {
		if data.States[i].ID == id {
			return &data.States[i]
		}
	}
	return nil
}
func userByID(data *domain.Bootstrap, id string) *domain.User {
	if id == "" {
		return nil
	}
	for i := range data.Users {
		if data.Users[i].ID == id {
			user := data.Users[i]
			return &user
		}
	}
	return nil
}
func projectByID(data *domain.Bootstrap, id string) *domain.ProjectSummary {
	if id == "" {
		return nil
	}
	for _, project := range data.Projects {
		if project.ID == id {
			return &domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color}
		}
	}
	return nil
}
func fullProjectByID(data *domain.Bootstrap, id string) (*domain.Project, error) {
	for i := range data.Projects {
		if data.Projects[i].ID == id {
			return &data.Projects[i], nil
		}
	}
	return nil, errNotFound
}
func initiativeByID(data *domain.Bootstrap, id string) (*domain.Initiative, error) {
	for i := range data.Initiatives {
		if data.Initiatives[i].ID == id {
			return &data.Initiatives[i], nil
		}
	}
	return nil, errNotFound
}
func defaultProjectStatus(data *domain.Bootstrap) domain.ProjectStatus {
	if len(data.ProjectStatuses) > 0 {
		return data.ProjectStatuses[0]
	}
	if len(data.Projects) > 0 {
		return data.Projects[0].Status
	}
	return domain.ProjectStatus{ID: "project_status_backlog", Name: "Backlog", Color: "#6b6f76", Type: "backlog"}
}
func applyProjectUpdate(data *domain.Bootstrap, project *domain.Project, input domain.ProjectMutationInput) error {
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		project.Name = strings.TrimSpace(*input.Name)
		project.SlugID = slug(project.Name)
	}
	if input.Summary != nil {
		project.Summary = *input.Summary
	}
	if input.Description != nil {
		project.Description = *input.Description
	}
	if input.UpdateCadence != nil {
		if !slices.Contains([]string{"none", "weekly", "biweekly", "monthly"}, *input.UpdateCadence) {
			return errInvalid
		}
		project.UpdateCadence = *input.UpdateCadence
	}
	if input.Icon != nil {
		project.Icon = *input.Icon
	}
	if input.Color != nil {
		project.Color = *input.Color
	}
	if input.StatusID != nil {
		index := slices.IndexFunc(data.ProjectStatuses, func(candidate domain.ProjectStatus) bool { return candidate.ID == *input.StatusID })
		if index < 0 {
			return errInvalid
		}
		project.Status = data.ProjectStatuses[index]
	}
	if input.Priority != nil {
		if *input.Priority < 0 || *input.Priority > 4 {
			return errInvalid
		}
		project.Priority = *input.Priority
		project.PriorityLabel = priorityLabel(*input.Priority)
	}
	if input.Health != nil {
		if !slices.Contains([]string{"onTrack", "atRisk", "offTrack", "noUpdate"}, *input.Health) {
			return errInvalid
		}
		project.Health = *input.Health
	}
	if input.LeadID != nil {
		project.Lead = userByID(data, *input.LeadID)
		if *input.LeadID != "" && project.Lead == nil {
			return errInvalid
		}
	}
	if input.MemberIDs != nil {
		for _, id := range input.MemberIDs {
			if userByID(data, id) == nil {
				return errInvalid
			}
		}
		project.MemberIDs = slices.Clone(input.MemberIDs)
	}
	if input.LabelIDs != nil {
		for _, id := range input.LabelIDs {
			if !labelExistsForResource(data, id, "project") {
				return errInvalid
			}
		}
		project.LabelIDs = slices.Clone(input.LabelIDs)
	}
	if input.TeamIDs != nil {
		for _, id := range input.TeamIDs {
			if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id }) {
				return errInvalid
			}
		}
		project.TeamIDs = slices.Clone(input.TeamIDs)
	}
	if input.DependencyIDs != nil {
		for _, id := range input.DependencyIDs {
			if id == project.ID || !slices.ContainsFunc(data.Projects, func(candidate domain.Project) bool { return candidate.ID == id }) {
				return errInvalid
			}
		}
		project.DependencyIDs = slices.Clone(input.DependencyIDs)
	}
	if input.Initiatives != nil {
		project.Initiatives = normalizedStrings(input.Initiatives)
	}
	if input.Customers != nil {
		project.Customers = normalizedStrings(input.Customers)
	}
	if input.StartDate != nil {
		project.StartDate = optionalString(*input.StartDate)
	}
	if input.StartDateResolution != nil {
		if !slices.Contains([]string{"halfYear", "month", "quarter", "year"}, *input.StartDateResolution) {
			return errInvalid
		}
		project.StartDateResolution = *input.StartDateResolution
	}
	if input.TargetDate != nil {
		project.TargetDate = optionalString(*input.TargetDate)
	}
	if input.TargetDateResolution != nil {
		if !slices.Contains([]string{"halfYear", "month", "quarter", "year"}, *input.TargetDateResolution) {
			return errInvalid
		}
		project.TargetDateResolution = *input.TargetDateResolution
	}
	return nil
}
func applyInitiativeUpdate(data *domain.Bootstrap, initiative *domain.Initiative, input domain.InitiativeMutationInput) error {
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		initiative.Name = strings.TrimSpace(*input.Name)
		initiative.SlugID = slug(initiative.Name)
	}
	if input.Summary != nil {
		initiative.Summary = *input.Summary
	}
	if input.Description != nil {
		if *input.Description != initiative.Description {
			now := time.Now().UTC()
			initiative.DescriptionHistory = append([]domain.InitiativeDescriptionRevision{{ID: fmt.Sprintf("initiative_description_revision_%d", now.UnixNano()), Description: initiative.Description, EditedAt: now, Editor: data.Viewer}}, initiative.DescriptionHistory...)
		}
		initiative.Description = *input.Description
	}
	if input.Icon != nil {
		initiative.Icon = strings.TrimSpace(*input.Icon)
	}
	if input.Color != nil {
		if !validHexColor(*input.Color) {
			return errInvalid
		}
		initiative.Color = *input.Color
	}
	if input.Status != nil {
		if !slices.Contains([]string{"proposed", "planned", "active", "completed", "canceled"}, *input.Status) {
			return errInvalid
		}
		initiative.Status = *input.Status
	}
	if input.Priority != nil {
		if *input.Priority < 0 || *input.Priority > 4 {
			return errInvalid
		}
		initiative.Priority = *input.Priority
		initiative.PriorityLabel = priorityLabel(*input.Priority)
	}
	if input.Health != nil {
		if !slices.Contains([]string{"onTrack", "atRisk", "offTrack", "noUpdate"}, *input.Health) {
			return errInvalid
		}
		initiative.Health = *input.Health
	}
	if input.OwnerID != nil {
		initiative.Owner = userByID(data, *input.OwnerID)
		if *input.OwnerID != "" && initiative.Owner == nil {
			return errInvalid
		}
	}
	if input.LeadTeamID != nil {
		if *input.LeadTeamID != "" && !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == *input.LeadTeamID }) {
			return errInvalid
		}
		initiative.LeadTeamID = *input.LeadTeamID
	}
	if input.ContributingTeamIDs != nil {
		for _, id := range *input.ContributingTeamIDs {
			if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id }) {
				return errInvalid
			}
		}
		initiative.ContributingTeamIDs = normalizedStrings(*input.ContributingTeamIDs)
	}
	if input.LabelIDs != nil {
		for _, id := range *input.LabelIDs {
			if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
				return label.ID == id && labelResourceType(label) == "initiative" && label.ArchivedAt == nil
			}) {
				return errInvalid
			}
		}
		initiative.LabelIDs = slices.Clone(*input.LabelIDs)
	}
	if input.ParentInitiativeIDs != nil {
		for _, id := range *input.ParentInitiativeIDs {
			if id == initiative.ID || !slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id }) {
				return errInvalid
			}
		}
		initiative.ParentInitiativeIDs = normalizedStrings(*input.ParentInitiativeIDs)
	}
	if input.ProjectIDs != nil {
		for _, id := range *input.ProjectIDs {
			if _, err := fullProjectByID(data, id); err != nil {
				return errInvalid
			}
		}
		initiative.ProjectIDs = slices.Clone(*input.ProjectIDs)
	}
	if input.TargetDate != nil {
		initiative.TargetDate = optionalString(*input.TargetDate)
	}
	if input.Favorite != nil {
		initiative.Favorite = *input.Favorite
	}
	if input.Subscribed != nil {
		initiative.Subscribed = *input.Subscribed
	}
	if input.NotificationRules != nil {
		initiative.NotificationRules = *input.NotificationRules
	}
	if input.UpdateSchedule != nil {
		if !slices.Contains([]string{"none", "weekly", "biweekly", "monthly", "custom", "never"}, input.UpdateSchedule.Cadence) || input.UpdateSchedule.Weekday < 0 || input.UpdateSchedule.Weekday > 6 || strings.TrimSpace(input.UpdateSchedule.TimeRange) == "" {
			return errInvalid
		}
		initiative.UpdateSchedule = *input.UpdateSchedule
	}
	return nil
}
func syncInitiativeProjects(data *domain.Bootstrap, initiativeID string, previous, next []string) {
	for i := range data.Projects {
		project := &data.Projects[i]
		if slices.Contains(next, project.ID) {
			project.Initiatives = appendUnique(project.Initiatives, initiativeID)
		} else if slices.Contains(previous, project.ID) || slices.Contains(project.Initiatives, initiativeID) {
			project.Initiatives = removeString(project.Initiatives, initiativeID)
		}
	}
}
func normalizedStrings(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, value), "-")
}
func labelsByID(data *domain.Bootstrap, ids []string) []domain.IssueLabel {
	return labelsByIDForResource(data, ids, "issue")
}
func labelsByIDForResource(data *domain.Bootstrap, ids []string, resourceType string) []domain.IssueLabel {
	result := []domain.IssueLabel{}
	for _, label := range data.Labels {
		if slices.Contains(ids, label.ID) && labelResourceType(label) == resourceType {
			result = append(result, label)
		}
	}
	return result
}
func labelExistsForResource(data *domain.Bootstrap, id string, resourceType string) bool {
	return slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
		return label.ID == id && labelResourceType(label) == resourceType
	})
}
func labelResourceType(label domain.IssueLabel) string {
	if label.ResourceType == "project" || label.ResourceType == "initiative" {
		return label.ResourceType
	}
	return "issue"
}
func appendActivity(data *domain.Bootstrap, issueID, eventType string, actor domain.User, metadata map[string]string) domain.ActivityEvent {
	activity := domain.ActivityEvent{ID: fmt.Sprintf("activity_%d", time.Now().UnixNano()), Type: eventType, CreatedAt: time.Now().UTC(), Actor: actor, Metadata: metadata}
	data.Activities[issueID] = append(data.Activities[issueID], activity)
	return activity
}
func validRelation(value string) bool {
	return slices.Contains([]string{"related", "blocks", "blocked_by", "duplicate", "parent_of", "sub_issue_of"}, value)
}
func inverseRelation(value string) string {
	switch value {
	case "blocks":
		return "blocked_by"
	case "blocked_by":
		return "blocks"
	case "parent_of":
		return "sub_issue_of"
	case "sub_issue_of":
		return "parent_of"
	default:
		return value
	}
}
func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
func removeString(values []string, value string) []string {
	return slices.DeleteFunc(values, func(item string) bool { return item == value })
}
func nextIssueNumber(issues []domain.Issue) int {
	next := 1
	for _, issue := range issues {
		if issue.Number >= next {
			next = issue.Number + 1
		}
	}
	return next
}
func priorityLabel(priority int) string {
	switch priority {
	case 1:
		return "Urgent"
	case 2:
		return "High"
	case 3:
		return "Medium"
	case 4:
		return "Low"
	default:
		return "No priority"
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	return true
}
func queryBool(w http.ResponseWriter, r *http.Request, key string) (bool, bool) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return false, true
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, key+" must be a boolean")
		return false, false
	}
	return parsed, true
}
func optionalQueryBool(w http.ResponseWriter, r *http.Request, key string) (*bool, bool) {
	if !r.URL.Query().Has(key) {
		return nil, true
	}
	parsed, ok := queryBool(w, r, key)
	if !ok {
		return nil, false
	}
	return &parsed, true
}
func respondMutation(w http.ResponseWriter, err error, success int, value any) {
	if errors.Is(err, store.ErrAuthForbidden) {
		writeError(w, http.StatusForbidden, "You don't have permission to perform this action")
		return
	}
	if errors.Is(err, store.ErrAuthExpired) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if errors.Is(err, errInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if value != nil {
		writeJSON(w, success, value)
		return
	}
	w.WriteHeader(success)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeVersionConflict(w http.ResponseWriter, current any) {
	writeJSON(w, http.StatusConflict, map[string]any{"error": "This item changed in another session", "code": "VERSION_CONFLICT", "current": current})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := s.allowedOrigin
		if origin == "" {
			origin = "http://localhost:5173"
		}
		w.Header().Set("Access-Control-Allow-Origin", strings.TrimRight(origin, "/"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Workspace-Key, X-Client-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started).Round(time.Millisecond))
	})
}
