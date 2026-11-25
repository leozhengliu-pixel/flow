package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW3IntegrationProbeWithoutConnectionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	host := newFakeGitLab(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, test := range []struct {
				name, provider, token string
				status                int
			}{
				{"github", "github", "", http.StatusUnprocessableEntity},
				{"gitlab-missing-token", "gitlab", "", http.StatusUnprocessableEntity},
				{"gitlab-valid-token", "gitlab", "glpat-fake-api", http.StatusOK},
				{"gitlab-invalid-token", "gitlab", "glpat-fake-invalid", http.StatusBadGateway},
			} {
				t.Run(test.name, func(t *testing.T) {
					var outcomes []any
					for _, s := range []*server{
						{store: repo, devLoopbackCodeHosts: true},
						{store: f.probe, devLoopbackCodeHosts: true},
					} {
						r := f.request(user, key, map[string]string{"host": host.URL, "token": test.token})
						r.SetPathValue("provider", test.provider)
						w := httptest.NewRecorder()
						s.testIntegrationConnection(w, r)
						if w.Code != test.status {
							t.Fatalf("probe status %d: %s", w.Code, w.Body.String())
						}
						var response map[string]any
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						if test.status == http.StatusOK {
							stamp, ok := response["testedAt"].(string)
							if _, err := time.Parse(time.RFC3339Nano, stamp); !ok || err != nil {
								t.Fatalf("missing probe timestamp: %s", w.Body.String())
							}
							if response["connectionId"] != "" || response["username"] != "flow-bot" {
								t.Fatalf("token-only probe response: %s", w.Body.String())
							}
							delete(response, "testedAt")
						}
						outcomes = append(outcomes, []any{w.Code, response})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("no-connection probe differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("no-connection probe decoded full metadata")
		}
	}
}
