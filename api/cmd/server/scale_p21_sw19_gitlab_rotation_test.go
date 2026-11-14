package main

import (
	"bytes"
	"context"
	"log"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW19GitLabRotationSnapshotCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	enableConnectorSecrets(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw19.rotation_context", "", nil, func(data *domain.Bootstrap) error {
			data.IntegrationConnections = []domain.IntegrationConnection{{
				ID: "sw19-not-due", Provider: "gitlab", Config: map[string]string{
					"rotationEnabled": "true", "canSelfRotate": "true", "nextRotationAt": "2027-01-01",
				},
			}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		original := log.Writer()
		log.SetOutput(&output)
		t.Cleanup(func() { log.SetOutput(original) })
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
		for _, source := range []*store.SQLiteStore{repo, probe} {
			(&server{store: source}).rotateDueGitLabTokens(cancelled, workspace, now)
		}
		log.SetOutput(original)
		if output.Len() != 0 {
			t.Fatal("canceled context changed the context-insensitive snapshot phase", output.String())
		}
		installed, found := probe.WorkspaceMetadataFields(workspace, "integrationConnections")
		if !found || len(installed.IntegrationConnections) != 0 || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("actual policy-only source was substituted or decoded")
		}
	}
}
