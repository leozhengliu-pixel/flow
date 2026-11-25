package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW12Applications(t *testing.T, repo *store.SQLiteStore, workspace string, actor domain.User, logo string) domain.OAuthApplication {
	t.Helper()
	target := domain.OAuthApplication{
		ID: idgen.New("oauth_app_sw12_"), ClientID: idgen.New("flow_client_sw12_"),
		Name: "SW12 application", CreatorID: actor.ID, ClientSecret: "sw12-before",
		LogoURL: logo, RedirectURIs: []string{"https://sw12.example.test/callback"}, Scopes: []string{"read", "write"},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "oauthApplications"), workspace, "scale_p21_sw12.seed", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = []domain.OAuthApplication{target}
		for range 70 {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
				ID: idgen.New("oauth_app_sw12_other_"), ClientID: idgen.New("flow_client_sw12_other_"),
				Name: "Retained", ClientSecret: "sw12-unrelated", CreatorID: actor.ID,
			})
		}
		data.OAuthApplications[len(data.OAuthApplications)-1].Description = strings.Repeat("unrelated application ", 20000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return target
}

func scaleP21SW12Reset(t *testing.T, repo *store.SQLiteStore, workspace string, target domain.OAuthApplication) {
	t.Helper()
	repo.SetWebhookSink(nil)
	repo.SetRealtimeSink(nil)
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "oauthApplications"), workspace, "scale_p21_sw12.reset", "", nil, func(data *domain.Bootstrap) error {
		for i := range data.OAuthApplications {
			if data.OAuthApplications[i].ID == target.ID {
				data.OAuthApplications[i] = target
				return nil
			}
		}
		t.Fatal("SW12 target missing during reset")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW12OtherApplications(t *testing.T, repo *store.SQLiteStore, workspace, exclude string) [32]byte {
	t.Helper()
	hash := sha256.New()
	if err := repo.StreamMetadataRecords(t.Context(), workspace, "oauthApplications", func(key store.RecordKey, reader io.Reader) error {
		if key.ID == exclude {
			return nil
		}
		if _, err := io.WriteString(hash, key.ID+"\x00"); err != nil {
			return err
		}
		_, err := io.Copy(hash, reader)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

// Only the operation's random credential is replaced; old credentials, actor
// fields and permissions remain part of the comparison.
func scaleP21SW12JSON(t *testing.T, value any, secret string) any {
	t.Helper()
	result := scaleP21M2JSON(t, value, false)
	var normalize func(any) any
	normalize = func(value any) any {
		switch item := value.(type) {
		case string:
			if secret != "" && item == secret {
				return "<generated-secret>"
			}
		case map[string]any:
			for key, value := range item {
				item[key] = normalize(value)
			}
		case []any:
			for i, value := range item {
				item[i] = normalize(value)
			}
		}
		return value
	}
	return normalize(result)
}

func TestScaleP21SW12SecretRotationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		target := scaleP21SW12Applications(t, repo, workspace, f.data.Viewer, "")
		unrelated := scaleP21SW12OtherApplications(t, repo, workspace, target.ID)
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, missing := range []bool{false, true} {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					scaleP21SW12Reset(t, repo, workspace, target)
					var webhooks, realtime []any
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						webhooks = append(webhooks, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						realtime = append(realtime, []any{event.Type, event.Payload})
					})
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					id := target.ID
					if missing {
						id += "-missing"
					}
					r.SetPathValue("id", id)
					before := time.Now().UTC()
					scaleP21M3Call(&server{store: source}, w, r, (*server).rotateOAuthApplicationSecret)
					var response struct {
						Secret string `json:"clientSecret"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					hydrated, err := f.probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "oauthApplications", ID: target.ID}}})
					current := hydrated.Data
					if err != nil || len(current.OAuthApplications) != 1 {
						t.Fatal("read rotated application", err)
					}
					if missing {
						if w.Code != http.StatusNotFound || current.OAuthApplications[0].ClientSecret != target.ClientSecret {
							t.Fatalf("missing rotation status=%d", w.Code)
						}
					} else {
						rotated := current.OAuthApplications[0]
						if w.Code != http.StatusOK || !strings.HasPrefix(response.Secret, "flow_secret_") || response.Secret == target.ClientSecret ||
							rotated.ClientSecret != response.Secret || rotated.CredentialsAuthorizedBy != user.ID ||
							rotated.UpdatedAt.Before(before) || rotated.UpdatedAt.After(time.Now().UTC()) || w.Header().Get("Cache-Control") != "no-store" {
							t.Fatalf("rotation lost credential, actor, clock or response headers: status=%d", w.Code)
						}
					}
					entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: before})
					if err != nil {
						t.Fatal(err)
					}
					for i := range entries {
						entries[i].ID, entries[i].Hash, entries[i].Seq, entries[i].CreatedAt = "", "", 0, time.Time{}
					}
					results = append(results, scaleP21SW12JSON(t, []any{
						w.Code, scaleP21M2Response(t, w, false), current.OAuthApplications,
						scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "users", ID: user.ID}),
						webhooks, realtime, entries,
					}, response.Secret))
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("rotation differential: %s", scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		scaleP21SW12Reset(t, repo, workspace, target)
		original := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", target.ID)
		(&server{store: f.probe}).rotateOAuthApplicationSecret(w, r.WithContext(ctx))
		if w.Code < 400 || scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") != original {
			t.Fatal("cancelled rotation changed canonical metadata")
		}
		large := target
		large.Description = strings.Repeat("selected application ", 20000)
		scaleP21SW12Reset(t, repo, workspace, large)
		original = scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
		for range 2 {
			r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", target.ID)
			(&server{store: f.probe}).rotateOAuthApplicationSecret(w, r)
			if w.Code < 400 || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
				scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") != original {
				t.Fatal("selected budget rejection changed canonical metadata or leaked admission")
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 || scaleP21SW12OtherApplications(t, repo, workspace, target.ID) != unrelated {
			t.Fatal("rotation decoded full workspace metadata or changed unrelated application bytes/order")
		}
	}
}

func TestScaleP21SW12LogoInputDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		logo := "/uploads/" + idgen.New("oauth_app_icon_sw12_current_") + ".png"
		target := scaleP21SW12Applications(t, repo, workspace, f.data.Viewer, logo)
		owned := idgen.New("oauth_app_icon_sw12_owned_") + ".png"
		foreign := idgen.New("oauth_app_icon_sw12_foreign_") + ".png"
		otherPurpose := idgen.New("oauth_app_icon_sw12_other_") + ".png"
		for _, owner := range []store.FileUpload{
			{WorkspaceKey: workspace, ObjectKey: owned, Purpose: store.FileUploadPurposeOAuthAppIcon},
			{WorkspaceKey: idgen.New("sw12-foreign-"), ObjectKey: foreign, Purpose: store.FileUploadPurposeOAuthAppIcon},
			{WorkspaceKey: workspace, ObjectKey: otherPurpose, Purpose: store.FileUploadPurposeWorkspaceLogo},
		} {
			owner.UserID, owner.Filename, owner.ContentType, owner.ActualSize = f.data.Viewer.ID, "icon.png", "image/png", 42
			if err := repo.RecordStoredUpload(t.Context(), owner, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := repo.DeleteStoredUpload(context.Background(), owner.ObjectKey); err != nil {
					t.Error(err)
				}
			})
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, logo string
				want       error
				appID      string
			}{
				{"external", "https://sw12.example.test/logo.png", nil, target.ID},
				{"current-no-owner", logo, nil, target.ID},
				{"owned", "/uploads/" + owned, nil, target.ID},
				{"trimmed-owned", "  /uploads/" + owned + "  ", nil, target.ID},
				{"foreign", "/uploads/" + foreign, errOAuthAppIconNotOwned, target.ID},
				{"other-purpose", "/uploads/" + otherPurpose, errOAuthAppIconNotOwned, target.ID},
				{"unknown", "/uploads/oauth_app_icon_sw12_missing.png", errOAuthAppIconNotOwned, target.ID},
				{"missing-app-current", logo, errOAuthAppIconNotOwned, target.ID + "-missing"},
				{"create-owned", "/uploads/" + owned, nil, ""},
				{"create-foreign", "/uploads/" + foreign, errOAuthAppIconNotOwned, ""},
			} {
				t.Run(spec.name, func(t *testing.T) {
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						err := (&server{store: source}).checkOAuthAppLogoInput(f.request(user, key, nil), &spec.logo, spec.appID)
						if !errors.Is(err, spec.want) {
							t.Fatalf("logo ownership error=%v, want=%v", err, spec.want)
						}
					}
				})
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				if err := (&server{store: source}).checkOAuthAppLogoInput(f.request(user, key, nil), nil, target.ID); err != nil {
					t.Fatal("nil logo patch performed ownership reads", err)
				}
			}
		})
		large := target
		large.Description = strings.Repeat("selected application ", 20000)
		scaleP21SW12Reset(t, repo, workspace, large)
		for range 2 {
			if err := (&server{store: f.probe}).checkOAuthAppLogoInput(f.request(f.data.Viewer, nil, nil), &logo, target.ID); !errors.Is(err, store.ErrMetadataPageBudget) {
				t.Fatalf("selected large application error=%v", err)
			}
		}
		scaleP21SW12Reset(t, repo, workspace, target)
		if scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") != before || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("logo validation changed or decoded unrelated metadata")
		}
	}
}

func TestScaleP21SW12IconUploadDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		scaleP21SW12Applications(t, repo, workspace, f.data.Viewer, "")
		before := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, filename string
				payload        []byte
				status         int
			}{
				{"png", "icon.png", oauthAppIconRaster(t, "png"), http.StatusCreated},
				{"invalid", "icon.png", []byte("invalid-image"), http.StatusBadRequest},
			} {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					var body bytes.Buffer
					form := multipart.NewWriter(&body)
					part, err := form.CreateFormFile("file", spec.filename)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := part.Write(spec.payload); err != nil {
						t.Fatal(err)
					}
					if err := form.Close(); err != nil {
						t.Fatal(err)
					}
					s := &server{store: source, uploadPath: t.TempDir()}
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body.Bytes())), int64(body.Len())
					r.Header.Set("Content-Type", form.FormDataContentType())
					scaleP21M3Call(s, w, r, (*server).uploadOAuthApplicationIcon)
					if w.Code != spec.status {
						t.Fatalf("%s status=%d body=%s", spec.name, w.Code, w.Body.String())
					}
					var result any = scaleP21M2Response(t, w, false)
					if spec.status == http.StatusCreated {
						var output map[string]string
						if err := json.Unmarshal(w.Body.Bytes(), &output); err != nil {
							t.Fatal(err)
						}
						object := strings.TrimPrefix(output["url"], "/uploads/")
						owner, err := repo.FileUploadByObject(t.Context(), object)
						if err != nil || owner.WorkspaceKey != workspace || owner.UserID != user.ID ||
							owner.Purpose != store.FileUploadPurposeOAuthAppIcon || owner.ContentType != "image/png" || owner.ActualSize != int64(len(spec.payload)) {
							t.Fatal("uploaded icon owner differs", err)
						}
						storage, err := s.storage()
						if err != nil {
							t.Fatal(err)
						}
						reader, _, _, err := storage.Open(t.Context(), object)
						if err != nil {
							t.Fatal(err)
						}
						payload, readErr := io.ReadAll(reader)
						reader.Close()
						if readErr != nil || !bytes.Equal(payload, spec.payload) {
							t.Fatal("uploaded icon bytes differ", readErr)
						}
						if err := repo.DeleteStoredUpload(t.Context(), object); err != nil {
							t.Fatal(err)
						}
						result = []any{owner.WorkspaceKey, owner.UserID, owner.Filename, owner.ContentType, owner.ActualSize, owner.Purpose, owner.Status, payload}
					}
					files, err := os.ReadDir(s.uploadPath)
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					wantFiles := 0
					if spec.status == http.StatusCreated {
						wantFiles = 1
					}
					if len(files) != wantFiles {
						t.Fatalf("icon validation left %d objects, want=%d", len(files), wantFiles)
					}
					results = append(results, []any{w.Code, result})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("icon upload differential: %s", scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		if scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") != before || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("icon upload changed or decoded application metadata")
		}
	}
}

func TestScaleP21SW12OAuthAuthorityAndWorkspaceIsolation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		currentLogo := "/uploads/" + idgen.New("oauth_app_icon_sw12_held_") + ".png"
		target := scaleP21SW12Applications(t, repo, workspace, f.data.Viewer, currentLogo)
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW12 foreign", idgen.New("sw12-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if err := f.probe.ReloadWorkspace(t.Context(), foreign.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		original := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
		otherOriginal := scaleP21M3Fingerprint(t, repo, foreign.Workspace.URLKey, "oauthApplications")
		member := f.principals[len(f.principals)-1].user
		type heldRequest struct {
			source  *store.SQLiteStore
			request *http.Request
		}
		queued := []heldRequest{}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r := f.request(member, nil, nil)
			r.SetPathValue("id", target.ID)
			queued = append(queued, heldRequest{source, r})
		}
		for _, spec := range []struct {
			name, workspace string
			user            domain.User
			want            int
		}{
			{"cross-workspace-owner", foreign.Workspace.URLKey, f.data.Viewer, http.StatusNotFound},
			{"cross-workspace-outsider", foreign.Workspace.URLKey, member, http.StatusForbidden},
		} {
			t.Run(spec.name, func(t *testing.T) {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(spec.user, nil, nil), httptest.NewRecorder()
					r.Header.Set("X-Workspace-Key", spec.workspace)
					r.SetPathValue("workspaceKey", spec.workspace)
					r.SetPathValue("workspace", spec.workspace)
					r.SetPathValue("id", target.ID)
					scaleP21M3Call(&server{store: source}, w, r, (*server).rotateOAuthApplicationSecret)
					if w.Code != spec.want {
						t.Fatalf("cross workspace rotation status=%d", w.Code)
					}
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
					if err := (&server{store: source}).checkOAuthAppLogoInput(r, &currentLogo, target.ID); !errors.Is(err, errOAuthAppIconNotOwned) {
						t.Fatalf("another workspace adopted current-logo exception: %v", err)
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("cross workspace rotation response differs: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replay"} {
			for _, held := range queued {
				r := held.request
				if phase == "replay" {
					r = f.request(member, nil, nil)
					r.SetPathValue("id", target.ID)
				}
				w := httptest.NewRecorder()
				scaleP21M3Call(&server{store: held.source}, w, r, (*server).rotateOAuthApplicationSecret)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s rotation ignored live revocation: status=%d", phase, w.Code)
				}
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				part, err := form.CreateFormFile("file", "icon.png")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write(oauthAppIconRaster(t, "png")); err != nil {
					t.Fatal(err)
				}
				if err := form.Close(); err != nil {
					t.Fatal(err)
				}
				r = r.Clone(r.Context())
				r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body.Bytes())), int64(body.Len())
				r.Header.Set("Content-Type", form.FormDataContentType())
				s, w := &server{store: held.source, uploadPath: t.TempDir()}, httptest.NewRecorder()
				scaleP21M3Call(s, w, r, (*server).uploadOAuthApplicationIcon)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s upload ignored live revocation: status=%d", phase, w.Code)
				}
				files, err := os.ReadDir(s.uploadPath)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(files) != 0 {
					t.Fatal("revoked upload stored an icon")
				}
			}
		}
		if scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") != original ||
			scaleP21M3Fingerprint(t, repo, foreign.Workspace.URLKey, "oauthApplications") != otherOriginal ||
			f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("workspace isolation or authority checks changed application metadata or decoded full state")
		}
	}
}
