package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW0HeaderLegacyUsers(t *testing.T, f scaleP09Fixture) []domain.User {
	t.Helper()
	var root []byte
	workspace := f.data.Workspace.URLKey
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	f.dropMetadataCache(t.Context(), workspace)
	data, err := f.decodeWorkspaceMetadata(t.Context(), workspace, root, f.db)
	if err != nil {
		t.Fatal(err)
	}
	return data.Users
}

func TestScaleP21SW0MetadataHeaderByteOrder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	prefix := idgen.New("sw0-header-order-")
	suffixes := []string{"A", "a", "Z", "z", "E", "e", "É", "é", "Σ", "σ", "K", "k", "中", "文"}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// More than two header pages share one ordinal, so ordering and seek
	// must use the same byte comparator across both page boundaries.
	for i := range 127 {
		key := fmt.Sprintf("%s-%s-%03d", prefix, suffixes[i%len(suffixes)], i)
		raw, err := json.Marshal(domain.User{ID: key, Name: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		var storedKey any = key
		if f.dialect == "sqlite" && i%3 == 0 {
			// SQLite permits BLOB keys in its VARCHAR column. The actual
			// decoder scans them as strings and sorts their bytes too.
			storedKey = []byte(key)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,700,?)`, workspace, storedKey, raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var expected []string
	for _, user := range scaleP21SW0HeaderLegacyUsers(t, f) {
		expected = append(expected, user.ID)
	}
	if len(expected) <= 100 {
		t.Fatal("fixture did not cross two page boundaries")
	}
	f.db.SetMaxOpenConns(1)
	read, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	var actual []string
	seen := map[string]bool{}
	err = scanMetadataHeaders(t.Context(), read, workspace, "users", func(key RecordKey, size int64) error {
		if key.Kind != "users" || size <= 0 {
			return fmt.Errorf("bad header: %+v bytes=%d", key, size)
		}
		if seen[key.ID] {
			return fmt.Errorf("header keyset repeated record %q", key.ID)
		}
		seen[key.ID] = true
		actual = append(actual, key.ID)
		// Header Rows must be closed before body/native queries use the
		// transaction's only connection.
		var one int
		return read.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("header keyset order differs from actual metadata decoding:\nactual=%q\nexpected=%q", actual, expected)
	}
	if err := read.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW0MetadataHeaderSQLiteTextBytes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite TEXT and BLOB have distinct LENGTH semantics")
	}
	workspace := f.data.Workspace.URLKey
	key := idgen.New("sw0-header-unicode-")
	raw, err := json.Marshal(map[string]any{
		"id": key, "name": "共享用户",
		"futureBody": strings.Repeat("文", 2*metadataRecordChunkBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,900,?)`, workspace, key, string(raw)); err != nil {
		t.Fatal(err)
	}
	var storage string
	var characters, octets int
	if err := f.db.QueryRowContext(t.Context(), `SELECT typeof(data),LENGTH(data),LENGTH(CAST(data AS BLOB)) FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND record_key=?`, workspace, key).Scan(&storage, &characters, &octets); err != nil {
		t.Fatal(err)
	}
	if storage != "text" || characters >= octets || octets <= 2*metadataRecordChunkBytes {
		t.Fatal("Unicode TEXT fixture did not exceed multiple byte chunks")
	}
	found := false
	for _, user := range scaleP21SW0HeaderLegacyUsers(t, f) {
		found = found || user.ID == key && user.Name == "共享用户"
	}
	if !found {
		t.Fatal("actual metadata decoder lost the Unicode record")
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	err = probe.StreamMetadataRecords(t.Context(), workspace, "users", func(record RecordKey, source io.Reader) error {
		if record.ID != key {
			return nil
		}
		seen++
		var actual bytes.Buffer
		buffer := make([]byte, 4*metadataRecordChunkBytes)
		chunks := 0
		for {
			n, err := source.Read(buffer)
			if n > metadataRecordChunkBytes {
				return fmt.Errorf("canonical reader transferred an oversized chunk: %d", n)
			}
			if n > 0 {
				chunks++
				actual.Write(buffer[:n])
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
		if chunks < 3 || !bytes.Equal(actual.Bytes(), raw) {
			return fmt.Errorf("Unicode stream differs: chunks=%d got=%d want=%d", chunks, actual.Len(), len(raw))
		}
		return nil
	})
	if err != nil || seen != 1 {
		t.Fatal("complete Unicode record stream failed", seen, err)
	}
	probe.metadataResidency.work.mu.Lock()
	used, waiting := probe.metadataResidency.work.used, probe.metadataResidency.work.waiting
	probe.metadataResidency.work.mu.Unlock()
	if used != 0 || waiting != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("stream decoded full metadata or leaked work admission")
	}
	t.Logf("SQLite TEXT characters=%d bytes=%d, complete stream retained fixed chunks", characters, octets)
}

func TestScaleP21SW0MetadataHeaderCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	err = probe.StreamMetadataRecords(ctx, workspace, "users", func(RecordKey, io.Reader) error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("pre-cancelled stream entered its visitor", calls, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	err = probe.StreamMetadataRecords(ctx, workspace, "users", func(RecordKey, io.Reader) error {
		calls++
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("visitor cancellation was lost", calls, err)
	}
	if err := probe.StreamMetadataRecords(t.Context(), workspace, "users", func(RecordKey, io.Reader) error { return nil }); err != nil {
		t.Fatal("stream failed after cancellation", err)
	}
	probe.metadataResidency.work.mu.Lock()
	used, waiting := probe.metadataResidency.work.used, probe.metadataResidency.work.waiting
	probe.metadataResidency.work.mu.Unlock()
	if used != 0 || waiting != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("cancellation leaked admission or decoded full metadata")
	}
}
