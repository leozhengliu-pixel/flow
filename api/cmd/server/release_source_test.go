package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// This preserves evidence for an activation blocker, not migration approval:
// the accepted trusted writer retains NUL in warm state but strips it in SQL.
func TestScaleP21SW25ReleaseKeySourceCompatibilityHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		secret := "flow_release_" + idgen.New("sw25-source-")
		hash := secretHash(secret)
		future := time.Now().UTC().Add(time.Hour)
		for _, test := range []struct {
			name     string
			previous bool
			nul      bool
		}{
			{"current-control", false, false},
			{"previous-control", true, false},
			{"current-warm-nul", false, true},
			{"previous-warm-nul", true, true},
		} {
			t.Run(test.name, func(t *testing.T) {
				stored := hash
				if test.nul {
					stored = hash[:8] + "\x00" + hash[8:]
				}
				pipeline := domain.ReleasePipeline{ID: idgen.New("release_pipeline_")}
				if test.previous {
					pipeline.PreviousAccessKeyHash, pipeline.PreviousAccessKeyExpiresAt = stored, &future
				} else {
					pipeline.AccessKeyHash = stored
				}
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "releasePipelines"), workspace, "release_pipeline.updated", "", nil, func(data *domain.Bootstrap) error {
					data.ReleasePipelines = []domain.ReleasePipeline{pipeline}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				warm, ok := repo.WorkspaceMetadata(workspace)
				if !ok || len(warm.ReleasePipelines) != 1 || warm.ReleasePipelines[0].AccessKeyHash != pipeline.AccessKeyHash ||
					warm.ReleasePipelines[0].PreviousAccessKeyHash != pipeline.PreviousAccessKeyHash {
					t.Fatal("accepted trusted producer lost its warm value")
				}
				var canonical domain.ReleasePipeline
				if err := repo.StreamMetadataRecords(t.Context(), workspace, "releasePipelines", func(_ store.RecordKey, input io.Reader) error {
					return json.NewDecoder(input).Decode(&canonical)
				}); err != nil {
					t.Fatal(err)
				}
				if canonical.AccessKeyHash != strings.ReplaceAll(pipeline.AccessKeyHash, "\x00", "") ||
					canonical.PreviousAccessKeyHash != strings.ReplaceAll(pipeline.PreviousAccessKeyHash, "\x00", "") {
					t.Fatal("persistence transformation precondition changed")
				}
				probe, err := repo.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, source := range []*store.SQLiteStore{repo, probe} {
					request := httptest.NewRequest(http.MethodGet, "/api/release-ci/releases?limit=0", nil)
					request.Header.Set("Authorization", "Bearer "+secret)
					response := httptest.NewRecorder()
					// Invalid limit follows authentication and precedes every
					// independently pending metadata read and key-use mutation.
					(&server{store: source}).releaseCIRecentReleases(response, request)
					wantStatus, wantError := http.StatusBadRequest, "limit must be between 1 and 50"
					if test.nul && !source.BoundedMetadataEnabled() {
						wantStatus, wantError = http.StatusUnauthorized, "invalid release pipeline access key"
					}
					var body map[string]string
					if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != wantStatus ||
						body["error"] != wantError || response.Header().Get("Cache-Control") != "no-store" {
						t.Fatalf("source=%s bounded=%t response=%d %s err=%v", test.name, source.BoundedMetadataEnabled(), response.Code, response.Body.String(), err)
					}
					t.Logf("source=%s bounded=%t status=%d", test.name, source.BoundedMetadataEnabled(), response.Code)
				}
				if probe.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("source witness decoded complete metadata")
				}
			})
		}
	}
}
