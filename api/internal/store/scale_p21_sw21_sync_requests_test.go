package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW21TypedSyncRequestVisitor(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	scaleP21SW21SyncManifest(t, f)
	raws := []string{
		`{"id":"sparse","applicationId":"target","unknown":{"private":"never output"}}`,
		`{"id":"duplicates","applicationId":"wrong","applicationId":"target","groupName":"first","groupName":"last"}`,
		`{"id":"duplicate-away","applicationId":"target","applicationId":"wrong","groupName":"wrong"}`,
		`{"id":"case","ApplicationID":"target","GroupName":"case-insensitive","Status":"pending"}`,
		`{"id":"escaped","\u0061pplicationId":"target","groupName":"escaped key"}`,
		`{"id":"null-after","applicationId":"target","applicationId":null,"groupName":null}`,
		`{"id":"missing"}`,
		`{"id":"null","applicationId":null}`,
		`null`,
		`{"id":"literal","applicationId":"%_!","createdAt":"2026-01-02T03:04:05Z","reviewedAt":"2026-01-03T04:05:06Z"}`,
	}
	large, _ := json.Marshal(domain.OAuthSyncGroupRequest{ID: "large", ApplicationID: "target", GroupName: strings.Repeat("<large legal body> ", 40000)})
	raws = append(raws, string(large))
	for i := range 76 {
		raw, _ := json.Marshal(domain.OAuthSyncGroupRequest{ID: fmt.Sprintf("unrelated-%d", i), ApplicationID: "unrelated", GroupName: "Keep"})
		raws = append(raws, string(raw))
	}
	// A plainly unrelated body above the selected-record cap remains in SQL.
	unrelatedLarge, _ := json.Marshal(domain.OAuthSyncGroupRequest{ID: "unrelated-large", ApplicationID: "unrelated", GroupName: strings.Repeat("large irrelevant ", 350000)})
	raws = append(raws, string(unrelatedLarge))
	for i, raw := range raws {
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'oauthSyncGroupRequests',?,?,?)`, workspace, fmt.Sprintf("sw21-key-%03d", i), i, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"target", "", "%_!", "missing-target"} {
		t.Run(selector, func(t *testing.T) {
			want := []domain.OAuthSyncGroupRequest{}
			for _, raw := range raws {
				var item domain.OAuthSyncGroupRequest
				if err := json.Unmarshal([]byte(raw), &item); err != nil {
					t.Fatal(err)
				}
				if item.ApplicationID == selector {
					want = append(want, item)
				}
			}
			got := []domain.OAuthSyncGroupRequest{}
			err := probe.VisitOAuthSyncGroupRequests(t.Context(), workspace, selector, func(ctx context.Context, item domain.OAuthSyncGroupRequest) error {
				lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
				if !ok || !lease.live || lease.bytes > scaleP21SW21RecordWorkMax || lease.bytes < scaleP21SW21RecordOverhead || probe.metadataResidency.work.used < lease.bytes+256<<10 {
					t.Fatal("record encoding did not retain its adaptive admission lease")
				}
				encoded, err := json.Marshal(item)
				if err != nil {
					return err
				}
				var typed domain.OAuthSyncGroupRequest
				if err := json.Unmarshal(encoded, &typed); err != nil {
					return err
				}
				got = append(got, typed)
				return nil
			})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("typed visitor selector=%q differs: got=%d want=%d err=%v", selector, len(got), len(want), err)
			}
			if probe.metadataResidency.work.used != 0 || probe.metadataResidency.work.waiting != 0 {
				t.Fatal("visitor retained work after record encoding")
			}
		})
	}
	for _, callbackErr := range []error{context.Canceled, errors.New("consumer failed")} {
		calls := 0
		err := probe.VisitOAuthSyncGroupRequests(t.Context(), workspace, "target", func(context.Context, domain.OAuthSyncGroupRequest) error {
			calls++
			return callbackErr
		})
		if !errors.Is(err, callbackErr) || calls != 1 || probe.metadataResidency.work.used != 0 {
			t.Fatalf("callback failure calls=%d err=%v", calls, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := probe.VisitOAuthSyncGroupRequests(cancelled, workspace, "target", func(context.Context, domain.OAuthSyncGroupRequest) error {
		t.Fatal("cancelled visitor reached callback")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled visit=%v", err)
	}
	if err := probe.VisitOAuthSyncGroupRequests(t.Context(), workspace+"-foreign", "target", func(context.Context, domain.OAuthSyncGroupRequest) error {
		t.Fatal("visitor escaped workspace")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	maximum := (scaleP21SW21RecordWorkMax-scaleP21SW21RecordOverhead)/scaleP21SW21RecordExpansion + 1
	over, _ := json.Marshal(domain.OAuthSyncGroupRequest{ID: "over", ApplicationID: "over-budget", GroupName: strings.Repeat("x", maximum)})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'oauthSyncGroupRequests','sw21-over',999,?)`, workspace, over); err != nil {
		t.Fatal(err)
	}
	if err := probe.VisitOAuthSyncGroupRequests(t.Context(), workspace, "over-budget", func(context.Context, domain.OAuthSyncGroupRequest) error {
		t.Fatal("oversized selected record reached callback")
		return nil
	}); !errors.Is(err, ErrMetadataWorkBudget) || probe.metadataResidency.work.used != 0 {
		t.Fatalf("oversized record budget=%v", err)
	}
	if err := probe.VisitOAuthSyncGroupRequests(t.Context(), workspace, "target", func(context.Context, domain.OAuthSyncGroupRequest) error { return nil }); err != nil {
		t.Fatal("visitor failed after budget rejection", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("typed visitor decoded complete workspace metadata")
	}
}

func TestScaleP21SW21EscapedKeyCandidatePattern(t *testing.T) {
	mysql := &SQLiteStore{dialect: "mysql"}
	_, args := mysql.scaleP21SW21ApplicationCandidate("target")
	pattern, err := regexp.Compile(args[1].(string))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		raw  string
		want bool
	}{
		{`{"id":"x","applicationId":"other","groupName":"escaped \\n and \\"}`, false},
		{`{"id":"x","\u0061pplicationId":"target"}`, true},
		{`{ "application\u0049d" : "target"}`, true},
		{`{"applicationId":"other","groupName":"quoted {\"applicationId\":\"target\"}"}`, false},
		{`{"applicationId":"target","unknown":{"\u0061pplicationId":"other"}}`, true},
	} {
		if got := pattern.MatchString(item.raw); got != item.want {
			t.Fatalf("escaped object-key candidate=%v want=%v raw=%s", got, item.want, item.raw)
		}
	}
}

func TestScaleP21SW21SyncRequestSnapshot(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	scaleP21SW21SyncManifest(t, f)
	for i := range 55 {
		raw, _ := json.Marshal(domain.OAuthSyncGroupRequest{ID: fmt.Sprintf("snapshot-%d", i), ApplicationID: "target", GroupName: "before"})
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'oauthSyncGroupRequests',?,?,?)`, workspace, fmt.Sprintf("sw21-snapshot-%03d", i), i, raw); err != nil {
			t.Fatal(err)
		}
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = probe.VisitOAuthSyncGroupRequests(t.Context(), workspace, "target", func(_ context.Context, item domain.OAuthSyncGroupRequest) error {
		count++
		if count == 1 {
			raw, _ := json.Marshal(domain.OAuthSyncGroupRequest{ID: "snapshot-54", ApplicationID: "other", GroupName: "after"})
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='oauthSyncGroupRequests' AND record_key='sw21-snapshot-054'`, raw, workspace); err != nil {
				return err
			}
		}
		if item.GroupName != "before" {
			t.Fatal("one compatibility list crossed SQL snapshots")
		}
		return nil
	})
	if err != nil || count != 55 || probe.metadataResidency.work.used != 0 {
		t.Fatalf("snapshot list count=%d err=%v", count, err)
	}
}

func scaleP21SW21SyncManifest(t *testing.T, f scaleP09Fixture) {
	t.Helper()
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthSyncGroupRequests"), f.data.Workspace.URLKey, "scale_p21_sw21.manifest", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthSyncGroupRequests = []domain.OAuthSyncGroupRequest{{ID: "sw21-manifest", ApplicationID: "unused"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthSyncGroupRequests'`, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
}
