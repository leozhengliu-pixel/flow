package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW268Fixture struct {
	scaleP21M2Fixture
	other      string
	principals []scaleP21M2Principal
	urls       []string
	uploads    string
}

const scaleP21SW268Prefix = "/uploads/sw268-"

func scaleP21SW268URL(name string) string { return scaleP21SW268Prefix + name + ".png" }

func scaleP21SW268Seed(t *testing.T, repo *store.SQLiteStore) scaleP21SW268Fixture {
	t.Helper()
	f := scaleP21M2Setup(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw268-")
	stamp := time.Date(2026, time.November, 17, 10, 0, 0, 0, time.UTC)
	member, guest := f.principals[2].user, f.principals[3].user
	attachment := func(name string) domain.Attachment {
		return domain.Attachment{ID: prefix + name, URL: scaleP21SW268URL(name), Title: name, CreatedAt: stamp}
	}
	attachments := func(names ...string) []domain.Attachment {
		result := []domain.Attachment{}
		for _, name := range names {
			result = append(result, attachment(name))
		}
		return result
	}
	urls := []string{scaleP21SW268URL("unreferenced"), "/uploads/agent_upload_" + agentUploadOwner(member.ID) + "_sw268.png"}
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw268_seed", "", nil, func(data *domain.Bootstrap) error {
		private, public := "", f.public
		var publicTeam, privateTeam domain.Team
		for _, team := range data.Teams {
			if team.Private && private == "" {
				private, privateTeam = team.ID, team
			}
			if team.ID == public {
				publicTeam = team
			}
		}
		privateIssue := ""
		for _, issue := range data.Issues {
			if issue.Team.ID == private {
				privateIssue = issue.ID
			}
		}
		if private == "" || privateIssue == "" {
			return fmt.Errorf("fixture lacks its private team or issue")
		}
		owner := data.Viewer
		publicIssue := domain.Issue{
			ID: prefix + "public-issue", Identifier: "SW268-1", Number: 9268, Version: 1, Title: "Public",
			Team: publicTeam, State: data.States[0], Creator: owner, CreatedAt: stamp, UpdatedAt: stamp,
			Attachments: attachments("issue-public"),
		}
		hiddenIssue := domain.Issue{
			ID: prefix + "private-issue", Identifier: "SW268-2", Number: 9269, Version: 1, Title: "Private",
			Team: privateTeam, State: data.States[0], Creator: owner, CreatedAt: stamp, UpdatedAt: stamp,
			Attachments: attachments("issue-private"),
		}
		data.Issues = append(data.Issues, publicIssue, hiddenIssue)
		// Loops: attached files have no viewer predicate; drafts follow their creator.
		data.Loops = append(data.Loops, domain.Loop{
			ID: prefix + "loop", Name: "Private loop", TeamID: private, Level: "team", Status: "draft", OwnerID: owner.ID,
			Creator: owner, CreatedAt: stamp, UpdatedAt: stamp,
			Attachments: []domain.LoopAttachment{{ID: prefix + "loop-a", Name: "a", URL: scaleP21SW268URL("loop-attached"), CreatedAt: stamp}},
		})
		data.LoopAttachments = append(data.LoopAttachments,
			domain.LoopAttachment{ID: prefix + "draft-own", URL: scaleP21SW268URL("loop-draft-own"), CreatorID: member.ID, CreatedAt: stamp},
			domain.LoopAttachment{ID: prefix + "draft-linked", URL: scaleP21SW268URL("loop-draft-linked"), CreatorID: guest.ID, LoopID: prefix + "loop", CreatedAt: stamp},
		)
		// Customer requests: guests never; links follow issue/project visibility.
		data.Customers = append(data.Customers, domain.Customer{ID: prefix + "customer", Name: "Customer", Status: "active"})
		data.CustomerRequests = append(data.CustomerRequests,
			domain.CustomerRequest{ID: prefix + "request-open", CustomerID: prefix + "customer", Attachments: attachments("request-open"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.CustomerRequest{ID: prefix + "request-private-project", CustomerID: prefix + "customer", ProjectID: "m2-unrelated", Attachments: attachments("request-private-project"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.CustomerRequest{ID: prefix + "request-public-issue", CustomerID: prefix + "customer", IssueID: publicIssue.ID, Attachments: attachments("request-public-issue"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.CustomerRequest{ID: prefix + "request-private-issue", CustomerID: prefix + "customer", IssueID: hiddenIssue.ID, Attachments: attachments("request-private-issue"), CreatedAt: stamp, UpdatedAt: stamp},
		)
		publicProject := domain.Project{
			ID: prefix + "project", Name: "Public project", SlugID: prefix + "project-slug", TeamIDs: []string{public},
			CommentAttachments: attachments("project-public", "duplicate", "duplicate"), CreatedAt: stamp, UpdatedAt: stamp,
		}
		archived := stamp
		archivedProject := domain.Project{
			ID: prefix + "archived-project", Name: "Archived project", SlugID: prefix + "archived-slug", TeamIDs: []string{public},
			CommentAttachments: attachments("project-archived"), ArchivedAt: &archived, CreatedAt: stamp, UpdatedAt: stamp,
		}
		data.Projects = append(data.Projects, publicProject, archivedProject)
		for index := range data.Projects {
			if data.Projects[index].ID == "m2-unrelated" {
				data.Projects[index].CommentAttachments = attachments("project-private", "duplicate-hidden")
			}
		}
		data.ProjectUpdates[publicProject.ID] = []domain.ProjectUpdate{{ID: prefix + "update-public", ProjectID: publicProject.ID, Body: "u", User: owner, Attachments: attachments("update-public"), CreatedAt: stamp}}
		data.ProjectUpdates["m2-unrelated"] = []domain.ProjectUpdate{{ID: prefix + "update-private", ProjectID: "m2-unrelated", Body: "u", User: owner, Attachments: attachments("update-private", "duplicate-hidden"), CreatedAt: stamp}}
		data.Documents = append(data.Documents,
			domain.Document{ID: prefix + "doc-public", SlugID: prefix + "doc-public", Title: "Public", Content: "body", Creator: owner, TeamIDs: []string{public}, Attachments: attachments("document-public", "duplicate", "missing"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.Document{ID: prefix + "doc-private", SlugID: prefix + "doc-private", Title: "Private", Content: "body", Creator: owner, TeamIDs: []string{private}, Attachments: attachments("document-private"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.Document{ID: prefix + "doc-shared", SlugID: prefix + "doc-shared", Title: "Shared", Content: "body", Creator: owner, TeamIDs: []string{private}, Attachments: attachments("document-user"),
				Permissions: []domain.DocumentPermission{{ID: prefix + "grant", DocumentID: prefix + "doc-shared", SubjectType: "user", SubjectID: member.ID, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp}}, CreatedAt: stamp, UpdatedAt: stamp},
			domain.Document{ID: prefix + "doc-archived", SlugID: prefix + "doc-archived", Title: "Archived", Content: "body", Creator: owner, TeamIDs: []string{public}, Attachments: attachments("document-archived"), ArchivedAt: &archived, CreatedAt: stamp, UpdatedAt: stamp},
		)
		trashed, _ := json.Marshal(domain.Document{ID: prefix + "doc-trashed", Title: "Trashed", Attachments: attachments("document-trashed")})
		data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "document", ResourceID: prefix + "doc-trashed", Title: "Trashed", Payload: trashed, DeletedBy: owner, DeletedAt: stamp, ExpiresAt: stamp.Add(30 * 24 * time.Hour)})
		deleted := stamp
		data.AgentSessions = append(data.AgentSessions,
			domain.AgentSession{ID: prefix + "session", SlugID: prefix + "session", UserID: member.ID, Title: "Mine", CreatedAt: stamp, UpdatedAt: stamp,
				Messages: []domain.AgentMessage{{ID: prefix + "m1", Role: "user", Content: "see file", CreatedAt: stamp, Attachments: []domain.AgentMessageAttachment{{Name: "f", URL: scaleP21SW268URL("session-own")}}}}},
			domain.AgentSession{ID: prefix + "session-deleted", SlugID: prefix + "session-deleted", UserID: member.ID, Title: "Gone", DeletedAt: &deleted, CreatedAt: stamp, UpdatedAt: stamp,
				Messages: []domain.AgentMessage{{ID: prefix + "m2", Role: "user", Content: "see file", CreatedAt: stamp, Attachments: []domain.AgentMessageAttachment{{Name: "f", URL: scaleP21SW268URL("session-deleted")}}}}},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: prefix + "initiative-public", Name: "Public", SlugID: prefix + "ip", LeadTeamID: public, CommentAttachments: attachments("initiative-public"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.Initiative{ID: prefix + "initiative-private", Name: "Private", SlugID: prefix + "iq", LeadTeamID: private, CommentAttachments: attachments("initiative-private"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.Initiative{ID: prefix + "initiative-workspace", Name: "Workspace", SlugID: prefix + "iw", CommentAttachments: attachments("initiative-workspace"), CreatedAt: stamp, UpdatedAt: stamp},
		)
		data.InitiativeUpdates[prefix+"initiative-public"] = []domain.InitiativeUpdate{{ID: prefix + "iu-public", InitiativeID: prefix + "initiative-public", Body: "u", User: owner, Attachments: attachments("initiative-update-public"), CreatedAt: stamp}}
		data.InitiativeUpdates[prefix+"initiative-private"] = []domain.InitiativeUpdate{{ID: prefix + "iu-private", InitiativeID: prefix + "initiative-private", Body: "u", User: owner, Attachments: attachments("initiative-update-private"), CreatedAt: stamp}}
		data.Reviews = append(data.Reviews,
			domain.CodeReview{ID: prefix + "review-open", SlugID: prefix + "ro", ExternalID: "1", RepositoryName: "r", CommentAttachments: attachments("review-open"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.CodeReview{ID: prefix + "review-hidden", SlugID: prefix + "rh", ExternalID: "2", RepositoryName: "r", IssueIDs: []string{hiddenIssue.ID, privateIssue}, CommentAttachments: attachments("review-hidden"), CreatedAt: stamp, UpdatedAt: stamp},
			domain.CodeReview{ID: prefix + "review-public", SlugID: prefix + "rp", ExternalID: "3", RepositoryName: "r", IssueIDs: []string{publicIssue.ID}, CommentAttachments: attachments("review-public"), CreatedAt: stamp, UpdatedAt: stamp},
		)
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"issue-public", "issue-private", "loop-attached", "loop-draft-own", "loop-draft-linked",
		"request-open", "request-private-project", "request-public-issue", "request-private-issue",
		"project-public", "project-archived", "project-private", "duplicate", "duplicate-hidden",
		"update-public", "update-private", "document-public", "document-private", "document-user", "document-archived",
		"document-trashed", "missing", "session-own", "session-deleted",
		"initiative-public", "initiative-private", "initiative-workspace", "initiative-update-public", "initiative-update-private",
		"review-open", "review-hidden", "review-public", "other-workspace",
	} {
		urls = append(urls, scaleP21SW268URL(name))
	}
	// A second membership: only its own owner (the member) sees its file.
	other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), member), "SW268 other", idgen.New("sw268-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
	restore = store.ForceFullMutationsForTesting(true)
	err = repo.MutateWorkspace(store.ContextWithActor(t.Context(), member), other.Workspace.URLKey, "scale_p21.sw268_other", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: prefix + "other-project", Name: "Other", SlugID: prefix + "other", CommentAttachments: attachments("other-workspace"), CreatedAt: stamp, UpdatedAt: stamp})
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
		ClientID: idgen.New("sw268-client-"), Name: "SW268 app", InstalledBy: f.data.Viewer.ID,
		Active: true, TeamIDs: []string{f.public}, Scopes: []string{"read"},
	}, "sw268-secret")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.appUsers = append(f.resources.appUsers, struct{ workspace, id string }{workspace, installed.UserID})
	app, err := repo.UserByID(t.Context(), installed.UserID)
	if err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()
	for _, url := range urls {
		name := strings.TrimPrefix(url, "/uploads/")
		if strings.Contains(name, "missing") {
			continue
		}
		if err := os.WriteFile(filepath.Join(uploads, name), []byte("file "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	principals := append(append([]scaleP21M2Principal{}, f.principals...), scaleP21M2Principal{"app", app})
	return scaleP21SW268Fixture{scaleP21M2Fixture: f, other: other.Workspace.URLKey, principals: principals, urls: urls, uploads: uploads}
}

type scaleP21SW268Outcome struct {
	Status   int
	Header   http.Header
	Body     string
	Readable bool
}

func scaleP21SW268Serve(t *testing.T, f scaleP21SW268Fixture, source *store.SQLiteStore, user domain.User, url string) scaleP21SW268Outcome {
	t.Helper()
	service := &server{store: source, uploadPath: f.uploads}
	name := strings.TrimPrefix(url, "/uploads/")
	request := func() (*http.Request, func()) {
		r := f.literalRequest(user, nil, nil)
		r.Method, r.URL.Path = http.MethodGet, url
		r.SetPathValue("name", name)
		return beginRequestResourceReads(r)
	}
	decodes, held := source.WorkspaceMetadataDecodes(), source.MetadataWorkUsedForTesting()
	r, closeOutput := request()
	writer := httptest.NewRecorder()
	service.serveUpload(writer, r)
	closeOutput()
	r, closeOutput = request()
	readable := service.fileReadable(r, name)
	closeOutput()
	if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != held || source.WorkspaceMetadataDecodes() != decodes) {
		t.Fatal("bounded upload visibility retained work or decoded a workspace", source.MetadataWorkUsedForTesting())
	}
	return scaleP21SW268Outcome{writer.Code, writer.Header().Clone(), writer.Body.String(), readable}
}

func TestScaleP21SW268AttachmentVisibilityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW268Seed(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, reload := range []bool{false, true} {
			if reload {
				for _, key := range []string{workspace, f.other} {
					if err := repo.ReloadWorkspace(t.Context(), key); err != nil {
						t.Fatal(err)
					}
				}
			}
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			visible := map[string]map[string]bool{}
			for _, principal := range f.principals {
				for _, url := range f.urls {
					t.Run(fmt.Sprintf("reload=%t/%s/%s", reload, principal.name, strings.TrimPrefix(url, scaleP21SW268Prefix)), func(t *testing.T) {
						want := scaleP21SW268Serve(t, f, repo, principal.user, url)
						got := scaleP21SW268Serve(t, f, view, principal.user, url)
						if !reflect.DeepEqual(want, got) {
							t.Fatalf("upload visibility differs:\nlegacy=%+v\nbounded=%+v", want, got)
						}
						if visible[url] == nil {
							visible[url] = map[string]bool{}
						}
						visible[url][principal.name] = want.Readable
						if want.Readable != (want.Status == http.StatusOK) && !strings.Contains(url, "missing") {
							t.Fatal("readable and served disagree", want.Status, want.Readable)
						}
					})
				}
			}
			// A work-budget refusal is retryable, never "not found": the
			// member's own visible files answer 503 + Retry-After while the
			// budget is held, a check that needs no read is unaffected, and
			// the same requests match the original once it is released.
			t.Run(fmt.Sprintf("reload=%t/work-budget-refusal", reload), func(t *testing.T) {
				member := f.principals[2].user
				for _, test := range []struct {
					name    string
					refused bool
				}{
					{"document-public", true}, {"session-own", true}, {"loop-draft-own", true},
					{"unreferenced", true}, {"agent", false},
				} {
					url := scaleP21SW268URL(test.name)
					if test.name == "agent" {
						url = "/uploads/agent_upload_" + agentUploadOwner(member.ID) + "_sw268.png"
					}
					want := scaleP21SW268Serve(t, f, repo, member, url)
					release := scaleP21SW266HoldBudget(t, view)
					got := scaleP21SW268Serve(t, f, view, member, url)
					release()
					if !test.refused {
						if !reflect.DeepEqual(want, got) {
							t.Fatalf("%s: a check needing no read changed under a held budget:\nlegacy=%+v\nbounded=%+v", test.name, want, got)
						}
						continue
					}
					var payload map[string]string
					if got.Status != http.StatusServiceUnavailable || got.Header.Get("Retry-After") != "2" || got.Readable ||
						json.Unmarshal([]byte(got.Body), &payload) != nil || payload["error"] == "" {
						t.Fatalf("%s: refused upload check is not a retryable 503: %+v", test.name, got)
					}
					if after := scaleP21SW268Serve(t, f, view, member, url); !reflect.DeepEqual(want, after) {
						t.Fatalf("%s: upload differs after the budget was released:\nlegacy=%+v\nbounded=%+v", test.name, want, after)
					}
				}
			})
			// The fixture must exercise both outcomes where the original has a
			// viewer predicate, and the expected outcome where it has none.
			expect := func(name, principal string, readable bool) {
				t.Helper()
				if got := visible[scaleP21SW268URL(name)][principal]; got != readable {
					t.Errorf("reload=%t %s for %s: readable=%t want %t", reload, name, principal, got, readable)
				}
			}
			for _, principal := range f.principals {
				expect("unreferenced", principal.name, false)
				expect("loop-attached", principal.name, true)
				expect("loop-draft-linked", principal.name, true)
				expect("session-deleted", principal.name, false)
				expect("document-trashed", principal.name, false)
				expect("missing", principal.name, visible[scaleP21SW268URL("document-public")][principal.name])
			}
			expect("loop-draft-own", "member-private-outsider", true)
			expect("loop-draft-own", "admin", false)
			expect("session-own", "member-private-outsider", true)
			expect("session-own", "admin-private-outsider", false)
			expect("request-open", "member-private-outsider", true)
			expect("request-open", "guest-explicit", false)
			expect("request-private-project", "admin", true)
			expect("request-private-project", "member-private-outsider", false)
			expect("document-public", "member-private-outsider", true)
			expect("document-private", "member-private-outsider", false)
			expect("document-private", "admin", true)
			expect("document-user", "member-private-outsider", true)
			expect("document-user", "guest-explicit", false)
			expect("project-private", "member-private-outsider", false)
			expect("project-private", "sub-team-inheritance", true)
			expect("duplicate", "member-private-outsider", true)
			expect("duplicate-hidden", "member-private-outsider", false)
			expect("update-private", "member-private-outsider", false)
			expect("update-public", "member-private-outsider", true)
			expect("initiative-private", "member-private-outsider", false)
			expect("initiative-public", "member-private-outsider", true)
			expect("initiative-public", "guest-explicit", false)
			expect("initiative-update-public", "guest-explicit", false)
			expect("issue-private", "member-private-outsider", false)
			expect("issue-public", "member-private-outsider", true)
			expect("other-workspace", "member-private-outsider", true)
			expect("other-workspace", "admin-private-outsider", false)
		}
	}
}
