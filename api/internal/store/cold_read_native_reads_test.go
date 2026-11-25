package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW24ClassicGitHubPredicate(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for value := rune(0); value <= unicode.MaxRune; value++ {
		if unicode.IsSpace(value) != slices.Contains(scaleP21SW24Spaces, value) {
			t.Fatalf("SQL whitespace predicate differs from Go for U+%04X", value)
		}
	}
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	whitespace := string(scaleP21SW24Spaces)
	for _, spec := range []struct {
		name, provider, status, secret, env string
	}{
		{"nil-config", "github", "connected", "", ""},
		{"configured", "github", "configured", " x ", ""},
		{"connected", "github", "connected", "", "FLOW_GITHUB_SECRET"},
		{"all-unicode-space", "github", "configured", whitespace, whitespace},
		{"large-space-only", "github", "configured", strings.Repeat(whitespace, 5000), ""},
		{"large-with-content", "github", "connected", strings.Repeat(whitespace, 5000) + "x", ""},
		{"zero-width-is-content", "github", "connected", "\u200b", ""},
		{"bom-is-content", "github", "connected", "\ufeff", ""},
		{"paused", "github", "paused", "x", "x"},
		{"case-provider", "GitHub", "connected", "x", ""},
		{"case-status", "github", "Connected", "x", ""},
		{"other-provider", "gitlab", "connected", "x", ""},
	} {
		t.Run(spec.name, func(t *testing.T) {
			config := map[string]string(nil)
			if spec.secret != "" || spec.env != "" {
				config = map[string]string{"webhookSecret": spec.secret, "webhookSecretEnv": spec.env}
			}
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
				data.IntegrationConnections = []domain.IntegrationConnection{{ID: "sw24-classic", Provider: spec.provider, Status: spec.status, Config: config}}
				for range 75 {
					data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: idgen.New("sw24-other-"), Provider: "gitlab", Status: "connected", Config: map[string]string{"body": strings.Repeat("unused ", 2000)}})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			want := spec.provider == "github" && slices.Contains([]string{"configured", "connected"}, spec.status) &&
				(strings.TrimSpace(spec.secret) != "" || strings.TrimSpace(spec.env) != "")
			got, err := probe.ClassicGitHubSyncConfigured(t.Context(), workspace)
			if err != nil || got != want {
				t.Fatalf("predicate=%v err=%v want=%v", got, err, want)
			}
		})
	}
	if got, err := probe.ClassicGitHubSyncConfigured(t.Context(), workspace+"-missing"); got || err != nil {
		t.Fatal("missing workspace predicate changed", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probe.ClassicGitHubSyncConfigured(ctx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled predicate hid its error", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("scalar predicate decoded complete metadata")
	}
}

func TestScaleP21SW24ExportNativePage(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, owner := f.data.Workspace.URLKey, f.data.Viewer.ID
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "exportJobs"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
		data.ExportJobs = []domain.ExportJob{{ID: "sw24-export-other", UserID: owner + "-other", Error: strings.Repeat("hidden export ", 30000)}}
		for i := range 123 {
			data.ExportJobs = append(data.ExportJobs, domain.ExportJob{ID: fmt.Sprintf("sw24-export-%03d", i), UserID: owner, Status: "completed", Format: "csv", Filename: fmt.Sprintf("owned-%03d.csv", i)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []struct{ offset, limit, count int }{
		{0, 50, 50}, {50, 100, 73}, {-1, 10, 10}, {124, 10, 10}, {123, 10, 0},
	} {
		jobs, total, err := probe.ExportJobPage(t.Context(), workspace, owner, false, page.offset, page.limit)
		if err != nil || total != 123 || len(jobs) != page.count {
			t.Fatalf("native page count=%d total=%d err=%v", len(jobs), total, err)
		}
	}
	job, found, err := probe.ExportJobRecord(t.Context(), workspace, owner, "sw24-export-050", false)
	if err != nil || !found || job.Filename != "owned-050.csv" {
		t.Fatal("native point lookup changed", job, found, err)
	}
	if _, found, err := probe.ExportJobRecord(t.Context(), workspace, owner, "sw24-export-other", false); err != nil || found {
		t.Fatal("unowned oversized job revealed its body budget", found, err)
	}
	if _, _, err := probe.ExportJobRecord(t.Context(), workspace, owner, "sw24-export-other", true); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatal("oversized selected job did not report its budget", err)
	}
	if _, _, err := probe.ExportJobPage(t.Context(), workspace, owner, true, 0, 100); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatal("oversized page did not report its budget", err)
	}
	if _, found, err := probe.ExportJobRecord(t.Context(), workspace, owner, strings.Repeat("x", 192), false); err != nil || found {
		t.Fatal("malformed missing ID changed error precedence", found, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := probe.ExportJobPage(cancelled, workspace, owner, false, 0, 50); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled page hid its error", err)
	}
	if jobs, total, err := probe.ExportJobPage(t.Context(), workspace, owner, false, 0, 50); err != nil || total != 123 || len(jobs) != 50 {
		t.Fatal("page after cancellation did not recover", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("native export page decoded complete metadata")
	}
}
