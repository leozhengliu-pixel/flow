package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW16FormerResult(repo *SQLiteStore, ctx context.Context, workspace string) (map[string]bool, error) {
	read, err := repo.BeginFormerMembersRead(ctx, workspace)
	if read == nil {
		return nil, err
	}
	defer read.Close()
	return maps.Clone(read.Members()), err
}

// Exercise the canonical decoder independently. This helper is deliberately
// separate from BeginFormerMembersRead's production logical-source hold.
func scaleP21SW16CanonicalFormerResult(repo *SQLiteStore, ctx context.Context, workspace string) (map[string]bool, error) {
	ctx, release, err := repo.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := repo.db.BeginTx(ctx, metadataReadOptions(repo.dialect))
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	read := &FormerMembersRead{members: map[string]bool{}}
	defer read.Close()
	remaining := scaleP21SW16FormerMapBytes
	add := func(id string) error {
		if read.members[id] {
			return nil
		}
		cost := len(id) + 128
		if cost > remaining {
			return ErrMetadataWorkBudget
		}
		remaining -= cost
		read.members[id] = true
		return nil
	}
	_, err = repo.scaleP21SW16FallbackFormerMembers(ctx, tx, workspace, read, add, true)
	return maps.Clone(read.Members()), err
}

func TestScaleP21SW16FormerMembersDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stamp := "2026-01-02T03:04:05Z"
	prefix := idgen.New("sw16-former-")
	owned := []string{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, id := range owned {
			if _, err := f.db.ExecContext(ctx, `DELETE FROM auth_users WHERE id=?`, id); err != nil {
				t.Error(err)
			}
		}
	})
	for index := range 133 {
		id := fmt.Sprintf("%s-%03d", prefix, index)
		owned = append(owned, id)
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, id+"@example.test", id, id, "", "", 1, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		status := "active"
		if index%3 == 0 {
			status = "suspended"
		}
		if index%3 == 1 {
			status = "removed"
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) VALUES(?,?,?,?,?)`, workspaceID, id, "member", status, stamp); err != nil {
			t.Fatal(err)
		}
		if index < 4 {
			if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_departures(workspace_id,user_id,role,joined_at,left_at) VALUES(?,?,?,?,?)`, workspaceID, id, "member", stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, owned[0]); err != nil {
		t.Fatal(err)
	}
	canonicalFallback := false
	compare := func(label string) {
		t.Helper()
		for _, actor := range []domain.User{
			f.data.Viewer,
			{ID: owned[1], Active: true},
			{ID: owned[2], Active: true, App: true, AppTeamIDs: []string{}},
			{ID: "guest-explicit", Active: true},
			{ID: "private-outsider", Active: true},
			{ID: "sub-team-member", Active: true},
		} {
			ctx := ContextWithActor(t.Context(), actor)
			old, oldErr := f.FormerMembers(ctx, workspace)
			var bounded map[string]bool
			var err error
			if canonicalFallback {
				bounded, err = scaleP21SW16CanonicalFormerResult(probe, ctx, workspace)
			} else {
				bounded, err = scaleP21SW16FormerResult(probe, ctx, workspace)
			}
			if err != nil || oldErr != nil || !reflect.DeepEqual(old, bounded) {
				t.Fatalf("%s actor=%s differs: old=%v bounded=%v errors=%v/%v", label, actor.ID, old, bounded, oldErr, err)
			}
		}
	}
	compare("memberships-and-departures")
	// A rejoined active member is no longer former even with a departure.
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) VALUES(?,?,?,?,?)`, workspaceID, owned[0], "member", "active", stamp); err != nil {
		t.Fatal(err)
	}
	compare("rejoined")
	for _, state := range []string{"suspended", "active", "removed"} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status=? WHERE workspace_id=? AND user_id=?`, state, workspaceID, owned[0]); err != nil {
			t.Fatal(err)
		}
		compare("queued-and-replayed-live-" + state)
	}
	// The development fallback is used only when no membership exists;
	// departed rows are intentionally ignored in that original branch.
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, workspaceID); err != nil {
		t.Fatal(err)
	}
	canonicalFallback = true
	members := []domain.WorkspaceMember{
		{User: domain.User{ID: "", Name: "Empty accepted ID"}, Status: "suspended"},
		{User: domain.User{ID: prefix + "-duplicate"}, Status: "suspended"},
		{User: domain.User{ID: prefix + "-duplicate"}, Status: "active"},
		{User: domain.User{ID: strings.Repeat("x", 192)}, Status: "suspended"},
		{User: domain.User{ID: "\u6c49\u5b57"}, Status: "suspended"},
		{User: domain.User{ID: prefix + "-case"}, Status: "Suspended"},
		{User: domain.User{ID: prefix + "-active"}, Status: "active"},
	}
	for index := range 77 {
		members = append(members, domain.WorkspaceMember{User: domain.User{ID: fmt.Sprintf("%s-fallback-%03d", prefix, index)}, Status: "suspended"})
	}
	members[1].User.Name = strings.Repeat("x", 2<<20)
	seed := func(items []domain.WorkspaceMember) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members"), workspace, "scale_p21_sw16.former_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Members = items
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed(members)
	compare("inline-fallback-large-unrelated-user")
	for _, size := range []int{0, 64 << 10} {
		t.Run(fmt.Sprintf("spool=%d", size), func(t *testing.T) {
			t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", fmt.Sprint(size))
			compare("bounded-without-disk-spool")
		})
	}
	t.Run("temporary-directory-unavailable", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
		compare("bounded-without-temporary-files")
	})
	var before, after runtime.MemStats
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		runtime.GC()
		runtime.ReadMemStats(&before)
		var result map[string]bool
		var err error
		if source.BoundedMetadataEnabled() {
			result, err = scaleP21SW16CanonicalFormerResult(source, t.Context(), workspace)
		} else {
			result, err = scaleP21SW16FormerResult(source, t.Context(), workspace)
		}
		runtime.ReadMemStats(&after)
		if err != nil || len(result) != 81 {
			t.Fatalf("measurement fixture count=%d err=%v", len(result), err)
		}
		t.Logf("bounded=%t former-members allocated-bytes=%d allocations=%d", source.BoundedMetadataEnabled(), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
	seed(nil)
	compare("nil-fallback")
	seed([]domain.WorkspaceMember{})
	compare("empty-fallback")
	seed(members[:4])
	reloadFromRows(t, f.SQLiteStore, workspace)
	compare("actual-reload-fallback")
	if _, err := scaleP21SW16FormerResult(probe, t.Context(), workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unproven logical fallback was exposed by production API", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scaleP21SW16FormerResult(probe, ctx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled former-member read changed", err)
	}
	for _, key := range []string{workspace + "-missing", workspace + "\x00", strings.ToUpper(workspace)} {
		for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
			got, err := scaleP21SW16FormerResult(source, t.Context(), key)
			if err != nil || len(got) != 0 || got == nil {
				t.Fatal("missing/cross-workspace lookup changed", key, err)
			}
		}
	}
	probe.metadataResidency.work.limit = scaleP21SW16FormerWorkBytes - 1
	if _, err := scaleP21SW16CanonicalFormerResult(probe, t.Context(), workspace); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("result allowance bypassed the process budget", err)
	}
	probe.metadataResidency.work.limit = metadataWorkBytes
	if _, err := scaleP21SW16CanonicalFormerResult(probe, t.Context(), workspace); err != nil || probe.metadataResidency.work.used != 0 || probe.metadataResidency.work.waiting != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("former-member read failed recovery or leaked work/full decode", err)
	}
	// Callback SQL must reuse the caller's sole connection.
	f.db.SetMaxOpenConns(1)
	ctx, release, err := probe.tryAdmitAdditionalMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	if _, err := scaleP21SW16FormerResult(probe, ctx, workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("caller snapshot was not reused for the logical source check", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1`); err != nil {
		t.Fatal("lookup closed caller transaction", err)
	}
	_ = tx.Rollback()
	f.db.SetMaxOpenConns(4)
}

func TestScaleP21SW16FormerMemberDecodeAccounting(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`{"user":{"id":"chosen"},"status":"suspended","unknown":{"large":"ignored"}}`,
		`{"User":{"ID":"chosen"},"STATUS":"suspended","status":null}`,
		`{"user":{"id":"before"},"user":null,"status":"suspended"}`,
		`{"user":{"id":"before","id":"after"},"status":"suspended"}`,
		`{"user":{"id":"chosen","name":"` + strings.Repeat("x", 2<<20) + `"},"status":"suspended"}`,
	} {
		var want, got domain.WorkspaceMember
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		record := MetadataArrayRecord{EncodedBytes: int64(len(raw)), Reader: strings.NewReader(raw)}
		cost, err := scaleP21SW16MemberDecodeCost(record)
		if err != nil || cost < len(raw)*8 {
			t.Fatal("record decode was not conservatively counted", err)
		}
		if err := scaleP21SW16DecodeMember(t.Context(), record, &got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("typed member duplicate/null behavior changed", err)
		}
	}
}

func TestScaleP21SW16FormerMembersRepeatedRoot(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		name, first, second string
		want                map[string]bool
	}{
		{"retained-member-fields", `[{"user":{"id":"retained"},"status":"suspended"}]`, `[{"user":{"name":"later"}}]`, map[string]bool{"retained": true}},
		{"null-clears-backing", `null`, `[{"user":{"name":"later"},"status":"suspended"}]`, map[string]bool{"": true}},
		{"empty-clears-backing", `[]`, `[{"user":{"id":"fresh"},"status":"suspended"}]`, map[string]bool{"fresh": true}},
		{"later-null", `[{"user":{"id":"discarded"},"status":"suspended"}]`, `null`, map[string]bool{}},
		{"null-struct-retains", `[{"user":{"id":"retained"},"status":"suspended"}]`, `[null]`, map[string]bool{"retained": true}},
	} {
		t.Run(spec.name, func(t *testing.T) {
			fields["Members"], fields["members"] = json.RawMessage(spec.first), json.RawMessage(spec.second)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
				t.Fatal(err)
			}
			reloadFromRows(t, f.SQLiteStore, workspace)
			old, oldErr := f.FormerMembers(t.Context(), workspace)
			bounded, err := scaleP21SW16CanonicalFormerResult(probe, t.Context(), workspace)
			if oldErr != nil || err != nil || !reflect.DeepEqual(old, spec.want) || !reflect.DeepEqual(bounded, old) {
				t.Fatalf("typed backing merge differs: old=%v bounded=%v want=%v errors=%v/%v", old, bounded, spec.want, oldErr, err)
			}
			if probe.metadataResidency.work.used != 0 {
				t.Fatal("repeated root merge leaked admission")
			}
		})
	}
}

func TestScaleP21SW16FormerMembersHandleLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, workspaceID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	defer f.db.SetMaxOpenConns(4)
	read, err := probe.BeginFormerMembersRead(t.Context(), workspace)
	if read == nil || err != nil || !read.Members()[f.data.Viewer.ID] {
		t.Fatal("could not open the owned former-member handle", err)
	}
	defer read.Close()
	if probe.metadataResidency.work.used == 0 {
		t.Fatal("output admission ended before caller map use")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var one int
	if err := f.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatal("returned handle pinned the sole SQL connection", err)
	}
	remaining := probe.metadataResidency.work.limit - probe.metadataResidency.work.used
	if _, _, err := probe.tryAdmitAdditionalMetadataWork(t.Context(), remaining+1); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("caller-owned result escaped process admission", err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil || probe.metadataResidency.work.used != 0 {
		t.Fatal("handle close leaked or double-released admission", err)
	}
	_, release, err := probe.tryAdmitAdditionalMetadataWork(t.Context(), remaining+1)
	if err != nil {
		t.Fatal("closed handle did not restore work", err)
	}
	release()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	previous, previousErr := f.FormerMembers(cancelled, workspace)
	off, offErr := f.BeginFormerMembersRead(cancelled, workspace)
	if off == nil || !reflect.DeepEqual(off.Members(), previous) || !errors.Is(offErr, previousErr) {
		t.Fatal("off handle dropped the original partial/error map", offErr, previousErr)
	}
	_ = off.Close()
	queries := 0
	ctx = WithSQLStatementObserver(t.Context(), func(string) { queries++ })
	if _, err := probe.FormerMembers(ctx, workspace); !errors.Is(err, ErrBoundedMetadataCoverage) || queries != 0 {
		t.Fatal("old unleased map API stayed callable in bounded mode", err)
	}
}
