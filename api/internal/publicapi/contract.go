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
	// Form says which response shape of a route with two (Operation.Legacy)
	// reads the parameter: "legacy", "connection" or both when empty.
	Form string
	// JSON, when set, makes the parameter a JSON document in the query with
	// this schema (used for filters).
	JSON map[string]any
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
	// FilterType, when set, types the `filter` parameter as this Go struct
	// (the issue query API's filter tree) instead of a free-form object.
	FilterType any
	// MaxPageSize narrows the largest first/last of a connection (default 250).
	MaxPageSize int
	// Legacy is the response returned without pagination arguments by list
	// routes that predate connections. The connection form is selected by any
	// of first, last, after, before, orderBy or connection=1.
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
	{Method: "GET", Path: "/api/projects", Tag: "projects", Summary: "List projects", Description: "With first/last/after/before/orderBy the response is a connection; without them the project directory page (limit/cursor/filter) is returned for existing clients.", Response: domain.Project{}, Connection: true, Legacy: store.ProjectRecordPage{}, Filter: []string{"id", "name", "slugId", "health", "priority", "status", "lead", "teams", "labels", "members", "initiatives", "startDate", "targetDate", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{teamParam, {Name: "q", Form: "legacy", Description: "Text search over name and summary."}, {Name: "archived", Form: "legacy", Enum: []string{"true", "false", "all"}, Description: "false (default) returns active projects, true only archived ones, all both."}, {Name: "limit", Type: "integer", Form: "legacy", Description: "Page size of the original form (default and maximum as the directory allows)."}, {Name: "cursor", Form: "legacy", Description: "nextCursor of the previous page."}, {Name: "includeTotal", Type: "boolean", Form: "legacy", Description: "Count all matching projects (total)."}, {Name: "filter", JSON: map[string]any{"oneOf": []any{map[string]any{"type": "object", "additionalProperties": true, "description": "Connection form: a filter object."}, map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"field", "operator", "values"}, "properties": map[string]any{"field": map[string]any{"type": "string", "enum": []string{"status", "priority", "lead", "members", "health", "dates", "milestones", "labels", "teams", "project", "customers"}}, "operator": map[string]any{"type": "string", "enum": []string{"is", "isNot", "gte", "lte", "eq", "neq"}}, "values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}, "description": "Original form: conditions that must all match."}}}, Description: "A filter object in the connection form; an array of {field, operator, values} conditions in the original form. Filterable fields of the connection form: id, name, slugId, health, priority, status, lead, teams, labels, members, initiatives, startDate, targetDate, createdAt, updatedAt, archivedAt."}}, Scopes: readScopes},
	{Method: "GET", Path: "/api/documents", Tag: "documents", Summary: "List documents", Description: "Connection with pagination arguments; otherwise the legacy array (q, teamId, archived).", Response: domain.Document{}, Connection: true, Legacy: []domain.Document{}, Filter: []string{"id", "title", "slugId", "creator", "teams", "projects", "issue", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{teamParam, {Name: "q", Form: "legacy", Description: "Text search over title and content."}, {Name: "archived", Form: "legacy", Enum: []string{"true", "false", "all"}, Description: "false (default) returns active documents, true only archived ones, all both."}}, Scopes: readScopes},
	{Method: "GET", Path: "/api/releases", Tag: "releases", Summary: "List releases", Description: "Connection with pagination arguments; otherwise the legacy array.", Response: domain.Release{}, Connection: true, Legacy: []domain.Release{}, Filter: []string{"id", "name", "slugId", "status", "pipeline", "createdAt", "updatedAt", "archivedAt"}, Params: []Param{{Name: "pipelineId", Description: "Only releases of these pipelines."}, {Name: "status", Form: "legacy", Description: "Only releases with this status."}}, Scopes: []string{"read", "release:read"}},
	{Method: "GET", Path: "/api/webhooks", Tag: "webhooks", Summary: "List webhooks", Description: "Workspace admins (or the admin scope). Connection with pagination arguments; otherwise the legacy array.", Response: Webhook{}, Connection: true, Legacy: []Webhook{}, Filter: []string{"id", "name", "url", "enabled", "resourceTypes", "teams", "createdAt", "updatedAt"}, Scopes: adminScopes},
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

	{Method: "GET", Path: "/api/issue-records", Tag: "issues", Summary: "Query issues", Description: "Cursor-paged issue query over an indexed keyset, at most 100 per page. With first/last/after/before/orderBy (or connection=1) the response is a connection and pages in both directions: pageInfo.endCursor continues forward with `first` + `after`, pageInfo.startCursor backward with `last` + `before` (`last` alone returns the final page). Without them the original page {items, hasMore, nextCursor, total} paged with `limit` + `cursor` (forward only) is returned. Cursors are bound to the filter and ordering they were issued for. Default order in connection form is createdAt, newest first.", Response: domain.Issue{}, Connection: true, Legacy: store.IssueRecordPage{}, MaxPageSize: MaxIssuePageSize, Orderings: []string{"createdAt", "updatedAt", "priority", "title", "sortOrder"}, FilterType: IssueFilter{}, Params: []Param{{Name: "limit", Type: "integer", Description: "Legacy page size (1-500, default 100), or an alias of first in connection form (1-100)."}, {Name: "cursor", Description: "Legacy continuation token (nextCursor), or an alias of after in connection form."}, {Name: "sort", Enum: []string{"sortOrder", "priority", "createdAt", "updatedAt", "title"}, Description: "Legacy ordering (default sortOrder); an alias of orderBy in connection form."}, {Name: "direction", Enum: []string{"asc", "desc"}, Description: "Sort direction. Default asc, except createdAt and updatedAt in connection form (desc)."}, {Name: "orderDirection", Enum: []string{"asc", "desc"}, Description: "Alias of direction."}, {Name: "q", Description: "Text search over title and identifier."}, teamParam, {Name: "projectId", Description: "Only issues of these projects (comma-separated ids)."}, {Name: "stateId", Description: "Only issues in these workflow states (comma-separated ids)."}, {Name: "archived", Enum: []string{"false", "true", "all"}, Description: "false (default) returns active issues, true only archived ones, all both. includeArchived=true is the same as archived=all."}, {Name: "includeTotal", Type: "boolean", Description: "Count all matching issues: `total` in the legacy page, `totalCount` in a connection."}}, Scopes: readScopes},
	{Method: "GET", Path: "/api/issue-records/{id}", Tag: "issues", Summary: "Get an issue by id or identifier", Response: domain.Issue{}, Scopes: readScopes},
	{Method: "POST", Path: "/api/issues", Tag: "issues", Summary: "Create an issue", Request: domain.IssueCreateInput{}, Response: domain.Issue{}, Status: 201, Scopes: []string{"write", "create_issues", "issues:create"}},
	{Method: "PATCH", Path: "/api/issues/{id}", Tag: "issues", Summary: "Update an issue", Request: domain.IssueUpdateInput{}, Response: domain.Issue{}, Scopes: []string{"write", "create_issues", "issues:create"}},
	{Method: "DELETE", Path: "/api/issues/{id}", Tag: "issues", Summary: "Delete an issue", Scopes: writeScopes},
	{Method: "POST", Path: "/api/projects", Tag: "projects", Summary: "Create a project", Request: domain.ProjectMutationInput{}, Response: domain.Project{}, Status: 201, Scopes: writeScopes},
	{Method: "PATCH", Path: "/api/projects/{id}", Tag: "projects", Summary: "Update a project", Request: domain.ProjectMutationInput{}, Response: domain.Project{}, Scopes: writeScopes},
	{Method: "GET", Path: "/api/documents/{id}", Tag: "documents", Summary: "Get a document", Response: domain.Document{}, Scopes: readScopes},
	{Method: "POST", Path: "/api/initiatives", Tag: "initiatives", Summary: "Create an initiative", Request: domain.InitiativeMutationInput{}, Response: domain.Initiative{}, Status: 201, Scopes: []string{"write", "initiative:write"}},
	{Method: "PATCH", Path: "/api/initiatives/{id}", Tag: "initiatives", Summary: "Update an initiative", Request: domain.InitiativeMutationInput{}, Response: domain.Initiative{}, Scopes: []string{"write", "initiative:write"}},

	// Single records. Visibility is that of the matching list; archived
	// records are returned too.
	{Method: "GET", Path: "/api/teams/{id}", Tag: "teams", Summary: "Get a team", Description: "By id or team key (ENG).", Response: domain.Team{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/users/{id}", Tag: "users", Summary: "Get a user", Description: "By id, or `me` for the caller.", Response: domain.User{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/workflow-states/{id}", Tag: "workflow-states", Summary: "Get a workflow state", Response: domain.WorkflowState{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/issue-labels/{id}", Tag: "labels", Summary: "Get an issue label", Response: domain.IssueLabel{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/cycles/{id}", Tag: "cycles", Summary: "Get a cycle", Response: domain.Cycle{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/projects/{id}", Tag: "projects", Summary: "Get a project", Description: "By id or slugId, with its milestones, comments and history.", Response: domain.Project{}, Scopes: readScopes},
	{Method: "GET", Path: "/api/initiatives/{id}", Tag: "initiatives", Summary: "Get an initiative", Description: "By id or slugId.", Response: domain.Initiative{}, Scopes: []string{"read", "initiative:read"}},
	{Method: "GET", Path: "/api/customers/{id}", Tag: "customers", Summary: "Get a customer", Response: domain.Customer{}, Scopes: []string{"read", "customer:read"}},

	// Deletes.
	{Method: "DELETE", Path: "/api/projects/{id}", Tag: "projects", Summary: "Delete a project", Scopes: writeScopes},
	{Method: "DELETE", Path: "/api/initiatives/{id}", Tag: "initiatives", Summary: "Delete an initiative", Scopes: []string{"write", "initiative:write"}},
	{Method: "DELETE", Path: "/api/documents/{id}", Tag: "documents", Summary: "Delete a document", Scopes: writeScopes},
	{Method: "DELETE", Path: "/api/customers/{id}", Tag: "customers", Summary: "Delete a customer", Scopes: []string{"write", "customer:write"}},
	{Method: "DELETE", Path: "/api/issues/{id}/comments/{commentId}", Tag: "comments", Summary: "Delete a comment", Scopes: writeScopes},

	// Documents.
	{Method: "POST", Path: "/api/documents", Tag: "documents", Summary: "Create a document", Description: "Content is Markdown. An actor=app token may name the external author with createAsUser.", Request: DocumentCreateInput{}, Response: domain.Document{}, Status: 201, Scopes: writeScopes},
	{Method: "PATCH", Path: "/api/documents/{id}", Tag: "documents", Summary: "Update a document", Description: "Send expectedVersion to detect a concurrent change: the write is then refused with 409 and the current document.", Request: DocumentUpdateInput{}, Response: domain.Document{}, Scopes: writeScopes},

	// Project milestones.
	{Method: "POST", Path: "/api/projects/{id}/milestones", Tag: "projects", Summary: "Create a project milestone", Request: domain.ProjectMilestoneMutationInput{}, Response: domain.ProjectMilestone{}, Status: 201, Scopes: writeScopes},
	{Method: "PATCH", Path: "/api/projects/{id}/milestones/{milestoneId}", Tag: "projects", Summary: "Update a project milestone", Request: domain.ProjectMilestoneMutationInput{}, Response: domain.ProjectMilestone{}, Scopes: writeScopes},
	{Method: "DELETE", Path: "/api/projects/{id}/milestones/{milestoneId}", Tag: "projects", Summary: "Delete a project milestone", Description: "Issues in the milestone keep their project and lose the milestone.", Scopes: writeScopes},
	{Method: "POST", Path: "/api/projects/{id}/milestones/reorder", Tag: "projects", Summary: "Reorder the milestones of a project", Description: "ids must list every milestone of the project exactly once.", Request: ProjectMilestoneReorderInput{}, Response: []domain.ProjectMilestone{}, Scopes: writeScopes},

	// Project and initiative updates.
	{Method: "GET", Path: "/api/projects/{id}/updates", Tag: "projects", Summary: "List project updates", Description: "Newest first. Not paged.", Response: []domain.ProjectUpdate{}, Scopes: readScopes},
	{Method: "POST", Path: "/api/projects/{id}/updates", Tag: "projects", Summary: "Post a project update", Description: "Posting archives the project's pending update reminders and sets the project health. health defaults to the project's current one.", Request: domain.ProjectUpdateCreateInput{}, Response: domain.ProjectUpdate{}, Status: 201, Scopes: writeScopes},
	{Method: "PATCH", Path: "/api/projects/{id}/updates/{updateId}", Tag: "projects", Summary: "Edit a project update", Description: "The author or a workspace administrator.", Request: domain.ProjectUpdateMutationInput{}, Response: domain.ProjectUpdate{}, Scopes: writeScopes},
	{Method: "DELETE", Path: "/api/projects/{id}/updates/{updateId}", Tag: "projects", Summary: "Delete a project update", Description: "The author or a workspace administrator.", Scopes: writeScopes},
	{Method: "GET", Path: "/api/initiatives/{id}/updates", Tag: "initiatives", Summary: "List initiative updates", Description: "Newest first. Not paged.", Response: []domain.InitiativeUpdate{}, Scopes: []string{"read", "initiative:read"}},
	{Method: "POST", Path: "/api/initiatives/{id}/updates", Tag: "initiatives", Summary: "Post an initiative update", Request: domain.InitiativeUpdateCreateInput{}, Response: domain.InitiativeUpdate{}, Status: 201, Scopes: []string{"write", "initiative:write"}},
	{Method: "PATCH", Path: "/api/initiatives/{id}/updates/{updateId}", Tag: "initiatives", Summary: "Edit an initiative update", Description: "The author or a workspace administrator. Same body as a project update edit; health is onTrack, atRisk or offTrack.", Request: domain.ProjectUpdateMutationInput{}, Response: domain.InitiativeUpdate{}, Scopes: []string{"write", "initiative:write"}},
	{Method: "DELETE", Path: "/api/initiatives/{id}/updates/{updateId}", Tag: "initiatives", Summary: "Delete an initiative update", Description: "The author or a workspace administrator.", Scopes: []string{"write", "initiative:write"}},

	// Webhook management (workspace administrators, or the admin scope).
	{Method: "POST", Path: "/api/webhooks", Tag: "webhooks", Summary: "Create a webhook", Description: "The response carries the signing secret, shown only once.", Request: WebhookCreateInput{}, Response: WebhookWithSecret{}, Status: 201, Scopes: adminScopes},
	{Method: "GET", Path: "/api/webhooks/{id}", Tag: "webhooks", Summary: "Get a webhook", Response: Webhook{}, Scopes: adminScopes},
	{Method: "PATCH", Path: "/api/webhooks/{id}", Tag: "webhooks", Summary: "Update a webhook", Description: "Re-enabling a webhook resets its failure window; disabling it cancels its queued retries. Webhooks managed by an application accept only `enabled`.", Request: WebhookUpdateInput{}, Response: Webhook{}, Scopes: adminScopes},
	{Method: "DELETE", Path: "/api/webhooks/{id}", Tag: "webhooks", Summary: "Delete a webhook", Description: "Not available for application-managed webhooks; uninstall the application instead.", Scopes: adminScopes},
	{Method: "POST", Path: "/api/webhooks/{id}/rotate-secret", Tag: "webhooks", Summary: "Rotate the signing secret", Description: "Takes effect immediately for new attempts. The new secret is returned once.", Response: WebhookWithSecret{}, Scopes: adminScopes},
	{Method: "POST", Path: "/api/webhooks/{id}/revoke-secret", Tag: "webhooks", Summary: "Revoke the signing secret", Description: "Deliveries go out unsigned (Flow-Signature-Status: unsigned) until a new secret is rotated in.", Scopes: adminScopes},
	{Method: "GET", Path: "/api/webhooks/{id}/deliveries", Tag: "webhooks", Summary: "List deliveries", Description: "The delivery log of the last 30 days, newest first, with cursor paging. Requests are stored without their payload here; fetch one delivery for it.", Params: []Param{{Name: "status", Enum: []string{"pending", "delivering", "retrying", "succeeded", "failed", "canceled"}, Description: "failed also lists deliveries that are retrying."}, {Name: "cursor", Description: "nextCursor of the previous page."}, {Name: "limit", Type: "integer", Description: "Page size (1-100, default 50)."}}, Response: store.WebhookDeliveryPage{}, Scopes: adminScopes},
	{Method: "GET", Path: "/api/webhooks/{id}/deliveries/{deliveryId}", Tag: "webhooks", Summary: "Get a delivery", Description: "With the request payload, every attempt and the first 2 KB of the response body.", Response: domain.WebhookDelivery{}, Scopes: adminScopes},
	{Method: "POST", Path: "/api/webhooks/{id}/deliveries/{deliveryId}/redeliver", Tag: "webhooks", Summary: "Redeliver", Description: "Sends the payload again as a new delivery (new id, timestamp and signature) and returns it after its first attempt. 409 while the webhook is disabled.", Response: domain.WebhookDelivery{}, Scopes: adminScopes},
	{Method: "POST", Path: "/api/webhooks/{id}/test", Tag: "webhooks", Summary: "Send a test event", Description: "Delivers a WebhookTest event once and returns the delivery.", Response: domain.WebhookDelivery{}, Scopes: adminScopes},
	{Method: "GET", Path: "/api/webhooks/{id}/failures", Tag: "webhooks", Summary: "List failed attempts", Description: "Compatibility view of the delivery log: failed attempts, newest first.", Response: []domain.WebhookFailureEvent{}, Scopes: adminScopes},
	{Method: "GET", Path: "/api/oauth-applications/{id}/webhook", Tag: "webhooks", Summary: "Get the webhook of an OAuth application", Response: ApplicationWebhookSettings{}, Scopes: adminScopes},
	{Method: "PUT", Path: "/api/oauth-applications/{id}/webhook", Tag: "webhooks", Summary: "Configure the webhook of an OAuth application", Description: "Enabling the webhook for the first time creates the application's signing secret, returned once.", Request: ApplicationWebhookInput{}, Response: ApplicationWebhookSettings{}, Scopes: adminScopes},
	{Method: "POST", Path: "/api/oauth-applications/{id}/webhook/rotate-secret", Tag: "webhooks", Summary: "Rotate the signing secret of an application webhook", Response: ApplicationWebhookSettings{}, Scopes: adminScopes},
}

func withOrderings(operation Operation, orderings ...string) Operation {
	if len(orderings) > 0 {
		operation.Orderings = orderings
	} else {
		operation.Orderings = []string{"createdAt"}
	}
	return operation
}
