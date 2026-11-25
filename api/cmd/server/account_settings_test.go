package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW127AccountSettingsAdapter(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, other := f.data.Workspace.URLKey, idgen.New("sw127-other-")
		at, disabled := time.Now(), false
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw127_seed", "", nil, func(data *domain.Bootstrap) error {
			data.UserSettings[f.data.Viewer.ID] = domain.UserSettings{
				UserID: f.data.Viewer.ID, AgentEnabled: true, AgentInstructions: "Complete native settings",
				EnabledCodingTools: []string{}, SuggestedActionsOnComments: &disabled, UpdatedAt: at,
				CommitSigningKey: &domain.CommitSigningKey{Name: "Signing", Type: "ssh", AddedAt: at},
			}
			data.UserSettings[other] = domain.UserSettings{UserID: "different-map-value-id", Language: "zh-CN", UpdatedAt: at}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		literal, bounded := &server{store: repo}, &server{store: view}
		// This adapter is a trusted lookup, with no principal or key policy.
		// SW122 proves its primitive; only transfer and off/on adaptation belong here.
		for _, test := range []struct{ name, workspace, user string }{
			{"viewer", workspace, f.data.Viewer.ID},
			{"other-map-key", workspace, other},
			{"missing-settings", workspace, other + "-missing"},
			{"missing-workspace", workspace + "-missing", f.data.Viewer.ID},
		} {
			for _, background := range []bool{false, true} {
				name := "request"
				if background {
					name = "background"
				}
				t.Run(test.name+"/"+name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					var closeOutput func()
					if background {
						ctx, closeOutput = beginMetadataOutputReads(ctx)
					} else {
						request, close := beginRequestResourceReads(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
						ctx, closeOutput = request.Context(), close
					}
					defer closeOutput()
					_, expected, expectedFound := repo.AccountCollections(test.workspace, test.user)
					want, wantFound, wantErr := literal.requestAccountSettings(ctx, test.workspace, test.user)
					got, found, err := bounded.requestAccountSettings(ctx, test.workspace, test.user)
					if wantErr != nil || err != nil || wantFound != expectedFound || found != expectedFound ||
						!reflect.DeepEqual(want, expected) || !reflect.DeepEqual(got, expected) {
						t.Fatalf("settings adapter changed literal values: found=%v/%v expected=%v errors=%v/%v", wantFound, found, expectedFound, wantErr, err)
					}
					if found && view.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("settings output was not transferred to its containing operation")
					}
					cancel()
					if _, err := json.Marshal(got); err != nil || found && view.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("cancellation released settings before final encoding", err)
					}
					closeOutput()
					closeOutput()
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("settings adapter retained admission after owner close")
					}
				})
			}
		}
		for _, closed := range []bool{false, true} {
			ctx := t.Context()
			if closed {
				var closeOutput func()
				ctx, closeOutput = beginMetadataOutputReads(ctx)
				closeOutput()
			}
			settings, found, err := bounded.requestAccountSettings(ctx, workspace, f.data.Viewer.ID)
			if !found || !errors.Is(err, store.ErrBoundedMetadataCoverage) ||
				!reflect.DeepEqual(settings, domain.UserSettings{}) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("unowned settings escaped or leaked", closed, found, err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, expected, _ := repo.AccountCollections(workspace, f.data.Viewer.ID)
		settings, found, err := literal.requestAccountSettings(ctx, workspace, f.data.Viewer.ID)
		if err != nil || !found || !reflect.DeepEqual(settings, expected) {
			t.Fatal("off branch stopped being the literal contextless accessor", found, err)
		}
		if settings, _, err := bounded.requestAccountSettings(ctx, workspace, f.data.Viewer.ID); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(settings, domain.UserSettings{}) {
			t.Fatal("new bounded cancellation was swallowed", err)
		}
	}
}
