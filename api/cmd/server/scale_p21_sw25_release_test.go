package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW25ReleaseKeyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		prefix := idgen.New("sw25-pipeline-")
		current, previous, revoked := prefix+"-current", prefix+"-previous", prefix+"-revoked"
		at := time.Now().UTC()
		past, future := at.Add(-time.Hour), at.Add(time.Hour)
		targetID := prefix + "-target"
		seed := func(workspace, id, secret string) {
			t.Helper()
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "releasePipelines"), workspace, "release_pipeline.updated", "", nil, func(data *domain.Bootstrap) error {
				data.ReleasePipelines = []domain.ReleasePipeline{}
				for range 73 {
					data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{
						ID: idgen.New("sw25-expired-"), Name: "Expired", AccessKeyHash: secretHash(secret), AccessKeyRevokedAt: &past,
					})
				}
				data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{
					ID: id, SlugID: prefix + "-old-slug", Name: "Target", ReleaseNotesTemplate: strings.Repeat("legal large release notes template ", 14000),
					AccessKeyHash: secretHash(secret), AccessKeyCreatedAt: &at, AccessKeyRevokedAt: &future,
					PreviousAccessKeyHash: secretHash(previous), PreviousAccessKeyExpiresAt: &future,
				}, domain.ReleasePipeline{
					ID: prefix + "-revoked-row", Name: "Revoked", AccessKeyHash: secretHash(revoked), AccessKeyRevokedAt: &past,
					PreviousAccessKeyHash: secretHash(revoked), PreviousAccessKeyExpiresAt: &past,
				})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		seed(workspace, targetID, current)
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW25 other key workspace", idgen.New("sw25-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		foreignID, foreignSecret := prefix+"-foreign", prefix+"-foreign-secret"
		seed(foreign.Workspace.URLKey, foreignID, foreignSecret)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "releasePipelines")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, secret, id, wantWorkspace, wantID string
			}{
				{"current", current, "", workspace, targetID},
				{"selected", current, targetID, workspace, targetID},
				{"grace", previous, targetID, workspace, targetID},
				{"revoked", revoked, "", "", ""},
				{"wrong-pipeline", current, foreignID, "", ""},
				{"old-slug", current, prefix + "-old-slug", "", ""},
				{"case", strings.ToUpper(current), targetID, "", ""},
				{"missing", current, prefix + "-missing", "", ""},
				{"empty", "", targetID, "", ""},
				{"foreign", foreignSecret, foreignID, foreign.Workspace.URLKey, foreignID},
			} {
				t.Run(spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, probe} {
						service := &server{store: source}
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						r.Header.Set("Authorization", "Bearer "+spec.secret)
						r.SetPathValue("id", spec.id)
						gotWorkspace, gotID, gotOK, err := service.pipelineForAccessKey(r.Context(), spec.secret, spec.id)
						if err != nil || gotWorkspace != spec.wantWorkspace || gotID != spec.wantID || gotOK != (spec.wantID != "") {
							t.Fatalf("key lookup=%s/%s/%t err=%v want=%s/%s", gotWorkspace, gotID, gotOK, err, spec.wantWorkspace, spec.wantID)
						}
						gotWorkspace, gotID, gotOK = service.authenticateReleaseCI(w, r)
						if spec.wantID != "" && (!gotOK || gotWorkspace != spec.wantWorkspace || gotID != spec.wantID) {
							t.Fatal("CI key authentication lost its independently bound workspace")
						}
						if spec.wantID == "" && (gotOK || w.Code != http.StatusUnauthorized) {
							t.Fatal("unavailable CI key authenticated")
						}
						outcomes = append(outcomes, []any{gotWorkspace, gotID, gotOK, w.Code, w.Body.String(), w.Header()})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("release authentication differs: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		if scaleP21M3Fingerprint(t, repo, workspace, "releasePipelines") != before {
			t.Fatal("CI authentication changed canonical pipeline rows")
		}
		// Request identities and ordinary API-key scopes never replace the
		// independent CI bearer credential, including after user revocation.
		member := f.principals[2].user
		queued := f.request(member, nil, nil)
		queued.SetPathValue("id", targetID)
		queued.Header.Set("Authorization", "Bearer "+current)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, probe} {
			w := httptest.NewRecorder()
			if _, _, ok := (&server{store: source}).authenticateReleaseCI(w, queued); !ok {
				t.Fatal("ordinary membership revocation changed independent CI key authority")
			}
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "releasePipelines"), workspace, "release_pipeline.access_key_revoked", targetID, nil, func(data *domain.Bootstrap) error {
			for index := range data.ReleasePipelines {
				if data.ReleasePipelines[index].ID == targetID {
					data.ReleasePipelines[index].AccessKeyRevokedAt = &past
					data.ReleasePipelines[index].PreviousAccessKeyExpiresAt = &past
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				w := httptest.NewRecorder()
				if _, _, ok := (&server{store: source}).authenticateReleaseCI(w, queued); ok || w.Code != http.StatusUnauthorized {
					t.Fatalf("%s authentication accepted a revoked key", phase)
				}
			}
		}
		seed(workspace, targetID, current)
		t.Run("paired-allocation-samples", func(t *testing.T) {
			for pair := range 5 {
				for _, source := range []*store.SQLiteStore{repo, probe} {
					service := &server{store: source}
					run := func() {
						t.Helper()
						if gotWorkspace, gotID, ok, err := service.pipelineForAccessKey(t.Context(), current, targetID); err != nil || !ok || gotWorkspace != workspace || gotID != targetID {
							t.Fatal("measurement lookup did not succeed", err)
						}
					}
					run()
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					for range 3 {
						run()
					}
					runtime.ReadMemStats(&after)
					t.Logf("pair=%d bounded=%t bytes/op=%d allocs/op=%d", pair+1, source.BoundedMetadataEnabled(), (after.TotalAlloc-before.TotalAlloc)/3, (after.Mallocs-before.Mallocs)/3)
				}
			}
		})
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, _, err := (&server{store: probe}).pipelineForAccessKey(cancelled, current, targetID); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled native authentication did not return its error", err)
		}
		if _, _, ok, err := (&server{store: probe}).pipelineForAccessKey(t.Context(), current, targetID); err != nil || !ok {
			t.Fatal("native authentication did not recover from cancellation", err)
		}
		// The reset uses fresh unrelated IDs; test all-read output ownership
		// against a stable snapshot independently of those fixture IDs.
		stable := scaleP21M3Fingerprint(t, repo, workspace, "releasePipelines")
		if _, _, _, err := (&server{store: probe}).pipelineForAccessKey(t.Context(), current, targetID); err != nil ||
			scaleP21M3Fingerprint(t, repo, workspace, "releasePipelines") != stable || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("authentication changed canonical state or decoded complete metadata", err)
		}
	}
}
