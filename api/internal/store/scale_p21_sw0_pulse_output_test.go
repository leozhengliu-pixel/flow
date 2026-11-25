package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW0PulseOutputLifetime(t *testing.T) {
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{
		"physical": {
			Workspace: domain.Workspace{URLKey: "logical"},
			Projects:  []domain.Project{{ID: "project", Name: "Project"}},
			ProjectUpdates: map[string][]domain.ProjectUpdate{
				"project": {{ID: "update", Body: "Complete selected output"}},
			},
		},
	})
	for _, register := range []func(func()) bool{nil, func(func()) bool { return false }} {
		ctx := WithMetadataOutputOwner(t.Context(), register)
		read, found, err := view.BeginPulseRead(ctx, "physical", false)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		if err := read.TransferOutput(ctx); !errors.Is(err, ErrBoundedMetadataCoverage) ||
			!errors.Is(metadataOutputError(ctx), ErrBoundedMetadataCoverage) ||
			read.Snapshot != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("rejected output did not close and record coverage failure", err)
		}
	}
	var nilRead *PulseRead
	if err := nilRead.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("nil output handle accepted", err)
	}
	var releases []func()
	ctx, cancel := context.WithCancel(WithMetadataOutputOwner(t.Context(), func(close func()) bool {
		releases = append(releases, close)
		return true
	}))
	read, found, err := view.BeginPulseRead(ctx, "physical", false)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := read.LoadUpdates(ctx, "project", "project", []string{"update"}); err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if view.MetadataWorkUsedForTesting() == 0 || read.Snapshot.ProjectUpdates["project"][0].Body != "Complete selected output" {
		t.Fatal("cancellation released output during encoding")
	}
	if _, err := json.Marshal(read.Snapshot); err != nil {
		t.Fatal("retained output could not encode", err)
	}
	for _, release := range releases {
		release()
		release()
	}
	if read.Snapshot != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("operation completion leaked Pulse output")
	}
}
