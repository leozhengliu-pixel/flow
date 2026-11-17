package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SW295: the application webhook reconcile (the delivery tick's
// reconcileAllApplicationWebhooks and dispatchWebhookEvent's per-workspace
// reconcile on application, OAuth and team lifecycle events) and its OAuth
// application index on the legacy store, a warm bounded view and a bounded
// view loaded from SQL. Managed webhook rows, queued revocation and
// permission-change deliveries, domain events and publications must agree;
// bounded, the installed image must equal a reload.

// sw295Normalize orders what the original writes in map order: managed hooks
// it creates in one reconcile take collection positions in map order.
func sw295Normalize(outcome scaleP21SW294Outcome) scaleP21SW294Outcome {
	var hooks [][]any
	rows := outcome.State.Metadata[:0:0]
	for _, row := range outcome.State.Metadata {
		if row[0] == "webhooks" {
			hooks = append(hooks, []any{row[0], row[1], "<order>", row[3]})
			continue
		}
		rows = append(rows, row)
	}
	slices.SortFunc(hooks, func(a, b []any) int { return strings.Compare(fmt.Sprint(a[1]), fmt.Sprint(b[1])) })
	outcome.State.Metadata = append(rows, hooks...)
	var reloaded map[string]json.RawMessage
	if json.Unmarshal(outcome.Reloaded, &reloaded) == nil {
		var items []map[string]any
		if json.Unmarshal(reloaded["webhooks"], &items) == nil {
			slices.SortFunc(items, func(a, b map[string]any) int { return strings.Compare(fmt.Sprint(a["id"]), fmt.Sprint(b["id"])) })
			reloaded["webhooks"], _ = json.Marshal(items)
		}
		outcome.Reloaded, _ = json.Marshal(reloaded)
	}
	return outcome
}

func (f scaleP21TeamCreateFixture) sw295Differential(t *testing.T, name string, setup func(*testing.T), call func(*server) string, interleaved bool) []scaleP21SW294Outcome {
	t.Helper()
	f.scaleP21TriageWriterFixture.prepare = f.sw294Prepare(setup)
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	var normalized []string
	var outcomes []scaleP21SW294Outcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.sw294Run(t, source, call)
		got = sw295Normalize(got)
		if interleaved {
			got.State.Events, got.Webhooks, got.Realtime = nil, nil, nil
			delete(got.Extra, "deliveries")
		}
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// sw295Hooks lists the workspace's managed hook rows.
func (f scaleP21TeamCreateFixture) sw295Hooks(t *testing.T) string {
	t.Helper()
	var hooks []string
	for _, row := range f.rows(t, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='webhooks' ORDER BY record_key`, f.data.Workspace.URLKey) {
		hooks = append(hooks, fmt.Sprint(row...))
	}
	return strings.Join(hooks, "\n")
}

// sw295Settle waits for dispatchWebhookEvent's background reconcile: the
// managed hooks changed and the reconcile finished its queued deliveries.
func (f scaleP21TeamCreateFixture) sw295Settle(t *testing.T, before string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if f.sw295Hooks(t) != before && applicationWebhookReconcile.TryLock() {
			applicationWebhookReconcile.Unlock()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background application webhook reconcile did not run")
}

func TestScaleP21SW295ApplicationWebhookReconcileDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	// Queued deliveries stay queued: no background attempt races the state.
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	owner, admin, member := f.data.Viewer, f.principals[1].user, f.principals[2].user
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	revoked := at.Add(time.Hour)
	// A second workspace indexes one of the same client IDs with another
	// webhook URL; it holds no installation, grant or hook.
	other, err := f.repository.CreateWorkspace(store.ContextWithActor(t.Context(), owner), "SW295 other", idgen.New("sw295-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = f.repository.DeleteWorkspace(ctx, other.Workspace.URLKey)
	})
	if err := f.repository.MutateWorkspace(t.Context(), other.Workspace.URLKey, "scale_p21_sw295.other_app", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: "sw295-app-shadow", ClientID: "sw295-shared", Name: "Shared elsewhere", WebhookEnabled: true, WebhookURL: "https://hooks.example.test/elsewhere", WebhookResourceTypes: []string{"Issue"}, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	const many = 24
	app := func(client, url string, types ...string) domain.OAuthApplication {
		return domain.OAuthApplication{ID: "app-" + client, ClientID: client, Name: "App " + client, Scopes: []string{"read", "write"}, WebhookEnabled: url != "-", WebhookURL: strings.TrimPrefix(url, "-"), WebhookResourceTypes: types, CreatorID: owner.ID, RedirectURIs: []string{}, CreatedAt: at, UpdatedAt: at}
	}
	managed := func(client, url, name string, teams []string, types ...string) domain.Webhook {
		return domain.Webhook{ID: managedWebhookID(workspace, client), Name: name, URL: url, ApplicationID: "app-" + client, ApplicationClientID: client, ManagedBy: "application", ResourceTypes: types, TeamIDs: teams, TeamRestriction: "selected", Enabled: true, CreatorID: owner.ID, PayloadVersion: domain.WebhookPayloadVersion, SigningEnabled: true, CreatedAt: at, UpdatedAt: at}
	}
	seed := func(t *testing.T, apps int) {
		f.mutate(t, "scale_p21_sw295.seed", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications,
				app("sw295-installed", "https://hooks.example.test/installed", "Issue", "Comment", "PermissionChange"),
				app("sw295-disabled", "-https://hooks.example.test/disabled", "Issue"),
				app("sw295-empty-url", "", "Issue"),
				app("sw295-granted", "https://hooks.example.test/granted", "Issue", "OAuthApp", "permission_changes"),
				app("sw295-shared", "https://hooks.example.test/shared-here", "Issue"),
				app("sw295-without-realtime", "https://hooks.example.test/norealtime", "Issue"),
				app("sw295-inactive", "https://hooks.example.test/inactive", "Issue"),
			)
			for i := range apps {
				data.OAuthApplications = append(data.OAuthApplications, app(fmt.Sprintf("sw295-many-%02d", i), fmt.Sprintf("https://hooks.example.test/many/%02d", i), "Issue", "Project"))
			}
			data.OAuthAuthorizations = append(data.OAuthAuthorizations,
				domain.OAuthAuthorization{ID: "sw295-grant-member", ClientID: "sw295-granted", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw295-grant-admin", ClientID: "sw295-granted", UserID: admin.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw295-grant-revoked", ClientID: "sw295-revoked", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
				domain.OAuthAuthorization{ID: "sw295-grant-empty", ClientID: "", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw295-grant-shared", ClientID: "sw295-shared", UserID: owner.ID, Scopes: []string{"read"}, CreatedAt: at},
			)
			data.Teams = append(data.Teams,
				domain.Team{ID: "sw295-team-archived", Key: "SWA", Name: "Archived", ArchivedAt: &revoked},
				domain.Team{ID: "sw295-team-retired", Key: "SWR", Name: "Retired", RetiredAt: &revoked},
				domain.Team{ID: "sw295-team-private", Key: "SWP", Name: "Private", Private: true})
			data.Webhooks = append(data.Webhooks,
				domain.Webhook{ID: "sw295-plain", Name: "Plain", URL: "https://hooks.example.test/plain", Enabled: true, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				// Stale URL, name, types and teams: updated with a permission change.
				managed("sw295-installed", "https://hooks.example.test/old", "Old name", []string{"tm-parent"}, "issues"),
				// Authorized no longer: revoked, its deliveries cancelled.
				managed("sw295-revoked", "https://hooks.example.test/revoked", "Revoked", nil, "issues"),
				// Still authorized but its webhook is off: dropped, not revoked.
				managed("sw295-disabled", "https://hooks.example.test/disabled", "App sw295-disabled", nil, "issues"),
			)
			for i := range apps / 2 {
				// Half of the many applications already have current hooks.
				client := fmt.Sprintf("sw295-many-%02d", i)
				data.Webhooks = append(data.Webhooks, managed(client, fmt.Sprintf("https://hooks.example.test/many/%02d", i), "App "+client, []string{f.public}, "issues", "projects"))
			}
		})
		for _, client := range []string{"sw295-installed", "sw295-shared", "sw295-without-realtime", "sw295-inactive"} {
			installation := domain.ApplicationInstallation{ClientID: client, Name: "App " + client, InstalledBy: owner.ID, Scopes: []string{"read"}, TeamIDs: []string{f.public, f.private}, Active: client != "sw295-inactive", WithoutRealtime: client == "sw295-without-realtime"}
			if _, err := f.repository.InstallApplication(t.Context(), workspace, installation, ""); err != nil {
				t.Fatal(err)
			}
		}
		for i := range apps {
			installation := domain.ApplicationInstallation{ClientID: fmt.Sprintf("sw295-many-%02d", i), Name: "Many", InstalledBy: owner.ID, Scopes: []string{"read"}, TeamIDs: []string{f.public}, Active: true}
			if _, err := f.repository.InstallApplication(t.Context(), workspace, installation, ""); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.repository.EnsureBuiltinApplication(t.Context(), workspace, true); err != nil {
			t.Fatal(err)
		}
		// A queued delivery of the hook that will be revoked.
		if _, err := f.repository.EnqueueWebhookDelivery(t.Context(), domain.WebhookDelivery{ID: "whd_sw295_pending", WorkspaceKey: workspace, WebhookID: managedWebhookID(workspace, "sw295-revoked"), URL: "https://hooks.example.test/revoked", Kind: domain.WebhookDeliveryKindEvent, EventID: "sw295-event", EventType: "Issue", Action: "create", Payload: json.RawMessage(`{}`), MaxAttempts: 3}); err != nil {
			t.Fatal(err)
		}
	}
	reconcile := func(s *server) string {
		s.reconcileApplicationWebhooks(t.Context(), workspace)
		return "done"
	}
	cases := []scaleP21SW294Case{
		{name: "reconcile-workspace", setup: func(t *testing.T) { seed(t, 1) }, call: reconcile, check: func(t *testing.T, outcome scaleP21SW294Outcome) {
			raw := string(outcome.Installed)
			// sw295-shared takes the webhook URL of the last workspace in key order.
			shared := "hooks.example.test/shared-here"
			if other.Workspace.URLKey > workspace {
				shared = "hooks.example.test/elsewhere"
			}
			for _, want := range []string{managedWebhookID(workspace, "sw295-installed"), managedWebhookID(workspace, "sw295-granted"), shared} {
				if !strings.Contains(raw, want) {
					t.Fatalf("reconcile did not write %s: %s", want, raw)
				}
			}
			if !strings.Contains(fmt.Sprint(outcome.Extra["deliveries"]), "revoked") {
				t.Fatalf("revocation not queued: %v", outcome.Extra["deliveries"])
			}
		}},
		{name: "reconcile-many", setup: func(t *testing.T) { seed(t, many) }, call: reconcile},
		{name: "reconcile-all", setup: func(t *testing.T) { seed(t, many) }, call: func(s *server) string {
			s.reconcileAllApplicationWebhooks(t.Context())
			return "done"
		}},
		{name: "reconcile-unchanged", setup: func(t *testing.T) {
			seed(t, 4)
			(&server{store: f.repository}).reconcileApplicationWebhooks(t.Context(), workspace)
			f.exec(t, `DELETE FROM webhook_deliveries WHERE workspace_key=?`, workspace)
		}, call: reconcile, check: func(t *testing.T, outcome scaleP21SW294Outcome) {
			if len(outcome.State.Events) != 0 || string(outcome.Installed) != "{}" {
				t.Fatalf("an unchanged reconcile wrote: %v %s", outcome.State.Events, outcome.Installed)
			}
		}},
		{name: "reconcile-nothing-installed", call: reconcile},
	}
	for _, eventType := range []string{"application.updated", "application.uninstalled", "oauth_authorization.created", "oauth_authorization.revoked", "team.created", "team.archived", "oauth_application.deleted"} {
		cases = append(cases, scaleP21SW294Case{name: "dispatch-" + eventType, setup: func(t *testing.T) { seed(t, 3) }, call: func(s *server) string {
			before := f.sw295Hooks(t)
			s.dispatchWebhookEvent(workspace, domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: "sw295-aggregate", Payload: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()})
			f.sw295Settle(t, before)
			return "settled"
		}})
	}
	cases = append(cases, scaleP21SW294Case{name: "concurrent-reconciles-and-install", setup: func(t *testing.T) { seed(t, 6) }, call: func(s *server) string {
		var group sync.WaitGroup
		for range 4 {
			group.Add(1)
			go func() {
				defer group.Done()
				s.reconcileAllApplicationWebhooks(t.Context())
			}()
		}
		group.Add(1)
		go func() {
			defer group.Done()
			installation := domain.ApplicationInstallation{ClientID: "sw295-granted", Name: "Granted", InstalledBy: owner.ID, Scopes: []string{"read"}, TeamIDs: []string{f.public}, Active: true}
			if _, err := s.store.InstallApplication(t.Context(), workspace, installation, ""); err != nil {
				t.Error(err)
			}
		}()
		group.Wait()
		// The reconcile that follows converges whatever order they ran in.
		s.reconcileApplicationWebhooks(t.Context(), workspace)
		return "converged"
	}, interleaved: true})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcomes := f.sw295Differential(t, test.name, test.setup, test.call, test.interleaved)
			if test.check != nil {
				test.check(t, outcomes[0])
			}
		})
	}
	t.Run("event-gate", func(t *testing.T) {
		for _, eventType := range []string{"issue.created", "comment.created", "webhook.updated", "team_member.updated"} {
			if applicationWebhookLifecycleEvent(eventType) {
				t.Fatalf("%s reconciles application webhooks", eventType)
			}
		}
	})
}

// The OAuth application index (oauthApplicationsByClient): every installed
// workspace in key order, a client ID in several workspaces mapping to the
// last, missing client IDs absent, with the fields the reconcile reads.
func TestScaleP21SW295ApplicationWebhookClients(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	owner := f.data.Viewer
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	keys := []string{"sw295-index-a-" + idgen.New(""), "sw295-index-b-" + idgen.New("")}
	for index, key := range keys {
		created, err := f.repository.CreateWorkspace(store.ContextWithActor(t.Context(), owner), "SW295 index", key, "us")
		if err != nil {
			t.Fatal(err)
		}
		workspace := created.Workspace.URLKey
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			_ = f.repository.DeleteWorkspace(ctx, workspace)
		})
		if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21_sw295.index", "", nil, func(data *domain.Bootstrap) error {
			data.OAuthApplications = append(data.OAuthApplications,
				domain.OAuthApplication{ID: fmt.Sprint("sw295-index-shared-", index), ClientID: "sw295-index-shared", Name: fmt.Sprint("Shared ", index), ClientSecret: "hidden", Description: strings.Repeat("unread ", 2000), WebhookEnabled: index == 1, WebhookURL: fmt.Sprint("https://hooks.example.test/index/", index), WebhookResourceTypes: []string{"Issue", fmt.Sprint("Type", index)}, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: fmt.Sprint("sw295-index-own-", index), ClientID: fmt.Sprint("sw295-index-own-", index), Name: "Own", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				// Two entries of one client in one workspace: the later wins.
				domain.OAuthApplication{ID: fmt.Sprint("sw295-index-dup-a-", index), ClientID: "sw295-index-dup", Name: fmt.Sprint("Dup A ", index), CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: fmt.Sprint("sw295-index-dup-b-", index), ClientID: "sw295-index-dup", Name: fmt.Sprint("Dup B ", index), WebhookURL: "https://hooks.example.test/dup", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	project := func(apps map[string]domain.OAuthApplication) string {
		ids := make([]string, 0, len(apps))
		for id := range apps {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		var lines []string
		for _, id := range ids {
			app := apps[id]
			raw, _ := json.Marshal([]any{app.ID, app.ClientID, app.Name, app.WebhookEnabled, app.WebhookURL, app.WebhookResourceTypes})
			lines = append(lines, id+"\t"+string(raw))
		}
		return strings.Join(lines, "\n")
	}
	var want string
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		service := f.service(t, source)
		apps, release, ok := service.applicationWebhookClients(t.Context())
		if !ok {
			t.Fatal(source, "index refused")
		}
		got := project(apps)
		release()
		if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
			t.Fatal(source, "index leaked work")
		}
		if source == "legacy" {
			want = got
			if project(service.oauthApplicationsByClient()) != got {
				t.Fatal("legacy index changed")
			}
			for _, check := range []string{"sw295-index-shared\t[\"sw295-index-shared-1\"", "sw295-index-dup\t[\"sw295-index-dup-b-1\"", "sw295-index-own-0", "sw295-index-own-1"} {
				if !strings.Contains(got, check) {
					t.Fatalf("legacy index lacks %s:\n%s", check, got)
				}
			}
			if strings.Contains(got, "sw295-index-missing") {
				t.Fatal("missing client indexed")
			}
			continue
		}
		if got != want {
			t.Fatalf("%s index changed: %s", source, scaleP21M2Diff(want, got))
		}
	}
}
