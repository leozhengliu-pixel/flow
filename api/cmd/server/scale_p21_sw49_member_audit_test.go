package main

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW49MemberAuditLargeAggregate(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		targetID := idgen.New("usr_sw49_audit_")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "users"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Users = append(data.Users,
				domain.User{ID: targetID, Name: strings.Repeat("target ", 50000) + "\x00warm", Active: true},
				domain.User{ID: targetID + "-unrelated", Name: strings.Repeat("unrelated ", 100000), Active: true})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		userHash := scaleP21M3Fingerprint(t, repo, workspace, "users")
		type outcome struct {
			Audits   []domain.AuditLogEntry
			Events   []domain.DomainEvent
			Realtime []domain.RealtimeEvent
		}
		f.matrix(t, func(t *testing.T, principal domain.User, key *domain.APIKey) {
			for _, cancelled := range []bool{false, true} {
				var results []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					got := outcome{}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.Events = append(got.Events, event) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = append(got.Realtime, event) })
					var queries atomic.Int64
					r := f.literalRequest(principal, key, nil)
					r.SetPathValue("userId", targetID)
					r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { queries.Add(1) }))
					if cancelled {
						ctx, cancel := context.WithCancel(r.Context())
						cancel()
						r = r.WithContext(ctx)
					}
					started := time.Now().UTC()
					(&server{store: source}).recordMemberStatusAudit(r, "sw49.member")
					t.Logf("large member aggregate cancelled=%v bounded=%v primary SQL=%d", cancelled, source == f.probe, queries.Load())
					var err error
					got.Audits, err = repo.ListAuditEntries(t.Context(), workspaceID, store.AuditQuery{Since: started})
					if err != nil {
						t.Fatal(err)
					}
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					expected := 1
					if cancelled {
						expected = 0
					}
					if len(got.Audits) != expected || len(got.Events) != expected || len(got.Realtime) != expected {
						t.Fatalf("large target is an event source, not a callback input: bounded=%v audits/events/realtime=%d/%d/%d want=%d", source == f.probe, len(got.Audits), len(got.Events), len(got.Realtime), expected)
					}
					for i := range got.Audits {
						if got.Audits[i].Actor.ID != principal.ID || got.Audits[i].ResourceID != targetID {
							t.Fatal("member audit attribution changed")
						}
						got.Audits[i].ID, got.Audits[i].Hash, got.Audits[i].Seq, got.Audits[i].CreatedAt = "", "", 0, time.Time{}
					}
					for i := range got.Events {
						got.Events[i].ID, got.Events[i].CreatedAt = "", time.Time{}
					}
					for i := range got.Realtime {
						got.Realtime[i].ID, got.Realtime[i].Seq, got.Realtime[i].CreatedAt = "", 0, time.Time{}
					}
					results = append(results, got)
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("large aggregate semantics changed: %s", scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 || scaleP21M3Fingerprint(t, repo, workspace, "users") != userHash {
			t.Fatal("member audit decoded full metadata or rewrote user rows")
		}
	}
}
