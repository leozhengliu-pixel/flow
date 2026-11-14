package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW31UserDecodeProbe struct {
	called bool
	user   domain.User
}

func (p *scaleP21SW31UserDecodeProbe) UnmarshalJSON(raw []byte) error {
	p.called = true
	return json.Unmarshal(raw, &p.user)
}

func scaleP21SW31NormalizerWork(t *testing.T) (*metadataLogicalNormalizer, context.Context, func()) {
	t.Helper()
	store := &SQLiteStore{metadataResidency: newMetadataResidency()}
	ctx, releaseParent, err := store.admitMetadataWork(t.Context(), 194<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, releaseOther, err := store.tryAdmitAdditionalMetadataWork(ctx, 62<<20)
	if err != nil {
		releaseParent()
		t.Fatal(err)
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		releaseOther()
		releaseParent()
		t.Fatal(err)
	}
	if _, err := stage.exec(ctx, 4096, `CREATE TABLE logical_values(kind TEXT NOT NULL,key TEXT NOT NULL,ordinal INTEGER NOT NULL,value BLOB,frame_offset INTEGER,frame_size INTEGER,frame_cost INTEGER,frame_unavailable INTEGER,frame_blocked INTEGER,PRIMARY KEY(kind,key))`); err != nil {
		_ = stage.Close()
		releaseOther()
		releaseParent()
		t.Fatal(err)
	}
	n := &metadataLogicalNormalizer{store: store, stage: stage, baseWork: 2 << 20}
	close := func() {
		if n.work != nil {
			n.work.Close()
			n.work = nil
		}
		_ = stage.Close()
		releaseOther()
		releaseParent()
		if store.metadataResidency.work.used != 0 {
			t.Error("normalizer dependency accounting leaked admission")
		}
	}
	t.Cleanup(close)
	return n, ctx, close
}

func TestScaleP21SW31NormalizerLargeDependencyUsesParentCredit(t *testing.T) {
	n, ctx, _ := scaleP21SW31NormalizerWork(t)
	const nameBytes = 8 << 20
	raw, err := json.Marshal(domain.User{ID: "sw31-large-dependency", Name: strings.Repeat("x", nameBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.put(ctx, "users", "large", 0, raw); err != nil {
		t.Fatal(err)
	}
	n.work, err = startMetadataLogicalNormalizeWork(ctx, n.store, n.baseWork, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	var user scaleP21SW31UserDecodeProbe
	found, release, err := n.getAdmitted(ctx, "users", "large", &user)
	if err != nil || !found || !user.called || len(user.user.Name) != nameBytes {
		t.Fatalf("legal large dependency lost covered parent credit: found=%t decoded=%t err=%v", found, user.called, err)
	}
	n.work.keep(release)
	if n.work.total <= 128<<20 || n.work.total > 194<<20 || n.work.extra != 0 ||
		n.store.metadataResidency.work.used != metadataWorkBytes {
		t.Fatalf("parent credit was duplicated or a covered dependency acquired extra work: total=%d extra=%d used=%d",
			n.work.total, n.work.extra, n.store.metadataResidency.work.used)
	}
	n.work.Close()
	n.work = nil
	if n.store.metadataResidency.work.used != metadataWorkBytes {
		t.Fatal("record cleanup released another operation's parent or blocker lease")
	}
}

func TestScaleP21SW31NormalizerActualLargeUserDependency(t *testing.T) {
	n, ctx, _ := scaleP21SW31NormalizerWork(t)
	large := domain.User{ID: "sw31-actual-large-user", Name: strings.Repeat("z", 8<<20)}
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "sw31-normalizer-workspace"},
		Viewer:    domain.User{ID: "sw31-viewer", Name: "Viewer"},
		Users:     []domain.User{{ID: "sw31-viewer", Name: "Viewer"}, large},
		Members:   []domain.WorkspaceMember{{User: domain.User{ID: large.ID, Name: "Old embedded user"}}},
	}
	expected := cloneBootstrap(data)
	normalizeStoredMetadata(&expected)
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	normalized, err := n.store.normalizeMetadataLogicalSource(ctx, source)
	if err != nil {
		t.Fatal("actual normalizer double-charged covered dependencies", err)
	}
	defer normalized.Release()
	matched := false
	if err := normalized.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != "members" {
			return nil
		}
		return node.Children(ctx, func(_ reflect.Value, member metadataLogicalNode) error {
			user, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{member}, "User")
			if err != nil {
				return err
			}
			name, err := metadataAggregateChild(ctx, user, "Name")
			if err != nil {
				return err
			}
			matched, err = metadataAggregateStringEqual(ctx, name, expected.Members[0].User.Name)
			return err
		})
	}); err != nil || !matched {
		t.Fatal("actual normalizer changed the old full-hydration user dependency", matched, err)
	}
	if n.store.metadataResidency.work.used != metadataWorkBytes {
		t.Fatal("actual normalizer leaked children or released unrelated parent work")
	}
}

func TestScaleP21SW31NormalizerTypedDependencyNativeOffset(t *testing.T) {
	n, ctx, _ := scaleP21SW31NormalizerWork(t)
	var data domain.Bootstrap
	if err := json.Unmarshal([]byte(`{
		"workspace":{"id":"sw31-offset-workspace"},
		"viewer":{"id":"sw31-viewer","name":"Viewer"},
		"users":[{"id":"sw31-offset-user","name":"Directory","outOfOfficeUntil":"2026-11-13T01:02:03+00:00","appScopes":[]}],
		"projects":[{"id":"sw31-offset-project","name":"Project","creator":{"id":"sw31-offset-user","name":"Old"}}]
	}`), &data); err != nil {
		t.Fatal(err)
	}
	original := data.Users[0].OutOfOfficeUntil
	if original == nil || original.Location() == time.UTC {
		t.Skip("current process local zone makes +00:00 decode into UTC")
	}
	expected := cloneBootstrap(data)
	normalizeStoredMetadata(&expected)
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	value := reflect.ValueOf(data)
	for i := 0; i < value.NumField(); i++ {
		name := strings.Split(value.Type().Field(i).Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if err := builder.auditJSONLocations(value.Field(i)); err != nil {
			t.Fatal(err)
		}
		if err := builder.Field(name, value.Field(i)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := builder.Finish(copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := n.store.normalizeMetadataLogicalSource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer normalized.Release()
	selected, err := normalized.Select(ctx, MetadataRecordScope{Fields: []string{"projects"}}, 2<<20)
	if err != nil || len(selected.Projects) != 1 || selected.Projects[0].Creator == nil {
		t.Fatal("typed dependency failed to produce project creator", err)
	}
	got, want := selected.Projects[0].Creator, expected.Projects[0].Creator
	if !reflect.DeepEqual(got, want) || got.OutOfOfficeUntil.Location() != original.Location() ||
		got.OutOfOfficeUntil.Location() == time.UTC || got.AppScopes == nil {
		t.Fatalf("typed dependency lost native location identity or empty slice: got=%#v want=%#v", got, want)
	}
	if n.store.metadataResidency.work.used != metadataWorkBytes {
		t.Fatal("typed dependency retained extra work after the record ended")
	}
}

func TestScaleP21SW31NormalizerDenseDependenciesRejectBeforeDecode(t *testing.T) {
	n, ctx, _ := scaleP21SW31NormalizerWork(t)
	raw, err := json.Marshal(domain.User{ID: "sw31-dense-dependency", Name: "directory", AppScopes: make([]string, 100000)})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.put(ctx, "users", "dense", 0, raw); err != nil {
		t.Fatal(err)
	}
	n.work, err = startMetadataLogicalNormalizeWork(ctx, n.store, n.baseWork, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]*scaleP21SW31UserDecodeProbe, 0, 16)
	previous := n.work.total
	rejected := false
	for range 32 {
		output := &scaleP21SW31UserDecodeProbe{}
		found, release, err := n.getAdmitted(ctx, "users", "dense", output)
		if err != nil {
			if !errors.Is(err, ErrMetadataWorkBudget) || output.called || found {
				t.Fatalf("dense dependency reached object decode before aggregate admission: decoded=%t found=%t err=%v", output.called, found, err)
			}
			rejected = true
			break
		}
		if !found || !output.called || len(output.user.AppScopes) != 100000 || n.work.total <= previous {
			t.Fatal("successful dense dependency did not remain charged alongside previous dependencies")
		}
		previous = n.work.total
		n.work.keep(release)
		outputs = append(outputs, output)
	}
	if !rejected || len(outputs) < 2 || n.work.extra != 0 {
		t.Fatalf("dense dependencies were not bounded by shared parent credit: retained=%d extra=%d", len(outputs), n.work.extra)
	}
	n.work.Close()
	n.work = nil
	if n.store.metadataResidency.work.used != metadataWorkBytes {
		t.Fatal("dependency cleanup released unrelated admission")
	}
}

func TestScaleP21SW31NormalizerDependencyMissingAndCanceled(t *testing.T) {
	n, ctx, _ := scaleP21SW31NormalizerWork(t)
	var output bool
	found, release, err := n.getAdmitted(ctx, "seen", "absent", &output)
	if err != nil || found || release == nil {
		t.Fatal("absent dependency did not return a safe no-op release", found, err)
	}
	release()
	if err := n.put(ctx, "seen", "exists", 0, []byte("true")); err != nil {
		t.Fatal(err)
	}
	found, release, err = n.getAdmitted(ctx, "seen", "exists", &output)
	if err != nil || !found || !output {
		t.Fatal("standalone bounded boolean dependency failed", found, err)
	}
	release()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	output = false
	if _, _, err := n.getAdmitted(canceled, "seen", "exists", &output); !errors.Is(err, context.Canceled) || output {
		t.Fatal("cancellation reached the decoder or changed its error", err)
	}
	if _, err := startMetadataLogicalNormalizeWork(ctx, n.store, 194<<20, 1); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("operation exceeded its only available parent credit", err)
	}
}
