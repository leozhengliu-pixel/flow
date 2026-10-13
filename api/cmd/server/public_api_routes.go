package main

import "net/http"

// registerPublicAPIRoutes adds the public REST platform routes: identity,
// connection list endpoints, file storage, link attachments and customer
// upserts. Legacy list routes gain connection forms in main.go (connectionOr).
func (s *server) registerPublicAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/viewer", s.getViewer)
	mux.HandleFunc("GET /api/organization", s.getOrganization)
	mux.HandleFunc("GET /api/teams", s.listTeamsConnection)
	mux.HandleFunc("GET /api/teams/{id}", s.getTeamRecord)
	s.registerTeamMemberRoutes(mux)
	mux.HandleFunc("GET /api/users", s.listUsersConnection)
	mux.HandleFunc("GET /api/users/{id}", s.getUserRecord)
	mux.HandleFunc("GET /api/workflow-states", s.listWorkflowStatesConnection)
	mux.HandleFunc("GET /api/workflow-states/{id}", s.getWorkflowStateRecord)
	mux.HandleFunc("GET /api/issue-labels", s.listIssueLabelsConnection)
	mux.HandleFunc("GET /api/issue-labels/{id}", s.getIssueLabelRecord)
	mux.HandleFunc("GET /api/cycles", s.listCyclesConnection)
	mux.HandleFunc("GET /api/cycles/{id}", s.getCycleRecord)
	mux.HandleFunc("GET /api/project-milestones", s.listProjectMilestonesConnection)
	mux.HandleFunc("GET /api/initiatives", s.listInitiativesConnection)
	mux.HandleFunc("GET /api/initiatives/{id}", s.getInitiativeRecord)
	mux.HandleFunc("GET /api/projects/{id}", s.getProjectRecord)
	mux.HandleFunc("GET /api/comments", s.listCommentsConnection)
	mux.HandleFunc("GET /api/attachments", s.listAttachmentsConnection)
	mux.HandleFunc("GET /api/attachments/{id}", s.getAttachment)
	mux.HandleFunc("GET /api/customers", s.listCustomersConnection)
	mux.HandleFunc("GET /api/customers/{id}", s.getCustomerRecord)
	mux.HandleFunc("PUT /api/customers", s.upsertCustomer)
	mux.HandleFunc("GET /api/customer-requests", s.listCustomerNeedsConnection)
	mux.HandleFunc("POST /api/issues/{id}/link-attachments", s.upsertLinkAttachment)
	mux.HandleFunc("POST /api/issue-records/{id}/link-attachments", s.issueRecordAlias(s.upsertLinkAttachment))
	mux.HandleFunc("PATCH /api/issues/{id}/attachments/{attachmentId}", s.updateLinkAttachment)
	mux.HandleFunc("PATCH /api/issue-records/{id}/attachments/{attachmentId}", s.issueRecordAlias(s.updateLinkAttachment))
	mux.HandleFunc("POST /api/files", s.createFileUpload)
	mux.HandleFunc("PUT /api/files/uploads/{token}", s.putFileUpload)
	mux.HandleFunc("GET /api/files/sign", s.signFileURLs)
	mux.HandleFunc("GET /api/files/signed/{name}", s.serveSignedFile)
}
