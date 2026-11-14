package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW21ScrubRow struct {
	target int
	column string
	keys   []string
	raw    []byte
	about  bool
}

type scaleP21SW21ScrubFixture struct {
	scaleP09Fixture
	person string
	rows   []scaleP21SW21ScrubRow
}

func scaleP21SW21ScrubSeed(t *testing.T) scaleP21SW21ScrubFixture {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	result := scaleP21SW21ScrubFixture{scaleP09Fixture: f, person: idgen.New("sw21-%_!-person-")}
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano)
	quotedPerson, _ := json.Marshal(result.person)
	body := func(id, text string) []byte {
		t.Helper()
		value, err := json.Marshal(map[string]any{
			"id": id, "creator": domain.User{ID: result.person, Name: "private", DisplayName: "Private <&>", Email: "private@example.test", AvatarURL: "/private.png"},
			"mention":  map[string]any{"id": result.person, "label": "Private"},
			"audit":    map[string]any{"userId": result.person, "userName": "Private", "userEmail": "private@example.test"},
			"sw21Body": text,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Stored generic JSON may contain literal HTML characters. The old
		// rewrite canonicalizes their escaping with encoding/json.
		value = bytes.ReplaceAll(value, []byte(`\u003c`), []byte("<"))
		value = bytes.ReplaceAll(value, []byte(`\u003e`), []byte(">"))
		value = bytes.ReplaceAll(value, []byte(`\u0026`), []byte("&"))
		value = bytes.ReplaceAll(value, []byte(`\u2028`), []byte("\u2028"))
		return value
	}
	for i := range 67 {
		key := fmt.Sprintf("sw21-scrub-%03d-%s", i, idgen.New(""))
		raw := body(key, strings.Repeat("retained text ", 512))
		if i == 0 {
			raw = []byte(`{"creator":{"id":` + string(quotedPerson) + `,"name":"private","displayName":"Private","email":"private@example.test"},"number":123456789012345678901234567890,"duplicate":"first","duplicate":"last","escaped":"\\u0000"}`)
		}
		if i == 66 {
			raw = body(key, strings.Repeat("retained <&> \u2028 ", 250000))
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'settings',?,?,?)`, workspace, key, i, raw); err != nil {
			t.Fatal(err)
		}
		result.rows = append(result.rows, scaleP21SW21ScrubRow{target: 2, column: "data", keys: []string{"settings", key}, raw: raw})
	}
	unchanged := []byte(`{ "z":1,"literal":` + string(quotedPerson) + `, "a":2 }`)
	key := idgen.New("sw21-scrub-unchanged-")
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'settings',?,100,?)`, workspace, key, unchanged); err != nil {
		t.Fatal(err)
	}
	result.rows = append(result.rows, scaleP21SW21ScrubRow{target: 2, column: "data", keys: []string{"settings", key}, raw: unchanged})
	for _, kind := range []string{"comment", "notification"} {
		id, resource := idgen.New("sw21-scrub-content-"), idgen.New("sw21-scrub-resource-")
		raw := body(id, "retained content")
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`, workspace, kind, resource, id, stamp, raw); err != nil {
			t.Fatal(err)
		}
		result.rows = append(result.rows, scaleP21SW21ScrubRow{target: 1, column: "data", keys: []string{kind, resource, id}, raw: raw})
	}
	issueID := idgen.New("sw21-scrub-issue-")
	issueRaw := body(issueID, "retained issue")
	issueArgs := []any{workspace, issueID, issueID, f.data.Teams[0].ID, "state_backlog", "backlog", "", "", f.data.Viewer.ID, "", "", 0, 0, "Scrub fixture", stamp, stamp, 0, 1, issueRaw, issueRaw}
	if _, err := f.db.ExecContext(t.Context(), issueRecordInsert+issueRecordValuesSQL, issueArgs...); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"data", "list_data"} {
		result.rows = append(result.rows, scaleP21SW21ScrubRow{target: 0, column: column, keys: []string{issueID}, raw: issueRaw})
	}
	for _, about := range []bool{false, true} {
		id, aggregate := idgen.New("sw21-scrub-event-"), idgen.New("sw21-other-")
		payload := body(id, strings.Repeat("保留 café ", metadataRecordChunkBytes/4))
		if about {
			aggregate = result.person
			payload = []byte(`{"displayName":"Private","username":"private","phoneNumber":"123","name":"Private","number":12345678901234567890}`)
		}
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = insertDomainEvent(t.Context(), tx, domainEventRow{ID: id, Type: "scale_p21_sw21.fixture", AggregateID: aggregate, Payload: payload, PreviousValues: payload, CreatedAt: stamp, WorkspaceID: workspaceID, WorkspaceKey: workspace})
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		for _, column := range []string{"payload", "previous_values"} {
			result.rows = append(result.rows, scaleP21SW21ScrubRow{target: 4, column: column, keys: []string{id}, raw: payload, about: about})
		}
	}
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, original, workspace); err != nil {
			t.Error(err)
		}
	})
	var root map[string]json.RawMessage
	if err := json.Unmarshal(original, &root); err != nil {
		t.Fatal(err)
	}
	root["viewer"], _ = json.Marshal(domain.User{ID: result.person, Name: "private", DisplayName: "Private", Email: "private@example.test", Active: true})
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
		t.Fatal(err)
	}
	result.rows = append(result.rows, scaleP21SW21ScrubRow{target: 3, column: "data", raw: raw})
	return result
}

func (f scaleP21SW21ScrubFixture) restore(t *testing.T) {
	t.Helper()
	for _, row := range f.rows {
		target := personScrubTargets[row.target]
		scope := f.data.Workspace.URLKey
		if target.byID {
			scope = f.data.Workspace.ID
		}
		value := any(row.raw)
		if personTextColumns[row.column] {
			value = string(row.raw)
		}
		args := []any{value, scope}
		for _, key := range row.keys {
			args = append(args, key)
		}
		if _, err := f.db.ExecContext(t.Context(), target.updates[row.column], args...); err != nil {
			t.Fatal(err)
		}
	}
}

func (f scaleP21SW21ScrubFixture) verify(t *testing.T, rewritten bool) int {
	t.Helper()
	changes := 0
	anonymous := domain.DeletedUserIdentity(f.person)
	for _, row := range f.rows {
		target := personScrubTargets[row.target]
		scope := f.data.Workspace.URLKey
		if target.byID {
			scope = f.data.Workspace.ID
		}
		query := `SELECT ` + row.column + ` FROM ` + target.table + ` WHERE ` + target.scope + `=?`
		args := []any{scope}
		for i, key := range target.keys {
			query += ` AND ` + key + `=?`
			args = append(args, row.keys[i])
		}
		var actual []byte
		if err := f.db.QueryRowContext(t.Context(), query, args...).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		expected, changed := ScrubPersonJSON(row.raw, anonymous, row.about)
		if !changed || !rewritten {
			expected = row.raw
		}
		if changed {
			changes++
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("scrub SQL bytes differ %s.%s keys=%v changed=%v actual=%x expected=%x", target.table, row.column, row.keys, changed, sha256.Sum256(actual), sha256.Sum256(expected))
		}
	}
	return changes
}

func TestScaleP21SW21PersonalScrubBaseline(t *testing.T) {
	f := scaleP21SW21ScrubSeed(t)
	f.verify(t, false)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	count, err := f.scrubPersonCopies(t.Context(), f.data.Workspace.URLKey, f.data.Workspace.ID, domain.DeletedUserIdentity(f.person))
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	want := f.verify(t, true)
	if count != want {
		t.Fatalf("legacy scrub count=%d expected=%d", count, want)
	}
	t.Logf("legacy scrub values=%d bytes/op=%d allocs/op=%d elapsed=%s", count, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, elapsed)
}

func TestScaleP21SW21PersonalScrubDifferential(t *testing.T) {
	f := scaleP21SW21ScrubSeed(t)
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	foreign, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW21 untouched", idgen.New("sw21-scrub-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'settings',?,0,?)`, foreign.Workspace.URLKey, idgen.New("sw21-foreign-copy-"), f.rows[0].raw); err != nil {
		t.Fatal(err)
	}
	foreignBefore := scaleP21SW21MetadataHash(t, f.SQLiteStore, foreign.Workspace.URLKey)
	events := 0
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		source.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
		source.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
		source.SetAccountAccessSink(func(string) { events++ })
	}
	// Scrubbing is a trusted storage operation. Credentials, team roles and
	// actor identity do not narrow or expand its explicit workspace scope.
	for _, actor := range []domain.User{
		f.data.Viewer, {ID: f.person},
		{ID: idgen.New("sw21-scrub-outsider-")},
		{ID: foreign.Viewer.ID},
	} {
		for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
			f.restore(t)
			ctx, cancel := context.WithTimeout(ContextWithActor(t.Context(), actor), 30*time.Second)
			count, err := source.ScrubPersonCopies(ctx, workspace, f.person)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if want := f.verify(t, true); count != want {
				t.Fatalf("scrub count=%d expected=%d", count, want)
			}
			again, err := source.ScrubPersonCopies(t.Context(), workspace, f.person)
			if err != nil || again != 0 {
				t.Fatalf("second scrub count=%d error=%v", again, err)
			}
			f.verify(t, true)
		}
	}
	if _, err := probe.ScrubPersonCopies(t.Context(), idgen.New("sw21-missing-"), f.person); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("missing workspace=%v", err)
	}
	if foreignBefore != scaleP21SW21MetadataHash(t, f.SQLiteStore, foreign.Workspace.URLKey) ||
		events != 0 || probe.WorkspaceMetadataDecodes() != 0 ||
		probe.metadataResidency.work.used != 0 || probe.metadataResidency.work.waiting != 0 {
		t.Fatal("scrub changed foreign rows, published, decoded full metadata or leaked work")
	}
}

func TestScaleP21SW21PersonalScrubAtomicAdmission(t *testing.T) {
	f := scaleP21SW21ScrubSeed(t)
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	workspace := f.data.Workspace.URLKey
	anonymous := domain.DeletedUserIdentity(f.person)
	for _, failure := range []string{"cancelled", "transaction-budget", "child-admission"} {
		t.Run(failure, func(t *testing.T) {
			f.restore(t)
			ctx := t.Context()
			want := error(context.Canceled)
			switch failure {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "transaction-budget":
				limit := f.db.maxTransactionBytes
				f.db.maxTransactionBytes = 200 << 10
				defer func() { f.db.maxTransactionBytes = limit }()
				want = ErrTransactionWriteBudget
			case "child-admission":
				limit := probe.metadataResidency.work.limit
				probe.metadataResidency.work.limit = 4 << 20
				defer func() { probe.metadataResidency.work.limit = limit }()
				want = ErrMetadataWorkBudget
			}
			count, err := probe.ScrubPersonCopies(ctx, workspace, f.person)
			if !errors.Is(err, want) || count != 0 {
				t.Fatalf("failed scrub count=%d error=%v expected=%v", count, err, want)
			}
			f.verify(t, false)
			if probe.metadataResidency.work.used != 0 || probe.metadataResidency.work.waiting != 0 {
				t.Fatal("failed scrub leaked admission")
			}
		})
	}
	f.restore(t)
	t.Setenv("TMPDIR", "/nonexistent/sw21-scrub")
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
	beforeSpool := recordSpoolBytes.Load()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	count, err := probe.scrubPersonCopiesBounded(t.Context(), workspace, f.data.Workspace.ID, anonymous)
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	if err != nil || count != f.verify(t, true) {
		t.Fatalf("pool-one no-spool scrub count=%d error=%v", count, err)
	}
	if recordSpoolBytes.Load() != beforeSpool || probe.metadataResidency.work.used != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("bounded scrub used spool, leaked admission or decoded metadata")
	}
	t.Logf("bounded scrub values=%d bytes/op=%d allocs/op=%d elapsed=%s", count, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, elapsed)
	f.restore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	count, err = probe.ScrubPersonCopies(ctx, workspace, f.person)
	if err != nil || count != f.verify(t, true) || probe.metadataResidency.work.used != 0 {
		t.Fatalf("pool-one no-spool wrapper/reload count=%d error=%v", count, err)
	}
}

type scaleP21SW21ScrubFailedReader struct{ err error }

func (r scaleP21SW21ScrubFailedReader) ReadAt([]byte, int64) (int, error) {
	return 0, &scaleP21SW21ScrubReadError{r.err}
}

func TestScaleP21SW21PersonalScrubCodec(t *testing.T) {
	anonymous := domain.DeletedUserIdentity("sw21-person")
	for _, raw := range [][]byte{
		[]byte(`{"id":"sw21-person","name":"Private","displayName":"Private","n":123456789012345678901234567890,"x":"first","x":"last"}`),
		[]byte(`{"id":"sw21-person","email":"private","nul":"\u0000","literal":"\\u0000"} trailing junk`),
		[]byte(`{"id":"sw21-person","email":"private","x":"<&>  "}`),
		append([]byte(`{"id":"sw21-person","email":"private","x":"`), 0xff, '"', '}'),
		[]byte(`{"id":"sw21-person","email":"private","x":"\ud800"}`),
		[]byte(`[{"userId":"sw21-person","userName":"Private","label":"Private"}] {}`),
		[]byte(` { "literal":"sw21-person","keep":"bytes" } trailing`),
		[]byte(`{"name":"Private","phoneNumber":"123"}`),
		[]byte(`null`), []byte(`"sw21-person"`), []byte(`123e999999`),
		[]byte(`{"x":`), []byte(`{"x":"bad\q"}`), []byte(`[]`), nil,
	} {
		for _, about := range []bool{false, true} {
			expected, changed := ScrubPersonJSON(raw, anonymous, about)
			part, cost, valid, err := scaleP21SW21ScrubPreflight(t.Context(), bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			actual, actualChanged := raw, false
			if valid {
				actual, actualChanged = ScrubPersonJSON(raw[part.offset:part.offset+part.size], anonymous, about)
				if !actualChanged {
					actual = raw
				}
			}
			if changed != actualChanged || !bytes.Equal(expected, actual) {
				t.Fatalf("codec mismatch input=%x about=%v", sha256.Sum256(raw), about)
			}
			if changed && int64(cost) < 4*part.size+6*int64(len(expected)) {
				t.Fatalf("codec output exceeded reserved work: cost=%d raw=%d output=%d", cost, part.size, len(expected))
			}
		}
	}
	large := []byte(`{"id":"sw21-person","email":"private","x":"` + strings.Repeat("<", 8<<20) + `"}`)
	if _, _, _, err := scaleP21SW21ScrubPreflight(t.Context(), bytes.NewReader(large), int64(len(large))); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("oversized escaped output was not refused: %v", err)
	}
	structural := []byte("[" + strings.Repeat("{},", 260000) + "{}]")
	if _, _, _, err := scaleP21SW21ScrubPreflight(t.Context(), bytes.NewReader(structural), int64(len(structural))); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("small encoded input with excessive containers was not refused: %v", err)
	}
	if _, _, _, err := scaleP21SW21ScrubPreflight(t.Context(), scaleP21SW21ScrubFailedReader{io.ErrUnexpectedEOF}, 100); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("SQL read failure was treated as unchanged JSON: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := scaleP21SW21ScrubPreflight(ctx, bytes.NewReader([]byte(`{}`)), 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preflight=%v", err)
	}
}

func TestScaleP21SW21PersonalScrubNestedLease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, person, key := f.data.Workspace.URLKey, idgen.New("sw21-nested-person-"), idgen.New("sw21-nested-record-")
	quoted, _ := json.Marshal(person)
	raw := []byte(`{"id":` + string(quoted) + `,"displayName":"Private","name":"Private","email":"private@example.test"}`)
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'settings',?,0,?)`, workspace, key, raw); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		source.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
		source.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
		for _, allowance := range []int{scaleP21SW21ScrubFixedWork, metadataPolicyBytes + scaleP21SW21ScrubFixedWork} {
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='settings' AND record_key=?`, raw, workspace, key); err != nil {
				t.Fatal(err)
			}
			ctx, release, err := probe.admitMetadataWork(t.Context(), allowance)
			if err != nil {
				t.Fatal(err)
			}
			parent := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
			statements := 0
			ctx = WithSQLStatementObserver(ctx, func(string) { statements++ })
			count, scrubErr := source.ScrubPersonCopies(ctx, workspace, person)
			var actual []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key=?`, workspace, key).Scan(&actual); err != nil {
				release()
				t.Fatal(err)
			}
			rejected := source.BoundedMetadataEnabled() && allowance == scaleP21SW21ScrubFixedWork
			expected, _ := ScrubPersonJSON(raw, domain.DeletedUserIdentity(person), false)
			if rejected {
				expected = raw
				if count != 0 || !errors.Is(scrubErr, ErrMetadataWorkBudget) || statements != 0 {
					release()
					t.Fatalf("small parent mutated before rejection: count=%d error=%v statements=%d", count, scrubErr, statements)
				}
			} else if scrubErr != nil || count != 1 {
				release()
				t.Fatalf("bounded=%v allowance=%d count=%d error=%v", source.BoundedMetadataEnabled(), allowance, count, scrubErr)
			}
			probe.metadataResidency.work.mu.Lock()
			live, used, waiting := parent.live, probe.metadataResidency.work.used, probe.metadataResidency.work.waiting
			probe.metadataResidency.work.mu.Unlock()
			release()
			if !bytes.Equal(actual, expected) || !live || used != allowance || waiting != 0 ||
				probe.metadataResidency.work.used != 0 || events != 0 || probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("nested scrub changed rows incorrectly, released caller admission, leaked child work, published or decoded metadata")
			}
		}
	}
}
