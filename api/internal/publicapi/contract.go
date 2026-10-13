package publicapi

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Param documents one query or path parameter.
type Param struct {
	Name        string
	In          string // "query" (default) or "path"
	Type        string // string (default), integer, boolean
	Description string
	Required    bool
	Enum        []string
	Array       bool
}

// Operation is the typed contract of one route. Request and Response hold a
// zero value of the Go type that the handler decodes and encodes.
type Operation struct {
	Method      string
	Path        string
	Tag         string
	Summary     string
	Description string
	Params      []Param
	Request     any
	// RequestBinary marks a raw file body (PUT uploads).
	RequestBinary bool
	Response      any
	// ResponseBinary marks a file download.
	ResponseBinary bool
	// Status is the success status (200 when zero; 204 when Response is nil).
	Status int
	// AltStatus is a second success status with the same body (200 for an
	// upsert that updated instead of created).
	AltStatus int
	// Connection adds first/after/last/before/orderBy/includeArchived.
	Connection bool
	// Orderings narrows orderBy (default createdAt, updatedAt).
	Orderings []string
	// Filter documents the `filter` object and its fields.
	Filter []string
	// Legacy is the response returned without pagination arguments by list
	// routes that predate connections.
	Legacy any
	// Public operations need no credentials (their own token or signature).
	Public bool
	// Scopes lists the OAuth/API key scopes that grant the operation.
	Scopes []string
}

var (
	readScopes  = []string{"read"}
	writeScopes = []string{"write"}
	adminScopes = []string{"admin"}
)

func connectionOf(tag, path, summary string, node any, filter []string, extra ...Param) Operation {
	return Operation{Method: "GET", Path: path, Tag: tag, Summary: summary, Response: node, Connection: true, Filter: filter, Params: extra, Scopes: readScopes}
}

var teamParam = Param{Name: "teamId", Description: "Only resources of these teams (comma-separated ids)."}

// Operations is the documented public REST surface. Routes not listed here
// still appear in the OpenAPI inventory with the generic error envelope.
var Operations = []Operation{
	{Method: "GET", Path: "/api/viewer", Tag: "identity", Summary: "Current user or application", Description: "Who the credentials belong to: user, workspace, role, actor (user or app), authentication kind, scopes and team restriction. Works for sessions, API keys and OAuth tokens.", Response: Viewer{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/organization", Tag: "identity", Summary: "Current workspace", Response: Organization{}, Scopes: readScopes},

	connectionOf("teams", "/api/teams", "List teams", domain.Team{}, []string{"id", "name", "key", "private", "parent", "createdAt", "updatedAt", "archivedAt"}),
	connectionOf("users", "/api/users", "List users", domain.User{}, []string{"id", "name", "displayName", "username", "email", "active", "app", "admin", "isMe", "createdAt"}, Param{Name: "includeDisabled", Type: "boolean", Description: "Include deactivated users."}),
	withOrderings(connectionOf("workflow-states", "/api/workflow-states", "List workflow states", domain.WorkflowState{}, []string{"id", "name", "type", "position", "team"}, teamParam), "createdAt"),
	withOrderings(connectionOf("labels", "/api/issue-labels", "List issue labels", domain.IssueLabel{}, []string{"id", "name", "color", "team", "parent", "createdAt", "archivedAt"}, teamParam, Param{Name: "resourceType", Enum: []string{"issue", "project", "all"}, Description: "Label kind (default issue)."}), "createdAt"),
	connectionOf("cycles", "/api/cycles", "List cycles", domain.Cycle{}, []string{"id", "name", "number", "status", "team", "startsAt", "endsAt", "isActive", "createdAt", "updatedAt"}, teamParam),
	connectionOf("projects", "/api/project-milestones", "List project milestones", domain.ProjectMilestone{}, []string{"id", "name", "project", "targetDate", "createdAt", "updatedAt"}, Param{Name: "projectId", Description: "Only milestones of these projects (comma-separated ids or slugs)."}),
	{Method: "GET", Path: "/api/initiatives", Tag: "initiatives", Summary: "List initiatives", Response: domain.Initiative{}, Connection: true, Filter: []string{"id", "name", "slugId", "status", "health", "owner", "creator", "targetDate", "projects", "parentInitiatives", "createdAt", "updatedAt"}, Scopes: []string{"read", "initiative:read"}},
	{Method: "GET", Path: "/api/projects", Tag: "projects", Summary: "List projects", Description: "With first/last/after/before/orderBy the response is a connection; without them the project directory page (limit/cursor/filter) is returned for existing clients.", Response: domain.Project{}, Connection: true, Legacy: store.ProjectRecordPage{}, Filter: []string{"id", "name", "slugId", "health", "priority", "status", "lead", "teams", "labels", "members", "initiatives", "startDate", "targetDate", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{teamParam}, Scopes: readScopes},
	{Method: "GET", Path: "/api/documents", Tag: "documents", Summary: "List documents", Description: "Connection with pagination arguments; otherwise the legacy array (q, teamId, archived).", Response: domain.Document{}, Connection: true, Legacy: []domain.Document{}, Filter: []string{"id", "title", "slugId", "creator", "teams", "projects", "issue", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{teamParam}, Scopes: readScopes},
	{Method: "GET", Path: "/api/releases", Tag: "releases", Summary: "List releases", Description: "Connection with pagination arguments; otherwise the legacy array.", Response: domain.Release{}, Connection: true, Legacy: []domain.Release{}, Filter: []string{"id", "name", "slugId", "status", "pipeline", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{{Name: "pipelineId", Description: "Only releases of these pipelines."}}, Scopes: []string{"read", "release:read"}},
	{Method: "GET", Path: "/api/webhooks", Tag: "webhooks", Summary: "List webhooks", Description: "Workspace admins (or the admin scope). Connection with pagination arguments; otherwise the legacy array.", Response: domain.Webhook{}, Connection: true, Legacy: []domain.Webhook{}, Filter: []string{"id", "name", "url", "enabled", "resourceTypes", "teams", "createdAt", "updatedAt"}, Scopes: adminScopes},
	{Method: "GET", Path: "/api/customers", Tag: "customers", Summary: "List customers", Response: domain.Customer{}, Connection: true, Filter: []string{"id", "name", "domains", "externalIds", "status", "tier", "owner", "revenue", "size", "createdAt", "updatedAt"}, Scopes: []string{"read", "customer:read"}},
	{Method: "PUT", Path: "/api/customers", Tag: "customers", Summary: "Upsert a customer", Description: "Matches an existing customer by id, then external id(s), then domain; merges domains and external ids. Creates the customer (201) when nothing matches; name is then required.", Request: CustomerUpsertInput{}, Response: domain.Customer{}, Status: 201, AltStatus: 200, Scopes: []string{"write", "customer:write"}},
	{Method: "POST", Path: "/api/customers", Tag: "customers", Summary: "Create a customer", Request: CustomerInput{}, Response: domain.Customer{}, Status: 201, Scopes: []string{"write", "customer:write"}},
	{Method: "PATCH", Path: "/api/customers/{id}", Tag: "customers", Summary: "Update a customer", Request: CustomerInput{}, Response: domain.Customer{}, Scopes: []string{"write", "customer:write"}},
	{Method: "GET", Path: "/api/customer-requests", Tag: "customers", Summary: "List customer requests (needs)", Response: domain.CustomerRequest{}, Connection: true, Filter: []string{"id", "body", "customer", "issue", "project", "priority", "source", "creator", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{{Name: "customerId"}, {Name: "issueId"}, {Name: "projectId"}}, Scopes: []string{"read", "customer:read"}},
	{Method: "POST", Path: "/api/customer-requests", Tag: "customers", Summary: "Create a customer request (need)", Description: "Select the customer with customerId or customerExternalId. attachmentUrl adds a link attachment to the request's issue.", Request: CustomerRequestInput{}, Response: domain.CustomerRequest{}, Status: 201, Scopes: []string{"write", "customer:write"}},
	{Method: "PATCH", Path: "/api/customer-requests/{id}", Tag: "customers", Summary: "Update a customer request", Request: CustomerRequestInput{}, Response: domain.CustomerRequest{}, Scopes: []string{"write", "customer:write"}},

	withOrderings(Operation{Method: "GET", Path: "/api/comments", Tag: "comments", Summary: "List comments of one parent", Description: "Provide exactly one of issueId, documentId, projectId or initiativeId. Issue and document comments are ordered by createdAt and do not accept filter.", Response: domain.Comment{}, Connection: true, Filter: []string{"id", "body", "user", "parent", "resolved", "createdAt", "updatedAt"}, Params: []Param{{Name: "issueId", Description: "Issue id or identifier (ENG-123)."}, {Name: "documentId"}, {Name: "projectId"}, {Name: "initiativeId"}}, Scopes: readScopes}),
	{Method: "POST", Path: "/api/issues/{id}/comments", Tag: "comments", Summary: "Create a comment", Request: domain.CommentCreateInput{}, Response: domain.Comment{}, Status: 201, Scopes: []string{"write", "create_comments", "comments:create"}},
	{Method: "PATCH", Path: "/api/issues/{id}/comments/{commentId}", Tag: "comments", Summary: "Update a comment", Request: domain.CommentUpdateInput{}, Response: domain.Comment{}, Scopes: writeScopes},

	{Method: "GET", Path: "/api/attachments", Tag: "attachments", Summary: "List attachments by issue or URL", Description: "Provide issueId and/or url (the reference app's attachmentsForURL).", Response: domain.Attachment{}, Connection: true, Filter: []string{"id", "title", "subtitle", "url", "contentType", "sourceType", "issue", "creator", "createdAt", "updatedAt"}, Params: []Param{{Name: "issueId"}, {Name: "url"}}, Scopes: readScopes},
	{Method: "GET", Path: "/api/attachments/{id}", Tag: "attachments", Summary: "Get an attachment", Response: domain.Attachment{}, Scopes: readScopes},
	{Method: "POST", Path: "/api/issues/{id}/link-attachments", Tag: "attachments", Summary: "Create or update a link attachment", Description: "Idempotent by URL per issue: an existing attachment with the same URL is updated (200) instead of duplicated (201).", Request: LinkAttachmentInput{}, Response: domain.Attachment{}, Status: 201, AltStatus: 200, Scopes: writeScopes},
	{Method: "PATCH", Path: "/api/issues/{id}/attachments/{attachmentId}", Tag: "attachments", Summary: "Update an attachment's title, subtitle, icon or metadata", Request: LinkAttachmentInput{}, Response: domain.Attachment{}, Scopes: writeScopes},
	{Method: "DELETE", Path: "/api/issues/{id}/attachments/{attachmentId}", Tag: "attachments", Summary: "Delete an attachment", Scopes: writeScopes},

	{Method: "POST", Path: "/api/files", Tag: "files", Summary: "Prepare a file upload", Description: "Returns uploadUrl (PUT the bytes with the listed headers within 15 minutes) and assetUrl (use it in Markdown or attachments).", Request: FileUploadInput{}, Response: FileUpload{}, Scopes: writeScopes},
	{Method: "PUT", Path: "/api/files/uploads/{token}", Tag: "files", Summary: "Upload file bytes", Description: "The token in the URL is the credential. Size must equal the prepared size; one successful PUT per token.", RequestBinary: true, Public: true},
	{Method: "GET", Path: "/api/files/sign", Tag: "files", Summary: "Sign file URLs", Description: "Short-lived URLs that download without credentials. Alternatively send X-Flow-File-Urls-Expire-In (or Public-File-Urls-Expire-In) on any API request to sign every /uploads/ URL in its response.", Params: []Param{{Name: "url", Required: true, Array: true, Description: "File URL (/uploads/…); repeat up to 100 times."}, {Name: "expiresIn", Type: "integer", Description: "Lifetime in seconds (1-604800, default 3600)."}}, Response: SignedFiles{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/files/signed/{name}", Tag: "files", Summary: "Download through a signed URL", Params: []Param{{Name: "expires", Type: "integer", Required: true}, {Name: "signature", Required: true}}, ResponseBinary: true, Public: true},
	{Method: "GET", Path: "/uploads/{name}", Tag: "files", Summary: "Download a file", Description: "Authenticated with the session or the Authorization header, like any API call.", ResponseBinary: true, Scopes: readScopes},

	{Method: "GET", Path: "/api/workspace/audit-log", Tag: "audit", Summary: "List audit log entries", Description: "Admins. Filters: type (resourceType.action or action), actorId, actorEmail, resourceType, resourceId, ip, since, until, or a filter object. Connection with pagination arguments; otherwise an array.", Response: domain.AuditLogEntry{}, Connection: true, Legacy: []domain.AuditLogEntry{}, Orderings: []string{"createdAt"}, Filter: []string{"id", "type", "action", "resourceType", "resourceId", "actor", "ip", "createdAt"}, Params: []Param{{Name: "type"}, {Name: "actorId"}, {Name: "actorEmail"}, {Name: "resourceType"}, {Name: "resourceId"}, {Name: "ip"}, {Name: "since", Description: "RFC 3339, YYYY-MM-DD or ISO 8601 duration (-P7D)."}, {Name: "until"}}, Scopes: adminScopes},
	{Method: "GET", Path: "/api/workspace/audit-log/types", Tag: "audit", Summary: "Audit entry types present in the log", Response: []AuditEntryTypeCount{}, Scopes: adminScopes},

	{Method: "GET", Path: "/api/issue-records", Tag: "issues", Summary: "Query issues", Description: "Cursor paging with limit/cursor (hasMore/nextCursor), sort, direction, teamId, projectId, stateId and the view filter tree in `filter`.", Response: store.IssueRecordPage{}, Params: []Param{{Name: "limit", Type: "integer"}, {Name: "cursor"}, {Name: "sort", Enum: []string{"sortOrder", "priority", "createdAt", "updatedAt", "title"}}, {Name: "direction", Enum: []string{"asc", "desc"}}, {Name: "q"}, teamParam, {Name: "projectId"}, {Name: "stateId"}, {Name: "archived", Enum: []string{"true", "false", "all"}}, {Name: "filter", Description: "JSON issue filter tree {and,or,not,field,operator,values}."}}, Scopes: readScopes},
	{Method: "GET", Path: "/api/issue-records/{id}", Tag: "issues", Summary: "Get an issue by id or identifier", Response: domain.Issue{}, Scopes: readScopes},
	{Method: "POST", Path: "/api/issues", Tag: "issues", Summary: "Create an issue", Request: domain.IssueCreateInput{}, Response: domain.Issue{}, Status: 201, Scopes: []string{"write", "create_issues", "issues:create"}},
	{Method: "PATCH", Path: "/api/issues/{id}", Tag: "issues", Summary: "Update an issue", Request: domain.IssueUpdateInput{}, Response: domain.Issue{}, Scopes: []string{"write", "create_issues", "issues:create"}},
	{Method: "DELETE", Path: "/api/issues/{id}", Tag: "issues", Summary: "Delete an issue", Scopes: writeScopes},
	{Method: "POST", Path: "/api/projects", Tag: "projects", Summary: "Create a project", Request: domain.ProjectMutationInput{}, Response: domain.Project{}, Status: 201, Scopes: writeScopes},
	{Method: "PATCH", Path: "/api/projects/{id}", Tag: "projects", Summary: "Update a project", Request: domain.ProjectMutationInput{}, Response: domain.Project{}, Scopes: writeScopes},
	{Method: "GET", Path: "/api/documents/{id}", Tag: "documents", Summary: "Get a document", Response: domain.Document{}, Scopes: readScopes},
	{Method: "POST", Path: "/api/initiatives", Tag: "initiatives", Summary: "Create an initiative", Request: domain.InitiativeMutationInput{}, Response: domain.Initiative{}, Status: 201, Scopes: []string{"write", "initiative:write"}},
	{Method: "PATCH", Path: "/api/initiatives/{id}", Tag: "initiatives", Summary: "Update an initiative", Request: domain.InitiativeMutationInput{}, Response: domain.Initiative{}, Scopes: []string{"write", "initiative:write"}},
}

func withOrderings(operation Operation, orderings ...string) Operation {
	if len(orderings) > 0 {
		operation.Orderings = orderings
	} else {
		operation.Orderings = []string{"createdAt"}
	}
	return operation
}
