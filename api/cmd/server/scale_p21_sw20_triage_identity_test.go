package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW20TriageRefreshIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		services := []*server{{store: repo}, {store: f.probe}}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, binding := range []string{f.data.Workspace.URLKey, "", "sw20-foreign-workspace"} {
				var outcomes []any
				for _, service := range services {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.Header.Set("X-Workspace-Key", binding)
					r.SetPathValue("id", "sw20-missing-issue")
					service.refreshIssueSuggestions(w, r)
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("explicit refresh actor binding=%q changed: %s", binding, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		for _, binding := range []string{f.data.Workspace.URLKey, ""} {
			var outcomes []any
			for _, service := range services {
				r, w := f.request(domain.User{}, nil, nil), httptest.NewRecorder()
				r.Header.Set("X-Workspace-Key", binding)
				r.SetPathValue("id", "sw20-missing-issue")
				service.refreshIssueSuggestions(w, r)
				outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
				read, ok, err := service.store.BeginInstalledViewerRead(r.Context(), binding)
				legacy, legacyOK := repo.WorkspaceMetadata(binding)
				if err != nil || !ok || !legacyOK || !reflect.DeepEqual(read.Viewer(), legacy.Viewer) {
					t.Fatal("fallback actor identity changed", err)
				}
				read.Close()
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("stored refresh actor binding=%q changed: %s", binding, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("refresh identity decoded complete metadata")
		}
		// The run service is deliberately independently inventoried. A missing
		// issue fails before its full heuristic input or mutation is entered.
		for _, service := range services {
			w := httptest.NewRecorder()
			service.refreshIssueSuggestions(w, f.request(f.data.Viewer, nil, nil))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("missing refresh target: %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestScaleP21SW20TriageInstalledViewerFullWrite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		expected := f.data.Viewer
		expected.ID += "\x00"
		expected.Name, expected.DisplayName = "warm\x00name", "warm\x00display"
		expected.AppScopes, expected.AppTeamIDs = []string{"read\x00"}, []string{"team\x00"}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "viewer"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Viewer = expected
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// Capture the same logical installed Viewer before a full rewrite.
		// The ensuing full write changes SQL provenance, not either retained
		// instance's Viewer. No baseline reload is performed.
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		input := f.data.Workspace
		input.Name += " full write"
		if _, err := repo.UpdateWorkspace(t.Context(), workspace, input); err != nil {
			t.Fatal(err)
		}
		canonical, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{})
		if err != nil || reflect.DeepEqual(canonical.Data.Viewer, expected) {
			t.Fatal("fixture did not expose canonical source difference", err)
		}
		for _, binding := range []string{workspace, "", workspace + "\x00", workspace + "-missing"} {
			for _, cancelled := range []bool{false, true} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, probe} {
					r, w := f.request(domain.User{}, nil, nil), httptest.NewRecorder()
					r.Header.Set("X-Workspace-Key", binding)
					r.SetPathValue("id", "sw20-missing-triage")
					if cancelled {
						ctx, cancel := context.WithCancel(r.Context())
						cancel()
						r = r.WithContext(ctx)
					}
					legacy, legacyOK := source.WorkspaceMetadata(binding)
					read, found, err := source.BeginInstalledViewerRead(t.Context(), binding)
					if err != nil || found != legacyOK {
						t.Fatal("installed identity lookup", found, legacyOK, err)
					}
					if found {
						if !reflect.DeepEqual(read.Viewer(), legacy.Viewer) || !reflect.DeepEqual(read.Viewer(), expected) {
							t.Fatal("full-write Viewer provenance changed")
						}
						read.Close()
					}
					(&server{store: source}).refreshIssueSuggestions(w, r)
					outcomes = append(outcomes, []any{w.Code, w.Body.String()})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("full-write actor handler binding=%q cancelled=%v: %v", binding, cancelled, outcomes)
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("installed actor fallback decoded full metadata")
		}
	}
}
