package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW0CollectionLexerMatchesJSONValidity(t *testing.T) {
	fixtures := []string{
		`null`, `true`, `false`, `0`, `-0`, `1.0e-9`, `[]`, `{}`,
		`{"":{"a":[null,false,-1,1E+9,"\ud800","\uffff"]}}`,
		`{"\u0075sers":[{"id":"x","unknown":"a\nb"}]}`,
		`[1,]`, `{"a":1,}`, `{"a":01}`, `{"a":1.}`, `{"a":1e}`,
		`{"a":"\x"}`, "{\"a\":\"\x01\"}", `{"a":`, `null null`, `[+1]`,
		`{"x":"` + strings.Repeat(`\u0061`, 100000) + `"}`,
		`{"x":` + strings.Repeat("7", 100000) + `}`,
		`{"x":"` + string([]byte{0xff, 0xfe}) + `"}`,
		strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000),
		strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001),
	}
	for i, raw := range fixtures {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			scanner := metadataCollectionScanner{ctx: t.Context(), input: bufio.NewReaderSize(strings.NewReader(raw), 31)}
			_, err := scanner.value(0)
			if err == nil {
				err = scanner.end()
			}
			if (err == nil) != json.Valid([]byte(raw)) {
				t.Fatalf("valid=%v parser=%v fixture=%d", json.Valid([]byte(raw)), err, i)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReader(strings.NewReader(`{"x":1}`))}
	if _, err := scanner.value(0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestScaleP21SW0CollectionInlineArrayDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, saved, workspace); err != nil {
			t.Error(err)
		}
	}()
	// Inactive manifest means these stale canonical rows must be ignored.
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users','sw15-stale',999,?)`, workspace, []byte(`{"id":"stale"}`)); err != nil {
		t.Fatal(err)
	}
	fixtures := []string{
		`{}`,
		`{"users":null}`,
		`{"users":[]}`,
		`{"Users":[{"ID":"one","name":"first"},{"id":"","name":"empty"},{"id":"one","name":"duplicate"},null]}`,
		`{"users":[{"id":"first"}],"\u0055sers":[{"id":"last","name":"old","Name":"new","name":null}]}`,
		`{"users":[{"id":"x","name":"retained"}],"Users":[{"id":"y"}]}`,
		`{"users":[{"id":"x","name":"retained"},{"id":"second","name":"retained-second"}],"Users":[null],"USERS":[{"id":"y"},{"id":"changed-second"}]}`,
		`{"users":[{"id":"x","name":"discarded"}],"Users":[],"users":[{"id":"fresh"}]}`,
		`{"users":[{"id":"x","name":"discarded"}],"Users":null,"users":[{"id":"fresh"}]}`,
		`{"users":[{"id":"first"}],"USERS":null}`,
		`{"users":[{"id":"` + strings.Repeat("x", 250) + `"}]}`,
		`{"users":[{"id":"unicode","name":"` + string([]rune{0x540d, 0x5b57}) + `"}]}`,
		`{"_flowCollections":{"users":"array"},"users":[{"id":"inline"}],"_FLOWCOLLECTIONS":null}`,
		`{"unknown":{"giant":"` + strings.Repeat("ignored ", 400000) + `"},"users":[{"id":"last"}]}`,
	}
	for i, raw := range fixtures {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var stored any = []byte(raw)
			if strings.Contains(raw, `"id":"unicode"`) {
				stored = raw // SQLite TEXT needs byte length, not character length.
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, stored, workspace); err != nil {
				t.Fatal(err)
			}
			var expected domain.Bootstrap
			if err := json.Unmarshal([]byte(raw), &expected); err != nil {
				t.Fatal(err)
			}
			beforeSpool, beforeDecode := recordSpoolBytes.Load(), f.metadataDecodes.Load()
			read, err := f.BeginMetadataArrayRead(t.Context(), workspace, "users")
			if err != nil {
				t.Fatal(err)
			}
			if !read.Shape.WorkspaceFound || read.Shape.Nil != (expected.Users == nil) {
				t.Fatalf("shape=%+v expected nil=%v", read.Shape, expected.Users == nil)
			}
			for range 2 {
				var actual []domain.User
				if !read.Shape.Nil {
					actual = []domain.User{}
				}
				callback := func(ctx context.Context, record MetadataArrayRecord) error {
					if record.Ordinal != int64(len(actual)) || record.Key.ID != "" {
						t.Fatalf("inline ordinal/key=%+v", record)
					}
					raw, err := io.ReadAll(record.Reader)
					if err != nil || int64(len(raw)) != record.EncodedBytes {
						return errors.Join(err, errors.New("encoded byte length mismatch"))
					}
					var user domain.User
					if err := json.Unmarshal(raw, &user); err != nil {
						return err
					}
					actual = append(actual, user)
					return nil
				}
				if read.Shape.RequiresTypedMerge {
					if err := read.Visit(callback); !errors.Is(err, ErrMetadataArrayMerge) {
						t.Fatalf("ordinary visit accepted typed-merge input: %v", err)
					}
					actual, err = scaleP21SW15MergeUserOccurrences(read)
				} else {
					err = read.Visit(callback)
				}
				if err != nil || !reflect.DeepEqual(actual, expected.Users) {
					t.Fatalf("actual=%#v expected=%#v error=%v", actual, expected.Users, err)
				}
			}
			if err := read.Close(); err != nil {
				t.Fatal(err)
			}
			if recordSpoolBytes.Load() != beforeSpool || f.metadataDecodes.Load() != beforeDecode {
				t.Fatal("reader leaked spool bytes or decoded full metadata")
			}
		})
	}
}

func scaleP21SW15MergeUserOccurrences(read *MetadataArrayRead) ([]domain.User, error) {
	var users []domain.User
	err := read.VisitOccurrences(func(_ context.Context, event MetadataArrayOccurrence) error {
		if event.Begin {
			return nil
		}
		if event.Null {
			users = nil
			return nil
		}
		if event.End {
			users = users[:int(event.Ordinal)]
			if event.Ordinal == 0 {
				users = []domain.User{}
			}
			return nil
		}
		index := int(event.Ordinal)
		if event.Append {
			users = append(users, domain.User{})
		} else {
			value := reflect.ValueOf(&users).Elem()
			if index >= value.Cap() {
				value.Grow(1)
			}
			if index >= value.Len() {
				value.SetLen(index + 1)
			}
		}
		return json.NewDecoder(event.Reader).Decode(&users[index])
	})
	return users, err
}

func TestScaleP21SW0CollectionSplitSnapshotAndMapOccurrences(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, saved, workspace); err != nil {
			t.Error(err)
		}
	}()
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('users','teamSettings')`, workspace); err != nil {
		t.Fatal(err)
	}
	rawRoot := `{"workspace":{"id":"first","name":"retained"},"WORKSPACE":null,"\u0077orkspace":{"id":"last"},"users":[{"id":"inline"}],"teamSettings":{"discarded":{"teamId":"discarded"}},"TeamSettings":null,"TEAMSETTINGS":{"":{"teamId":"empty"},"same":{"teamId":"old"},"same":{"teamId":"new"}},"_flowCollections":{"users":"array","teamSettings":"map"}}`
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(rawRoot), workspace); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		kind, key, raw string
		order          int64
	}{
		{"users", "key-two", `{"id":"duplicate","name":"second"}`, 40},
		{"users", "key-one", `{"id":"duplicate","name":"first"}`, 10},
		{"teamSettings", "same", `{"teamId":"canonical"}`, 1},
	} {
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?)`, workspace, row.kind, row.key, row.order, []byte(row.raw)); err != nil {
			t.Fatal(err)
		}
	}
	f.dropMetadataCache(t.Context(), workspace)
	expected, err := f.decodeWorkspaceMetadata(t.Context(), workspace, []byte(rawRoot), f.db)
	if err != nil {
		t.Fatal(err)
	}
	before := recordSpoolBytes.Load()
	read, err := f.BeginMetadataCollectionRead(t.Context(), workspace, "users", "teamSettings")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	// This succeeds with one connection only when Begin released its SQL
	// snapshot. The existing detached bytes must still show the old value.
	f.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*3)
	defer cancel()
	if _, err := f.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='users' AND record_key='key-one'`, []byte(`{"id":"later"}`), workspace); err != nil {
		t.Fatal(err)
	}
	var actualWorkspace domain.Workspace
	foundCore, err := read.VisitRootFieldOccurrences("workspace", func(_ context.Context, size int64, input io.Reader) error {
		if size == 0 {
			t.Fatal("core occurrence omitted its encoded byte length")
		}
		return json.NewDecoder(input).Decode(&actualWorkspace)
	})
	if err != nil || !foundCore || !reflect.DeepEqual(actualWorkspace, expected.Workspace) {
		t.Fatalf("same-snapshot core actual=%+v expected=%+v found=%v error=%v", actualWorkspace, expected.Workspace, foundCore, err)
	}
	var names []string
	var expectedNames []string
	for _, user := range expected.Users {
		expectedNames = append(expectedNames, user.Name)
	}
	if err := read.VisitArray("users", func(ctx context.Context, record MetadataArrayRecord) error {
		var user domain.User
		if err := json.NewDecoder(record.Reader).Decode(&user); err != nil {
			return err
		}
		names = append(names, user.Name)
		// A database query from a callback cannot deadlock pool-one.
		var exists int
		return f.db.QueryRowContext(ctx, `SELECT 1`).Scan(&exists)
	}); err != nil || !reflect.DeepEqual(names, expectedNames) {
		t.Fatalf("snapshot names=%v expected=%v error=%v", names, expectedNames, err)
	}
	var actual map[string]domain.TeamSettings
	if err := read.VisitMap("teamSettings", func(_ context.Context, event MetadataMapOccurrence) error {
		if event.Reset {
			actual = nil
			return nil
		}
		if event.BeginObject {
			if actual == nil {
				actual = map[string]domain.TeamSettings{}
			}
			return nil
		}
		key := event.Key
		if !event.CanonicalKey {
			if err := json.NewDecoder(event.RawKey).Decode(&key); err != nil {
				return err
			}
		}
		var value domain.TeamSettings
		if err := json.NewDecoder(event.Reader).Decode(&value); err != nil {
			return err
		}
		actual[key] = value
		return nil
	}); err != nil || !reflect.DeepEqual(actual, expected.TeamSettings) {
		t.Fatalf("map occurrences=%#v expected=%#v error=%v", actual, expected.TeamSettings, err)
	}
	calls := 0
	if err := read.VisitArray("users", func(context.Context, MetadataArrayRecord) error {
		calls++
		return ErrMetadataIterationDone
	}); err != nil || calls != 1 {
		t.Fatalf("stop calls=%d error=%v", calls, err)
	}
	if err := read.Close(); err != nil || recordSpoolBytes.Load() != before {
		t.Fatalf("close=%v spool before=%d after=%d", err, before, recordSpoolBytes.Load())
	}
}

func TestScaleP21SW0CollectionSpoolFailureMissingAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	before := recordSpoolBytes.Load()
	missing, err := f.BeginMetadataArrayRead(t.Context(), f.data.Workspace.URLKey+"-missing", "users")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Shape.WorkspaceFound {
		t.Fatal("missing workspace marked present")
	}
	if err := missing.VisitOccurrences(func(context.Context, MetadataArrayOccurrence) error {
		t.Fatal("missing array emitted an occurrence")
		return nil
	}); err != nil {
		t.Fatalf("missing array occurrence visit=%v", err)
	}
	missing.Close()
	missingMap, err := f.BeginMetadataCollectionRead(t.Context(), f.data.Workspace.URLKey+"-missing", "teamSettings")
	if err != nil {
		t.Fatal(err)
	}
	if err := missingMap.VisitMap("teamSettings", func(context.Context, MetadataMapOccurrence) error {
		t.Fatal("missing workspace emitted a map occurrence")
		return nil
	}); err != nil {
		t.Fatalf("missing map visit=%v", err)
	}
	missingMap.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.BeginMetadataArrayRead(ctx, f.data.Workspace.URLKey, "users"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "1")
	if _, err := f.BeginMetadataArrayRead(t.Context(), f.data.Workspace.URLKey, "users"); !errors.Is(err, ErrRecordSpoolBudget) {
		t.Fatalf("spool budget=%v", err)
	}
	if recordSpoolBytes.Load() != before {
		t.Fatal("failure leaked spool budget")
	}
}

func TestScaleP21SW0CollectionRejectsOversizedCanonicalIndexKey(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("external schemas enforce the canonical key length at insertion")
	}
	workspace := f.data.Workspace.URLKey
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, saved, workspace); err != nil {
			t.Error(err)
		}
	}()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(`{"users":[],"_flowCollections":{"users":"array"}}`), workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,999,?)`, workspace, strings.Repeat("x", 65536), []byte(`{"id":"oversized-key"}`)); err != nil {
		t.Fatal(err)
	}
	before := recordSpoolBytes.Load()
	if read, err := f.BeginMetadataArrayRead(t.Context(), workspace, "users"); !errors.Is(err, ErrMetadataRecordQuery) {
		if read != nil {
			read.Close()
		}
		t.Fatalf("oversized canonical index key=%v", err)
	}
	if recordSpoolBytes.Load() != before {
		t.Fatal("index validation failure leaked source spool")
	}
}

func TestScaleP21SW0CollectionCanonicalGrammar(t *testing.T) {
	// Catalog triggers reject malformed JSON on ordinary writes. Exercise
	// the same detached-row validator directly for historical/corrupt bytes.
	if err := validateMetadataCollectionJSON(t.Context(), strings.NewReader(" \n{\"id\":\"valid\"}\t ")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"id":"first"} {"id":"second"}`, `{"id":"broken"`, `{"id":"bad","unknown":[1,]}`} {
		if err := validateMetadataCollectionJSON(t.Context(), strings.NewReader(raw)); err == nil {
			t.Fatalf("invalid canonical JSON accepted: %s", raw)
		}
	}
}

func TestScaleP21SW0CollectionRejectsEarlierWrongRootShape(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, saved, workspace); err != nil {
			t.Error(err)
		}
	}()
	for _, root := range []string{
		`{"users":{},"Users":[]}`,
		`{"USERS":"wrong","users":[]}`,
		`{"users":false,"\u0075sers":null}`,
		`{"_flowCollections":{"ignored":false},"users":[]}`,
	} {
		var legacy domain.Bootstrap
		if err := json.Unmarshal([]byte(root), &legacy); err == nil {
			// Bootstrap ignores the private manifest; the metadata loader
			// separately unmarshals it into map[string]string.
			var header struct {
				Collections map[string]string `json:"_flowCollections"`
			}
			if err := json.Unmarshal([]byte(root), &header); err == nil {
				t.Fatal("invalid fixture accepted by legacy decoding")
			}
		}
		before := recordSpoolBytes.Load()
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(root), workspace); err != nil {
			t.Fatal(err)
		}
		read, err := f.BeginMetadataArrayRead(t.Context(), workspace, "users")
		if err == nil {
			read.Close()
			t.Fatalf("discarded invalid earlier root shape accepted: %s", root)
		}
		if recordSpoolBytes.Load() != before {
			t.Fatal("invalid input leaked spool reservation")
		}
	}
	root := `{"users":[{"id":"discarded","name":7}],"Users":[{"id":"final","name":"valid"}]}`
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(root), workspace); err != nil {
		t.Fatal(err)
	}
	var legacy domain.Bootstrap
	if err := json.Unmarshal([]byte(root), &legacy); err == nil {
		t.Fatal("legacy unexpectedly accepted earlier invalid element field")
	}
	read, err := f.BeginMetadataArrayRead(t.Context(), workspace, "users")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if _, err := scaleP21SW15MergeUserOccurrences(read); err == nil {
		t.Fatal("typed occurrence merging ignored the discarded first type error")
	}
}

func TestScaleP21SW0CollectionLexerAllocationDoesNotFollowIgnoredString(t *testing.T) {
	// Both inputs are allocated before measurement. The parser retains only
	// its fixed buffered reader while validating the ignored giant token.
	small := []byte(`{"unknown":"short","users":[]}`)
	large := []byte(`{"unknown":"` + strings.Repeat(`\u0061`, 500000) + `","users":[]}`)
	allocations := func(raw []byte) float64 {
		return testing.AllocsPerRun(2, func() {
			scanner := metadataCollectionScanner{ctx: context.Background(), input: bufio.NewReaderSize(bytes.NewReader(raw), 4096)}
			if _, err := scanner.value(0); err != nil {
				panic(err)
			}
			if err := scanner.end(); err != nil {
				panic(err)
			}
		})
	}
	if short, long := allocations(small), allocations(large); long > short+2 {
		t.Fatalf("token-sized allocation short=%v long=%v", short, long)
	}
}

func TestScaleP21SW0CollectionAdaptiveAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginMetadataArrayRead(t.Context(), f.data.Workspace.URLKey, "users")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = read.Visit(func(ctx context.Context, record MetadataArrayRecord) error {
		calls++
		workCtx, release, err := view.admitMetadataWork(ctx, 4<<20)
		if err != nil {
			return err
		}
		defer release()
		lease, ok := workCtx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
		if !ok || !lease.live || lease.bytes != 4<<20 || view.metadataResidency.work.used < 6<<20 {
			t.Fatal("typed admission reused the parser's smaller lease")
		}
		_, err = io.Copy(io.Discard, record.Reader)
		return err
	})
	if err != nil || calls == 0 {
		t.Fatalf("adaptive admission callbacks=%d error=%v", calls, err)
	}
	if err := read.Close(); err != nil || view.metadataResidency.work.used != 0 || view.metadataResidency.work.waiting != 0 {
		t.Fatalf("admission release error=%v used=%d waiting=%d", err, view.metadataResidency.work.used, view.metadataResidency.work.waiting)
	}
}
