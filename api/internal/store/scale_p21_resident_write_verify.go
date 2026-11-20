package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"flow/api/internal/domain"
)

// The resident write's reference comparison (residentWriteVerify, on in
// tests). Before the resident commit, the same edit runs through the scoped
// image writer (mutateBoundedScopedImage) up to its commit, with the
// resident callback's result replayed as its callback: its callback input
// must equal the resident one, and its previous values, realtime payload,
// SQL state before commit and next generation must equal the resident
// write's. The reference transaction is rolled back; a difference fails the
// edit.

type residentWriteShadowKey struct{}

// errResidentShadow ends the reference run before its commit.
var errResidentShadow = errors.New("resident write reference run ended before commit")

type residentWriteShadow struct {
	before, after      domain.Bootstrap
	aggregate          string
	mismatch           string
	captured           bool
	previous, realtime []byte
	rows               map[string]string
	image              domain.Bootstrap
}

func residentWriteShadowFrom(ctx context.Context) *residentWriteShadow {
	shadow, _ := ctx.Value(residentWriteShadowKey{}).(*residentWriteShadow)
	return shadow
}

// verify runs the reference and compares what precedes the commit.
func (e *residentDocumentEdit) verify(ctx context.Context, payload any) error {
	shadow := &residentWriteShadow{before: e.before, after: e.data, aggregate: e.aggregate}
	replay := func(data *domain.Bootstrap) (string, error) {
		got, want := *data, shadow.before
		got.FormerMembers, got.CollaborationUpdates, got.CollaborationWork = nil, nil, nil
		if field := residentBootstrapDifference(got, want); field != "" {
			shadow.mismatch = "callback input field " + field
			return "", errResidentShadow
		}
		*data = exactCopyValue(reflect.ValueOf(shadow.after)).Interface().(domain.Bootstrap)
		return shadow.aggregate, nil
	}
	err := e.store.mutateBoundedScopedImage(context.WithValue(ctx, residentWriteShadowKey{}, shadow), e.workspace, e.eventType, payload,
		&documentUpdateImageProfile{request: e.profile.request, scope: e.profile.scope}, replay)
	// The reference ran on the installed generation of its time: when
	// another write installed one since the edit pinned its own, the two
	// are not comparable and the edit takes the scoped writer.
	installed, retainErr := e.store.retainMetadataLogicalSource(ctx, e.workspace)
	if retainErr != nil {
		return retainErr
	}
	same := installed == e.stored
	installed.Release()
	if !same {
		return residentFallback("generation replaced during the reference run")
	}
	switch {
	case shadow.mismatch != "":
		return fmt.Errorf("%w: resident document write differs from the scoped writer: %s", ErrBoundedMetadataCoverage, shadow.mismatch)
	case !errors.Is(err, errResidentShadow) || !shadow.captured:
		return fmt.Errorf("%w: the scoped writer's reference run did not reach its commit: %v", ErrBoundedMetadataCoverage, err)
	case !bytes.Equal(shadow.previous, e.previous):
		return fmt.Errorf("%w: resident previous values differ from the scoped writer's:\n%s\n%s", ErrBoundedMetadataCoverage, e.previous, shadow.previous)
	case !bytes.Equal(shadow.realtime, e.realtime):
		return fmt.Errorf("%w: resident realtime payload differs from the scoped writer's:\n%s\n%s", ErrBoundedMetadataCoverage, e.realtime, shadow.realtime)
	}
	e.shadow = shadow
	residentWritesVerified.Add(1)
	return nil
}

// capture is the reference run's last step before its commit.
func (shadow *residentWriteShadow) capture(ctx context.Context, s *SQLiteStore, tx *sqlTx, workspace string, previous, realtime []byte, candidate *metadataLogicalSource) error {
	rows, err := residentWriteSQLState(ctx, tx, workspace)
	if err != nil {
		return err
	}
	image, err := buildColdDecodedImage(ctx, candidate, nil, nil, false)
	if err != nil {
		return err
	}
	shadow.previous, shadow.realtime = bytes.Clone(previous), bytes.Clone(realtime)
	shadow.rows, shadow.image, shadow.captured = rows, image.data, true
	return errResidentShadow
}

// compareCommit compares the resident write's SQL state before its commit
// and its next generation with the reference's.
func (shadow *residentWriteShadow) compareCommit(ctx context.Context, tx *sqlTx, workspace string, candidate *metadataLogicalSource) error {
	rows, err := residentWriteSQLState(ctx, tx, workspace)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for key := range rows {
		keys[key] = true
	}
	for key := range shadow.rows {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		got, gotOK := rows[key]
		want, wantOK := shadow.rows[key]
		if got != want || gotOK != wantOK {
			return fmt.Errorf("%w: resident document write stored %s differently from the scoped writer:\n%s\n%s", ErrBoundedMetadataCoverage, key, residentClip(got, want), residentClip(want, got))
		}
	}
	image, err := buildColdDecodedImage(ctx, candidate, nil, nil, false)
	if err != nil {
		return err
	}
	if field := residentBootstrapDifference(image.data, shadow.image); field != "" {
		return fmt.Errorf("%w: resident next generation differs from the scoped writer's in %s", ErrBoundedMetadataCoverage, field)
	}
	return nil
}

// residentClip shows a stored value around where it first differs from
// other.
func residentClip(value, other string) string {
	at := 0
	for at < len(value) && at < len(other) && value[at] == other[at] {
		at++
	}
	from, to := max(at-300, 0), min(at+300, len(value))
	return fmt.Sprintf("(len %d, differs at %d) ...%s...", len(value), at, value[from:to])
}

// residentBootstrapDifference names the first field of a and b that differs
// ("" when none does).
func residentBootstrapDifference(a, b domain.Bootstrap) string {
	left, right := reflect.ValueOf(a), reflect.ValueOf(b)
	for index, field := range metadataStructFields(left.Type()) {
		x, y := left.Field(index), right.Field(index)
		if x.Kind() == reflect.Func {
			if x.IsNil() != y.IsNil() {
				return field.name
			}
			continue
		}
		if !reflect.DeepEqual(x.Interface(), y.Interface()) {
			return field.name
		}
	}
	return ""
}

// residentWriteSQLState is the workspace's metadata as stored in tx: every
// metadata record row, search document, blob reference, content record, the
// root document and the account row.
func residentWriteSQLState(ctx context.Context, tx *sqlTx, workspace string) (map[string]string, error) {
	state := map[string]string{}
	for _, query := range []struct {
		name, sql string
		keys      int
	}{
		{"record", "SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=?", 2},
		{"search", "SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=?", 2},
		{"blob", "SELECT blob_key,orphaned_at FROM metadata_blobs WHERE workspace_key=?", 1},
		// Comments, activities, notifications and import inputs.
		{"content", "SELECT kind,resource_id,id,created_at,data FROM workspace_content_records WHERE workspace_key=?", 3},
		{"root", "SELECT workspace_id,data FROM workspace_states WHERE workspace_key=?", 1},
		{"account", "SELECT last_workspace_key,viewer FROM account_state WHERE id=1 AND last_workspace_key=?", 1},
	} {
		rows, err := tx.QueryContext(ctx, query.sql, workspace)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			text := make([]string, len(values))
			for index, value := range values {
				if raw, ok := value.([]byte); ok {
					text[index] = string(raw)
				} else {
					text[index] = fmt.Sprint(value)
				}
			}
			state[query.name+":"+strings.Join(text[:query.keys], "|")] = strings.Join(text[query.keys:], "|")
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return state, nil
}
