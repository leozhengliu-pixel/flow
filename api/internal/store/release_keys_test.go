package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW25ReleaseKeyScalars(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	now := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	before, after := now.Add(-time.Nanosecond), now.Add(time.Nanosecond)
	pipelines := []domain.ReleasePipeline{}
	for i := range 73 {
		pipelines = append(pipelines, domain.ReleasePipeline{
			ID: fmt.Sprintf("sw25-expired-%03d", i), AccessKeyHash: "paged", AccessKeyRevokedAt: &before,
		})
	}
	pipelines = append(pipelines,
		domain.ReleasePipeline{ID: "sw25-current", SlugID: "old-current", AccessKeyHash: "paged", ReleaseNotesTemplate: strings.Repeat("legal unrelated pipeline body ", 16000)},
		domain.ReleasePipeline{ID: "sw25-future", AccessKeyHash: "future", AccessKeyRevokedAt: &after},
		domain.ReleasePipeline{ID: "sw25-boundary", AccessKeyHash: "boundary", AccessKeyRevokedAt: &now},
		domain.ReleasePipeline{ID: "sw25-previous", PreviousAccessKeyHash: "previous", PreviousAccessKeyExpiresAt: &after},
		domain.ReleasePipeline{ID: "sw25-previous-boundary", PreviousAccessKeyHash: "previous-boundary", PreviousAccessKeyExpiresAt: &now},
		domain.ReleasePipeline{ID: "sw25-previous-missing", PreviousAccessKeyHash: "previous-missing"},
		domain.ReleasePipeline{ID: "sw25-no-current", AccessKeyRevokedAt: &after},
	)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "releasePipelines"), workspace, "scale_p21_sw25.seed", "", nil, func(data *domain.Bootstrap) error {
		data.ReleasePipelines = pipelines
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='releasePipelines' AND record_key='sw25-current'`, workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if len(original) <= MetadataRecordPageMaxBytes {
		t.Fatal("key lookup fixture must exceed the detail-body budget")
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		hash, id, want string
	}{
		{"paged", "", "sw25-current"},
		{"paged", "sw25-current", "sw25-current"},
		{"paged", "old-current", ""},
		{"paged", "sw25-expired-002", ""},
		{"future", "", "sw25-future"},
		{"boundary", "", ""},
		{"previous", "", "sw25-previous"},
		{"previous-boundary", "", ""},
		{"previous-missing", "", ""},
		{"", "", ""},
		{"PAGED", "", ""},
		{"paged\x00", "", ""},
		{"paged", "SW25-CURRENT", ""},
		{"paged", "sw25-current\x00", ""},
		{"paged", strings.Repeat("x", 192), ""},
		{"paged", "sw25-current' OR 1=1 --", ""},
		{"missing", "", ""},
	} {
		t.Run(spec.hash+"/"+spec.id, func(t *testing.T) {
			got, found, err := probe.ReleasePipelineForKeyHash(t.Context(), workspace, spec.hash, spec.id, now)
			if err != nil || got != spec.want || found != (spec.want != "") {
				t.Fatalf("key result=%q/%t err=%v want=%q", got, found, err, spec.want)
			}
		})
	}
	if id, ok, err := probe.ReleasePipelineForKeyHash(t.Context(), workspace+"-missing", "paged", "", now); err != nil || ok || id != "" {
		t.Fatalf("cross-workspace key lookup=%q/%t err=%v", id, ok, err)
	}
	if id, ok, err := probe.ReleasePipelineForKeyHash(t.Context(), workspace+"\x00", "paged", "sw25-current", now); err != nil || ok || id != "" {
		t.Fatalf("raw NUL workspace matched after parameter rewriting: %q/%t err=%v", id, ok, err)
	}
	for _, altered := range []string{strings.ToUpper(workspace), workspace + " "} {
		if id, ok, err := probe.ReleasePipelineForKeyHash(t.Context(), altered, "paged", "sw25-current", now); err != nil || ok || id != "" {
			t.Fatalf("noncanonical workspace matched by SQL collation: %q/%t err=%v", id, ok, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if id, ok, err := probe.ReleasePipelineForKeyHash(ctx, workspace, "paged", "", now); !errors.Is(err, context.Canceled) || ok || id != "" {
		t.Fatalf("cancelled key lookup=%q/%t err=%v", id, ok, err)
	}
	if id, ok, err := probe.ReleasePipelineForKeyHash(ctx, workspace, "paged", "sw25-current\x00", now); !errors.Is(err, context.Canceled) || ok || id != "" {
		t.Fatalf("NUL lookup lost cancellation precedence: %q/%t err=%v", id, ok, err)
	}
	if id, ok, err := probe.ReleasePipelineForKeyHash(ctx, workspace, "", "sw25-current\x00", now); err != nil || ok || id != "" {
		t.Fatalf("empty hash lost its existing no-query precedence: %q/%t err=%v", id, ok, err)
	}
	if id, ok, err := probe.ReleasePipelineForKeyHash(t.Context(), workspace, "paged", "", now); err != nil || !ok || id != "sw25-current" {
		t.Fatal("key lookup did not recover from cancellation", id, ok, err)
	}
	var final []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='releasePipelines' AND record_key='sw25-current'`, workspace).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, final) || probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("key lookup changed a body, decoded full metadata or leaked admission")
	}
}
