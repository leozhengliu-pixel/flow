package store

import (
	"encoding/json"
	"testing"
)

func TestTrashedDocumentOwnerGrant(t *testing.T) {
	payload := json.RawMessage(`{"id":"d1","title":"Old","creator":{"id":"u1"},"createdAt":"2025-01-02T03:04:05Z"}`)
	next, changed, err := trashedDocumentOwnerGrant(payload)
	if err != nil || !changed {
		t.Fatalf("a payload without a creator grant is migrated: changed=%v err=%v", changed, err)
	}
	var document struct {
		Title       string `json:"title"`
		Permissions []struct {
			ID, SubjectType, SubjectID, Role string
		} `json:"permissions"`
	}
	if err := json.Unmarshal(next, &document); err != nil || document.Title != "Old" || len(document.Permissions) != 1 || document.Permissions[0].ID != ownerGrantID("u1") || document.Permissions[0].Role != "owner" {
		t.Fatalf("grant not written: %s %v", next, err)
	}
	if _, changed, _ := trashedDocumentOwnerGrant(next); changed {
		t.Fatal("the migration is idempotent")
	}
	if _, changed, _ := trashedDocumentOwnerGrant(json.RawMessage(`{"id":"d2","creator":{"id":""}}`)); changed {
		t.Fatal("a document without a creator gets no grant")
	}
}
