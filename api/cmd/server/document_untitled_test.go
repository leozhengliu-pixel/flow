package main

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDocumentsStartUntitledAndSlugFollowsTitle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{}, http.StatusCreated)
	if created.Title != "" {
		t.Fatalf("new document title = %q, want empty", created.Title)
	}
	if !regexp.MustCompile(`^untitled-[0-9a-f]{12}$`).MatchString(created.SlugID) {
		t.Fatalf("untitled slug = %q", created.SlugID)
	}
	suffix := created.SlugID[strings.LastIndex(created.SlugID, "-")+1:]

	renamed := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+created.ID, map[string]any{"title": "Quarterly plan"}, http.StatusOK)
	if renamed.Title != "Quarterly plan" || renamed.SlugID != "quarterly-plan-"+suffix {
		t.Fatalf("renamed = %q / %q, want slug quarterly-plan-%s", renamed.Title, renamed.SlugID, suffix)
	}

	cleared := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+created.ID, map[string]any{"title": ""}, http.StatusOK)
	if cleared.Title != "" || cleared.SlugID != "untitled-"+suffix {
		t.Fatalf("cleared = %q / %q, want empty title and untitled-%s", cleared.Title, cleared.SlugID, suffix)
	}

	// Omitting the title leaves it (and the slug) alone.
	kept := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+created.ID, map[string]any{"icon": "Page"}, http.StatusOK)
	if kept.Title != "" || kept.SlugID != "untitled-"+suffix {
		t.Fatalf("icon-only patch changed title/slug: %q / %q", kept.Title, kept.SlugID)
	}
}

func TestDocumentFromTemplateKeepsTemplateTitleAndDisplayFallbacks(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	project := bootstrap.Projects[0]

	template := requestJSON[domain.DocumentTemplate](t, handler, http.MethodPost, "/api/document-templates", map[string]any{"name": "Weekly", "title": "Weekly notes", "icon": "Page", "content": "Agenda"}, http.StatusCreated)
	fromTemplate := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"templateId": template.ID}, http.StatusCreated)
	if fromTemplate.Title != "Weekly notes" || !strings.HasPrefix(fromTemplate.SlugID, "weekly-notes-") || fromTemplate.Content != "Agenda" {
		t.Fatalf("template document = %#v", fromTemplate)
	}

	// An untitled document linked to a project shows the fallback name in the project's resources.
	untitled := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"projectIds": []string{project.ID}}, http.StatusCreated)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, item := range bootstrap.Projects {
		if item.ID != project.ID {
			continue
		}
		for _, resource := range item.Resources {
			if resource.ID == untitled.ID && resource.Title != "Untitled document" {
				t.Fatalf("project resource title = %q, want fallback", resource.Title)
			}
		}
	}
	if got := documentDisplayTitle("  "); got != "Untitled document" {
		t.Fatalf("documentDisplayTitle = %q", got)
	}
}

func TestApplyingATemplateReplacesTheBodyThroughANewCollaborationGeneration(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	created := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{}, http.StatusCreated)
	if created.CollaborationID != "" {
		t.Fatalf("new document starts in the default collaboration generation, got %q", created.CollaborationID)
	}
	applied := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+created.ID, map[string]any{"title": "Weekly notes", "icon": "Page", "content": "Agenda"}, http.StatusOK)
	if applied.Title != "Weekly notes" || applied.Icon != "Page" || applied.Content != "Agenda" {
		t.Fatalf("template fields were not applied: %#v", applied)
	}
	if applied.CollaborationID == "" || applied.CollaborationID == created.CollaborationID {
		t.Fatalf("a Markdown body replacement must start a new collaboration generation so open editors reload, got %q", applied.CollaborationID)
	}
}
