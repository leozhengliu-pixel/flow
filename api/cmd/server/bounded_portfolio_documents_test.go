package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestBoundedPortfolioDocumentsDifferential(t *testing.T) {
	t.Setenv("FLOW_API_RATE_LIMIT", "off")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	x := sw530Setup(t)
	owner, admin, member, guest, inherited, former := x.principals[0], x.principals[1], x.principals[2], x.principals[3], x.principals[4], x.principals[5]
	public, private, child := x.ids["teams"], x.ids["private-team"], x.ids["child-team"]
	project, secondProject := x.fx.ids["child-project"], "portfolio-second-project"
	docID, alias, previous := "portfolio-doc", "portfolio-current", "portfolio-previous"
	templateID := "portfolio-template"
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	body := func(fields map[string]any) string {
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	w, timedOut := x.call(x.legacy.handler, http.MethodPost, "/api/issue-records", body(map[string]any{"title": "Child issue", "teamId": child}), inherited, false)
	var childIssue domain.Issue
	if timedOut || w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &childIssue) != nil || childIssue.ID == "" {
		t.Fatalf("child issue fixture status=%d timeout=%t", w.Code, timedOut)
	}
	if err := x.legacy.store.MutateWorkspace(t.Context(), x.workspace, "portfolio.fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: secondProject, Name: "Other project", TeamIDs: []string{public}})
		data.DocumentTemplates = append(data.DocumentTemplates, domain.DocumentTemplate{
			ID: templateID, Name: "Team template", Title: "Team notes", TeamID: child, Content: "Template content", Creator: owner.user, CreatedAt: at, UpdatedAt: at,
		})
		doc := domain.Document{
			ID: docID, SlugID: alias, PreviousSlugIDs: []string{previous}, Title: "Delete with links", Content: "Document content", Creator: owner.user,
			ProjectIDs: []string{project}, IssueID: childIssue.ID, IssueTeamID: child, InheritedTeamIDs: []string{child},
			SubscriberIDs: []string{member.user.ID, guest.user.ID, inherited.user.ID, former.user.ID},
			Permissions: []domain.DocumentPermission{
				{ID: "portfolio-creator-grant", DocumentID: docID, SubjectType: "user", SubjectID: owner.user.ID, Role: "owner", CreatedAt: at, UpdatedAt: at},
				{ID: "portfolio-workspace-grant", DocumentID: docID, SubjectType: "workspace", Role: "viewer", CreatedAt: at, UpdatedAt: at},
				{ID: "portfolio-user-grant", DocumentID: docID, SubjectType: "user", SubjectID: member.user.ID, Role: "owner", CreatedAt: at, UpdatedAt: at},
			},
			CreatedAt: at, UpdatedAt: at,
		}
		data.Documents = append(data.Documents, doc)
		for i := range data.Projects {
			p := &data.Projects[i]
			if p.ID == project || p.ID == secondProject {
				// The second project contains a reverse link the document
				// does not name. Both links belong in the trash payload.
				p.Resources = append(p.Resources, domain.ProjectResource{
					ID: docID, ProjectID: p.ID, Type: "document", Title: doc.Title, URL: "/" + x.workspace + "/document/" + alias,
					PinnedTeamIDs: []string{public}, CreatedAt: at,
				})
			}
		}
		data.Drafts = append(data.Drafts,
			domain.Draft{ID: "portfolio-document-draft", Type: "document", ResourceID: docID, UserID: member.user.ID, Title: "Document draft", CreatedAt: at, UpdatedAt: at},
			domain.Draft{ID: "portfolio-comment-draft", Type: "comment", ResourceID: docID, UserID: member.user.ID, Metadata: map[string]any{"resourceType": "document"}, CreatedAt: at, UpdatedAt: at},
			domain.Draft{ID: "portfolio-unrelated-draft", Type: "issue", ResourceID: docID, UserID: member.user.ID, CreatedAt: at, UpdatedAt: at},
		)
		data.Favorites = append(data.Favorites, domain.Favorite{ID: "portfolio-favorite", ResourceType: "document", ResourceID: docID, UserID: member.user.ID, CreatedAt: at})
		data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "portfolio-subscription", ResourceType: "document", ResourceID: docID, UserID: admin.user.ID, CreatedAt: at})
		if data.NotificationPreferences == nil {
			data.NotificationPreferences = map[string]domain.NotificationPreferences{}
		}
		for _, principal := range []*sw530Principal{member, inherited} {
			prefs := defaultPreferences(principal.user.ID)
			prefs.EmailFormat, prefs.UpdatedAt = "immediate", at
			data.NotificationPreferences[principal.user.ID] = prefs
		}
		for i := range data.APIKeys {
			if data.APIKeys[i].CreatorID == member.user.ID {
				data.APIKeys[i].TeamRestriction, data.APIKeys[i].TeamIDs = "selected", []string{public}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.legacy.store.ReloadWorkspace(t.Context(), x.workspace); err != nil {
		t.Fatal(err)
	}
	data, found := x.legacy.store.BootstrapFor(x.workspace)
	if !found {
		t.Fatal("missing document fixture")
	}
	var err error
	x.baseline, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}

	create := func(principal *sw530Principal, bearer bool, body string) sw530Request {
		return sw530Request{pattern: "POST /api/documents", method: http.MethodPost, path: "/api/documents", body: body, principal: principal, bearer: bearer}
	}
	remove := func(principal *sw530Principal, bearer bool, ref string) sw530Request {
		return sw530Request{pattern: "DELETE /api/documents/{id}", method: http.MethodDelete, path: "/api/documents/" + ref, principal: principal, bearer: bearer}
	}
	for _, tc := range []struct {
		name    string
		request sw530Request
		status  int
		check   func(*testing.T, sw530Outcome)
	}{
		{"linked-template", create(inherited, false, body(map[string]any{
			"templateId": templateID, "issueId": " " + childIssue.ID + " ",
			"projectIds": []string{project, " " + secondProject + " ", project}, "subscriberIds": []string{admin.user.ID, admin.user.ID},
		})), 201, func(t *testing.T, got sw530Outcome) {
			var document struct {
				Title, Content, IssueID                              string
				TeamIDs, InheritedTeamIDs, ProjectIDs, SubscriberIDs []string
			}
			stamp := func(id string) string { return scaleP21TriageStamp.ReplaceAllString(id, "<stamp>") }
			if err := json.Unmarshal([]byte(got.Body), &document); err != nil ||
				document.Title != "Team notes" || document.Content != "Template content" || document.IssueID != childIssue.ID ||
				!slices.Equal(document.TeamIDs, []string{stamp(child)}) || !slices.Equal(document.InheritedTeamIDs, []string{stamp(child), stamp(public)}) ||
				!slices.Equal(document.ProjectIDs, []string{stamp(project), secondProject}) || !slices.Equal(document.SubscriberIDs, []string{stamp(admin.user.ID)}) {
				t.Error("incorrect template defaults, link inheritance or subscribers")
			}
		}},
		{"unknown-subscriber", create(owner, false, `{"title":"Markdown","content":"Body","subscriberIds":["missing"]}`), 201, nil},
		{"unknown-template", create(owner, false, `{"templateId":"missing"}`), 400, nil},
		{"hidden-template", create(member, false, body(map[string]any{"templateId": templateID})), 400, nil},
		{"outside-key-team", create(member, true, body(map[string]any{"teamIds": []string{private}})), 403, nil},
		{"former-member-mention", create(owner, false, body(map[string]any{"contentData": map[string]any{
			"type": "doc", "content": []any{map[string]any{"type": "userMention", "userId": former.user.ID}},
		}})), 400, nil},
		{"bad-json", create(owner, false, `{"title":`), 403, nil},
		{"owner-id", remove(owner, false, docID), 204, nil},
		{"owner-current-slug", remove(owner, false, alias), 204, nil},
		{"explicit-owner", remove(member, false, docID), 204, func(t *testing.T, got sw530Outcome) {
			if !slices.ContainsFunc(got.Rows, func(row string) bool {
				return strings.Contains(row, "portfolio-subscription") && strings.HasSuffix(row, " removed")
			}) {
				t.Error("subscription was not removed")
			}
			if !slices.ContainsFunc(got.Rows, func(row string) bool { return strings.Contains(row, "documentDeleted") }) {
				t.Error("subscriber notification missing")
			}
		}},
		{"previous-slug", remove(owner, false, previous), 403, nil},
		{"inherited-viewer", remove(inherited, false, docID), 403, nil},
		{"restricted-owner", remove(member, true, docID), 403, nil},
		{"missing", remove(owner, false, "missing"), 403, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slots, views := x.totalSlots, x.totalViews
			got := x.outcomes(t, tc.request, []string{"legacy", "warm", "reloaded"})
			for _, source := range []string{"legacy", "warm", "reloaded"} {
				outcome := got[source]
				if tc.request.method == http.MethodPost && outcome.Status == http.StatusCreated {
					normalizePortfolioDocumentURLs(&outcome)
					got[source] = outcome
				}
				if outcome.TimedOut || outcome.Status != tc.status {
					t.Errorf("%s status=%d timeout=%t want %d", source, outcome.Status, outcome.TimedOut, tc.status)
				}
				if kind, _, _ := sw530Differences(got["legacy"], outcome); kind != "" {
					t.Errorf("%s differs from legacy in %s", source, kind)
				}
				if tc.status < 300 {
					if len(outcome.Events) < 2 || len(outcome.Webhooks) == 0 || len(outcome.Realtime) == 0 {
						t.Errorf("%s did not publish audit, event, webhook and realtime", source)
					}
				} else if len(outcome.Rows)+len(outcome.Events)+len(outcome.Webhooks)+len(outcome.Realtime) != 0 {
					t.Errorf("%s refused request produced side effects", source)
				}
				if tc.check != nil {
					tc.check(t, outcome)
				}
			}
			if slots != x.totalSlots || views != x.totalViews || x.bounded.store.MetadataWorkUsedForTesting() != 0 {
				t.Error("document route used the COLD lane or left metadata work admitted")
			}
		})
	}

	t.Run("restore-links-from-bounded-delete", func(t *testing.T) {
		for _, source := range []string{"warm", "reloaded"} {
			got, _ := x.run(t, source, remove(owner, false, alias), time.Now().UTC())
			if got.Status != http.StatusNoContent {
				t.Fatalf("%s delete status=%d", source, got.Status)
			}
			if err := x.legacy.store.ReloadWorkspace(t.Context(), x.workspace); err != nil {
				t.Fatal(err)
			}
			deleted, _ := x.legacy.store.BootstrapFor(x.workspace)
			index := slices.IndexFunc(deleted.Trash, func(entry domain.TrashEntry) bool {
				return entry.ResourceType == "document" && entry.ResourceID == docID
			})
			if index < 0 {
				t.Fatal("bounded delete did not leave a restorable trash entry")
			}
			w, timedOut := x.call(x.legacy.handler, http.MethodPost, "/api/trash/"+deleted.Trash[index].ID+"/restore", "", owner, false)
			if timedOut || w.Code != http.StatusOK {
				t.Fatalf("%s restore status=%d timeout=%t", source, w.Code, timedOut)
			}
			restored, _ := x.legacy.store.BootstrapFor(x.workspace)
			p, err := fullProjectByID(&restored, project)
			if err != nil || !slices.ContainsFunc(p.Resources, func(r domain.ProjectResource) bool {
				return r.ID == docID && slices.Equal(r.PinnedTeamIDs, []string{public})
			}) {
				t.Error("restore lost the attached resource's team pins")
			}
			for _, id := range []string{"portfolio-document-draft", "portfolio-comment-draft", "portfolio-unrelated-draft"} {
				if !slices.ContainsFunc(restored.Drafts, func(d domain.Draft) bool { return d.ID == id }) {
					t.Error("restore lost a draft")
				}
			}
			x.reset(t)
		}
	})
}

var portfolioDocumentURLSuffix = regexp.MustCompile(`(/document/[^"\\]+-)[0-9a-f]{12}(\\?")`)

func normalizePortfolioDocumentURLs(outcome *sw530Outcome) {
	// SW530 normalizes the generated slug field, while project-resource rows
	// and event snapshots can also embed that slug in a URL.
	for _, entries := range [][]string{outcome.Rows, outcome.Events, outcome.Webhooks, outcome.Realtime} {
		for i, entry := range entries {
			entries[i] = portfolioDocumentURLSuffix.ReplaceAllString(entry, "${1}<suffix>${2}")
		}
	}
}
