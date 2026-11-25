package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW309ResourceAuthorizationSQL(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21M2Setup(t, repo)
			if repo.BoundedMetadataEnabled() || !f.probe.BoundedMetadataEnabled() {
				t.Fatal("diagnostic requires a literal source and a bounded source")
			}
			workspace := f.data.Workspace.URLKey
			project := idgen.New("sw309-project-")
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw309_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: project, TeamIDs: []string{f.public}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			user := f.principals[2].user
			_, token, err := repo.Login(t.Context(), user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				Allowed bool
				Status  int
				Body    string
			}
			var expected outcome
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				mode := "legacy"
				if source.BoundedMetadataEnabled() {
					mode = "bounded"
				}
				request := f.literalRequest(user, nil, nil)
				request.Method = http.MethodGet
				request.URL, err = url.Parse("/api/projects/" + project)
				if err != nil {
					t.Fatal(err)
				}
				request.SetPathValue("id", project)
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
				request, release := beginRequestResourceReads(request)
				counts := map[string]int{}
				var mu sync.Mutex
				total := 0
				request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
					var pcs [96]uintptr
					stack := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
					var chain []string
					fallback := ""
					for {
						frame, more := stack.Next()
						name := frame.Function
						if strings.HasPrefix(name, "flow/api/internal/store.") {
							name = strings.TrimPrefix(name, "flow/api/internal/store.")
							if at := strings.Index(name, ".func"); at >= 0 {
								name = name[:at]
							}
							wrapper := name == "observeSQLStatement" ||
								strings.HasPrefix(name, "(*sqlDB).") ||
								strings.HasPrefix(name, "(*sqlDatabase).") ||
								strings.HasPrefix(name, "(*sqlTx).") ||
								strings.HasPrefix(name, "(*sqlStmt).")
							if !wrapper && fallback == "" {
								fallback = name
							}
							for _, relevant := range []string{
								"readResidentResourcePolicy", "scaleP21SW23ReadTeamPolicyRows",
								"(*scaleP21SW23TeamPolicyReaderAt).ReadAt",
								"teamPolicyRows", "scanResourcePolicyHeaders", "resourcePolicyRecords",
								"resourceAccessPrincipal", "ResolveAccess", "BeginResourceAuthorizationRead",
								"readMetadataStamp", "accessStampAndEpoch", "AuthzEpoch",
							} {
								if name == relevant || strings.HasSuffix(name, "."+relevant) {
									if !slices.Contains(chain, relevant) {
										chain = append(chain, relevant)
									}
									break
								}
							}
						}
						if !more {
							break
						}
					}
					label := strings.Join(chain, " <- ")
					if label == "" {
						label = fallback
					}
					if label == "" {
						label = "unattributed"
					}
					mu.Lock()
					total++
					counts[label]++
					mu.Unlock()
				}))
				recorder := httptest.NewRecorder()
				allowed := (&server{store: source}).authorizeWorkspaceRequest(recorder, request, user)
				if allowed {
					recorder.WriteHeader(http.StatusNoContent)
				}
				actual := outcome{Allowed: allowed, Status: recorder.Code, Body: recorder.Body.String()}
				release()
				release()
				if !allowed || actual.Status != http.StatusNoContent || actual.Body != "" {
					t.Fatalf("%s public project must authorize exactly: %+v", mode, actual)
				}
				if mode == "legacy" {
					expected = actual
				} else if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("actual project authorization differs: legacy=%+v bounded=%+v", expected, actual)
				}
				mu.Lock()
				t.Logf("resource authorization SQL mode=%s total=%d", mode, total)
				labels := make([]string, 0, len(counts))
				for label := range counts {
					labels = append(labels, label)
				}
				slices.Sort(labels)
				for _, label := range labels {
					t.Logf("resource authorization SQL mode=%s callsite=%s count=%d", mode, label, counts[label])
				}
				mu.Unlock()
				if total == 0 {
					t.Fatalf("%s SQL observer did not reach the actual authorization path", mode)
				}
				if source.BoundedMetadataEnabled() &&
					(source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("bounded authorization retained work or hydrated complete metadata")
				}
			}
		})
	}
}
