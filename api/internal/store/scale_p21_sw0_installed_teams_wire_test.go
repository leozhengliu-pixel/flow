package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

func sw15WireTeamsPair(teams []domain.Team) (*SQLiteStore, *SQLiteStore) {
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "logical-id", URLKey: "logical-key"}, Teams: teams}
	return &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}, lastWorkspaceKey: "physical"},
		&SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}, lastWorkspaceKey: "physical", metadataResidency: newMetadataResidency()}
}

func sw15WireTeamsUsed(s *SQLiteStore) int {
	s.metadataResidency.work.mu.Lock()
	defer s.metadataResidency.work.mu.Unlock()
	return s.metadataResidency.work.used
}

func sw15WireTimes(team domain.Team) []*time.Time {
	return []*time.Time{team.RetiredAt, team.ArchivedAt, team.CreatedAt, team.UpdatedAt}
}

func sw15WireJSONEqual(t *testing.T, actual, expected []domain.Team) {
	t.Helper()
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	if !bytes.Equal(actualJSON, expectedJSON) || (actualErr == nil) != (expectedErr == nil) {
		t.Fatal("Team JSON output/error presence changed", actualErr, expectedErr)
	}
	if actualErr != nil && (reflect.TypeOf(actualErr) != reflect.TypeOf(expectedErr) || actualErr.Error() != expectedErr.Error()) {
		t.Fatal("Team encoding error changed", actualErr, expectedErr)
	}
	var actualWire, expectedWire bytes.Buffer
	actualErr = json.NewEncoder(&actualWire).Encode(actual)
	expectedErr = json.NewEncoder(&expectedWire).Encode(expected)
	if !bytes.Equal(actualWire.Bytes(), expectedWire.Bytes()) || (actualErr == nil) != (expectedErr == nil) {
		t.Fatal("Team streaming JSON output/error presence changed", actualErr, expectedErr)
	}
	if actualErr != nil && (reflect.TypeOf(actualErr) != reflect.TypeOf(expectedErr) || actualErr.Error() != expectedErr.Error()) {
		t.Fatal("Team streaming JSON error changed", actualErr, expectedErr)
	}
	for index := range actual {
		for field, when := range sw15WireTimes(actual[index]) {
			source := sw15WireTimes(expected[index])[field]
			if (when == nil) != (source == nil) {
				t.Fatal("nil timestamp changed")
			}
			if when == nil {
				continue
			}
			actualJSON, actualErr := when.MarshalJSON()
			expectedJSON, expectedErr := source.MarshalJSON()
			if !bytes.Equal(actualJSON, expectedJSON) || (actualErr == nil) != (expectedErr == nil) {
				t.Fatal("timestamp MarshalJSON changed", actualErr, expectedErr)
			}
			if actualErr != nil && actualErr.Error() != expectedErr.Error() {
				t.Fatal("timestamp MarshalJSON error changed", actualErr, expectedErr)
			}
			if !when.Equal(*source) || when.Before(*source) || when.After(*source) {
				t.Fatal("timestamp instant changed")
			}
		}
	}
}

func TestScaleP21SW0InstalledTeamsWireDifferential(t *testing.T) {
	kind := reflect.TypeFor[domain.Team]()
	expectedFields := []string{"ID", "Name", "Key", "Color", "Icon", "Private", "ExternalSource", "RetiredAt", "ArchivedAt", "ArchivedByID", "CreatedAt", "UpdatedAt"}
	var fields []string
	for index := range kind.NumField() {
		fields = append(fields, kind.Field(index).Name)
	}
	if !reflect.DeepEqual(fields, expectedFields) {
		t.Fatal("update complete Team wire clone/admission", fields)
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	large := time.FixedZone(strings.Repeat("retired-zone", 150000), 3661)
	locations := []*time.Location{time.UTC, time.Local, zone, large}
	for _, offset := range []int{-1 << 40, -1 << 31, -90061, -86400, -86399, -43201, -1, 0, 1, 19845, 50401, 86399, 86400, 90061, 1<<31 - 1, 1 << 40} {
		locations = append(locations, time.FixedZone("source-zone", offset))
	}
	instants := []time.Time{
		{}, time.Now(), time.Date(2026, 3, 8, 6, 59, 59, 999999999, time.UTC),
		time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 5, 59, 59, 999999999, time.UTC),
		time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC),
		time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC),
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for _, location := range locations {
		for _, instant := range instants {
			when := instant.In(location)
			full := domain.Team{
				ID: "duplicate\x00id", Name: "name<&>\x00" + string([]byte{0xff, 0xfe}),
				Key: "key", Color: "color", Icon: "icon", Private: true, ExternalSource: "hr:org:node:external",
				RetiredAt: &when, ArchivedAt: &when, ArchivedByID: "actor", CreatedAt: &when, UpdatedAt: &when,
			}
			legacy, bounded := sw15WireTeamsPair([]domain.Team{full, {ID: full.ID}, {}, full})
			expected, _ := legacy.WorkspaceMetadata("physical")
			original := when
			for _, source := range []*SQLiteStore{legacy, bounded} {
				read, err := source.BeginInstalledTeamsWireRead(t.Context(), "physical")
				if err != nil || !read.Found {
					t.Fatal("wire read failed", err)
				}
				sw15WireJSONEqual(t, read.Teams, expected.Teams)
				if when != original || when.Location() != location {
					t.Fatal("wire normalization mutated installed source time")
				}
				if source == bounded {
					for field, output := range sw15WireTimes(read.Teams[0]) {
						input := sw15WireTimes(full)[field]
						if output == input || output.Location().String() != "" {
							t.Fatal("timestamp pointer/location not detached")
						}
						if (location == large || location == zone || location == time.Local) && output.Location() == input.Location() {
							t.Fatal("wire output retained installed location")
						}
					}
				}
				read.Close()
				read.Close()
				if read.Teams != nil || read.Found || source == bounded && sw15WireTeamsUsed(source) != 0 {
					t.Fatal("Close retained output or admission")
				}
			}
		}
	}
}

func TestScaleP21SW0InstalledTeamsWireEncodingErrors(t *testing.T) {
	for _, when := range []time.Time{
		time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 3, 0, 0, 0, 0, time.FixedZone("invalid-positive", 86400)),
		time.Date(2026, 11, 3, 0, 0, 0, 0, time.FixedZone("invalid-negative", -86400)),
	} {
		for field := range 4 {
			team := domain.Team{ID: "encoding-error"}
			switch field {
			case 0:
				team.RetiredAt = &when
			case 1:
				team.ArchivedAt = &when
			case 2:
				team.CreatedAt = &when
			case 3:
				team.UpdatedAt = &when
			}
			legacy, bounded := sw15WireTeamsPair([]domain.Team{team})
			expected, _ := legacy.WorkspaceMetadata("physical")
			if _, err := json.Marshal(expected.Teams); err == nil {
				t.Fatal("encoding-error fixture unexpectedly valid")
			}
			read, err := bounded.BeginInstalledTeamsWireRead(t.Context(), "physical")
			if err != nil {
				t.Fatal(err)
			}
			sw15WireJSONEqual(t, read.Teams, expected.Teams)
			read.Close()
		}
	}
}

func TestScaleP21SW0InstalledTeamsWireNilKeysBounds(t *testing.T) {
	for _, teams := range [][]domain.Team{nil, {}, {{ID: "team"}}} {
		legacy, bounded := sw15WireTeamsPair(teams)
		for _, key := range []string{"physical", "", "missing", "PHYSICAL", "physical\x00", "logical-key"} {
			expected, found := legacy.WorkspaceMetadata(key)
			for _, source := range []*SQLiteStore{legacy, bounded} {
				read, err := source.BeginInstalledTeamsWireRead(t.Context(), key)
				if err != nil || read.Found != found {
					t.Fatal("lookup changed", err)
				}
				sw15WireJSONEqual(t, read.Teams, expected.Teams)
				read.Close()
			}
		}
	}
	legacy, bounded := sw15WireTeamsPair([]domain.Team{{Name: strings.Repeat("<", installedTeamsWireReadMaxBytes/32)}})
	read, err := bounded.BeginInstalledTeamsWireRead(t.Context(), "physical")
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || sw15WireTeamsUsed(bounded) != 0 {
		t.Fatal("over-cap read cloned output or admitted work", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := bounded.BeginInstalledTeamsWireRead(cancelled, "physical"); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("bounded cancellation changed", err)
	}
	off, err := legacy.BeginInstalledTeamsWireRead(cancelled, "physical")
	if err != nil || !off.Found || len(off.Teams) != 1 {
		t.Fatal("off branch acquired new cancellation behavior", err)
	}
	off.Close()
	_, bounded = sw15WireTeamsPair([]domain.Team{{ID: "small"}})
	bounded.metadataResidency.work.mu.Lock()
	bounded.metadataResidency.work.limit = 1
	bounded.metadataResidency.work.mu.Unlock()
	if read, err := bounded.BeginInstalledTeamsWireRead(t.Context(), "physical"); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || sw15WireTeamsUsed(bounded) != 0 {
		t.Fatal("process budget ignored", err)
	}
}

func TestScaleP21SW0InstalledTeamsWireOwnedGeneration(t *testing.T) {
	name := strings.Repeat("retired-owned-zone", 100000)
	when := time.Date(2026, 11, 3, 12, 13, 14, 15, time.FixedZone(name, 3661))
	originalTime := when
	original := domain.Team{ID: "owned-id", Name: strings.Repeat("old<&>\x00", 1000), Key: "old-key", Color: "color", Icon: "icon", ExternalSource: "source", ArchivedByID: "actor", RetiredAt: &when, ArchivedAt: &when, CreatedAt: &when, UpdatedAt: &when}
	_, bounded := sw15WireTeamsPair([]domain.Team{original})
	parentCtx, releaseParent, err := bounded.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, err := bounded.BeginInstalledTeamsWireRead(parentCtx, "physical")
	if err != nil {
		releaseParent()
		t.Fatal(err)
	}
	defer read.Close()
	releaseParent()
	charged := sw15WireTeamsUsed(bounded)
	if charged == 0 || charged >= len(name) {
		t.Fatal("invalid retired generation fixture", charged)
	}
	for _, pair := range [][2]string{
		{read.Teams[0].ID, original.ID}, {read.Teams[0].Name, original.Name}, {read.Teams[0].Key, original.Key},
		{read.Teams[0].Color, original.Color}, {read.Teams[0].Icon, original.Icon},
		{read.Teams[0].ExternalSource, original.ExternalSource}, {read.Teams[0].ArchivedByID, original.ArchivedByID},
	} {
		if pair[0] != pair[1] || unsafe.StringData(pair[0]) == unsafe.StringData(pair[1]) {
			t.Fatal("wire output pinned installed string generation")
		}
	}
	bounded.mu.Lock()
	next := bounded.workspaces["physical"]
	next.Teams = []domain.Team{{ID: "replacement"}}
	bounded.workspaces["physical"] = next
	bounded.mu.Unlock()
	for _, output := range sw15WireTimes(read.Teams[0]) {
		if output == &when || output.Location() == when.Location() || output.Location().String() != "" {
			t.Fatal("retired location name backing escaped into output")
		}
	}
	if when != originalTime || sw15WireTeamsUsed(bounded) != charged {
		t.Fatal("replacement mutated source or revoked output charge")
	}
	sw15WireJSONEqual(t, read.Teams, []domain.Team{original})
	encoded, err := json.Marshal(read.Teams)
	if err != nil || charged < 3*len(encoded)+len(original.Name) {
		t.Fatal("encoding allowance too small", charged, len(encoded), err)
	}
	fresh, err := bounded.BeginInstalledTeamsWireRead(t.Context(), "physical")
	if err != nil || len(fresh.Teams) != 1 || fresh.Teams[0].ID != "replacement" {
		t.Fatal("new read did not see replacement", err)
	}
	fresh.Close()
	read.Close()
	if sw15WireTeamsUsed(bounded) != 0 {
		t.Fatal("retired wire generation leaked admission")
	}
}

func TestScaleP21SW0InstalledTeamsWireWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	before, found := view.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("missing installed fixture")
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams"), workspace, "scale_p21_sw15.teams_wire_warm", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "sw15-wire-consent", Name: "Complete installed name", Private: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old, err := view.BeginInstalledTeamsWireRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	sw15WireJSONEqual(t, old.Teams, before.Teams)
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	current, found := view.WorkspaceMetadata(workspace)
	fresh, err := view.BeginInstalledTeamsWireRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("missing reload source", err)
	}
	sw15WireJSONEqual(t, fresh.Teams, current.Teams)
	sw15WireJSONEqual(t, old.Teams, before.Teams)
	old.Close()
	fresh.Close()
	if view.WorkspaceMetadataDecodes() != 0 || sw15WireTeamsUsed(view) != 0 {
		t.Fatal("wire reader decoded SQL metadata or leaked admission")
	}
}
