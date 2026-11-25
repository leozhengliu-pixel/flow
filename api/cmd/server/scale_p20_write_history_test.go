package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP20MetadataWriteHistoryProjection(t *testing.T) {
	db := newScaleP04Database(t)
	handler := db.handler(t, true)
	doc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "P20 saved", "content": "Saved body"}, http.StatusCreated)
	requestJSON[domain.Document](t, handler, "PATCH", "/api/documents/"+doc.ID, map[string]any{"content": "Current body"}, http.StatusOK)
	projected := requestJSON[map[string]json.RawMessage](t, handler, "PATCH", "/api/documents/"+doc.ID+"?history=summary", map[string]any{"title": "P20 metadata edit"}, http.StatusOK)
	var revisions []map[string]json.RawMessage
	if err := json.Unmarshal(projected["revisions"], &revisions); err != nil {
		t.Fatal(err)
	}
	if len(revisions) < 2 {
		t.Fatal("write lost history headers")
	}
	for _, revision := range revisions {
		if _, loaded := revision["content"]; loaded || string(revision["_bodyOmitted"]) != "true" {
			t.Fatal("metadata write sent history bodies")
		}
	}
	legacy := requestJSON[domain.Document](t, handler, "GET", "/api/documents/"+doc.ID, nil, http.StatusOK)
	if legacy.Content != "Current body" || legacy.Title != "P20 metadata edit" || legacy.Revisions[len(legacy.Revisions)-1].Content != "Saved body" {
		t.Fatal("metadata edit cleared a body or changed legacy history")
	}
}

func TestScaleP20OlderDocumentSuffixLink(t *testing.T) {
	db := newScaleP04Database(t)
	handler := db.handler(t, true)
	doc := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "P20 current title", "content": "Body at an older link"}, http.StatusCreated)
	old := "older-title" + doc.SlugID[strings.LastIndex(doc.SlugID, "-"):]
	loaded := requestJSON[domain.Document](t, handler, "GET", "/api/documents/"+old+"?history=summary", nil, http.StatusOK)
	if loaded.ID != doc.ID || loaded.Content != doc.Content {
		t.Fatal("an older suffix link lost its document")
	}
}
