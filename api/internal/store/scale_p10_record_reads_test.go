package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP10CursorKey(t *testing.T) {
	t.Helper()
	t.Setenv(connectorSecretKeyEnv, base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func TestScaleP10RecordPermissionDifferential(t *testing.T) {
	scaleP10CursorKey(t)
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			s, base := fixture.SQLiteStore, fixture.data
			workspace := base.Workspace.URLKey
			if err := s.MutateWorkspace(t.Context(), workspace, "scale_p10.fixture", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p10-public", Key: "P10"}, domain.Team{ID: "p10-private", Key: "P10P", Private: true}, domain.Team{ID: "p10-child", Key: "P10C"})
				data.TeamSettings["p10-public"] = domain.TeamSettings{TeamID: "p10-public", Access: "public"}
				data.TeamSettings["p10-private"] = domain.TeamSettings{TeamID: "p10-private", Access: "private"}
				data.TeamSettings["p10-child"] = domain.TeamSettings{TeamID: "p10-child", Access: "public", ParentTeamID: "p10-private"}
				data.Labels = append(data.Labels, domain.IssueLabel{ID: "p10-workspace-label", Scope: "workspace"}, domain.IssueLabel{ID: "p10-private-label", Scope: "p10-private"})
				actor := domain.User{ID: "p10-actor", App: true, AppTeamIDs: []string{"p10-public", "p10-private"}}
				data.Projects = append(data.Projects,
					domain.Project{ID: "p10-open", Name: "Open", SlugID: "open-000000000010", PreviousSlugIDs: []string{"old-p10"}, TeamIDs: []string{"p10-public", "p10-private"}, DependencyIDs: []string{"p10-hidden", "missing"}, Initiatives: []string{"p10-initiative", "p10-private-initiative"}, LabelIDs: []string{"p10-workspace-label", "p10-private-label"}, Description: "Full body", IssueCount: 42, Progress: .73,
						Resources: []domain.ProjectResource{{ID: "p10-resource", PinnedTeamIDs: []string{"p10-public", "p10-private"}}}, Creator: &actor, Lead: &actor, Comments: []domain.Comment{{ID: "p10-comment", User: actor}}, DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: "p10-revision", Author: actor}},
						IssueCountHistory: []domain.ProjectProgressHistoryPoint{{Value: 42}}, ProgressHistory: []domain.ProjectProgressHistoryPoint{{Value: .73}}},
					domain.Project{ID: "p10-hidden", Name: "Hidden", TeamIDs: []string{"p10-private"}},
					domain.Project{ID: "p10-child-project", TeamIDs: []string{"p10-child"}},
					domain.Project{ID: "p10-nil", TeamIDs: nil, DependencyIDs: nil},
					domain.Project{ID: "p10-empty", TeamIDs: []string{}, DependencyIDs: []string{}},
				)
				data.Initiatives = append(data.Initiatives,
					domain.Initiative{ID: "p10-initiative", Name: "Visible", ProjectIDs: []string{"p10-open", "p10-hidden"}, ParentInitiativeIDs: []string{"p10-private-initiative"}, ContributingTeamIDs: []string{"p10-public", "p10-private"}, LabelIDs: []string{"p10-workspace-label", "p10-private-label"}, Creator: actor, Owner: &actor, Comments: []domain.Comment{{ID: "p10-initiative-comment", User: actor}}, DescriptionHistory: []domain.InitiativeDescriptionRevision{{ID: "p10-history", Editor: actor}}},
					domain.Initiative{ID: "p10-private-initiative", ProjectIDs: []string{"p10-hidden"}, LeadTeamID: "p10-private", Creator: base.Viewer},
				)
				data.Favorites = append(data.Favorites, domain.Favorite{ID: "p10-favorite", UserID: base.Viewer.ID, ResourceType: "initiative", ResourceID: "p10-initiative"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			join := func(role string, teams []string) domain.User {
				user, _, err := s.Register(t.Context(), "P10 "+role, idgen.New("p10-")+"@example.test", "p10-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := s.ErasePersonFromWorkspace(context.Background(), workspace, user.ID); err != nil {
						t.Error("clean up owned membership", err)
					}
					if err := s.EraseAccount(context.Background(), user.ID); err != nil {
						t.Error("clean up owned account", err)
					}
				})
				invitation, err := s.Invite(t.Context(), base.Workspace.ID, base.Viewer.ID, user.Email, role, teams)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
					t.Fatal(err)
				}
				return user
			}
			outsider := join("admin", []string{"p10-public"})
			member := join("member", []string{"p10-public", "p10-private"})
			guest := join("guest", []string{"p10-public"})
			privateOutsider := join("member", []string{"p10-public"})
			grantCtx := WithoutIssueRecordMutations(ContextWithActor(t.Context(), base.Viewer))
			if err := s.MutateWorkspace(grantCtx, workspace, "scale_p10.fixture", "", nil, func(data *domain.Bootstrap) error {
				data.Issues = append(data.Issues, domain.Issue{ID: "p10-granted-issue", Identifier: "P10P-1", Number: 1, Title: "Explicit grant", Team: domain.Team{ID: "p10-private", Key: "P10P"}, Project: &domain.ProjectSummary{ID: "p10-hidden"},
					Permissions: []domain.IssuePermission{{ID: idgen.New("p10-grant-"), IssueID: "p10-granted-issue", SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}})
				data.Issues = append(data.Issues, domain.Issue{ID: "p10-private-demand", Identifier: "P10P-2", Number: 2, Title: "Private demand", Team: domain.Team{ID: "p10-private", Key: "P10P"}, Project: &domain.ProjectSummary{ID: "p10-open"}})
				data.Customers = append(data.Customers, domain.Customer{ID: "p10-demand-customer", Name: "Demand"})
				data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: "p10-demand", CustomerID: "p10-demand-customer", IssueID: "p10-private-demand"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			_, grantedAccess, err := s.IssueQueryAccess(t.Context(), workspace, guest.ID)
			if err != nil {
				t.Fatal(err)
			}
			granted, err := s.VisibleIssueRecordIDs(t.Context(), IssueRecordQuery{Workspace: workspace, Access: &grantedAccess}, []string{"p10-granted-issue"})
			if err != nil || !granted["p10-granted-issue"] {
				t.Fatal("guest fixture did not exercise an explicit grant", err)
			}
			for _, kind := range []string{"projects", "initiatives"} {
				scaleP05Finish(t, s, workspace, kind)
			}
			other, err := s.CreateWorkspace(ContextWithActor(t.Context(), base.Viewer), "P10 other", idgen.New("p10-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
					t.Error("clean up owned second workspace", err)
				}
			})
			if _, err := s.ResolveAccess(t.Context(), other.Workspace.URLKey, guest.ID, nil); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("cross-workspace principal accepted", err)
			}
			if err := s.SetTeamMembership(t.Context(), base.Workspace.ID, "p10-private", base.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			viewers := []struct {
				name string
				user domain.User
				key  *domain.APIKey
			}{
				{"admin", base.Viewer, nil}, {"admin-outside-private", outsider, nil}, {"member-subteam", member, nil},
				{"guest-explicit-grant", guest, nil}, {"private-outsider", privateOutsider, nil},
				{"key-unrestricted", base.Viewer, &domain.APIKey{Scopes: []string{"read"}}},
				{"key-selected-empty", base.Viewer, &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
				{"key-selected-set", base.Viewer, &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{"p10-public"}}},
				{"key-owner-outside-private", outsider, &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{"p10-private"}}},
			}
			keys := []RecordKey{{"projects", "p10-open"}, {"projects", "p10-hidden"}, {"projects", "p10-child-project"}, {"projects", "p10-nil"}, {"projects", "p10-empty"}, {"initiatives", "p10-initiative"}, {"initiatives", "p10-private-initiative"}, {"projects", "missing"}}
			for _, viewer := range viewers {
				t.Run(viewer.name, func(t *testing.T) {
					access, err := s.ResolveAccess(t.Context(), workspace, viewer.user.ID, viewer.key)
					if err != nil {
						t.Fatal(err)
					}
					for _, projection := range []string{"summary", "detail"} {
						batch, err := s.readAccessRecords(t.Context(), access, keys, projection)
						if err != nil || len(batch.Items) != len(keys) || batch.AuthzEpoch != access.AuthzEpoch {
							t.Fatalf("batch: %v", err)
						}
						for i, key := range keys {
							oracle, allowed, err := s.ResourceReadMetadata(t.Context(), access, key.Kind, key.ID)
							if err != nil {
								t.Fatal(err)
							}
							visible := allowed && (len(oracle.Projects) > 0 || len(oracle.Initiatives) > 0)
							item := batch.Items[i]
							if item.Missing == visible {
								t.Fatalf("%s visibility changed", key.ID)
							}
							if !visible {
								if item.Version != "" || len(item.Data) != 0 {
									t.Fatal("hidden record disclosed data or version")
								}
								continue
							}
							if key.Kind == "projects" {
								want := oracle.Projects[0]
								if projection == "summary" {
									want = ProjectListProjection(want)
								}
								scaleP10Equal(t, want, item.Data)
								if viewer.key != nil && viewer.key.TeamRestriction == "selected" {
									var actual domain.Project
									if err := json.Unmarshal(item.Data, &actual); err != nil {
										t.Fatal(err)
									}
									if actual.IssueCount != 0 || actual.Progress != 0 || slices.Contains(actual.DependencyIDs, "p10-hidden") || slices.Contains(actual.Initiatives, "p10-private-initiative") || slices.Contains(actual.LabelIDs, "p10-private-label") {
										t.Fatal("selected key leaked references or stored aggregates")
									}
								}
							} else {
								want := oracle.Initiatives[0]
								if projection == "summary" {
									want.Description, want.DescriptionHistory, want.Resources, want.Comments = "", nil, nil, nil
								}
								scaleP10Equal(t, want, item.Data)
							}
						}
					}
					for _, query := range []struct {
						field, value string
					}{
						{"labels", "p10-private-label"}, {"teams", "p10-private"}, {"customers", "customer:p10-demand-customer"},
					} {
						filter, _ := json.Marshal(ProjectRecordQuery{Filters: []ProjectDirectoryFilter{{Field: query.field, Operator: "is", Values: []string{query.value}}}})
						page, err := s.readAccessRecordPage(t.Context(), access, PageRequest{Kind: "projects", Filter: string(filter)})
						if viewer.user.ID == guest.ID && query.field == "customers" {
							if !errors.Is(err, ErrMetadataRecordQuery) {
								t.Fatal("guest customer filter was allowed", err)
							}
							continue
						}
						if err != nil {
							t.Fatal(err)
						}
						if viewer.key != nil && viewer.key.TeamRestriction == "selected" && len(page.Items) != 0 {
							t.Fatal("a filter matched a hidden reference or private customer demand")
						}
						if viewer.name == "key-unrestricted" && !slices.ContainsFunc(page.Items, func(item MetadataRecord) bool { return item.Key.ID == "p10-open" }) {
							t.Fatal("unrestricted key lost an owner-visible filtered record")
						}
					}
					for _, order := range []string{"collection", "createdAt", "updatedAt"} {
						q := PageRequest{Kind: "projects", Limit: 1, Order: order, Filter: `{"Archived":"all"}`}
						seen := []string{}
						for range 20 {
							page, err := s.readAccessRecordPage(t.Context(), access, q)
							if err != nil {
								t.Fatal(err)
							}
							if viewer.name == "key-selected-empty" || viewer.name == "key-owner-outside-private" {
								if len(page.Items) != 0 || page.HasMore || page.Next != "" {
									t.Fatal("empty effective scope exposed a collection continuation")
								}
							}
							raw, err := json.Marshal(page)
							if err != nil || len(raw)+1 > MetadataRecordPageMaxBytes {
								t.Fatal("page exceeded its encoded budget")
							}
							for _, item := range page.Items {
								if slices.Contains(seen, item.Key.ID) {
									t.Fatal("duplicate page item")
								}
								seen = append(seen, item.Key.ID)
							}
							if !page.HasMore {
								break
							}
							q.Cursor = page.Next
						}
						for _, key := range keys {
							if key.Kind != "projects" || key.ID == "missing" {
								continue
							}
							data, allowed, err := s.ResourceReadMetadata(t.Context(), access, key.Kind, key.ID)
							if err != nil || slices.Contains(seen, key.ID) != (allowed && len(data.Projects) > 0) {
								t.Fatalf("%s page visibility differs from record read: %v", key.ID, err)
							}
						}
					}
					if _, err := s.ReadRecordPage(t.Context(), access, PageRequest{Kind: "projects"}); !errors.Is(err, ErrMetadataCatalogGap) {
						t.Fatal("shadow catalog served through public read contract", err)
					}
					if viewer.name == "key-selected-empty" || viewer.name == "key-owner-outside-private" {
						page, err := s.readAccessRecordPage(t.Context(), access, PageRequest{Kind: "initiatives", Limit: 1})
						if err != nil || len(page.Items) != 0 || page.HasMore || page.Next != "" {
							t.Fatal("empty effective scope exposed an initiative continuation", err)
						}
					}
					if _, err := s.ReadRecords(t.Context(), access, keys, "detail"); !errors.Is(err, ErrMetadataCatalogGap) {
						t.Fatal("shadow catalog served through public batch contract", err)
					}
					if viewer.key != nil && viewer.key.TeamRestriction == "selected" {
						if _, err := s.QueryProjectDirectoryWithAccess(t.Context(), access, ProjectRecordQuery{Workspace: workspace, Admin: true, IncludeTotal: true}); !errors.Is(err, ErrMetadataCatalogGap) {
							t.Fatal("raw catalog directory bypassed the credential projection", err)
						}
					}
				})
			}
			access, err := s.ResolveAccess(t.Context(), workspace, member.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			heldQuery := PageRequest{Kind: "projects", Limit: 1}
			heldPage, err := s.readAccessRecordPage(t.Context(), access, heldQuery)
			if err != nil || !heldPage.HasMore {
				t.Fatal("revocation fixture has no continuation", err)
			}
			if err := s.SetTeamMembership(t.Context(), base.Workspace.ID, "p10-private", member.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.readAccessRecords(t.Context(), access, keys, "detail"); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("queued batch survived revocation", err)
			}
			if _, err := s.QueryProjectDirectoryWithAccess(t.Context(), access, ProjectRecordQuery{Workspace: workspace, Admin: true}); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("queued directory survived revocation", err)
			}
			fresh, err := s.ResolveAccess(t.Context(), workspace, member.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			heldQuery.Cursor = heldPage.Next
			if _, err := s.readAccessRecordPage(t.Context(), fresh, heldQuery); !errors.Is(err, ErrMetadataCursor) {
				t.Fatal("cursor from a revoked epoch was replayed", err)
			}
			access.WorkspaceID = "another-workspace"
			if _, err := s.readAccessRecordPage(t.Context(), access, PageRequest{Kind: "projects"}); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("cross-workspace access accepted", err)
			}
		})
	}
}

func BenchmarkScaleP10AccessRecordPage(b *testing.B) {
	b.Setenv(connectorSecretKeyEnv, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	for _, scale := range []int{1, 10} {
		b.Run(fmt.Sprintf("body%d", scale), func(b *testing.B) {
			s, err := OpenSQLiteTestFixture(filepath.Join(b.TempDir(), "reads.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = s.Close() })
			data := s.Bootstrap()
			workspace := data.Workspace.URLKey
			body := strings.Repeat("x", scale*24<<10)
			for start := 0; start < 205; start += 16 {
				if err := s.MutateWorkspace(b.Context(), workspace, "scale_p10.fixture", "", nil, func(data *domain.Bootstrap) error {
					if start == 0 {
						data.Projects = nil
					}
					for i := start; i < min(start+16, 205); i++ {
						data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p10-%03d", i), Name: fmt.Sprintf("Project %03d", i), Description: body, TeamIDs: []string{data.Teams[0].ID}})
					}
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
			for {
				progress, err := s.BackfillMetadataCatalog(b.Context(), workspace, "projects")
				if err != nil {
					b.Fatal(err)
				}
				if progress.Complete {
					break
				}
			}
			access, err := s.ResolveAccess(b.Context(), workspace, data.Viewer.ID, nil)
			if err != nil {
				b.Fatal(err)
			}
			q := PageRequest{Kind: "projects", Limit: 100}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				page, err := s.readAccessRecordPage(b.Context(), access, q)
				if err != nil || len(page.Items) != 100 {
					b.Fatalf("items=%d err=%v", len(page.Items), err)
				}
			}
		})
	}
}

func TestScaleP10RecordCoreOwnership(t *testing.T) {
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		access, err := s.ResolveAccess(t.Context(), fixture.data.Workspace.URLKey, fixture.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		until := time.Now().UTC()
		access.policy.metadata.Viewer.AppScopes = []string{"read"}
		access.policy.metadata.Viewer.AppTeamIDs = []string{"team"}
		access.policy.metadata.Viewer.OutOfOfficeUntil = &until
		core, err := s.ReadRecordCore(t.Context(), access)
		if err != nil {
			t.Fatal(err)
		}
		core.Viewer.AppScopes[0], core.Viewer.AppTeamIDs[0] = "write", "other"
		*core.Viewer.OutOfOfficeUntil = time.Time{}
		if access.policy.metadata.Viewer.AppScopes[0] != "read" || access.policy.metadata.Viewer.AppTeamIDs[0] != "team" || access.policy.metadata.Viewer.OutOfOfficeUntil.IsZero() {
			t.Fatal("manifest core exposed a mutable policy object")
		}
	}
}

func TestScaleP10RecordPageAllocationBudget(t *testing.T) {
	scaleP10CursorKey(t)
	for _, fixture := range scaleP03Stores(t) {
		s, base := fixture.SQLiteStore, fixture.data
		if err := s.MutateWorkspace(t.Context(), base.Workspace.URLKey, "scale_p10.fixture", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = nil
			for i := range 105 {
				data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p10-%03d", i), Name: fmt.Sprintf("Project %03d", i), Summary: "Directory row", TeamIDs: []string{data.Teams[0].ID}})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		scaleP05Finish(t, s, base.Workspace.URLKey, "projects")
		access, err := s.ResolveAccess(t.Context(), base.Workspace.URLKey, base.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		result := testing.Benchmark(func(b *testing.B) {
			for range b.N {
				page, err := s.readAccessRecordPage(b.Context(), access, PageRequest{Kind: "projects", Limit: 100})
				if err != nil || len(page.Items) != 100 || !page.HasMore {
					failure = fmt.Errorf("items=%d hasMore=%t err=%v", len(page.Items), page.HasMore, err)
					b.Fatal(failure)
				}
			}
		})
		t.Logf("100-record page: %s %s", result.String(), result.MemString())
		if failure != nil || result.N == 0 || result.AllocedBytesPerOp() > 2<<20 {
			t.Fatalf("page allocation budget: bytes=%d err=%v", result.AllocedBytesPerOp(), failure)
		}
	}
}

func TestScaleP10CursorBindingAndPrivacy(t *testing.T) {
	scaleP10CursorKey(t)
	access := AccessContext{WorkspaceID: "w", PrincipalID: "u", CredentialScope: "scope", AuthzEpoch: "epoch"}
	q := PageRequest{Kind: "projects", Filter: `{"Archived":"all"}`, Order: "updatedAt"}
	plain := `{"i":"hidden-private-record","s":2}`
	cursor, err := sealRecordCursor(access, q, plain)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(cursor)
	if strings.Contains(string(raw), "hidden-private-record") {
		t.Fatal("cursor disclosed the last scanned hidden identity")
	}
	q.Cursor = cursor
	if got, err := openRecordCursor(access, q); err != nil || got != plain {
		t.Fatal("cursor did not roundtrip", err)
	}
	for _, field := range []string{"workspace", "principal", "scope", "epoch", "filter", "order", "kind", "tamper"} {
		t.Run(field, func(t *testing.T) {
			a, changed := access, q
			switch field {
			case "workspace":
				a.WorkspaceID = "other"
			case "principal":
				a.PrincipalID = "other"
			case "scope":
				a.CredentialScope = "other"
			case "epoch":
				a.AuthzEpoch = "other"
			case "filter":
				changed.Filter = "{}"
			case "order":
				changed.Order = "createdAt"
			case "kind":
				changed.Kind = "initiatives"
			case "tamper":
				changed.Cursor = fmt.Sprintf("%s!", cursor)
			}
			if _, err := openRecordCursor(a, changed); !errors.Is(err, ErrMetadataCursor) {
				t.Fatal("cursor was rebound", err)
			}
		})
	}
	q.Cursor = base64.RawURLEncoding.EncodeToString([]byte(`{"offset":1}`))
	if _, err := openRecordCursor(access, q); !errors.Is(err, ErrMetadataCursor) {
		t.Fatal("legacy directory token was accepted as a sync token")
	}
}

func TestScaleP10RecordQueryBounds(t *testing.T) {
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		access, err := s.ResolveAccess(t.Context(), fixture.data.Workspace.URLKey, fixture.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, projection := range []string{"detail", "summary", "invalid"} {
			if _, err := s.ReadRecords(t.Context(), access, make([]RecordKey, 51), projection); !errors.Is(err, ErrMetadataRecordQuery) {
				t.Fatal("unbounded batch accepted", err)
			}
		}
		if _, err := s.ReadRecordPage(t.Context(), access, PageRequest{Kind: "apiKeys"}); !errors.Is(err, ErrMetadataRecordQuery) {
			t.Fatal("private collection accepted", err)
		}
		batch, err := s.ReadRecords(t.Context(), access, nil, "detail")
		if err != nil {
			t.Fatal(err)
		}
		if batch.Items == nil || len(batch.Items) != 0 || batch.AuthzEpoch == "" {
			t.Fatal("empty batch lost its explicit empty collection")
		}
		raw, err := json.Marshal(batch)
		if err != nil || len(raw) > MetadataRecordPageMaxBytes {
			t.Fatal("batch exceeded wire budget")
		}
	}
}
