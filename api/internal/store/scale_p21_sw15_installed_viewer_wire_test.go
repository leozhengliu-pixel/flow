package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

func sw15ViewerWireUsed(s *SQLiteStore) int {
	if s.metadataResidency == nil {
		return 0
	}
	s.metadataResidency.work.mu.Lock()
	defer s.metadataResidency.work.mu.Unlock()
	return s.metadataResidency.work.used
}

func sw15ViewerWireFull(until *time.Time) domain.User {
	var user domain.User
	value := reflect.ValueOf(&user).Elem()
	for index := range value.NumField() {
		item := value.Field(index)
		switch item.Kind() {
		case reflect.String:
			item.SetString(value.Type().Field(index).Name + "<&>\x00" + string([]byte{0xff, 0xfe}))
		case reflect.Bool:
			item.SetBool(true)
		case reflect.Slice:
			item.Set(reflect.ValueOf([]string{"owned<&>\x00", string([]byte{0xff})}))
		}
	}
	user.OutOfOfficeUntil = until
	user.Via = &domain.ActorBot{ID: "runtime-only"}
	return user
}

func sw15ViewerWireAssert(t *testing.T, actual, expected domain.User) {
	t.Helper()
	want := cloneBootstrap(domain.Bootstrap{Viewer: expected}).Viewer
	actualTime, expectedTime := actual.OutOfOfficeUntil, want.OutOfOfficeUntil
	actualWithoutTime, expectedWithoutTime := actual, want
	actualWithoutTime.OutOfOfficeUntil = nil
	expectedWithoutTime.OutOfOfficeUntil = nil
	if !reflect.DeepEqual(actualWithoutTime, expectedWithoutTime) {
		t.Fatal("non-time User fields changed")
	}
	if (actualTime == nil) != (expectedTime == nil) || !equalTimePointer(actualTime, expectedTime) {
		t.Fatal("nil/instant decision changed")
	}
	if actualTime != nil {
		if actualTime.IsZero() != expectedTime.IsZero() || !actualTime.Equal(*expectedTime) || actualTime.Before(*expectedTime) || actualTime.After(*expectedTime) {
			t.Fatal("IsZero or wall-clock instant changed")
		}
		for _, other := range []*time.Time{nil, {}, expectedTime, actualTime} {
			if equalTimePointer(actualTime, other) != equalTimePointer(expectedTime, other) {
				t.Fatal("equalTimePointer decision changed")
			}
		}
		later, earlier := expectedTime.Add(time.Nanosecond), expectedTime.Add(-time.Nanosecond)
		for _, other := range []*time.Time{&later, &earlier} {
			if equalTimePointer(actualTime, other) != equalTimePointer(expectedTime, other) ||
				actualTime.Before(*other) != expectedTime.Before(*other) || actualTime.After(*other) != expectedTime.After(*other) {
				t.Fatal("neighboring instant decision changed")
			}
		}
	}
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(want)
	if !bytes.Equal(actualJSON, expectedJSON) || (actualErr == nil) != (expectedErr == nil) {
		t.Fatal("User JSON bytes/error presence changed", actualErr, expectedErr)
	}
	if actualErr != nil && (reflect.TypeOf(actualErr) != reflect.TypeOf(expectedErr) || actualErr.Error() != expectedErr.Error()) {
		t.Fatal("User JSON encoding error changed", actualErr, expectedErr)
	}
	var actualWire, expectedWire bytes.Buffer
	actualErr = json.NewEncoder(&actualWire).Encode(actual)
	expectedErr = json.NewEncoder(&expectedWire).Encode(want)
	if !bytes.Equal(actualWire.Bytes(), expectedWire.Bytes()) || (actualErr == nil) != (expectedErr == nil) {
		t.Fatal("User streaming JSON changed", actualErr, expectedErr)
	}
	if actualErr != nil && actualErr.Error() != expectedErr.Error() {
		t.Fatal("User streaming JSON encoding error changed", actualErr, expectedErr)
	}
}

func TestScaleP21SW15InstalledViewerWireTimeDifferential(t *testing.T) {
	var timestampFields []string
	kind := reflect.TypeFor[domain.User]()
	for index := range kind.NumField() {
		field := kind.Field(index)
		if field.Type == reflect.TypeFor[*time.Time]() || field.Type == reflect.TypeFor[time.Time]() {
			timestampFields = append(timestampFields, field.Name)
		}
	}
	if !reflect.DeepEqual(timestampFields, []string{"OutOfOfficeUntil"}) {
		t.Fatal("review User timestamp wire/instant contract", timestampFields)
	}
	dst, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	large := time.FixedZone(strings.Repeat("retired-owned-zone", 100000), 3661)
	locations := []*time.Location{time.UTC, time.Local, dst, large}
	for _, offset := range []int{-1 << 40, -90061, -86400, -86399, -1, 0, 1, 19845, 86399, 86400, 90061, 1 << 40} {
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
	fixtures := []*time.Time{nil}
	monotonic := time.Now()
	fixtures = append(fixtures, &monotonic)
	for _, location := range locations {
		for _, instant := range instants {
			when := instant.In(location)
			fixtures = append(fixtures, &when)
		}
	}
	for _, until := range fixtures {
		viewer := sw15ViewerWireFull(until)
		var original time.Time
		if until != nil {
			original = *until
		}
		repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": {Viewer: viewer}}, lastWorkspaceKey: "workspace", metadataResidency: newMetadataResidency()}
		expected, found := repo.WorkspaceMetadata("workspace")
		read, ok, err := repo.BeginInstalledViewerRead(t.Context(), "")
		if err != nil || !found || !ok {
			t.Fatal("viewer wire read failed", err)
		}
		actual := read.Viewer()
		sw15ViewerWireAssert(t, actual, expected.Viewer)
		if until != nil {
			if *until != original || actual.OutOfOfficeUntil == until {
				t.Fatal("source timestamp changed or pointer was borrowed")
			}
			if until.Location() != time.UTC && actual.OutOfOfficeUntil.Location().String() != "" {
				t.Fatal("source zone name retained")
			}
			if (until.Location() == large || until.Location() == dst || until.Location() == time.Local) &&
				actual.OutOfOfficeUntil.Location() == until.Location() {
				t.Fatal("source location graph retained")
			}
		}
		read.Close()
		if sw15ViewerWireUsed(repo) != 0 {
			t.Fatal("wire viewer leaked admission")
		}
	}
}

func TestScaleP21SW15InstalledViewerWireGenerationAndCost(t *testing.T) {
	name := strings.Repeat("retired-owned-zone", 100000)
	when := time.Date(2026, 11, 3, 12, 13, 14, 15, time.FixedZone(name, 3661))
	original := when
	viewer := sw15ViewerWireFull(&when)
	without := viewer
	without.OutOfOfficeUntil = nil
	baseCost, err := scaleP21SW20ViewerCost(without)
	if err != nil {
		t.Fatal(err)
	}
	cost, err := scaleP21SW20ViewerCost(viewer)
	if err != nil || cost-baseCost != 4096 {
		t.Fatal("owned fixed-offset location lacks explicit pre-clone charge", cost-baseCost, err)
	}
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": {Viewer: viewer}}, metadataResidency: newMetadataResidency()}
	parent, releaseParent, err := repo.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := repo.BeginInstalledViewerRead(parent, "workspace")
	if err != nil || !found {
		releaseParent()
		t.Fatal(err)
	}
	defer read.Close()
	releaseParent()
	got := read.Viewer()
	charged := sw15ViewerWireUsed(repo)
	if charged != cost || charged >= len(name) {
		t.Fatal("invalid retained source fixture/admission", charged, cost, len(name))
	}
	input, output := reflect.ValueOf(viewer), reflect.ValueOf(got)
	for index := range input.NumField() {
		left, right := input.Field(index), output.Field(index)
		switch {
		case left.Kind() == reflect.String && left.Len() > 0:
			if unsafe.StringData(left.String()) == unsafe.StringData(right.String()) {
				t.Fatal("wire viewer retained source string backing")
			}
		case left.Type() == reflect.TypeFor[[]string]() && !left.IsNil():
			if left.Pointer() == right.Pointer() {
				t.Fatal("wire viewer retained source slice backing")
			}
			for at := range left.Len() {
				if unsafe.StringData(left.Index(at).String()) == unsafe.StringData(right.Index(at).String()) {
					t.Fatal("wire viewer retained slice string backing")
				}
			}
		}
	}
	repo.mu.Lock()
	repo.workspaces["workspace"] = domain.Bootstrap{Viewer: domain.User{ID: "replacement"}}
	repo.mu.Unlock()
	if got.OutOfOfficeUntil.Location() == when.Location() || got.OutOfOfficeUntil.Location().String() != "" || got.OutOfOfficeUntil == &when {
		t.Fatal("retired source location escaped into owned viewer")
	}
	if when != original || sw15ViewerWireUsed(repo) != charged {
		t.Fatal("source replacement changed source or revoked result allowance")
	}
	sw15ViewerWireAssert(t, got, viewer)
	read.Close()
	read.Close()
	if sw15ViewerWireUsed(repo) != 0 {
		t.Fatal("retired viewer charge leaked")
	}
}

func TestScaleP21SW15InstalledViewerWirePersistent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	when := time.Date(2026, 11, 1, 1, 30, 0, 123456789, time.FixedZone("source-viewer-zone", -4*3600))
	expected := sw15ViewerWireFull(&when)
	expected.ID = f.data.Viewer.ID
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "viewer"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Viewer = expected
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"warm", "reloaded"} {
		if stage == "reloaded" {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		for _, store := range []*SQLiteStore{f.SQLiteStore, probe} {
			source, found := store.WorkspaceMetadata(workspace)
			read, ok, err := store.BeginInstalledViewerRead(t.Context(), workspace)
			if err != nil || !ok || !found {
				t.Fatal(stage, err)
			}
			sw15ViewerWireAssert(t, read.Viewer(), source.Viewer)
			read.Close()
		}
	}
	if sw15ViewerWireUsed(f.SQLiteStore) != 0 || sw15ViewerWireUsed(probe) != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("persistent wire viewer leaked admission")
	}
}
