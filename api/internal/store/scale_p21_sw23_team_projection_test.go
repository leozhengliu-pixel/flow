package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW23TeamProjected(t *testing.T, source *SQLiteStore, raw []byte) (*scaleP21SW23TeamProjectionRead, error) {
	t.Helper()
	return source.scaleP21SW23BeginTeamProjection(t.Context(), MetadataArrayRecord{
		Reader: bytes.NewReader(raw), EncodedBytes: int64(len(raw)),
	})
}

func scaleP21SW23TeamPolicyOnly(value domain.Team) domain.Team {
	return domain.Team{
		ID: value.ID, Key: value.Key, ExternalSource: value.ExternalSource, Private: value.Private,
		ArchivedAt: value.ArchivedAt, RetiredAt: value.RetiredAt,
	}
}

func scaleP21SW23TeamProjectionOracle(t *testing.T, raw []byte) {
	t.Helper()
	source := &SQLiteStore{metadataResidency: newMetadataResidency()}
	var old domain.Team
	oldErr := json.Unmarshal(raw, &old)
	read, err := scaleP21SW23TeamProjected(t, source, raw)
	if (oldErr != nil) != (err != nil) {
		t.Fatalf("typed error agreement old=%v new=%v raw-prefix=%q", oldErr, err, raw[:min(len(raw), 180)])
	}
	if err != nil {
		if read != nil || source.metadataResidency.work.used != 0 {
			t.Fatal("failed projection retained output or charge")
		}
		return
	}
	want, _ := json.Marshal(scaleP21SW23TeamPolicyOnly(old))
	got, _ := json.Marshal(read.Team)
	if !bytes.Equal(got, want) {
		t.Fatalf("typed selected fields differ old=%s new=%s", want, got)
	}
	if read.Team.Name != "" || read.Team.Color != "" || read.Team.Icon != "" || read.Team.ArchivedByID != "" ||
		read.Team.CreatedAt != nil || read.Team.UpdatedAt != nil {
		t.Fatal("projection retained unused Team fields")
	}
	if source.metadataResidency.work.used != scaleP21SW23TeamProjectionWork {
		t.Fatal("projection result lost its independent charge")
	}
	read.Close()
	read.Close()
	if !reflect.DeepEqual(read.Team, domain.Team{}) || source.metadataResidency.work.used != 0 {
		t.Fatal("projection close leaked owned state")
	}
}

func TestScaleP21SW23TeamProjectionTypedCorpus(t *testing.T) {
	corpus := []string{
		`null`, `{}`, `true`, `42`, `[]`, `"value"`,
		`{"id":"one","ID":null,"key":"first","\u212aey":"second","external\u017fource":"org"}`,
		`{"private":false,"PRIVATE":true,"Private":null}`,
		`{"private":true,"private":false}`,
		`{"name":null,"color":"red","icon":"x","archivedById":"unused"}`,
		`{"name":7}`, `{"name":false}`, `{"color":[]}`, `{"private":"true"}`, `{"private":1}`,
		`{"id":{},"id":"later"}`,
		`{"archivedAt":"2026-11-13T01:02:03+05:45","ARCHIVEDAT":null}`,
		`{"createdAt":"invalid","createdAt":null}`,
		`{"createdAt":7}`, `{"updatedAt":true}`,
		`{"createdAt":"2026-11-13T01:02:03Z","updatedAt":null}`,
		`{"retiredAt":"2026-11-13T01:02:03.\u0031Z"}`,
		"{\"id\":\"\xff\\ud800\",\"name\":\"\xfe\\udfff\"}",
		`{"unknown":{"name":7,"array":[{},null,true,3]},"private":true}`,
		`{"private":true} trailing`, `{"name":"bad` + string([]byte{1}) + `"}`,
	}
	for _, field := range scaleP21SW23TeamFields {
		for _, value := range []string{`null`, `false`, `true`, `0`, `1e0`, `"text"`, `[]`, `{}`} {
			corpus = append(corpus, fmt.Sprintf(`{%q:%s}`, field.name, value))
		}
	}
	for _, separator := range []string{".", ","} {
		for _, length := range []int{0, 1, 9, 10, 129, 10000} {
			for _, hour := range []string{"01", "1"} {
				value := "2026-11-13T" + hour + ":02:03" + separator + strings.Repeat("7", length) + "+05:45"
				encoded, _ := json.Marshal(value)
				corpus = append(corpus, `{"archivedAt":`+string(encoded)+`,"createdAt":`+string(encoded)+`}`)
			}
		}
	}
	for _, raw := range corpus {
		scaleP21SW23TeamProjectionOracle(t, []byte(raw))
	}
	for _, suffix := range []string{"Z", "bad"} {
		raw := []byte(`{"archivedAt": ` + strings.Repeat(" ", 512) + `"2026-11-13T01:02:03.` + strings.Repeat("7", 3<<20) + suffix + `"` + strings.Repeat("\t", 512) + `}`)
		scaleP21SW23TeamProjectionOracle(t, raw)
	}
}

func TestScaleP21SW23TeamProjectionTypedProperties(t *testing.T) {
	random := rand.New(rand.NewSource(230012))
	keys := []string{"id", "ID", "Key", "\u212aey", "name", "PRIVATE", "private", "externalSource", "external\u017fource", "createdAt", "ArchivedAt", "retiredAt", "unused"}
	values := []string{`null`, `true`, `false`, `0`, `1e0`, `""`, `"value"`, `"\ud800"`, `"2026-11-13T01:02:03.123456789Z"`, `"2026-11-13T1:02:03,123456789123+24:60"`, `[]`, `{}`, `{"nested":[1,2]}`}
	for range 1000 {
		var raw strings.Builder
		raw.WriteByte('{')
		count := random.Intn(10)
		for index := range count {
			if index != 0 {
				raw.WriteByte(',')
			}
			key, _ := json.Marshal(keys[random.Intn(len(keys))])
			raw.Write(key)
			raw.WriteByte(':')
			raw.WriteString(values[random.Intn(len(values))])
		}
		raw.WriteByte('}')
		scaleP21SW23TeamProjectionOracle(t, []byte(raw.String()))
	}
}

func TestScaleP21SW23TeamProjectionLargeIgnoredAndAdmission(t *testing.T) {
	source := &SQLiteStore{metadataResidency: newMetadataResidency()}
	var previous uint64
	for _, size := range []int{64 << 10, 3 << 20} {
		ignored := strings.Repeat("x", size)
		raw := []byte(`{"id":"selected","Name":"` + ignored + `","description":"` + ignored + `","Color":"` + ignored + `","private":true}`)
		var legacy domain.Team
		if err := json.Unmarshal(raw, &legacy); err != nil || len(legacy.Name) != size {
			t.Fatal("large typed oracle", err)
		}
		prime, err := scaleP21SW23TeamProjected(t, source, raw)
		if err != nil {
			t.Fatal(err)
		}
		prime.Close()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 3 {
			read, err := scaleP21SW23TeamProjected(t, source, raw)
			if err != nil || read.Team.ID != "selected" || !read.Team.Private || read.Team.Name != "" {
				t.Fatal("large ignored projection", err)
			}
			read.Close()
		}
		runtime.ReadMemStats(&after)
		perRead := (after.TotalAlloc - before.TotalAlloc) / 3
		if previous != 0 && perRead > previous+(64<<10) {
			t.Fatalf("unused string bytes scaled retained/temporary allocations: %d/%d", previous, perRead)
		}
		previous = perRead
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range 3 {
			var legacy domain.Team
			if err := json.Unmarshal(raw, &legacy); err != nil {
				t.Fatal(err)
			}
			runtime.KeepAlive(legacy)
		}
		runtime.ReadMemStats(&after)
		legacyPerRead := (after.TotalAlloc - before.TotalAlloc) / 3
		t.Logf("ignored field bytes=%d legacy allocated bytes/read=%d prototype allocated bytes/read=%d", size, legacyPerRead, perRead)
	}
	parent, releaseParent, err := source.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"id":"held"}`)
	read, err := source.scaleP21SW23BeginTeamProjection(parent, MetadataArrayRecord{Reader: bytes.NewReader(raw), EncodedBytes: int64(len(raw))})
	if err != nil {
		t.Fatal(err)
	}
	releaseParent()
	if source.metadataResidency.work.used != scaleP21SW23TeamProjectionWork || read.Team.ID != "held" {
		t.Fatal("parent release freed live projection charge")
	}
	read.Close()
	large := []byte(`{"id":"` + strings.Repeat("x", scaleP21SW23TeamProjectionStrings+1) + `"}`)
	if read, err := scaleP21SW23TeamProjected(t, source, large); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || source.metadataResidency.work.used != 0 {
		t.Fatal("selected string budget did not reject atomically", err)
	}
	_, releaseFull, err := source.admitMetadataWork(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if read, err := scaleP21SW23TeamProjected(t, source, raw); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("projection admitted while process allowance full", err)
	}
	releaseFull()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := source.scaleP21SW23BeginTeamProjection(cancelled, MetadataArrayRecord{Reader: bytes.NewReader(raw), EncodedBytes: int64(len(raw))}); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled projection", err)
	}
}

func TestScaleP21SW23TeamProjectionCanonicalReader(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	for _, body := range []string{
		`{"id":"sw23-stream-team","Private":true,"name":"` + strings.Repeat("unused", 400000) + `"}`,
		`{"id":"sw23-stream-team","private":false,"PRIVATE":true,"description":"` + strings.Repeat("unused", 400000) + `"}`,
		`{"id":"sw23-stream-team","private":true,"createdAt":"invalid"}`,
	} {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		key := metadataRecordKey{"teams", "sw23-stream-team"}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, key, metadataRecord{data: []byte(body), order: 999}, nil); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
		legacy, oldErr := f.resourcePolicyRecords(ctx, workspace, f.data.Workspace.ID)
		snapshot := &metadataCollectionSnapshot{store: view, tx: tx, workspace: workspace}
		reader := &metadataSnapshotReaderAt{ctx: ctx, snapshot: snapshot, key: RecordKey{Kind: "teams", ID: key.key}, size: int64(len(body))}
		read, err := view.scaleP21SW23BeginTeamProjection(ctx, MetadataArrayRecord{Reader: io.NewSectionReader(reader, 0, int64(len(body))), EncodedBytes: int64(len(body))})
		if (oldErr != nil) != (err != nil) {
			tx.Rollback()
			release()
			t.Fatal("canonical typed error mismatch", oldErr, err)
		}
		if err == nil {
			var old domain.Team
			for _, team := range legacy.Teams {
				if team.ID == "sw23-stream-team" {
					old = team
				}
			}
			if !reflect.DeepEqual(scaleP21SW23TeamPolicyOnly(old), read.Team) ||
				teamVisibleToUser(domain.Bootstrap{Teams: []domain.Team{read.Team}}, old.ID, "outside", "member") {
				t.Fatal("canonical stream projection lost historical private boundary")
			}
			read.Close()
		}
		tx.Rollback()
		release()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("canonical stream projection leaked admission")
		}
	}
}

func TestScaleP21SW23TeamProjectionOrdinaryTyped(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	id := idgen.New("sw23-typed-projection-")
	when := time.Date(2026, 11, 13, 1, 2, 3, 123456789, time.FixedZone("source-offset", 5*3600+45*60))
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw23.typed_projection", id, nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{
			ID: id, Key: "\u212a\\\"<", Name: strings.Repeat("ignored", 400000), Color: "red", Icon: "circle",
			Private: true, ExternalSource: "source", ArchivedAt: &when, RetiredAt: &when,
			ArchivedByID: "actor", CreatedAt: &when, UpdatedAt: &when,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, workspace, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	scaleP21SW23TeamProjectionOracle(t, raw)
}
