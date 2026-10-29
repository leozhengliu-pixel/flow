package domain

import (
	"slices"
	"time"
)

// NormalizeProjects makes ProjectIDs the binding's project scope: a binding
// stored with the single-project form gets it as a one-project set, the set
// is sorted without duplicates, and ProjectID repeats the project when the
// set holds exactly one (empty otherwise).
func (binding *ChatBinding) NormalizeProjects() {
	ids := make([]string, 0, len(binding.ProjectIDs)+1)
	for _, id := range binding.ProjectIDs {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 && binding.ProjectID != "" {
		ids = append(ids, binding.ProjectID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	binding.ProjectID = ""
	if len(ids) == 1 {
		binding.ProjectID = ids[0]
	}
	if len(ids) == 0 {
		ids = nil
	}
	binding.ProjectIDs = ids
}

// CoversProject reports whether the binding's project scope includes the
// project: every project when the binding is not limited to projects.
func (binding ChatBinding) CoversProject(projectID string) bool {
	normalized := binding
	normalized.NormalizeProjects()
	return len(normalized.ProjectIDs) == 0 || projectID != "" && slices.Contains(normalized.ProjectIDs, projectID)
}

const (
	ChatInitiativeBindingRoleOwner          = "initiative_owner"
	ChatInitiativeBindingRoleWorkspaceAdmin = "workspace_admin"
)

// ChatInitiativeBindingAuthorization is set by the server: who connected the
// initiative to the group, and in which capacity.
type ChatInitiativeBindingAuthorization struct {
	UserID       string    `json:"userId"`
	Role         string    `json:"role" enum:"initiative_owner,workspace_admin"`
	AuthorizedAt time.Time `json:"authorizedAt"`
}

// ChatInitiativeBinding connects one initiative directly to a chat group, so
// its updates reach the group whether or not the initiative has a lead team.
// It shares the initiative's own updates only: nothing of a private
// contributing team, and nothing while a private team leads the initiative.
type ChatInitiativeBinding struct {
	ID           string    `json:"id"`
	ConnectionID string    `json:"connectionId"`
	ChatID       string    `json:"chatId"`
	ChatName     string    `json:"chatName"`
	InitiativeID string    `json:"initiativeId"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	// Authorization is recorded by the server whenever the binding is saved.
	Authorization *ChatInitiativeBindingAuthorization `json:"authorization,omitempty"`
	// RevokedAt marks a revoked binding: it stays listed as a record and
	// never delivers again.
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	RevokedBy string     `json:"revokedBy,omitempty"`
}

// Chat thread resource types. A thread carries the conversation about one
// Flow resource in one group.
const (
	ChatThreadResourceIssue = "issue"
)

// ChatThread maps a Flow resource to the message thread that carries it in a
// chat group: the root message every later message about the resource
// replies to. When the root message is gone, the next message starts a new
// thread and replaces the mapping.
type ChatThread struct {
	ID            string `json:"id"`
	ConnectionID  string `json:"connectionId"`
	ChatID        string `json:"chatId"`
	ResourceType  string `json:"resourceType"`
	ResourceID    string `json:"resourceId"`
	RootMessageID string `json:"rootMessageId"`
	// PlatformThreadID is the platform's thread identity, when it named one.
	PlatformThreadID string `json:"platformThreadId,omitempty"`
	// BindingID is the group binding the thread was started under.
	BindingID string `json:"bindingId,omitempty"`
	// Origin is set when a member's message is the root: "message" (an
	// issue created from it with the message shortcut) or "request" (a
	// request made from it). Its first card is the creation's own reply.
	Origin string `json:"origin,omitempty" enum:"message,request"`
	// SyncFrom: items (such as comments) created before it are not mirrored
	// into the thread.
	SyncFrom  time.Time `json:"syncFrom"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Chat thread item origins: posted by Flow into the thread, or received from
// a member's message in the thread.
const (
	ChatThreadOriginFlow = "flow"
	ChatThreadOriginChat = "chat"
)

// ChatThreadItem pairs one Flow item (a comment) with the thread message that
// carries it. Origin says which side it started on, so a mirrored item is
// never sent back to where it came from.
type ChatThreadItem struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"threadId"`
	ItemType  string    `json:"itemType"`
	ItemID    string    `json:"itemId"`
	MessageID string    `json:"messageId"`
	Origin    string    `json:"origin" enum:"flow,chat"`
	Version   int64     `json:"version"`
	Deleted   bool      `json:"deleted,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
