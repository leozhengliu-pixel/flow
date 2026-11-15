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
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW163Row struct {
	resource string
	raw      []byte
	nilRaw   bool
}

func scaleP21SW163Fixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	fixture := scaleP09Database(t)
	workspace := idgen.New("sw163-")
	t.Cleanup(func() {
		_, err := fixture.db.ExecContext(context.Background(), `DELETE FROM workspace_content_records WHERE workspace_key=?`, workspace)
		if err != nil {
			t.Error(err)
		}
	})
	view := &SQLiteStore{db: fixture.db, dialect: fixture.dialect, metadataResidency: newMetadataResidency()}
	return fixture, view, workspace
}

func scaleP21SW163Insert(t *testing.T, fixture scaleP09Fixture, workspace, kind string, resource, created, id, raw any) {
	t.Helper()
	if _, err := fixture.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`,
		workspace, kind, resource, id, created, raw); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW163Collect(t *testing.T, repo *SQLiteStore, ctx context.Context, workspace, kind string, bounded bool) ([]scaleP21SW163Row, error) {
	t.Helper()
	result := []scaleP21SW163Row{}
	visit := func(resource string, raw json.RawMessage) error {
		result = append(result, scaleP21SW163Row{resource: resource, raw: bytes.Clone(raw), nilRaw: raw == nil})
		return nil
	}
	var err error
	if bounded {
		err = repo.walkBoundedBootstrapContentRecords(ctx, workspace, kind, visit)
	} else {
		err = repo.WalkContentRecords(ctx, workspace, kind, visit)
	}
	return result, err
}

func TestScaleP21SW163BootstrapContentSQLOrder(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	for index := range 181 {
		resource := fmt.Sprintf("resource-%02d-%s", index%7, []string{"A", "a", "共享"}[index%3])
		created := fmt.Sprintf("2026-11-15T00:%02d:%02dZ", index%5, index%3)
		id := fmt.Sprintf("row-%03d", index)
		raw := []byte(fmt.Sprintf(` {"id":"duplicate","body":"body-%d-共享","user":{"id":"actor"}} `, index))
		var resourceKey, createdKey, idKey, payload any = resource, created, id, raw
		if fixture.dialect == "sqlite" {
			if index%3 == 0 {
				resourceKey = []byte(resource)
			}
			if index%5 == 0 {
				createdKey = []byte(created)
			}
			if index%7 == 0 {
				idKey = []byte(id)
			}
			if index%2 == 0 {
				payload = string(raw)
			}
		}
		scaleP21SW163Insert(t, fixture, workspace, "comment", resourceKey, createdKey, idKey, payload)
	}
	if fixture.dialect == "sqlite" {
		for index := range 4 {
			var resource, id any = "same", "same"
			if index&1 != 0 {
				resource = []byte("same")
			}
			if index&2 != 0 {
				id = []byte("same")
			}
			scaleP21SW163Insert(t, fixture, workspace, "comment", resource, "same-time", id, fmt.Sprintf(`{"body":"storage-%d"}`, index))
		}
		scaleP21SW163Insert(t, fixture, workspace, "comment", "\x00\xff", "", "\x00\xff", []byte{})
		scaleP21SW163Insert(t, fixture, workspace, "comment", "", "", "empty-text", "")
	}
	scaleP21SW163Insert(t, fixture, workspace, "other-kind", "resource", "", "ignored-kind", "not-json")
	want, err := scaleP21SW163Collect(t, fixture.SQLiteStore, t.Context(), workspace, "comment", false)
	if err != nil {
		t.Fatal(err)
	}
	peak := 0
	bodyQueries := 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		peak = max(peak, view.MetadataWorkUsedForTesting())
		if rows := strings.Count(query, " AS ordinal,CASE WHEN"); rows != 0 {
			bodyQueries++
			if rows > 32 {
				t.Fatal("body query exceeded the row bound", rows)
			}
		}
	})
	got, err := scaleP21SW163Collect(t, view, ctx, workspace, "comment", true)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("literal SQL order, physical key storage or raw bytes changed", len(got), len(want), err)
	}
	if peak == 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("content stream lost or leaked admission/full metadata", peak)
	}
	if bodyQueries > (len(want)+31)/32+1 || bodyQueries >= len(want) {
		t.Fatal("ordinary small rows regressed to individual body queries", len(want), bodyQueries)
	}
	t.Logf("rows=%d body_queries=%d peak_observed_work=%d", len(want), bodyQueries, peak)
	empty, err := scaleP21SW163Collect(t, view, t.Context(), workspace, "missing-kind", true)
	if err != nil || len(empty) != 0 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("empty selection changed or leaked", err)
	}
}

func TestScaleP21SW163BootstrapContentKinds(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	for _, item := range []struct{ kind, raw string }{
		{"comment", `{"body":"ok","bodyData":{"nested":[null,{}]}}`},
		{"activity", `{"id":"activity","metadata":{"comment":"<>&","cycle":"cycle"}}`},
		{"notification", `{"payload":{"updateIds":["a","b"]},"actor":{"id":"actor"},"title":"<>&"}`},
		{"delivery", `{"status":"pending","attempts":1}`},
		{"unknown", `malformed ignored bytes`},
	} {
		scaleP21SW163Insert(t, fixture, workspace, item.kind, "resource", "", "row", []byte(item.raw))
		want, wantErr := scaleP21SW163Collect(t, fixture.SQLiteStore, t.Context(), workspace, item.kind, false)
		got, gotErr := scaleP21SW163Collect(t, view, t.Context(), workspace, item.kind, true)
		if wantErr != nil || gotErr != nil || !reflect.DeepEqual(want, got) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("raw kind/driver/ignored-value semantics changed", item.kind, wantErr, gotErr)
		}
	}
}

func TestScaleP21SW163BootstrapContentMalformedVisitor(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	for index, raw := range []string{"", "{", `{"body":`, `{"body":"ok"} trailing`} {
		scaleP21SW163Insert(t, fixture, workspace, "comment", "a-ignored", "", fmt.Sprint(index), raw)
	}
	scaleP21SW163Insert(t, fixture, workspace, "comment", "b-visible", "", "valid", `{"body":"\ud800","bodyData":{"children":[null,{}]}}`)
	scaleP21SW163Insert(t, fixture, workspace, "comment", "c-visible", "", "invalid", `{"body":`)
	scaleP21SW163Insert(t, fixture, workspace, "comment", "d-visible", "", "unreached", `{"body":"later"}`)
	run := func(repo *SQLiteStore, bounded bool) ([]string, error) {
		var visited []string
		visit := func(resource string, raw json.RawMessage) error {
			visited = append(visited, resource)
			if resource == "a-ignored" {
				return nil
			}
			var comment domain.Comment
			if err := json.Unmarshal(raw, &comment); err != nil {
				return err
			}
			return json.NewEncoder(io.Discard).Encode(comment)
		}
		if bounded {
			err := repo.walkBoundedBootstrapContentRecords(t.Context(), workspace, "comment", visit)
			return visited, err
		}
		err := repo.WalkContentRecords(t.Context(), workspace, "comment", visit)
		return visited, err
	}
	want, wantErr := run(fixture.SQLiteStore, false)
	got, gotErr := run(view, true)
	if !reflect.DeepEqual(want, got) || wantErr == nil || gotErr == nil || wantErr.Error() != gotErr.Error() {
		t.Fatal("malformed ignored rows, visitor error or visited prefix changed", want, got, wantErr, gotErr)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("visitor error retained row work")
	}
}

func TestScaleP21SW163BootstrapContentBorrowedSnapshot(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	fixture.db.SetMaxOpenConns(1)
	options := *metadataReadOptions(fixture.dialect)
	options.ReadOnly = false
	tx, err := fixture.db.BeginTx(t.Context(), &options)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,'comment','borrowed','inside','',?)`,
		workspace, []byte(`{"body":"uncommitted"}`)); err != nil {
		t.Fatal(err)
	}
	// The original walker selects by matching workspace, without inspecting
	// the incidental database field on this existing snapshot context.
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{workspace: workspace, reader: tx})
	want, err := scaleP21SW163Collect(t, fixture.SQLiteStore, ctx, workspace, "comment", false)
	if err != nil {
		t.Fatal(err)
	}
	var got []scaleP21SW163Row
	err = view.walkBoundedBootstrapContentRecords(ctx, workspace, "comment", func(resource string, raw json.RawMessage) error {
		got = append(got, scaleP21SW163Row{resource: resource, raw: bytes.Clone(raw), nilRaw: raw == nil})
		var one int
		return tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&one)
	})
	if err != nil || !reflect.DeepEqual(want, got) || len(got) != 1 {
		t.Fatal("borrowed snapshot/source changed or rows remained open", err)
	}
	var count int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=?`, workspace).Scan(&count); err != nil || count != 1 {
		t.Fatal("walker closed caller transaction", count, err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("borrowed walk retained work")
	}
}

func TestScaleP21SW163BootstrapContentOwnedSnapshot(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	fixture.db.SetMaxOpenConns(2)
	if fixture.dialect == "sqlite" {
		var mode string
		if err := fixture.db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(mode, "wal") {
			t.Skip("concurrent SQLite peer requires the existing WAL configuration")
		}
	}
	for index := range 140 {
		scaleP21SW163Insert(t, fixture, workspace, "comment", "resource", "", fmt.Sprintf("%03d", index), []byte(fmt.Sprintf(`{"body":"before-%d"}`, index)))
	}
	want, err := scaleP21SW163Collect(t, fixture.SQLiteStore, t.Context(), workspace, "comment", false)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	var got []scaleP21SW163Row
	err = view.walkBoundedBootstrapContentRecords(t.Context(), workspace, "comment", func(resource string, raw json.RawMessage) error {
		got = append(got, scaleP21SW163Row{resource: resource, raw: bytes.Clone(raw), nilRaw: raw == nil})
		if !changed {
			changed = true
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := fixture.db.ExecContext(ctx, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='comment' AND resource_id='resource' AND id='130'`,
				[]byte(`{"body":"peer"}`), workspace); err != nil {
				return err
			}
			_, err := fixture.db.ExecContext(ctx, `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,'comment','resource','999','',?)`,
				workspace, []byte(`{"body":"new peer row"}`))
			return err
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(want, got) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("keyset pages/body reads crossed the original single-query snapshot", len(want), len(got), err)
	}
	after, err := scaleP21SW163Collect(t, fixture.SQLiteStore, t.Context(), workspace, "comment", false)
	if err != nil || len(after) != len(want)+1 || reflect.DeepEqual(after[:len(want)], want) {
		t.Fatal("peer fixture did not change canonical rows", err)
	}
}

func TestScaleP21SW163BootstrapContentLargeDenseAndPageBounds(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	large := []byte(`{"id":"large","body":"` + strings.Repeat("<>&", 1<<20) + `"}`)
	dense := []byte(`{"bodyData":{"children":[` + strings.Repeat("null,{},", 2000) + `null]}}`)
	if len(large) <= 2<<20 {
		t.Fatal("large fixture is not above two MiB")
	}
	scaleP21SW163Insert(t, fixture, workspace, "comment", "large", "", "large", large)
	scaleP21SW163Insert(t, fixture, workspace, "comment", "dense", "", "dense", dense)
	var maxWork int
	count := 0
	err := view.walkBoundedBootstrapContentRecords(t.Context(), workspace, "comment", func(_ string, raw json.RawMessage) error {
		maxWork = max(maxWork, view.MetadataWorkUsedForTesting())
		count++
		var comment domain.Comment
		if err := json.Unmarshal(raw, &comment); err != nil {
			return err
		}
		return json.NewEncoder(io.Discard).Encode(comment)
	})
	if err != nil || count != 2 || maxWork <= len(large)*24 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("legal large/dense raw was capped or processing work uncharged", count, maxWork, err)
	}
	if fixture.dialect == "sqlite" {
		for index := range 70 {
			resource := strings.Repeat("native-key-", 1500) + fmt.Sprint(index)
			scaleP21SW163Insert(t, fixture, workspace, "comment", resource, "", fmt.Sprint(index), []byte(`{}`))
		}
	}
	headers, owner, err := view.bootstrapContentHeaders(t.Context(), view.db, workspace, "comment", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) > bootstrapContentHeaderRows || view.MetadataWorkUsedForTesting() > bootstrapContentHeaderWork {
		t.Fatal("header page exceeded row/work bound", len(headers), view.MetadataWorkUsedForTesting())
	}
	owner.Close()
	if fixture.dialect == "sqlite" && len(headers) >= bootstrapContentHeaderRows {
		t.Fatal("legal large keys did not narrow the page")
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("header page retained admission")
	}
	medium := []byte(`{"body":"` + strings.Repeat("x", 16<<10) + `"}`)
	for index := range 30 {
		scaleP21SW163Insert(t, fixture, workspace, "medium", "resource", "", fmt.Sprint(index), medium)
	}
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if rows := strings.Count(query, " AS ordinal,CASE WHEN"); rows != 0 && rows*len(medium) > 256<<10 {
			t.Fatal("ordinary body query exceeded payload bound", rows, len(medium))
		}
	})
	mediumCount := 0
	if err := view.walkBoundedBootstrapContentRecords(ctx, workspace, "medium", func(_ string, raw json.RawMessage) error {
		mediumCount++
		if !bytes.Equal(raw, medium) {
			t.Fatal("byte-bounded batch changed its raw payload")
		}
		return nil
	}); err != nil || mediumCount != 30 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("byte-bounded body batch changed or leaked", mediumCount, err)
	}
	denseRefusal := []byte(`{"bodyData":{"children":[` + strings.Repeat("{},", 100000) + `null]}}`)
	scaleP21SW163Insert(t, fixture, workspace, "dense-refusal", "resource", "", "dense", denseRefusal)
	visited := false
	if err := view.walkBoundedBootstrapContentRecords(t.Context(), workspace, "dense-refusal", func(string, json.RawMessage) error {
		visited = true
		return nil
	}); visited || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("dense structural decode/encoding escaped pre-visitor admission", err)
	}
	if fixture.dialect == "sqlite" {
		scaleP21SW163Insert(t, fixture, workspace, "key-refusal", strings.Repeat("x", 40000), "", "key", []byte(`{}`))
		transferred := false
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			transferred = transferred || strings.HasPrefix(query, "SELECT CASE WHEN")
		})
		if err := view.walkBoundedBootstrapContentRecords(ctx, workspace, "key-refusal", func(string, json.RawMessage) error {
			t.Fatal("oversized header visitor ran")
			return nil
		}); !errors.Is(err, ErrMetadataWorkBudget) || transferred || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("oversized header escaped guarded transfer", err)
		}
	}
}

func TestScaleP21SW163BootstrapContentCancellationQuotaAndRelease(t *testing.T) {
	fixture, view, workspace := scaleP21SW163Fixture(t)
	raw := []byte(`{"body":"` + strings.Repeat("legal body ", 2000) + `"}`)
	scaleP21SW163Insert(t, fixture, workspace, "comment", "resource", "", "row", raw)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.walkBoundedBootstrapContentRecords(ctx, workspace, "comment", func(string, json.RawMessage) error {
		t.Fatal("pre-canceled visitor ran")
		return nil
	}); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("pre-canceled walker escaped or leaked", err)
	}
	visitorErr := errors.New("visitor stopped")
	ctx, cancel = context.WithCancel(t.Context())
	err := view.walkBoundedBootstrapContentRecords(ctx, workspace, "comment", func(string, json.RawMessage) error {
		cancel()
		return visitorErr
	})
	if err != visitorErr || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("visitor error lost precedence over cancellation", err)
	}
	var borrowed json.RawMessage
	peak := 0
	err = view.walkBoundedBootstrapContentRecords(t.Context(), workspace, "comment", func(_ string, raw json.RawMessage) error {
		peak = view.MetadataWorkUsedForTesting()
		borrowed = raw
		return nil
	})
	if err != nil || peak == 0 || view.MetadataWorkUsedForTesting() != 0 || !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("row work/borrowed bytes survived the visitor", err)
	}
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	defer func() { budget.limit = limit }()
	for _, ceiling := range []int{1, 64 << 10, 256 << 10, peak - 1} {
		budget.limit = ceiling
		visited := false
		err := view.walkBoundedBootstrapContentRecords(t.Context(), workspace, "comment", func(string, json.RawMessage) error {
			visited = true
			return nil
		})
		if visited || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial transfer/processing refusal escaped or leaked", ceiling, err)
		}
	}
	budget.limit = limit
	ctx, cancel = context.WithCancel(t.Context())
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, " AS ordinal,CASE WHEN") && strings.Contains(query, " THEN data ELSE NULL END") {
			cancel()
		}
	})
	if err := view.walkBoundedBootstrapContentRecords(ctx, workspace, "comment", func(string, json.RawMessage) error {
		t.Fatal("mid-transfer canceled visitor ran")
		return nil
	}); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("mid-transfer cancellation leaked", err)
	}
	parent := &ResourceRead{store: view}
	if err := parent.reserve(t.Context(), 4096); err != nil {
		t.Fatal(err)
	}
	err = view.walkBoundedBootstrapContentRecords(t.Context(), workspace, "comment", func(string, json.RawMessage) error { return nil })
	if err != nil || view.MetadataWorkUsedForTesting() != 4096 {
		parent.Close()
		t.Fatal("row cleanup released or changed its caller's admission", err)
	}
	parent.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("parent fixture retained admission")
	}
}
