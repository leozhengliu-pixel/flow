package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP20PermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			if err := repo.MutateWorkspace(t.Context(), workspace, "p20.fixture", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p20-public", Key: "P20O"}, domain.Team{ID: "p20-private", Key: "P20P", Private: true}, domain.Team{ID: "p20-child", Key: "P20C"})
				data.TeamSettings["p20-public"] = domain.TeamSettings{TeamID: "p20-public", Access: "public"}
				data.TeamSettings["p20-private"] = domain.TeamSettings{TeamID: "p20-private", Access: "private"}
				data.TeamSettings["p20-child"] = domain.TeamSettings{TeamID: "p20-child", ParentTeamID: "p20-private", Access: "public"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, "p20-private", f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			member, _ := scaleP03Join(t, f, "P20 member", idgen.New("p20-member-")+"@example.test", "member", "p20-public")
			admin, _ := scaleP03Join(t, f, "P20 outside admin", idgen.New("p20-admin-")+"@example.test", "admin", "p20-public")
			guest, guestUser := scaleP03Join(t, f, "P20 guest", idgen.New("p20-guest-")+"@example.test", "guest", "p20-public")
			inherited, inheritedUser := scaleP03Join(t, f, "P20 inherited", idgen.New("p20-inherited-")+"@example.test", "member", "p20-private")
			unrestricted := scaleP03Key(t, f, []string{"read"}, nil)
			selected := scaleP03Key(t, f, []string{"read"}, []string{"p20-public"})
			empty := idgen.New("p20-empty-")
			at := time.Now().UTC().Truncate(time.Second)
			if err := repo.MutateWorkspace(t.Context(), workspace, "p20.fixture", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "p20-empty", SecretHash: secretHash(empty), CreatorID: data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}})
				data.Projects = append(data.Projects, domain.Project{ID: "p20-public-project", Name: "Public reference", TeamIDs: []string{"p20-public"}}, domain.Project{ID: "p20-hidden-project", Name: "Private reference", TeamIDs: []string{"p20-private"}})
				for index, team := range []string{"p20-public", "p20-private", "p20-child"} {
					id := []string{"p20-open", "p20-secret", "p20-inherited"}[index]
					doc := domain.Document{ID: id, SlugID: id + "-current", PreviousSlugIDs: []string{id + "-old"}, Title: id, Content: "Current body", Creator: data.Viewer, TeamIDs: []string{team}, CreatedAt: at, UpdatedAt: at, Version: 1,
						Permissions: []domain.DocumentPermission{{ID: id + "-owner", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner"}, {ID: id + "-workspace", SubjectType: "workspace", Role: "editor"}, {ID: id + "-guest", SubjectType: "user", SubjectID: guestUser.ID, Role: "viewer"}},
						Revisions:   []domain.DocumentRevision{{ID: id + "-current-version", DocumentID: id, Title: id, Content: "Current body", Author: data.Viewer, CreatedAt: at}, {ID: id + "-version", DocumentID: id, Title: "Earlier", Content: strings.Repeat("history ", 1024), Author: inheritedUser, CreatedAt: at.Add(-time.Hour)}}}
					if index == 0 {
						doc.ProjectIDs = []string{"p20-public-project", "p20-hidden-project"}
					}
					data.Documents = append(data.Documents, doc)
					doc.ID += "-deleted"
					doc.SlugID += "-deleted"
					raw, _ := json.Marshal(doc)
					data.Trash = append(data.Trash, domain.TrashEntry{ID: id + "-trash", ResourceType: "document", ResourceID: doc.ID, Title: doc.Title, TeamIDs: doc.TeamIDs, Payload: raw, DeletedBy: data.Viewer, DeletedAt: at, ExpiresAt: at.AddDate(0, 0, 30), Links: &domain.TrashLinks{}})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "P20 foreign", idgen.New("p20-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			principals := []struct {
				name, token string
				client      *http.Client
			}{
				{"admin", "", f.admin}, {"admin-private-outsider", "", admin}, {"member-private-outsider", "", member}, {"guest-explicit", "", guest}, {"subteam-inheritance", "", inherited}, {"key-unrestricted", unrestricted, nil}, {"key-selected-set", selected, nil}, {"key-selected-empty", empty, nil},
			}
			for _, principal := range principals {
				t.Run(principal.name, func(t *testing.T) {
					if principal.token != "" {
						scaleP11WarmCredential(t, repo, workspace, principal.token)
					}
					read := func(path, key string) (int, []byte) {
						response := scaleP03Read(t, f, principal.client, principal.token, "GET", path, key)
						return response.Code, response.Body.Bytes()
					}
					status, raw := read("/api/bootstrap", workspace)
					if status != http.StatusOK {
						t.Fatalf("bootstrap: %d %.200s", status, raw)
					}
					var legacy domain.Bootstrap
					if err := json.Unmarshal(raw, &legacy); err != nil {
						t.Fatal(err)
					}
					for _, kind := range []string{"users", "members", "teamMembers", "invitations", "departedMembers"} {
						want, _ := scaleP20Nodes(legacy, kind)
						var got []any
						cursor := ""
						for {
							status, raw := read("/api/resource-windows/members?collection="+kind+"&limit=2&cursor="+cursor, workspace)
							if status != http.StatusOK {
								t.Fatalf("%s: %d %.200s", kind, status, raw)
							}
							if len(raw) > 256<<10 {
								t.Fatal("member page exceeds budget")
							}
							var page store.RecordPage
							if err := json.Unmarshal(raw, &page); err != nil {
								t.Fatal(err)
							}
							for _, item := range page.Items {
								var value any
								_ = json.Unmarshal(item.Data, &value)
								got = append(got, value)
							}
							if !page.HasMore {
								break
							}
							if cursor == page.Next || page.Next == "" {
								t.Fatal("cursor did not advance")
							}
							cursor = page.Next
						}
						wantRaw, _ := json.Marshal(want)
						var expected []any
						_ = json.Unmarshal(wantRaw, &expected)
						if len(got) != len(expected) || len(got) > 0 && !reflect.DeepEqual(got, expected) {
							t.Fatalf("%s differs from bootstrap", kind)
						}
					}
					for _, id := range []string{"p20-open", "p20-open-old", "p20-secret", "p20-inherited", "p20-open-deleted"} {
						status, old := read("/api/documents/"+id, workspace)
						gotStatus, current := read("/api/documents/"+id+"?history=summary", workspace)
						if status != http.StatusOK {
							if gotStatus == http.StatusOK {
								t.Fatalf("hidden %s exposed", id)
							}
							continue
						}
						if gotStatus != http.StatusOK {
							t.Fatalf("%s unavailable: %d %.200s", id, gotStatus, current)
						}
						var a, b map[string]any
						_ = json.Unmarshal(old, &a)
						_ = json.Unmarshal(current, &b)
						delete(a, "revisions")
						delete(b, "revisions")
						if !reflect.DeepEqual(a, b) {
							t.Fatalf("document fields differ: %s", id)
						}
						var doc domain.Document
						_ = json.Unmarshal(old, &doc)
						for _, revision := range doc.Revisions {
							status, body := read("/api/documents/"+id+"/history/"+revision.ID, workspace)
							var got domain.DocumentRevision
							_ = json.Unmarshal(body, &got)
							if status != http.StatusOK || !reflect.DeepEqual(got, revision) {
								t.Fatalf("selected version differs: %s status=%d", id, status)
							}
						}
					}
					for _, id := range []string{"p20-open", "p20-secret", "p20-inherited"} {
						status, raw := read("/api/resource-windows/trash?resourceId="+id+"-deleted", workspace)
						if status == http.StatusOK {
							var got domain.TrashEntry
							_ = json.Unmarshal(raw, &got)
							if got.Links != nil || !slices.ContainsFunc(legacy.Trash, func(item domain.TrashEntry) bool { return item.ID == got.ID }) {
								t.Fatal("trash links or hidden entry exposed")
							}
						}
					}
					if status, _ := read("/api/documents/p20-open?history=summary", other.Workspace.URLKey); status == http.StatusOK {
						t.Fatal("cross-workspace document exposed")
					}
				})
			}
			t.Setenv("FLOW_INDEXED_READS", "on")
			first := scaleP03Read(t, f, inherited, "", "GET", "/api/resource-windows/members?collection=users&limit=1", workspace)
			var page store.RecordPage
			_ = json.Unmarshal(first.Body.Bytes(), &page)
			if first.Code != http.StatusOK || page.Next == "" {
				t.Fatal("revocation fixture has no cursor")
			}
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, "p20-private", inheritedUser.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			replayed := scaleP03Read(t, f, inherited, "", "GET", "/api/resource-windows/members?collection=users&limit=1&cursor="+page.Next, workspace)
			if replayed.Code == http.StatusOK {
				t.Fatal("pre-revocation continuation replayed")
			}
		})
	}
}
