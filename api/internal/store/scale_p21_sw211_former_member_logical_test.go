package store

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW211FormerMembersInstalledLogicalDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, f.data.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("sw211-")
	members := []domain.WorkspaceMember{
		{User: domain.User{ID: prefix + "nul\x00id", Name: strings.Repeat("irrelevant large name ", 110000)}, Status: "suspended"},
		{User: domain.User{ID: prefix + "status"}, Status: "suspended\x00"},
		{User: domain.User{ID: ""}, Status: "suspended"},
		{User: domain.User{ID: prefix + "duplicate"}, Status: "suspended"},
		{User: domain.User{ID: prefix + "duplicate"}, Status: "active"},
		{User: domain.User{ID: strings.Repeat("long-id", 40)}, Status: "suspended"},
		{User: domain.User{ID: prefix + "case"}, Status: "Suspended"},
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members"), workspace, "scale_p21.sw211_members", "", nil, func(data *domain.Bootstrap) error {
		data.Members = members
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updatedWorkspace := f.data.Workspace
	updatedWorkspace.Name = prefix + "supported full write"
	if _, err := f.UpdateWorkspace(t.Context(), workspace, updatedWorkspace); err != nil {
		t.Fatal(err)
	}
	warmWant, err := f.FormerMembers(t.Context(), workspace)
	if err != nil || !warmWant[prefix+"nul\x00id"] || warmWant[prefix+"status"] {
		t.Fatal("supported warm source no longer witnesses native ID/status provenance", warmWant, err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			reloadFromRows(t, f.SQLiteStore, workspace)
		}
		want, err := f.FormerMembers(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		probe, err := f.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { probe.releaseMetadataLogicalSources() })
		for _, key := range []string{workspace, "", workspace + "-missing", workspace + "\x00"} {
			expected := want
			if key != "" && key != workspace {
				expected = map[string]bool{}
			}
			queries := 0
			read, err := probe.BeginFormerMembersRead(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), key)
			if read == nil || err != nil || !reflect.DeepEqual(expected, read.Members()) {
				t.Fatalf("reload=%t key=%q want=%v got=%v err=%v", reload, key, expected, read, err)
			}
			if key == "" || key == workspace {
				if queries != 1 || probe.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("fallback did not retain its owned result or consulted canonical member rows", queries)
				}
			}
			_ = read.Close()
			_ = read.Close()
			if probe.MetadataWorkUsedForTesting() != 0 || probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("logical former-member read retained work or decoded full metadata")
			}
		}
		if reload && reflect.DeepEqual(warmWant, want) {
			t.Fatal("canonical reload no longer witnesses the supported native/canonical distinction")
		}
		f.db.SetMaxOpenConns(1)
		tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
		read, err := probe.BeginFormerMembersRead(ctx, workspace)
		if err != nil || read == nil || !reflect.DeepEqual(want, read.Members()) {
			t.Fatal("logical fallback did not reuse the borrowed SQL snapshot", err)
		}
		_ = read.Close()
		if _, err := tx.ExecContext(ctx, `SELECT 1`); err != nil {
			t.Fatal("former-member read closed the borrowed transaction", err)
		}
		_ = tx.Rollback()
		f.db.SetMaxOpenConns(4)
		limit := probe.metadataResidency.work.limit
		probe.metadataResidency.work.limit = scaleP21SW16FormerWorkBytes - 1
		read, err = probe.BeginFormerMembersRead(t.Context(), workspace)
		probe.metadataResidency.work.limit = limit
		if read == nil || !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("fallback escaped admission", err)
		}
		_ = read.Close()
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		read, err = probe.BeginFormerMembersRead(cancelled, workspace)
		if read == nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled logical fallback changed", err)
		}
		_ = read.Close()
		if probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("failed logical fallback retained work")
		}
	}
}

func TestScaleP21SW211FormerMembersPinsBeforeMembershipQuery(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, f.data.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members"), workspace, "scale_p21.sw211_pin", "", nil, func(data *domain.Bootstrap) error {
		data.Members = []domain.WorkspaceMember{{User: domain.User{ID: "sw211-before"}, Status: "suspended"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.releaseMetadataLogicalSources)
	candidate, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
		Workspace: f.data.Workspace,
		Members:   []domain.WorkspaceMember{{User: domain.User{ID: "sw211-after"}, Status: "suspended"}},
	}, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	swapped := false
	defer func() {
		if !swapped {
			candidate.Release()
		}
	}()
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if !swapped && strings.Contains(query, "SELECT 1 FROM workspace_memberships") {
			swapped = true
			probe.installMetadataLogicalSource(workspace, candidate)
		}
	})
	read, err := probe.BeginFormerMembersRead(ctx, workspace)
	if read != nil {
		defer read.Close()
	}
	if err != nil || read == nil || !swapped || !reflect.DeepEqual(read.Members(), map[string]bool{"sw211-before": true}) {
		t.Fatal("membership SQL changed the earlier installed source", err, swapped)
	}
	prior := maps.Clone(read.Members())
	_ = read.Close()
	next, err := probe.BeginFormerMembersRead(t.Context(), workspace)
	if next != nil {
		defer next.Close()
	}
	if err != nil || next == nil || !reflect.DeepEqual(prior, map[string]bool{"sw211-before": true}) ||
		!reflect.DeepEqual(next.Members(), map[string]bool{"sw211-after": true}) {
		t.Fatal("subsequent lookup did not observe the new installed generation", err)
	}
	_ = next.Close()
	if probe.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("pinned generation retained output admission")
	}
}
