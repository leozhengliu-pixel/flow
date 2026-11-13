package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW21PersonalSeed(t *testing.T) (scaleP09Fixture, *SQLiteStore, domain.User) {
	t.Helper()
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	subject, token, err := f.Register(t.Context(), "SW21 subject", idgen.New("sw21-person-")+"@example.test", "sw21-identity-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.ErasePersonFromWorkspace(ctx, workspace, subject.ID); err != nil {
			t.Errorf("clean up owned subject membership: %v", err)
		}
		if err := f.EraseAccount(ctx, subject.ID); err != nil {
			t.Errorf("clean up owned subject: %v", err)
		}
	})
	if err := f.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw21.seed", "", nil, func(data *domain.Bootstrap) error {
		for i := range 75 {
			data.Projects = append(data.Projects, domain.Project{
				ID: fmt.Sprintf("sw21-identity-project-%d", i), SlugID: fmt.Sprintf("sw21-old-%d", i),
				Name:       "Private identity fixture",
				IssueCount: 97, Creator: &subject, TeamIDs: []string{"sw21-private"},
			})
		}
		data.Projects[len(data.Projects)-1].Description = strings.Repeat("legal unrelated body ", 15000)
		data.Settings["sw21-identity-needle"] = map[string]any{"hiddenActor": subject.ID, "count": 99}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resident, ok := probe.storedWorkspace(workspace)
	if !ok || resident.Workspace.ID != f.data.Workspace.ID || len(resident.Projects) != 0 || len(resident.Users) != 0 || len(resident.Settings) != 0 {
		t.Fatal("personal-data probe must retain only core and policy")
	}
	return f, probe, subject
}

func scaleP21SW21MetadataHash(t *testing.T, repo *SQLiteStore, workspace string) [32]byte {
	t.Helper()
	hash := sha256.New()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var field, key string
		var order int64
		var raw []byte
		if err := rows.Scan(&field, &key, &order, &raw); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00", field, key, order)
		_, _ = hash.Write(raw)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

func TestScaleP21SW21PersonalErasureUsesResidentIdentity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, probe, subject := scaleP21SW21PersonalSeed(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano)
	actionID := idgen.New("sw21-action-")
	notificationID := idgen.New("sw21-notification-")
	deliveryID := idgen.New("sw21-delivery-")
	unrelatedID := idgen.New("sw21-retained-notification-")
	foreign, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW21 foreign", idgen.New("sw21-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey); err != nil {
			t.Errorf("clean up owned foreign workspace: %v", err)
		}
	})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) VALUES(?,?,'member','active',?)`, foreign.Workspace.ID, subject.ID, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id) VALUES(?,'notification','',?,?,?,?)`, workspace, unrelatedID, stamp, []byte(`{"id":"retained","actor":{"id":"hidden"},"count":99}`), f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	metadata := scaleP21SW21MetadataHash(t, f.SQLiteStore, workspace)
	reset := func() {
		t.Helper()
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM workspace_departures WHERE workspace_id=? AND user_id=?`, []any{workspaceID, subject.ID}},
			{`INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) VALUES(?,?,'member','active',?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role='member',status='active',joined_at=excluded.joined_at`, []any{workspaceID, subject.ID, stamp}},
			{`INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,? ,?,'member',?) ON CONFLICT(workspace_id,team_id,user_id) DO UPDATE SET role='member'`, []any{workspaceID, "sw21-private", subject.ID, stamp}},
			{`INSERT INTO search_history(user_id,workspace_id,query,last_used_at) VALUES(?,?,?,?) ON CONFLICT(user_id,workspace_id,query) DO UPDATE SET last_used_at=excluded.last_used_at`, []any{subject.ID, workspaceID, "sw21-owned-query", stamp}},
			{`INSERT INTO recently_viewed(user_id,workspace_id,resource_type,resource_id,last_viewed_at) VALUES(?,?,'project','sw21-old-0',?) ON CONFLICT(user_id,workspace_id,resource_type,resource_id) DO UPDATE SET last_viewed_at=excluded.last_viewed_at`, []any{subject.ID, workspaceID, stamp}},
			{`INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id) VALUES(?,'notification','',?,?,?,?) ON CONFLICT(workspace_key,kind,resource_id,id) DO UPDATE SET owner_id=excluded.owner_id`, []any{workspace, notificationID, stamp, []byte(`{"id":"owned"}`), subject.ID}},
			{`INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id) VALUES(?,'delivery','',?,?,?,?) ON CONFLICT(workspace_key,kind,resource_id,id) DO UPDATE SET owner_id=excluded.owner_id`, []any{workspace, deliveryID, stamp, []byte(`{"id":"owned-delivery"}`), subject.ID}},
		} {
			if _, err := f.db.ExecContext(t.Context(), statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.PutChatAction(t.Context(), domain.ChatAction{ID: actionID, WorkspaceKey: workspace, ConnectionID: "sw21-owned-connection", UserID: subject.ID, Payload: json.RawMessage(`{"pending":true}`), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		Events, Realtime, Access []any
		Counts                   []int
		Role                     string
	}
	// This is a trusted storage operation, not a request authorization boundary.
	// Its actor affects publication only; team membership and credentials cannot
	// alter the target workspace's identity or expand its deletion scope.
	for _, actor := range []struct {
		name string
		user domain.User
	}{
		{"workspace-owner", f.data.Viewer},
		{"subject", subject},
		{"unlinked-actor", domain.User{ID: idgen.New("sw21-unlinked-"), Name: "Unlinked", Active: true}},
		{"foreign-actor", domain.User{ID: idgen.New("sw21-foreign-"), Name: "Foreign", Active: true}},
	} {
		t.Run(actor.name, func(t *testing.T) {
			var outcomes []result
			for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
				reset()
				got := result{}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					got.Events = append(got.Events, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues, event.Snapshot})
				})
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
				})
				source.SetAccountAccessSink(func(userID string) { got.Access = append(got.Access, userID) })
				if err := source.ErasePersonFromWorkspace(ContextWithActor(t.Context(), actor.user), workspace, subject.ID); err != nil {
					t.Fatal(err)
				}
				for _, query := range []string{
					`SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND user_id=?`,
					`SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND user_id=?`,
					`SELECT COUNT(*) FROM search_history WHERE workspace_id=? AND user_id=?`,
					`SELECT COUNT(*) FROM recently_viewed WHERE workspace_id=? AND user_id=?`,
				} {
					var count int
					if err := f.db.QueryRowContext(t.Context(), query, workspaceID, subject.ID).Scan(&count); err != nil {
						t.Fatal(err)
					}
					got.Counts = append(got.Counts, count)
				}
				for _, query := range []string{
					`SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND owner_id=?`,
					`SELECT COUNT(*) FROM chat_actions WHERE workspace_key=? AND user_id=?`,
				} {
					var count int
					if err := f.db.QueryRowContext(t.Context(), query, workspace, subject.ID).Scan(&count); err != nil {
						t.Fatal(err)
					}
					got.Counts = append(got.Counts, count)
				}
				if err := f.db.QueryRowContext(t.Context(), `SELECT role FROM workspace_departures WHERE workspace_id=? AND user_id=?`, workspaceID, subject.ID).Scan(&got.Role); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Counts, []int{0, 0, 0, 0, 0, 0}) || got.Role != DepartureRoleErased || len(got.Events) != 1 || len(got.Realtime) != 1 || !reflect.DeepEqual(got.Access, []any{subject.ID}) {
					t.Fatalf("erasure scope or publication changed: %+v", got)
				}
				if scaleP21SW21MetadataHash(t, f.SQLiteStore, workspace) != metadata {
					t.Fatal("identity-only erasure changed unrelated canonical metadata")
				}
				if _, status, err := f.WorkspaceRole(t.Context(), foreign.Workspace.ID, subject.ID); err != nil || status != "active" {
					t.Fatal("erasure escaped the target workspace", err)
				}
				var retained []byte
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_content_records WHERE workspace_key=? AND id=?`, workspace, unrelatedID).Scan(&retained); err != nil || !bytes.Equal(retained, []byte(`{"id":"retained","actor":{"id":"hidden"},"count":99}`)) {
					t.Fatal("erasure changed another recipient's notification", err)
				}
				outcomes = append(outcomes, got)
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("personal erasure representation differential: legacy=%+v bounded=%+v", outcomes[0], outcomes[1])
			}
		})
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		if err := source.ErasePersonFromWorkspace(t.Context(), workspace+"-missing", subject.ID); !errors.Is(err, ErrAuthForbidden) {
			t.Fatalf("missing workspace erasure=%v", err)
		}
		reset()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := source.ErasePersonFromWorkspace(ctx, workspace, subject.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled erasure=%v", err)
		}
		if _, status, err := f.WorkspaceRole(t.Context(), workspaceID, subject.ID); err != nil || status != "active" {
			t.Fatal("cancelled erasure changed membership", err)
		}
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("identity-only personal erasure decoded metadata or leaked work")
	}
}

func TestScaleP21SW21PersonalLeakCheckUsesResidentIdentity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, probe, subject := scaleP21SW21PersonalSeed(t)
	workspace := f.data.Workspace.URLKey
	before := scaleP21SW21MetadataHash(t, f.SQLiteStore, workspace)
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		source.SetRealtimeSink(func(string, domain.RealtimeEvent) { t.Error("leak check published realtime") })
		source.SetWebhookSink(func(string, domain.DomainEvent) { t.Error("leak check published webhook") })
	}
	t.Cleanup(func() {
		f.SetRealtimeSink(nil)
		f.SetWebhookSink(nil)
		probe.SetRealtimeSink(nil)
		probe.SetWebhookSink(nil)
	})
	for _, inputs := range [][]string{
		{subject.ID},
		{"sw21-identity-needle"},
		{"%_!'", "missing personal text"},
		{"", "  ", subject.ID, subject.ID},
	} {
		left := f.PersonalDataLeaks(t.Context(), workspace, inputs...)
		right := probe.PersonalDataLeaks(t.Context(), workspace, inputs...)
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("leak detector changed: %v versus %v", left, right)
		}
		if reflect.DeepEqual(inputs, []string{subject.ID}) && len(left) == 0 {
			t.Fatal("known subject identity copies were not found")
		}
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		if leaks := source.PersonalDataLeaks(t.Context(), workspace+"-missing", subject.ID); leaks != nil {
			t.Fatal("missing workspace unexpectedly has leaks")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if !reflect.DeepEqual(f.PersonalDataLeaks(ctx, workspace, subject.ID), probe.PersonalDataLeaks(ctx, workspace, subject.ID)) {
			t.Fatal("cancelled leak check changed error-as-leak behavior")
		}
	}
	if scaleP21SW21MetadataHash(t, f.SQLiteStore, workspace) != before || probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("leak check changed canonical data, decoded metadata or leaked work")
	}
}
