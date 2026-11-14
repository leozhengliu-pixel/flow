//go:build loadtest

package store

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW38PublicationSQLBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	var boundedStatements int64
	for _, size := range []int{8 << 20, 14 << 20} {
		t.Run(strconv.Itoa(size/(1<<20)), func(t *testing.T) {
			f := scaleP09Database(t)
			probe, err := f.EnableScaleP01Probe(DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL")})
			if err != nil {
				t.Fatal(err)
			}
			workspace, target := f.data.Workspace.URLKey, idgen.New("sw38-sql-")
			for _, bounded := range []bool{false, true} {
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings", "documents", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Settings["sw38-target"] = "before"
					data.Documents = []domain.Document{{ID: "document-sw38-sql", ContentData: map[string]any{"id": target, "body": strings.Repeat("\x00", size)}}}
					data.Webhooks = []domain.Webhook{{ID: "hook-sw38", Enabled: true, URL: "https://example.test/sw38"}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				repo := f.SQLiteStore
				if bounded {
					repo, err = f.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(repo.releaseMetadataLogicalSources)
				}
				var webhook, realtime bool
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = len(event.Snapshot) > size })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = len(event.Payload) > size })
				before := probe.SQLSnapshot()
				err = repo.MutateWorkspaceScoped(t.Context(), workspace, "application_policy.updated", target, nil,
					MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw38-target"}}}, func(data *domain.Bootstrap) error {
						data.Settings["sw38-target"] = "after"
						return nil
					})
				after := probe.SQLSnapshot()
				if err != nil || !webhook || !realtime {
					t.Fatalf("body=%d bounded=%t publication webhook=%t realtime=%t err=%v", size, bounded, webhook, realtime, err)
				}
				statements := after.Statements - before.Statements
				t.Logf("body=%d bounded=%t statements=%d reads=%d writes=%d", size, bounded, statements, after.Reads-before.Reads, after.Writes-before.Writes)
				if bounded {
					if boundedStatements > 0 && statements != boundedStatements {
						t.Fatalf("SQL count grows with winner size: %d/%d", boundedStatements, statements)
					}
					boundedStatements = statements
				}
			}
		})
	}
}
