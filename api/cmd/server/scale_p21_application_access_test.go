package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21ApplicationAccessServerLifetimeDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		app, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
			ClientID: idgen.New("p21-app-access-"), Name: "Application", Active: true, InstalledBy: f.data.Viewer.ID,
			TeamIDs: []string{f.public}, Scopes: []string{"read", "write"}, WebhookURL: "https://example.test/application",
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		task := domain.AgentTask{AppUserID: app.UserID, TeamID: f.public}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			legacy, bounded := &server{store: repo}, &server{store: f.probe}
			for _, projection := range []string{"user", "delivery", "full"} {
				want, closeWant, err := legacy.applicationAccessRead(t.Context(), app.UserID, projection)
				if err != nil {
					t.Fatal(err)
				}
				closeWant()
				got, closeGot, err := bounded.applicationAccessRead(t.Context(), app.UserID, projection)
				if err != nil || !reflect.DeepEqual(got.User(), want.User()) || !reflect.DeepEqual(got.AccessTeamIDs(), want.AccessTeamIDs()) {
					t.Fatal("server user/credential input changed", projection, reload, err)
				}
				if projection != "user" && (got.ID != want.ID || got.WorkspaceKey != want.WorkspaceKey || got.WebhookURL != want.WebhookURL) {
					t.Fatal("delivery target/body input changed")
				}
				if projection == "full" && !reflect.DeepEqual(got, want) {
					t.Fatal("complete application changed")
				}
				if f.probe.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("server application output lacks lifetime")
				}
				closeGot()
				if f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("server application output leaked lifetime")
				}
			}
			for range 240 {
				if !bounded.applicationTaskAccessActive(t.Context(), task) || f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("revocation probe failed or accumulated output owners")
				}
			}
			for _, denied := range []domain.AgentTask{{AppUserID: app.UserID, TeamID: "foreign"}, {AppUserID: "missing", TeamID: f.public}} {
				if bounded.applicationTaskAccessActive(t.Context(), denied) != legacy.applicationTaskAccessActive(t.Context(), denied) || f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("denied app authorization changed or retained work")
				}
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if bounded.applicationTaskAccessActive(cancelled, task) || f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled probe retained work")
			}
			ctx, closeOutputs := applicationOutputContext(t.Context())
			if _, err := f.probe.ApplicationByUser(context.WithoutCancel(ctx), app.UserID); err != nil || f.probe.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("application operation did not own descendant outputs", err)
			}
			closeOutputs()
			closeOutputs()
			if f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("application operation did not release descendant outputs")
			}
			if _, err := f.probe.ApplicationByUser(ctx, app.UserID); !errors.Is(err, store.ErrBoundedMetadataCoverage) || f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("closed application operation accepted an output", err)
			}
		}
	}
}
