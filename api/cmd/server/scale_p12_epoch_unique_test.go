package main

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP12EpochTwoThousandCredentialContextsRemainReusable(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		teams := make([]string, 11)
		for i := range teams {
			teams[i] = idgen.New("p12-epoch-team-")
		}
		keys := make([]domain.APIKey, 2000)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p12.epoch_credentials", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range teams {
				data.Teams = append(data.Teams, domain.Team{ID: team, Key: "P12E"})
				data.TeamSettings[team] = domain.TeamSettings{TeamID: team, Access: "public"}
			}
			for i := range data.Issues {
				if data.Issues[i].ID == f.open.ID {
					data.Issues[i].SuggestedTeamIDs = slices.Clone(teams)
				}
			}
			for i := range keys {
				key := domain.APIKey{ID: idgen.New("p12-epoch-key-"), CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}}
				for bit, team := range teams {
					if i&(1<<bit) != 0 {
						key.TeamIDs = append(key.TeamIDs, team)
					}
				}
				keys[i] = key
				data.APIKeys = append(data.APIKeys, key)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		event := domain.RealtimeEvent{ID: "p12-epoch-unique", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
		deliver := func(key domain.APIKey, observe func(string)) []byte {
			t.Helper()
			request := (scaleP12Principal{user: f.data.Viewer, key: &key}).request(f.data.Workspace.URLKey)
			if observe != nil {
				request = request.WithContext(store.WithSQLStatementObserver(request.Context(), observe))
			}
			frame, _, err := f.service.sharedEventFrame(request, event)
			defer frame.Close()
			if err != nil || !frame.Visible() {
				t.Fatalf("unique context: visible=%v err=%v", frame.Visible(), err)
			}
			return frame.encoded
		}
		for _, key := range keys {
			deliver(key, nil)
		}
		for range len(keys) {
			_, stop := f.service.realtime.subscribe(f.data.Workspace.URLKey)
			t.Cleanup(stop)
		}
		var resolves, credentials, statements, epochs atomic.Int64
		observe := func(query string) {
			statements.Add(1)
			if strings.Contains(query, "FROM workspace_memberships m JOIN auth_users") {
				resolves.Add(1)
			}
			if strings.Contains(query, "field='apiKeys'") {
				credentials.Add(1)
			}
			if strings.Contains(query, "workspace_authz_epochs") {
				epochs.Add(1)
			}
		}
		event.ID += "-next"
		seen := map[string]bool{}
		var seenMu sync.Mutex
		var workers sync.WaitGroup
		start := make(chan struct{})
		for _, key := range keys {
			workers.Go(func() {
				<-start
				raw := deliver(key, observe)
				seenMu.Lock()
				seen[string(raw)] = true
				seenMu.Unlock()
			})
		}
		close(start)
		workers.Wait()
		cache := &f.service.realtime.rules.accesses
		cache.mu.Lock()
		entries, retained := len(cache.entries), cache.bytes
		cache.mu.Unlock()
		if resolves.Load() != 0 || credentials.Load() != 0 || len(seen) != 2000 || entries != 2000 || retained > eventAccessCacheBytes || statements.Load() > 32 || epochs.Load() != 1 {
			t.Fatalf("resolves=%d credential_reads=%d distinct=%d entries=%d retained=%d SQL=%d epochs=%d", resolves.Load(), credentials.Load(), len(seen), entries, retained, statements.Load(), epochs.Load())
		}
		t.Logf("2000 credential contexts: total SQL/event=%d principal resolves=%d retained access bytes=%d", statements.Load(), resolves.Load(), retained)
	}
}
