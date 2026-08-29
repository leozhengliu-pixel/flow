package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"net/http"
	"path/filepath"
	"testing"
)

type parityPage[T any] struct {
	Nodes      []T    `json:"nodes"`
	NextCursor string `json:"nextCursor"`
	Total      int    `json:"total"`
}

func TestParityCoreEntityLifecycles(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	project := bootstrap.Projects[0]
	related := bootstrap.Projects[1]
	projectRelation := requestJSON[domain.ProjectRelation](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/relations", map[string]any{"relatedProjectId": related.ID, "type": "blocks"}, http.StatusCreated)
	if projectRelation.ID == "" {
		t.Fatal("project relation was not created")
	}
	page := requestJSON[parityPage[domain.ProjectRelation]](t, handler, http.MethodGet, "/api/projects/"+project.ID+"/relations?limit=1", nil, http.StatusOK)
	if page.Total != 1 || len(page.Nodes) != 1 {
		t.Fatalf("project relations page=%#v", page)
	}
	initiativeA := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Initiative A"}, http.StatusCreated)
	initiativeB := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Initiative B"}, http.StatusCreated)
	initiativeRelation := requestJSON[domain.InitiativeRelation](t, handler, http.MethodPost, "/api/initiatives/"+initiativeA.ID+"/relations", map[string]any{"relatedInitiativeId": initiativeB.ID, "type": "parent"}, http.StatusCreated)
	if initiativeRelation.ID == "" {
		t.Fatal("initiative relation was not created")
	}
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Drafted"}, http.StatusCreated)
	draft := requestJSON[domain.DocumentContentDraft](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/drafts", map[string]any{"content": "draft body", "contentData": map[string]any{"type": "doc"}}, http.StatusCreated)
	published := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/drafts/"+draft.ID+"/publish", nil, http.StatusOK)
	if published.Content != "draft body" {
		t.Fatalf("document draft was not published: %#v", published)
	}
	status := requestJSON[domain.CustomerStatus](t, handler, http.MethodPost, "/api/customer-statuses", map[string]any{"name": "At risk", "color": "#f00"}, http.StatusCreated)
	tier := requestJSON[domain.CustomerTier](t, handler, http.MethodPost, "/api/customer-tiers", map[string]any{"name": "Enterprise", "color": "#00f"}, http.StatusCreated)
	if status.ID == "" || tier.ID == "" {
		t.Fatal("customer taxonomy was not persisted")
	}
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Parity customer"}, http.StatusCreated)
	need := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": customer.ID, "body": "Need", "issueId": bootstrap.Issues[0].ID}, http.StatusCreated)
	need = requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests/"+need.ID+"/archive", nil, http.StatusOK)
	if need.ArchivedAt == nil {
		t.Fatal("customer need was not archived")
	}
	var release domain.Release
	if len(bootstrap.Releases) > 0 {
		release = bootstrap.Releases[0]
	} else {
		release = requestJSON[domain.Release](t, handler, http.MethodPost, "/api/releases", map[string]any{"name": "Parity release"}, http.StatusCreated)
	}
	note := requestJSON[domain.ReleaseNote](t, handler, http.MethodPost, "/api/releases/"+release.ID+"/notes", map[string]any{"title": "Release note", "body": "Body"}, http.StatusCreated)
	if note.ID == "" {
		t.Fatal("release note missing")
	}
	team := bootstrap.Teams[0]
	section := requestJSON[domain.TeamResourceSection](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/resource-sections", map[string]any{"name": "Docs"}, http.StatusCreated)
	resource := requestJSON[domain.TeamPinnedResource](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/resources", map[string]any{"sectionId": section.ID, "resourceType": "document", "resourceId": document.ID, "title": "Drafted"}, http.StatusCreated)
	if resource.ID == "" {
		t.Fatal("team resource missing")
	}
	activity := requestJSON[domain.AgentActivity](t, handler, http.MethodPost, "/api/agent/activities", map[string]any{"sessionId": "session", "type": "tool"}, http.StatusCreated)
	conversation := requestJSON[domain.AIConversation](t, handler, http.MethodPost, "/api/ai/conversations", map[string]any{"title": "Conversation"}, http.StatusCreated)
	progress := requestJSON[domain.AIPromptProgress](t, handler, http.MethodPost, "/api/ai/prompt-progress", map[string]any{"conversationId": conversation.ID, "phase": "answer", "progress": 50}, http.StatusCreated)
	if activity.ID == "" || progress.Progress != 50 {
		t.Fatal("agent/AI lifecycle failed")
	}
	alert := requestJSON[domain.UsageAlert](t, handler, http.MethodPut, "/api/usage-alerts", map[string]any{"type": "aiCredits", "threshold": 100, "current": 110}, http.StatusOK)
	if alert.Status != "active" {
		t.Fatalf("usage alert=%#v", alert)
	}
}
