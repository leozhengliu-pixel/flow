package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW19NativeSCIMTeamRead(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first, second, mapped := idgen.New("team_sw19_first_"), idgen.New("team_sw19_second_"), idgen.New("team_sw19_mapped_")
	teams := []domain.Team{
		{ID: first, Key: "SHARED", Name: "First key wins", Private: true, CreatedAt: &stamp},
		{ID: second, Key: "SHARED", Name: "Second", ArchivedAt: &stamp},
		{ID: mapped, Key: "MAPPED", Name: "Explicit", Color: "green", RetiredAt: &stamp, UpdatedAt: &stamp},
		{ID: "Kelvin-sw19", Key: "ſcope-sw19", Name: "Unicode folding"},
	}
	for index := range 125 {
		teams = append(teams, domain.Team{ID: fmt.Sprintf("team_sw19_native_%03d", index), Key: fmt.Sprintf("KEY%d", index), Name: "Retained"})
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "workspaceSettings"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = slices.Clone(teams)
		data.WorkspaceSettings.SCIMTeamGroupMapping = map[string]string{
			mapped: "  named group  ", "not-a-real-team": "missing mapping", second: "external-map",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately legal large body and unknown raw property. Matching reads
	// only id/key; unlinked output must remain typed and omit the raw field.
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, workspace, first).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["name"] = strings.Repeat("large selected team ", 20000)
	object["futureUnknown"] = strings.Repeat("must not leak ", 25000)
	raw, _ = json.Marshal(object)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teams' AND record_key=?`, raw, workspace, first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &teams[0]); err != nil {
		t.Fatal(err)
	}
	groups := []SCIMGroup{
		{DisplayName: "named GROUP", ExternalID: "shared"},
		{ExternalID: "external-map"},
		{ExternalID: "shared"},
		{ExternalID: strings.ToUpper(second)},
		{ExternalID: "kelvin-sw19"},
		{ExternalID: "Scope-sw19"},
		{DisplayName: teams[124].Name},
		{DisplayName: "missing mapping", ExternalID: "KEY124"},
		{ExternalID: " MAPPED "},
	}
	want := []string{mapped, second, first, second, "Kelvin-sw19", "Kelvin-sw19", "", teams[len(teams)-1].ID, ""}
	read, found, err := view.BeginSCIMTeamRead(t.Context(), workspace, groups)
	if err != nil || !found {
		t.Fatal("begin native SCIM read", found, err)
	}
	for index, id := range want {
		if read.TeamID(index) != id {
			t.Fatalf("group%d=%q want=%q", index, read.TeamID(index), id)
		}
	}
	if view.metadataResidency.work.used == 0 {
		t.Fatal("selector is not charged while open")
	}
	unlinked, err := read.Teams()
	if err != nil {
		t.Fatal(err)
	}
	expected := []domain.Team{}
	for _, team := range teams {
		if !slices.Contains(want, team.ID) {
			expected = append(expected, team)
		}
	}
	if !reflect.DeepEqual(unlinked, expected) {
		t.Fatal("linked-team exclusion, canonical ordering or typed team fields changed")
	}
	read.Close()
	read.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("SCIM team read leaked work or decoded metadata")
	}
	large, found, err := view.BeginSCIMTeamRead(t.Context(), workspace, nil)
	if err != nil || !found {
		t.Fatal(err)
	}
	full, err := large.Teams()
	if err != nil || !reflect.DeepEqual(full, teams) {
		t.Fatal("legal large team or canonical order was lost", err)
	}
	encoded, _ := json.Marshal(full)
	if strings.Contains(string(encoded), "futureUnknown") || strings.Contains(string(encoded), "must not leak") {
		t.Fatal("typed output exposed an unknown canonical schema field")
	}
	if full[0].Name == "" || len(full[0].Name) < MetadataRecordPageMaxBytes {
		t.Fatal("legal large typed name was truncated")
	}
	large.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginSCIMTeamRead(ctx, workspace, groups); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SCIM selector: %v", err)
	}
	if read, found, err := view.BeginSCIMTeamRead(t.Context(), workspace+"-missing", groups); read != nil || found || err != nil {
		t.Fatalf("missing workspace: found=%v err=%v", found, err)
	}
	for _, invalid := range []string{strings.ToUpper(workspace), workspace + " ", workspace + "\t", workspace + "\x00", workspace[:len(workspace)/2] + "\x00" + workspace[len(workspace)/2:]} {
		if _, found := f.WorkspaceMetadata(invalid); found {
			t.Fatalf("malformed workspace fixture unexpectedly exists: %q", invalid)
		}
		for _, input := range [][]SCIMGroup{nil, groups} {
			if read, found, err := view.BeginSCIMTeamRead(t.Context(), invalid, input); read != nil || found || err != nil {
				if read != nil {
					read.Close()
				}
				t.Fatalf("noncanonical workspace matched native SCIM directory: workspace=%q groups=%d found=%v err=%v", invalid, len(input), found, err)
			}
		}
	}
	recovery, found, err := view.BeginSCIMTeamRead(t.Context(), "", []SCIMGroup{{ExternalID: "SHARED"}})
	if err != nil || !found || recovery.TeamID(0) != first {
		t.Fatalf("default workspace/recovery=%v %v", found, err)
	}
	recovery.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("selector recovery leaked work")
	}
	previous := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if read, _, err := view.BeginSCIMTeamRead(t.Context(), workspace, groups); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("selector admission should reject before data transfer: %v", err)
	}
	view.metadataResidency.work.limit = previous
	read, _, err = view.BeginSCIMTeamRead(t.Context(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	view.metadataResidency.work.mu.Lock()
	remaining := view.metadataResidency.work.limit - view.metadataResidency.work.used
	view.metadataResidency.work.mu.Unlock()
	_, held, err := view.admitMetadataWork(t.Context(), remaining)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	// Teams uses the parent request context for its separate output allowance.
	read.parent = short
	if _, err := read.Teams(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("output admission cancellation=%v", err)
	}
	cancel()
	read.Close()
	held()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("output admission failure leaked work")
	}
	var after []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, workspace, first).Scan(&after); err != nil || !bytes.Equal(raw, after) {
		t.Fatal("native SCIM reads rewrote a canonical large body", err)
	}
	object["name"] = strings.Repeat("large output ", 750000)
	oversized, _ := json.Marshal(object)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teams' AND record_key=?`, oversized, workspace, first); err != nil {
		t.Fatal(err)
	}
	read, _, err = view.BeginSCIMTeamRead(t.Context(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := read.Teams(); output != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("whole-output allowance failed to reject before decoding: len=%d err=%v", len(output), err)
	}
	read.Close()
	// Matching uses only the key scalars even when the matched team's body
	// exceeds the whole-directory output allowance.
	read, _, err = view.BeginSCIMTeamRead(t.Context(), workspace, []SCIMGroup{{ExternalID: "SHARED"}})
	if err != nil || read.TeamID(0) != first {
		t.Fatalf("scalar lookup fetched an unrelated body: %v", err)
	}
	read.Close()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teams' AND record_key=?`, raw, workspace, first); err != nil {
		t.Fatal(err)
	}
	read, _, err = view.BeginSCIMTeamRead(t.Context(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := read.Teams(); err != nil || len(output) != len(teams) {
		t.Fatalf("output after budget failure did not recover: %v", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("whole-output admission failure leaked work")
	}
}
