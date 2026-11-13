package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Personal data rights (docs/privacy.md).
//
// "Download your data" (GET/POST /api/account/data-exports, GET
// /api/account/data-exports/{id}/download) builds a zip of JSON files with
// the signed-in person's own data in the background; the download is served
// to them only, until it expires.
//
// Account deletion (DELETE /api/account) and a workspace admin's "Erase
// personal data" (POST /api/workspaces/{key}/members/{userId}/erase) delete a
// person's personal data and keep the workspace content they authored,
// attributed to the pseudonymous "Deleted user" (same opaque id, no name,
// email or avatar). Both need an interactive session (/api/account is closed
// to API keys; the member route is admin-only).

const (
	// personalDataExportTTL is how long a finished export can be downloaded.
	personalDataExportTTL = 7 * 24 * time.Hour
	// personalDataExportWindow/Limit rate-limit the requests per account.
	personalDataExportWindow = 24 * time.Hour
	personalDataExportLimit  = 3
	// personalDataObjectPrefix names the archives in object storage (a flat
	// key: the local driver keeps one directory). No content references
	// them, so the /uploads/ route never serves them.
	personalDataObjectPrefix = "personal_data_export_"
)

func personalDataExportView(item store.PersonalDataExportRecord, now time.Time) domain.PersonalDataExport {
	view := item.PersonalDataExport
	if view.Status == store.PersonalDataExportCompleted && view.ExpiresAt != nil && now.After(*view.ExpiresAt) {
		view.Status = store.PersonalDataExportExpired
	}
	if view.Status == store.PersonalDataExportCompleted {
		view.DownloadURL = "/api/account/data-exports/" + view.ID + "/download"
	}
	return view
}

func (s *server) refuseWithoutAccounts(w http.ResponseWriter) bool {
	if s.authDisabled {
		writeError(w, http.StatusBadRequest, "Personal data exports and account deletion need sign-in to be enabled")
		return true
	}
	return false
}

func (s *server) listPersonalDataExports(w http.ResponseWriter, r *http.Request) {
	if s.refuseWithoutAccounts(w) {
		return
	}
	items, err := s.store.ListPersonalDataExports(r.Context(), authUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list data exports")
		return
	}
	now := time.Now().UTC()
	result := make([]domain.PersonalDataExport, 0, len(items))
	for _, item := range items {
		result = append(result, personalDataExportView(item, now))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createPersonalDataExport(w http.ResponseWriter, r *http.Request) {
	if s.refuseWithoutAccounts(w) {
		return
	}
	user := authUser(r)
	item, created, err := s.store.CreatePersonalDataExport(r.Context(), user.ID, time.Now().UTC(), personalDataExportWindow, personalDataExportLimit)
	if errors.Is(err, store.ErrPersonalDataExportLimited) {
		w.Header().Set("Retry-After", fmt.Sprint(int(personalDataExportWindow/time.Second)))
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not request a data export")
		return
	}
	if created {
		s.auditPersonalData(r.Context(), user.ID, "data_export_requested", map[string]any{"exportId": item.ID})
		go s.buildPersonalDataExport(item.ID, user.ID)
	}
	writeJSON(w, http.StatusAccepted, personalDataExportView(item, time.Now().UTC()))
}

func (s *server) downloadPersonalDataExport(w http.ResponseWriter, r *http.Request) {
	if s.refuseWithoutAccounts(w) {
		return
	}
	user := authUser(r)
	item, err := s.store.PersonalDataExport(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	view := personalDataExportView(item, time.Now().UTC())
	switch view.Status {
	case store.PersonalDataExportCompleted:
	case store.PersonalDataExportExpired:
		writeError(w, http.StatusGone, "This export has expired. Request a new one.")
		return
	case store.PersonalDataExportFailed:
		writeError(w, http.StatusConflict, "This export failed. Request a new one.")
		return
	default:
		writeError(w, http.StatusConflict, "export is still being prepared")
		return
	}
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	reader, _, size, err := storage.Open(r.Context(), item.ObjectKey)
	if err != nil {
		writeError(w, http.StatusGone, "This export is no longer available. Request a new one.")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "flow-data-"+item.CreatedAt.Format("2006-01-02")+".zip"))
	w.Header().Set("Cache-Control", "no-store")
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprint(size))
	}
	_, _ = io.Copy(w, reader)
}

// auditPersonalData records a personal data action in the audit log of every
// workspace the person is an active member of.
func (s *server) auditPersonalData(ctx context.Context, userID, action string, metadata map[string]any) {
	account, err := s.store.AccountForUser(ctx, userID)
	if err != nil {
		return
	}
	for _, membership := range account.Workspaces {
		err := s.store.MutateWorkspaceScoped(store.WithMetadataFields(ctx, "auditLog"), membership.Workspace.URLKey, "audit.workspace_member", userID, nil, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "users", ID: userID}}}, func(data *domain.Bootstrap) error {
			appendAudit(data, action, "account", userID, metadata)
			return nil
		})
		if err != nil {
			log.Printf("personal data audit: %v", err)
		}
	}
}

// buildPersonalDataExport writes the zip to object storage and records the
// result. It runs in the background.
func (s *server) buildPersonalDataExport(id, userID string) {
	ctx := s.store.WorkerContext()
	if started, err := s.store.StartPersonalDataExport(ctx, id); err != nil || !started {
		return
	}
	now := time.Now().UTC()
	key, size, err := s.writePersonalDataArchive(ctx, id, userID)
	failure := ""
	if err != nil {
		log.Printf("personal data export %s failed: %v", id, err)
		failure = "The export could not be prepared"
	}
	if err := s.store.FinishPersonalDataExport(ctx, id, key, size, failure, time.Now().UTC(), now.Add(personalDataExportTTL)); err != nil {
		if errors.Is(err, store.ErrPersonalDataExportGone) && key != "" {
			// Withdrawn while it was built (a workspace purge or the account's
			// erasure): the archive must not outlive its job.
			if storage, storageErr := s.storage(); storageErr == nil {
				if err := storage.Delete(ctx, key); err != nil && !errors.Is(err, os.ErrNotExist) {
					log.Printf("personal data export %s: delete withdrawn archive: %v", id, err)
				}
			}
			return
		}
		log.Printf("personal data export %s: %v", id, err)
	}
}

// expirePersonalDataExports deletes the archives of every queued, running or
// downloadable export of userIDs and marks the jobs expired (a job being
// built deletes its archive when it finishes). It reports how many archives
// it deleted. A workspace purge runs it for the workspace's people, whose
// archives hold the workspace's data.
func (s *server) expirePersonalDataExports(ctx context.Context, userIDs []string) (int64, error) {
	items, err := s.store.LivePersonalDataExports(ctx, userIDs)
	if err != nil || len(items) == 0 {
		return 0, err
	}
	storage, err := s.storage()
	if err != nil {
		return 0, err
	}
	var deleted int64
	for _, item := range items {
		if item.ObjectKey != "" {
			if err := storage.Delete(ctx, item.ObjectKey); err != nil && !errors.Is(err, os.ErrNotExist) {
				return deleted, err
			}
			deleted++
		}
		if err := s.store.ExpirePersonalDataExport(ctx, item.ID); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

func (s *server) writePersonalDataArchive(ctx context.Context, id, userID string) (string, int64, error) {
	file, err := os.CreateTemp("", "flow-personal-data-*.zip")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	archive := zip.NewWriter(file)
	if err := s.writePersonalData(ctx, userID, archive); err != nil {
		return "", 0, err
	}
	if err := archive.Close(); err != nil {
		return "", 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	storage, err := s.storage()
	if err != nil {
		return "", 0, err
	}
	key := personalDataObjectPrefix + id + ".zip"
	size, err := storage.Put(ctx, key, file, "application/zip")
	return key, size, err
}

type personalDataWriter struct {
	archive *zip.Writer
	files   []string
}

func (p *personalDataWriter) add(name string, value any) error {
	writer, err := p.archive.Create(name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	p.files = append(p.files, name)
	return nil
}

const personalDataReadme = `Your Flow data

This archive holds the personal data Flow keeps about your account, as JSON:

  account.json                 your profile and the workspaces you belong to
  identities.json              linked sign-in methods (no secrets or identity provider claims)
  sessions.json                signed-in sessions and devices
  sign-in-notifications.json   sign-in alerts sent to you
  connections.json             personal connections, code host accounts and linked chat accounts (no tokens)
  workspaces/<workspace>/      per workspace you are an active member of:
    settings.json              your profile settings, notification preferences and push subscriptions (no keys)
    agent-chats.json           your Flow Agent conversations (private to you), agent skills and AI conversations
    api-keys.json              your API keys (names, scopes and dates; never the secret)
    passkeys.json              your passkeys (no credential material)
    oauth-grants.json          applications you authorized
    notifications.json         your inbox
    search-history.json        your recent searches
    recently-viewed.json       what you opened recently
    personal.json              your favorites, private views, drafts and subscriptions
    issues.json                issues you created that you can still access
    comments.json              comments you wrote on issues you can still access
    documents.json             documents you created that you can still access

Content you authored stays in the workspace when you delete your account; it is then shown as written by "Deleted user".
`

func (s *server) writePersonalData(ctx context.Context, userID string, archive *zip.Writer) error {
	out := &personalDataWriter{archive: archive}
	readme, err := archive.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(readme, personalDataReadme); err != nil {
		return err
	}
	account, err := s.store.AccountForUser(ctx, userID)
	if err != nil {
		return err
	}
	created, _ := s.store.AccountCreatedAt(ctx, userID)
	workspaces := []map[string]any{}
	for _, membership := range account.Workspaces {
		workspaces = append(workspaces, map[string]any{"urlKey": membership.Workspace.URLKey, "name": membership.Workspace.Name, "role": membership.Role, "joinedAt": membership.JoinedAt})
	}
	if err := out.add("account.json", map[string]any{"profile": account.Viewer, "createdAt": created, "workspaces": workspaces, "exportedAt": time.Now().UTC()}); err != nil {
		return err
	}
	identities, err := s.store.ListAuthIdentities(ctx, userID)
	if err != nil {
		return err
	}
	if err := out.add("identities.json", identities); err != nil {
		return err
	}
	sessions, err := s.store.ListSessions(ctx, userID, "")
	if err != nil {
		return err
	}
	if err := out.add("sessions.json", sessions); err != nil {
		return err
	}
	signIns, err := s.store.AccountSignInEvents(ctx, userID)
	if err != nil {
		return err
	}
	if err := out.add("sign-in-notifications.json", signIns); err != nil {
		return err
	}
	connections, err := s.store.ListPersonalConnections(ctx, userID)
	if err != nil {
		return err
	}
	codeHosts, err := s.store.ListCodeHostAccounts(ctx, userID)
	if err != nil {
		return err
	}
	chats, err := s.store.ChatIdentitiesForUser(ctx, userID)
	if err != nil {
		return err
	}
	chatAccounts := []map[string]any{}
	for _, item := range chats {
		chatAccounts = append(chatAccounts, map[string]any{"workspace": item.WorkspaceKey, "provider": item.Provider, "identity": item.Identity})
	}
	if err := out.add("connections.json", map[string]any{"personalConnections": connections, "codeHostAccounts": codeHosts, "chatAccounts": chatAccounts}); err != nil {
		return err
	}
	for _, membership := range account.Workspaces {
		if err := s.writeWorkspacePersonalData(ctx, out, membership.Workspace.URLKey, userID); err != nil {
			return fmt.Errorf("workspace %s: %w", membership.Workspace.URLKey, err)
		}
	}
	return nil
}

func (s *server) writeWorkspacePersonalData(ctx context.Context, out *personalDataWriter, key, userID string) error {
	readCtx, release, err := s.store.BeginWorkspaceRead(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	data, err := s.store.BootstrapOutline(readCtx, key, userID)
	if err != nil {
		return err
	}
	dir := "workspaces/" + key + "/"
	settings := map[string]any{}
	if value, ok := data.UserSettings[userID]; ok {
		settings["userSettings"] = value
	}
	if value, ok := data.NotificationPreferences[userID]; ok {
		settings["notificationPreferences"] = value
	}
	push := []map[string]any{}
	for _, item := range data.PushSubscriptions {
		if item.UserID == userID {
			push = append(push, map[string]any{"id": item.ID, "userAgent": item.UserAgent, "enabled": item.Enabled, "createdAt": item.CreatedAt})
		}
	}
	settings["pushSubscriptions"] = push
	if err := out.add(dir+"settings.json", settings); err != nil {
		return err
	}
	sessions := []domain.AgentSession{}
	for _, session := range data.AgentSessions {
		if session.UserID != userID {
			continue
		}
		if loaded, err := s.store.LoadAgentSessionBodies(readCtx, key, session); err == nil {
			session = loaded
		}
		sessions = append(sessions, session)
	}
	if err := out.add(dir+"agent-chats.json", map[string]any{
		"conversations":   sessions,
		"skills":          filterOwned(data.AgentSkills, func(item domain.PersonalAgentSkill) bool { return item.UserID == userID }),
		"aiConversations": filterOwned(data.AIConversations, func(item domain.AIConversation) bool { return item.UserID == userID }),
	}); err != nil {
		return err
	}
	keys := []domain.APIKey{}
	for _, item := range data.APIKeys {
		if item.SubjectID() == userID || item.CreatorID == userID {
			item.SecretHash, item.ClientSecretHash = "", ""
			keys = append(keys, item)
		}
	}
	if err := out.add(dir+"api-keys.json", keys); err != nil {
		return err
	}
	passkeys := []domain.Passkey{}
	for _, item := range data.Passkeys {
		if item.UserID == userID {
			passkeys = append(passkeys, passkeyPublic(item))
		}
	}
	if err := out.add(dir+"passkeys.json", passkeys); err != nil {
		return err
	}
	if err := out.add(dir+"oauth-grants.json", filterOwned(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.UserID == userID })); err != nil {
		return err
	}
	notifications := []json.RawMessage{}
	err = s.store.WalkContentRecords(readCtx, key, "notification", func(_ string, raw json.RawMessage) error {
		var item struct {
			RecipientID string `json:"recipientId"`
		}
		if json.Unmarshal(raw, &item) == nil && item.RecipientID == userID {
			notifications = append(notifications, slices.Clone(raw))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := out.add(dir+"notifications.json", notifications); err != nil {
		return err
	}
	searches, err := s.store.SearchHistory(readCtx, data.Workspace.ID, userID, 1000)
	if err != nil {
		return err
	}
	if err := out.add(dir+"search-history.json", searches); err != nil {
		return err
	}
	recent, err := s.store.RecentResources(readCtx, data.Workspace.ID, userID, 1000)
	if err != nil {
		return err
	}
	if err := out.add(dir+"recently-viewed.json", recent); err != nil {
		return err
	}
	if err := out.add(dir+"personal.json", map[string]any{
		"favorites":             filterOwned(data.Favorites, func(item domain.Favorite) bool { return item.UserID == userID }),
		"favoriteFolders":       filterOwned(data.FavoriteFolders, func(item domain.FavoriteFolder) bool { return item.UserID == userID }),
		"privateViews":          filterOwned(data.SavedViews, func(item domain.SavedView) bool { return item.OwnerID == userID && item.Scope == "personal" }),
		"drafts":                filterOwned(data.Drafts, func(item domain.Draft) bool { return item.UserID == userID }),
		"documentContentDrafts": filterOwned(data.DocumentContentDrafts, func(item domain.DocumentContentDraft) bool { return item.UserID == userID }),
		"subscriptions":         filterOwned(data.Subscriptions, func(item domain.Subscription) bool { return item.UserID == userID }),
		"threadSubscriptions":   filterOwned(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool { return item.UserID == userID }),
	}); err != nil {
		return err
	}
	// Content they authored and can still access (the outline is filtered
	// to what they can see).
	visible := map[string]bool{}
	created := []string{}
	for _, issue := range data.Issues {
		visible[issue.ID] = true
		if issue.Creator.ID == userID {
			created = append(created, issue.ID)
		}
	}
	issues := []domain.Issue{}
	if len(created) > 0 {
		err = s.store.WalkIssueRecords(readCtx, store.IssueRecordQuery{Workspace: key, Archived: "all", IssueIDs: created, RestrictToIssueIDs: true}, func(issue domain.Issue) error {
			if visible[issue.ID] && issue.Creator.ID == userID {
				issues = append(issues, issue)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := out.add(dir+"issues.json", issues); err != nil {
		return err
	}
	comments := []json.RawMessage{}
	err = s.store.WalkContentRecords(readCtx, key, "comment", func(resource string, raw json.RawMessage) error {
		var item struct {
			User domain.User `json:"user"`
		}
		if visible[resource] && json.Unmarshal(raw, &item) == nil && item.User.ID == userID {
			comments = append(comments, slices.Clone(raw))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := out.add(dir+"comments.json", comments); err != nil {
		return err
	}
	return out.add(dir+"documents.json", filterOwned(data.Documents, func(item domain.Document) bool { return item.Creator.ID == userID }))
}

func filterOwned[T any](items []T, owned func(T) bool) []T {
	result := []T{}
	for _, item := range items {
		if owned(item) {
			result = append(result, item)
		}
	}
	return result
}

// purgeExpiredPersonalDataExports deletes the archives whose download
// expired (and stale jobs that never finished). Storage maintenance runs it.
func (s *server) purgeExpiredPersonalDataExports(ctx context.Context, now time.Time) (int, error) {
	items, err := s.store.ExpiredPersonalDataExports(ctx, now, 200)
	if err != nil || len(items) == 0 {
		return 0, err
	}
	storage, err := s.storage()
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, item := range items {
		if item.ObjectKey != "" {
			if err := storage.Delete(ctx, item.ObjectKey); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		if err := s.store.ExpirePersonalDataExport(ctx, item.ID); err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}

// deleteAccount handles DELETE /api/account: the signed-in person deletes
// their own account. The body names the account's email (or, without one,
// its display name) as confirmation.
func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if s.refuseWithoutAccounts(w) {
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := s.store.UserByID(r.Context(), authUser(r).ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if user.App || user.Deleted {
		writeError(w, http.StatusBadRequest, "This account cannot be deleted")
		return
	}
	if !personalDataConfirmed(user, input.Confirmation) {
		writeError(w, http.StatusBadRequest, "Type your email address to confirm")
		return
	}
	if s.refuseSoleAdmin(w, r.Context(), user.ID, "") {
		return
	}
	// The workspace writes run as the pseudonymous person: a write records
	// its actor in the directory, which must not bring the name back.
	ctx := store.ContextWithActor(context.WithoutCancel(r.Context()), domain.DeletedUserIdentity(user.ID))
	if err := s.eraseAccount(ctx, user.ID); err != nil {
		log.Printf("account deletion %s: %v", user.ID, err)
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func personalDataConfirmed(user domain.User, confirmation string) bool {
	confirmation = strings.TrimSpace(confirmation)
	if confirmation == "" {
		return false
	}
	if user.Email != "" {
		return strings.EqualFold(confirmation, user.Email)
	}
	return confirmation == user.DisplayName
}

// refuseSoleAdmin answers 409 when the person is the only owner or admin of
// a workspace (other than except): ownership has to be handed over, or the
// workspace deleted, first.
func (s *server) refuseSoleAdmin(w http.ResponseWriter, ctx context.Context, userID, except string) bool {
	keys, err := s.store.SoleAdminWorkspaces(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not check workspace ownership")
		return true
	}
	keys = slices.DeleteFunc(keys, func(key string) bool { return key == except })
	if len(keys) == 0 {
		return false
	}
	workspaces := []map[string]string{}
	for _, key := range keys {
		item := map[string]string{"urlKey": key}
		if data, ok := s.store.WorkspaceMetadataFields(key, "workspace"); ok {
			item["name"] = data.Workspace.Name
		}
		workspaces = append(workspaces, item)
	}
	writeJSON(w, http.StatusConflict, map[string]any{"error": "Make someone else an owner or admin of these workspaces, or delete them, before deleting this account", "code": "LAST_ADMIN", "workspaces": workspaces})
	return true
}

// eraseAccount erases the person from every workspace that knows them, then
// deletes the account's own data and leaves its pseudonymous tombstone.
func (s *server) eraseAccount(ctx context.Context, userID string) error {
	ctx = context.WithoutCancel(ctx)
	keys, err := s.store.AccountWorkspaces(ctx, userID)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := s.erasePersonInWorkspace(ctx, key, userID); err != nil {
			return fmt.Errorf("workspace %s: %w", key, err)
		}
	}
	connections, err := s.store.ListPersonalConnections(ctx, userID)
	if err != nil {
		return err
	}
	for _, item := range connections {
		if _, err := s.store.DeletePersonalConnection(ctx, userID, item.ID); err != nil {
			return err
		}
	}
	accounts, err := s.store.ListCodeHostAccounts(ctx, userID)
	if err != nil {
		return err
	}
	for _, item := range accounts {
		if _, err := s.store.DeleteCodeHostAccount(ctx, userID, item.ID); err != nil {
			return err
		}
	}
	// The archives go before the jobs that name them; a failure stops here so
	// a retry still finds them.
	if _, err := s.expirePersonalDataExports(ctx, []string{userID}); err != nil {
		return fmt.Errorf("data export archives: %w", err)
	}
	// Every audit log chain that names them, the instance log and workspaces
	// they left included, while the account still has its email.
	if _, err := s.store.RedactAuditPerson(ctx, nil, userID, s.accountEmail(ctx, userID)); err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	return s.store.EraseAccount(ctx, userID)
}

// erasePersonInWorkspace erases one person from one workspace: their
// directory entry becomes the pseudonymous identity, their own records
// (settings, notification preferences, agent chats, skills, keys, passkeys,
// application grants, favorites, private views, drafts, subscriptions,
// notifications, search history, recently viewed, chat links) are deleted,
// they leave the workspace as an erased former member, and the stored copies
// of their identity are scrubbed. Content they authored stays.
func (s *server) erasePersonInWorkspace(ctx context.Context, key, userID string) error {
	anonymous := domain.DeletedUserIdentity(userID)
	var sessionIDs []string
	var attachments []domain.AgentMessageAttachment
	err := s.store.MutateWorkspace(ctx, key, "workspace_member.erased", userID, map[string]string{"userId": userID}, func(data *domain.Bootstrap) error {
		sessionIDs, attachments = nil, nil
		cascadeUserIdentity(data, anonymous)
		delete(data.UserSettings, userID)
		delete(data.NotificationPreferences, userID)
		for _, session := range data.AgentSessions {
			if session.UserID == userID {
				sessionIDs = append(sessionIDs, session.ID)
				attachments = append(attachments, messageAttachments(session.Messages)...)
			}
		}
		data.AgentSessions = slices.DeleteFunc(data.AgentSessions, func(item domain.AgentSession) bool { return item.UserID == userID })
		data.AgentSkills = slices.DeleteFunc(data.AgentSkills, func(item domain.PersonalAgentSkill) bool { return item.UserID == userID })
		data.AIConversations = slices.DeleteFunc(data.AIConversations, func(item domain.AIConversation) bool { return item.UserID == userID })
		// Keys acting as the person go; application keys they created for
		// the workspace stay (they act as the application).
		data.APIKeys = slices.DeleteFunc(data.APIKeys, func(item domain.APIKey) bool { return item.SubjectID() == userID })
		data.Passkeys = slices.DeleteFunc(data.Passkeys, func(item domain.Passkey) bool { return item.UserID == userID })
		data.PasskeyRegistrationChallenges = slices.DeleteFunc(data.PasskeyRegistrationChallenges, func(item domain.PasskeyRegistrationChallenge) bool { return item.UserID == userID })
		data.OAuthAuthorizations = slices.DeleteFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.UserID == userID && item.Actor != "app" })
		data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.UserID == userID })
		data.FavoriteFolders = slices.DeleteFunc(data.FavoriteFolders, func(item domain.FavoriteFolder) bool { return item.UserID == userID })
		data.SavedViews = slices.DeleteFunc(data.SavedViews, func(item domain.SavedView) bool { return item.OwnerID == userID && item.Scope == "personal" })
		data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool { return item.UserID == userID })
		data.DocumentContentDrafts = slices.DeleteFunc(data.DocumentContentDrafts, func(item domain.DocumentContentDraft) bool { return item.UserID == userID })
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.UserID == userID })
		data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool { return item.UserID == userID })
		data.PushSubscriptions = slices.DeleteFunc(data.PushSubscriptions, func(item domain.PushSubscription) bool { return item.UserID == userID })
		data.ImportJobs = slices.DeleteFunc(data.ImportJobs, func(item domain.ImportJob) bool { return item.UserID == userID })
		data.ExportJobs = slices.DeleteFunc(data.ExportJobs, func(item domain.ExportJob) bool { return item.UserID == userID })
		appendAudit(data, "erased", "workspace_member", userID, nil)
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.store.DeleteAgentSessionRuns(ctx, key, sessionIDs); err != nil {
		return err
	}
	s.deleteAgentAttachments(ctx, attachments)
	if err := s.store.ErasePersonFromWorkspace(ctx, key, userID); err != nil {
		return err
	}
	s.pruneIssueSuggestionTargets(ctx, key, func(item domain.IssueSuggestion) bool {
		return item.Type == "assignee" && item.SuggestedUserID == userID
	})
	if _, err := s.store.ScrubPersonCopies(ctx, key, userID); err != nil {
		return err
	}
	// The workspace's audit log keeps what they did, as the pseudonymous
	// person (store/audit_redaction.go); the chain still verifies.
	workspaceID, err := s.store.WorkspaceIDForKey(ctx, key)
	if err != nil {
		return err
	}
	_, err = s.store.RedactAuditPerson(ctx, []string{workspaceID}, userID, s.accountEmail(ctx, userID))
	return err
}

// accountEmail is the account's email while it still has one ("" after the
// account was erased or when it has none).
func (s *server) accountEmail(ctx context.Context, userID string) string {
	user, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return ""
	}
	return user.Email
}

// eraseMember handles POST /api/workspaces/{workspaceKey}/members/{userId}/erase:
// a workspace admin erases a member's (or former member's) personal data.
// When the account belongs to no other workspace it has no other use and is
// deleted as a whole (scope "account"); otherwise only what this workspace
// keeps about them is erased and the account lives on (scope "workspace").
func (s *server) eraseMember(w http.ResponseWriter, r *http.Request) {
	if s.refuseWithoutAccounts(w) {
		return
	}
	key, userID := r.PathValue("workspaceKey"), r.PathValue("userId")
	data, ok := s.store.WorkspaceMetadata(key)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	actor := authUser(r)
	if userID == actor.ID {
		writeError(w, http.StatusBadRequest, "Delete your own account from your account settings instead")
		return
	}
	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil || target.App || target.Deleted {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	member, err := s.store.HasWorkspaceMembership(r.Context(), data.Workspace.ID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if erased, _ := s.store.ErasedFromWorkspace(r.Context(), data.Workspace.ID, userID); erased {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	former, _ := s.store.FormerMembers(r.Context(), key)
	if !member && !former[userID] {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if s.refuseSCIMManagedMember(w, r, data.Workspace.ID, userID) {
		return
	}
	if member {
		role, status, _ := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, userID)
		actorRole, _, _ := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, actor.ID)
		if role == "owner" && actorRole != "owner" {
			writeError(w, http.StatusForbidden, "Only an owner can erase another owner")
			return
		}
		if workspaceAdminRole(role) && status == "active" {
			if sole, err := s.store.SoleAdminWorkspaces(r.Context(), userID); err == nil && slices.Contains(sole, key) {
				writeError(w, http.StatusConflict, store.ErrLastAdmin.Error())
				return
			}
		}
	}
	if !personalDataConfirmed(target, input.Confirmation) {
		writeError(w, http.StatusBadRequest, "Type the member's email address to confirm")
		return
	}
	other, err := s.store.OtherWorkspaceMemberships(r.Context(), data.Workspace.ID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	scope := "workspace"
	if !other {
		// The account exists only for this workspace: delete it as a whole.
		// A workspace it is the sole admin of would be another workspace.
		scope = "account"
		err = s.eraseAccount(r.Context(), userID)
	} else {
		err = s.erasePersonInWorkspace(context.WithoutCancel(r.Context()), key, userID)
	}
	if err != nil {
		log.Printf("member erasure %s in %s: %v", userID, key, err)
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"userId": userID, "scope": scope})
}
