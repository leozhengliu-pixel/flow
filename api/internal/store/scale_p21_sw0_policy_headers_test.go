package store

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW0PolicyHeaders(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	archived := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.policy_headers", "", nil, func(data *domain.Bootstrap) error {
		for i := range 70 {
			id := idgen.New("team_sw0_policy_")
			team := domain.Team{ID: id, Key: id, Name: strings.Repeat("unused name ", 200), Private: i%2 == 0, ExternalSource: "source"}
			if i%3 == 0 {
				team.ArchivedAt = &archived
			}
			if i%4 == 0 {
				team.RetiredAt = &archived
			}
			data.Teams = append(data.Teams, team)
			settings := domain.TeamSettings{
				TeamID: id, Access: "private", MembershipRestriction: "restricted", SettingsPermission: "admin",
				LabelPermission: "members", MemberPermission: "admin", ParentTeamID: data.Teams[0].ID,
				Description: "unused guidance",
			}
			if i == 0 {
				settings.Description = strings.Repeat("unused policy guidance ", 15000)
			}
			data.TeamSettings[id] = settings
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, ok := f.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("missing owned workspace")
	}
	want := resourcePolicyMetadata(legacy)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := probe.readResidentResourcePolicy(t.Context(), workspace, f.data.Workspace.ID)
	runtime.ReadMemStats(&after)
	t.Logf("policy headers bytes=%d allocs=%d error=%v", after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, err)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		for i := range want.Teams {
			if !reflect.DeepEqual(want.Teams[i], got.Teams[i]) {
				t.Errorf("team %d want=%+v got=%+v", i, want.Teams[i], got.Teams[i])
				break
			}
		}
		for id, item := range want.TeamSettings {
			if !reflect.DeepEqual(item, got.TeamSettings[id]) {
				t.Errorf("settings %s want=%+v got=%+v", id, item, got.TeamSettings[id])
				break
			}
		}
		t.Fatal("projected policy scalars, ordering, private flags or hierarchy differ")
	}
	version, err := probe.RealtimeSourceVersion(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	source, err := probe.ReadEventPolicySource(t.Context(), workspace, version)
	if err != nil || !reflect.DeepEqual(source.metadata, want) {
		t.Fatal("event policy source lost required policy headers", err)
	}
	got.Teams[0].Key = "caller mutation"
	for id, settings := range got.TeamSettings {
		settings.Access = "caller mutation"
		got.TeamSettings[id] = settings
		break
	}
	again, err := probe.readResidentResourcePolicy(t.Context(), workspace, f.data.Workspace.ID)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal("policy returned aliased state", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("policy decoded full metadata or leaked work")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probe.readResidentResourcePolicy(cancelled, workspace, f.data.Workspace.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled policy read did not stop", err)
	}
	if _, err := probe.readResidentResourcePolicy(t.Context(), workspace, "foreign-workspace-id"); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("policy read accepted foreign workspace binding", err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams[0].Key = strings.Repeat("large policy key ", 20000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.readResidentResourcePolicy(t.Context(), workspace, f.data.Workspace.ID); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatal("oversized required policy input was silently omitted", err)
	}
}
