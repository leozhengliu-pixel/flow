package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW334ProducerUploadBasenameAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Run("producer-prefix-boundary", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, path := range []string{
				"/uploads/attachment_id_report%2Fpart.txt",
				"/uploads/customer_request_attachment_id_report%2Fpart.txt",
				"/%75ploads/attachment_id_report%2fpart.txt",
			} {
				if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("supported producer URL not selected", method, path)
				}
			}
			for _, path := range []string{
				"/uploads/a%2Fb", "/uploads/%2F", "/uploads/projects%2Fid",
				"/uploads/attachment_id_report/part.txt",
				"/uploads/customer_request_attachment_id_report/part.txt",
				"/uploads%2Fextra/attachment_id_report%2Fpart.txt",
			} {
				if scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("producer addition captured an uncertified upload path", method, path)
				}
			}
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			if scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(method, "/uploads/attachment_id_report%2Fpart.txt", nil)) {
				t.Fatal("producer upload selector captured another method", method)
			}
		}
	})
	type outcome struct {
		Allowed bool
		Status  int
		Body    string
		Pattern string
		Name    string
	}
	f := scaleP21SW10Setup(t)
	repo := f.repository
	t.Run(f.reader.config.Driver, func(t *testing.T) {
		foreign := scaleP03Fixture(t, repo)
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		workspace, requestID := f.data.Workspace.URLKey, idgen.New("producer-upload-request-")
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "Producer upload auth", "teamId": f.public}, workspace, http.StatusCreated)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.producer_upload_seed", "", nil, func(data *domain.Bootstrap) error {
			data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: requestID, Body: "Producer upload auth", Creator: f.data.Viewer})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		producer := &server{store: repo, uploadPath: t.TempDir()}
		produced := map[string]map[string]string{}
		for _, kind := range []string{"issue", "customer-request"} {
			produced[kind] = map[string]string{}
			for _, filename := range []string{"report%2Fpart.txt", "report%2Fdefault-favorites", "report%2Fmembers%2Fchild"} {
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				part, err := writer.CreateFormFile("file", filename)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(part, "producer upload body"); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				request := f.literalRequest(f.data.Viewer, nil, nil)
				id, path := issue.ID, "/api/issues/"+issue.ID+"/attachments"
				if kind == "customer-request" {
					id, path = requestID, "/api/customer-requests/"+requestID+"/attachments"
				}
				request.URL.Path = path
				request.SetPathValue("id", id)
				request.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
				request.ContentLength = int64(body.Len())
				request.Header.Set("Content-Type", writer.FormDataContentType())
				request, release := beginRequestResourceReads(request)
				recorder := httptest.NewRecorder()
				if kind == "issue" {
					producer.createAttachment(recorder, request)
				} else {
					producer.createCustomerRequestAttachment(recorder, request)
				}
				release()
				var attachment domain.Attachment
				if recorder.Code != http.StatusCreated || json.Unmarshal(recorder.Body.Bytes(), &attachment) != nil {
					t.Fatalf("real %s producer failed: status=%d body=%s", kind, recorder.Code, recorder.Body.String())
				}
				if attachment.Title != filename || attachment.URL != "/uploads/"+attachment.ID+"_"+filename {
					t.Fatal("producer no longer emits the raw percent filename URL", kind, attachment)
				}
				parsed, err := url.Parse(attachment.URL)
				if err != nil || filepath.Base(strings.TrimPrefix(parsed.Path, "/uploads/")) == strings.TrimPrefix(parsed.Path, "/uploads/") || uploadNameFromURL(attachment.URL) != "" {
					t.Fatal("producer fixture no longer has a decoded basename-invalid name", attachment.URL, err)
				}
				produced[kind][filename] = attachment.URL
			}
		}
		f.reader.key = workspace
		tokens := map[string]string{}
		for _, principal := range f.principals {
			_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[principal.user.ID] = token
		}
		pairs, leaf404s, earlierRefusals, legacySQL, boundedSQL := 0, 0, 0, 0, 0
		for _, kind := range []string{"issue", "customer-request"} {
			t.Run(kind, func(t *testing.T) {
				for index, row := range []struct {
					name, filename string
					foreign        bool
				}{
					{"producer-url", "report%2Fpart.txt", false},
					{"lowercase-escape", "report%2Fpart.txt", false},
					{"encoded-fixed-token", "report%2Fpart.txt", false},
					{"earlier-default-favorites", "report%2Fdefault-favorites", false},
					{"outer-admin-precedence", "report%2Fmembers%2Fchild", false},
					{"foreign-workspace", "report%2Fpart.txt", true},
				} {
					t.Run(row.name, func(t *testing.T) {
						t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
						for _, method := range []string{http.MethodGet, http.MethodHead} {
							t.Run(method, func(t *testing.T) {
								for _, phase := range []string{"warm", "reloaded"} {
									t.Run(phase, func(t *testing.T) {
										f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
											var want outcome
											beforeSQL := 0
											for _, source := range []*store.SQLiteStore{repo, f.probe} {
												if source.BoundedMetadataEnabled() {
													if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
														t.Fatal(err)
													}
												}
												if phase == "reloaded" {
													scaleP21SW201ReloadSource(t, f.reader, source)
												}
												request := f.literalRequest(user, key, nil)
												path := produced[kind][row.filename]
												if row.name == "lowercase-escape" {
													path = strings.ReplaceAll(path, "%2F", "%2f")
												} else if row.name == "encoded-fixed-token" {
													path = strings.Replace(path, "/uploads/", "/%75ploads/", 1)
												}
												var err error
												request.URL, err = url.Parse(path)
												if err != nil {
													t.Fatal(err)
												}
												request.Method = method
												if row.foreign {
													request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
												}
												request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
												request, release := beginRequestResourceReads(request)
												service := &server{store: source}
												if !source.BoundedMetadataEnabled() {
													if _, handled := service.boundedMembershipAuthorization(request, workspace, user.ID); handled {
														release()
														t.Fatal("producer upload addition changed flag-off behavior")
													}
												}
												statements := 0
												request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
												recorder := httptest.NewRecorder()
												allowed := service.authorizeWorkspaceRequest(recorder, request, user)
												pattern, leafName := "", ""
												if allowed {
													mux := http.NewServeMux()
													mux.HandleFunc("GET /uploads/{name}", func(w http.ResponseWriter, r *http.Request) {
														leafName = r.PathValue("name")
														service.serveUploadOrAsset(w, r)
													})
													_, pattern = mux.Handler(request)
													mux.ServeHTTP(recorder, request)
												}
												got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern, leafName}
												admin := user.ID == f.principals[0].user.ID || user.ID == f.principals[1].user.ID
												required := !row.foreign && row.name != "earlier-default-favorites" && (row.name != "outer-admin-precedence" || admin)
												if allowed != required || required && (recorder.Code != http.StatusNotFound || recorder.Body.String() != "404 page not found\n" || pattern != "GET /uploads/{name}" || leafName != strings.TrimPrefix(request.URL.Path, "/uploads/")) || !required && recorder.Code != http.StatusForbidden {
													release()
													t.Fatalf("literal precedence requires policy=%t then exact leaf 404: got=%+v", required, got)
												}
												if !source.BoundedMetadataEnabled() {
													want, beforeSQL = got, statements
												} else {
													pairs++
													legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
													if !reflect.DeepEqual(want, got) {
														release()
														t.Fatalf("producer upload policy/output differs: literal=%+v bounded=%+v", want, got)
													}
													if allowed {
														leaf404s++
													} else {
														earlierRefusals++
													}
													if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
														release()
														t.Fatal("producer upload auth created a complete paged handoff")
													}
												}
												release()
												release()
												if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
													t.Fatal("producer upload auth leaked work or decoded complete metadata")
												}
											}
										})
									})
								}
							})
						}
					})
				}
			})
		}
		if pairs != 960 || leaf404s != 544 || earlierRefusals != 416 {
			t.Fatal("producer upload table lost required precedence controls", pairs, leaf404s, earlierRefusals)
		}
		t.Logf("producer upload pairs=%d exact-leaf-404/earlier-refusal=%d/%d SQL literal/bounded=%d/%d", pairs, leaf404s, earlierRefusals, legacySQL, boundedSQL)
	})
}
