// Package publicapi holds the public REST contract: response envelopes,
// request bodies of the platform endpoints and the operation registry that
// the OpenAPI generator (scripts/generate-openapi.mjs via cmd/openapi-schema)
// turns into schemas. Handlers in cmd/server use these types directly, so the
// published schema is the Go type the server encodes.
package publicapi

import (
	"time"

	"flow/api/internal/domain"
)

// PageInfo describes the position of a connection page.
type PageInfo struct {
	// HasNextPage is true when more nodes follow the last returned node.
	HasNextPage bool `json:"hasNextPage"`
	// HasPreviousPage is true when nodes precede the first returned node.
	HasPreviousPage bool `json:"hasPreviousPage"`
	// StartCursor is the cursor of the first node (pass as `before`).
	StartCursor *string `json:"startCursor"`
	// EndCursor is the cursor of the last node (pass as `after`).
	EndCursor *string `json:"endCursor"`
}

// Connection is the list envelope of every paginated endpoint.
type Connection[T any] struct {
	// Nodes are ordered newest first by the requested orderBy field.
	Nodes    []T      `json:"nodes"`
	PageInfo PageInfo `json:"pageInfo"`
}

// Error is the error envelope returned with every 4xx/5xx under /api/.
type Error struct {
	// Error is a human readable message.
	Error string `json:"error"`
	// Code is a machine code: a specific legacy code such as VERSION_CONFLICT
	// or, otherwise, the same value as Type.
	Code string `json:"code"`
	// Type is the stable category: INVALID_INPUT, AUTHENTICATION_ERROR,
	// FORBIDDEN, FEATURE_NOT_ACCESSIBLE, NOT_FOUND, CONFLICT, RATELIMITED,
	// INTERNAL_ERROR or UNAVAILABLE.
	Type string `json:"type"`
	// RequestID matches the X-Request-ID response header.
	RequestID string `json:"requestId,omitempty"`
}

// ViewerApplication identifies the OAuth application behind a token.
type ViewerApplication struct {
	ClientID string `json:"clientId"`
	Name     string `json:"name,omitempty"`
}

// Viewer describes the authenticated caller.
type Viewer struct {
	User      domain.User       `json:"user"`
	Workspace *domain.Workspace `json:"workspace,omitempty"`
	// Role is the caller's workspace role (owner, admin, member, guest, app).
	Role string `json:"role,omitempty"`
	// Actor is "app" for application (actor=app) tokens, otherwise "user".
	Actor string `json:"actor"`
	// Authentication is session, apiKey or oauth.
	Authentication string             `json:"authentication"`
	Scopes         []string           `json:"scopes,omitempty"`
	TeamIDs        []string           `json:"teamIds,omitempty"`
	Application    *ViewerApplication `json:"application,omitempty"`
	ExpiresAt      *time.Time         `json:"expiresAt,omitempty"`
}

// Organization is the workspace with headline counts.
type Organization struct {
	domain.Workspace
	UserCount int `json:"userCount"`
	TeamCount int `json:"teamCount"`
}

// FileUploadInput prepares an upload (the reference app's fileUpload).
type FileUploadInput struct {
	ContentType string `json:"contentType"`
	Filename    string `json:"filename"`
	// Size is the exact byte length of the file (at most 20 MB).
	Size int64 `json:"size"`
}

type FileUploadHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// FileUpload tells the client where to PUT the bytes. Send the listed
// headers with the PUT; the token in uploadUrl is the credential.
type FileUpload struct {
	ID          string             `json:"id"`
	UploadURL   string             `json:"uploadUrl"`
	AssetURL    string             `json:"assetUrl"`
	Headers     []FileUploadHeader `json:"headers"`
	ContentType string             `json:"contentType"`
	Filename    string             `json:"filename"`
	Size        int64              `json:"size"`
	ExpiresAt   time.Time          `json:"expiresAt"`
}

type SignedFile struct {
	URL       string    `json:"url"`
	SignedURL string    `json:"signedUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type SignedFiles struct {
	Files []SignedFile `json:"files"`
}

// LinkAttachmentInput creates or updates a link attachment.
type LinkAttachmentInput struct {
	// URL identifies the attachment on its issue; saving it again updates it.
	URL      string         `json:"url"`
	Title    *string        `json:"title,omitempty"`
	Subtitle *string        `json:"subtitle,omitempty"`
	IconURL  *string        `json:"iconUrl,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// CustomerInput creates or updates a customer.
type CustomerInput struct {
	Name          *string   `json:"name"`
	LogoURL       *string   `json:"logoUrl"`
	OwnerID       *string   `json:"ownerId"`
	Status        *string   `json:"status"`
	Tier          *string   `json:"tier"`
	AnnualRevenue *float64  `json:"annualRevenue"`
	Size          *int      `json:"size"`
	Domains       *[]string `json:"domains"`
	ExternalIDs   *[]string `json:"externalIds"`
}

// CustomerUpsertInput matches by id, then external id(s), then domain.
type CustomerUpsertInput struct {
	CustomerInput
	ID         string `json:"id,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
}

// CustomerRequestInput creates or updates a customer request (need).
type CustomerRequestInput struct {
	// CustomerID links the request to a customer; "" on update removes the
	// customer (an "Unknown customer" request).
	CustomerID *string `json:"customerId,omitempty"`
	// CustomerName creates a new customer with this name in the same write.
	CustomerName *string  `json:"customerName,omitempty"`
	Body         *string  `json:"body,omitempty"`
	Source       *string  `json:"source,omitempty"`
	SourceURL    *string  `json:"sourceUrl,omitempty"`
	IssueID      *string  `json:"issueId,omitempty"`
	ProjectID    *string  `json:"projectId,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
	// CustomerExternalID selects the customer by one of its external ids.
	CustomerExternalID *string `json:"customerExternalId,omitempty"`
	// AttachmentURL creates a link attachment on the request's issue and
	// becomes the request's source URL.
	AttachmentURL *string `json:"attachmentUrl,omitempty"`
}

// AuditEntryTypeCount is one audit entry type present in the log.
type AuditEntryTypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}
