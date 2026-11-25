package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21WebhookJSONView(t *testing.T, data domain.Bootstrap) *SQLiteStore {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{
		workspaces: map[string]domain.Bootstrap{"webhook-json": {}}, lastWorkspaceKey: "webhook-json",
		metadataResidency: newMetadataResidency(),
	}
	view.installMetadataLogicalSource("webhook-json", source)
	t.Cleanup(view.releaseMetadataLogicalSources)
	return view
}

func scaleP21WebhookJSONTeams(object map[string]any, teams []string) bool {
	if id, _ := object["teamId"].(string); id != "" {
		return slices.Contains(teams, id)
	}
	if team, ok := object["team"].(map[string]any); ok {
		if id, _ := team["id"].(string); id != "" {
			return slices.Contains(teams, id)
		}
	}
	if list, ok := object["teamIds"].([]any); ok {
		for _, item := range list {
			if id, _ := item.(string); id != "" && slices.Contains(teams, id) {
				return true
			}
		}
	}
	return false
}

func scaleP21WebhookJSONLiteral(t *testing.T, data domain.Bootstrap, id string, teams []string) bool {
	t.Helper()
	// WorkspaceMetadata discards the five issue/content families, deep-clones
	// metadata, and otherwise retains its installed native values.
	data.Issues, data.Activities, data.Comments, data.Notifications, data.NotificationDeliveries = nil, nil, nil, nil, nil
	data = cloneBootstrap(data)
	var root any
	raw, err := json.Marshal(data)
	if err != nil || json.Unmarshal(raw, &root) != nil || id == "" {
		return false
	}
	var find func(any) map[string]any
	find = func(value any) map[string]any {
		switch value := value.(type) {
		case map[string]any:
			if candidate, _ := value["id"].(string); candidate == id {
				return value
			}
			for _, child := range value {
				if found := find(child); found != nil {
					return found
				}
			}
		case []any:
			for _, child := range value {
				if found := find(child); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return scaleP21WebhookJSONTeams(find(root), teams)
}

func TestScaleP21WebhookJSONDifferential(t *testing.T) {
	const target = "event-webhook-json"
	object := func(team string) map[string]any { return map[string]any{"id": target, "teamId": team} }
	tests := []struct {
		name string
		data domain.Bootstrap
		id   string
	}{
		{"own-before-nested", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "allowed", "nested": object("hidden")}}, target},
		{"array-first-nested-before-later-root", domain.Bootstrap{Settings: map[string]any{"list": []any{map[string]any{"nested": object("hidden")}, object("allowed")}}}, target},
		{"typed-map", domain.Bootstrap{Settings: map[string]any{"entity": map[string]string{"id": target, "teamId": "allowed"}}}, target},
		{"raw-json", domain.Bootstrap{ProjectDisplayDefault: json.RawMessage(`{"id":"event-webhook-json","teamId":"allowed"}`)}, target},
		{"raw-last-key", domain.Bootstrap{ProjectDisplayDefault: json.RawMessage(`{"id":"old","id":"event-webhook-json","teamId":"hidden","teamId":"allowed"}`)}, target},
		{"omitted-empty", domain.Bootstrap{Settings: map[string]any{"entity": struct {
			ID    string `json:"id"`
			Team  string `json:"teamId,omitempty"`
			Teams []any  `json:"teamIds"`
		}{target, "", []any{"allowed"}}}}, target},
		{"id-json-tag", domain.Bootstrap{Settings: map[string]any{"entity": struct {
			Other string `json:"id"`
			Team  string `json:"teamId"`
		}{target, "allowed"}}}, target},
		{"team-object-before-teams", domain.Bootstrap{Settings: map[string]any{"id": target, "team": map[string]string{"id": "hidden"}, "teamIds": []any{"allowed"}}}, target},
		{"direct-team-before-object", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "hidden", "team": map[string]string{"id": "allowed"}}}, target},
		{"empty-team-falls-through", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "", "teamIds": []any{nil, 5, "", "allowed"}}}, target},
		{"quoted-id", domain.Bootstrap{Settings: map[string]any{"entity": struct {
			ID   int    `json:"id,string"`
			Team string `json:"teamId"`
		}{37, "allowed"}}}, "37"},
		{"quoted-string", domain.Bootstrap{Settings: map[string]any{"entity": struct {
			ID   string `json:"id,string"`
			Team string `json:"teamId"`
		}{target, "allowed"}}}, `"event-webhook-json"`},
		{"utf8-replacement", domain.Bootstrap{Settings: map[string]any{"id": "event\xff\xfe", "teamId": "allowed"}}, "event\ufffd\ufffd"},
		{"utf8-key-last-wins", domain.Bootstrap{Settings: map[string]any{"a\xff": object("hidden"), "a\xfe": object("allowed")}}, target},
		{"integer-keys", domain.Bootstrap{Settings: map[string]any{"entity": map[int]any{4: object("allowed")}}}, target},
		{"supported-activity-codec", domain.Bootstrap{Settings: map[string]any{"entity": domain.ActivityMetadata{"id": target, "teamId": "allowed", "addedLabelIds": `["old","new"]`}}}, target},
		{"supported-tool-codec", domain.Bootstrap{Settings: map[string]any{"entity": domain.AgentToolCall{ID: "different", Arguments: json.RawMessage(`{"id":"event-webhook-json","teamId":"allowed"}`), Result: json.RawMessage(`{"partial"`)}}}, target},
		{"bytes-are-not-raw-json", domain.Bootstrap{Settings: map[string]any{"entity": []byte(`{"id":"event-webhook-json","teamId":"allowed"}`)}}, target},
		{"nil-unsupported-map-key", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "allowed", "invalid": map[bool]string(nil)}}, target},
		{"unrelated-invalid-time", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "allowed", "invalid": time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}, target},
		{"unrelated-invalid-float", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "allowed", "invalid": math.NaN()}}, target},
		{"unrelated-invalid-raw", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "allowed"}, ProjectDisplayDefault: json.RawMessage(`{"broken"`)}, target},
		{"unrelated-overflow-number", domain.Bootstrap{Settings: map[string]any{"id": target, "teamId": "allowed", "invalid": json.Number("1e999")}}, target},
		{"heavy-content-excluded", domain.Bootstrap{Comments: map[string][]domain.Comment{"parent": {{ID: target, BodyData: object("allowed")}}}}, target},
		{"missing", domain.Bootstrap{Settings: object("allowed")}, "missing"},
		{"empty-id", domain.Bootstrap{Settings: map[string]any{"id": "", "teamId": "allowed"}}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := scaleP21WebhookJSONView(t, test.data)
			read, found, err := view.BeginWebhookAggregateRead(t.Context(), "")
			if err != nil || !found {
				t.Fatal(found, err)
			}
			for _, teams := range [][]string{nil, {"allowed"}, {"hidden"}, {"allowed", "hidden"}} {
				want := scaleP21WebhookJSONLiteral(t, test.data, test.id, teams)
				got, err := read.TeamsAllowed(t.Context(), test.id, teams)
				if err != nil || got != want {
					t.Fatalf("teams=%v got=%v want=%v error=%v", teams, got, want, err)
				}
			}
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("JSON routing work retained", view.metadataResidency.work.used)
			}
		})
	}
}

func TestScaleP21WebhookJSONPinnedCancelAndConcurrent(t *testing.T) {
	view := scaleP21WebhookJSONView(t, domain.Bootstrap{Settings: map[string]any{"id": "target", "teamId": "old"}})
	read, found, err := view.BeginWebhookAggregateRead(t.Context(), "")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	next, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{Settings: map[string]any{"id": "target", "teamId": "new"}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("webhook-json", next)
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	last, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{Settings: map[string]any{"id": "target", "teamId": "last"}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("webhook-json", last)
	current, _, err := view.BeginWebhookAggregateRead(pinned, "")
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	if allowed, err := current.TeamsAllowed(t.Context(), "target", []string{"last"}); err != nil || !allowed {
		t.Fatal("earlier caller pin changed the literal current-generation lookup", allowed, err)
	}
	current.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := read.TeamsAllowed(ctx, "target", []string{"old"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			if allowed, err := read.TeamsAllowed(t.Context(), "target", []string{"old"}); err != nil || !allowed {
				t.Errorf("retained routing generation changed: %v %v", allowed, err)
			}
		})
	}
	wait.Wait()
	held := view.metadataResidency.work.used
	view.metadataResidency.work.limit = held + 1
	if _, err := read.TeamsAllowed(t.Context(), "target", []string{"old"}); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("object quota bypassed", err)
	}
	if view.metadataResidency.work.used != held {
		t.Fatal("failed traversal retained partial work")
	}
	view.metadataResidency.work.limit = metadataWorkBytes
	read.Close()
	read.Close()
	if _, err := read.TeamsAllowed(t.Context(), "target", nil); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed handle stayed usable", err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("JSON read leaked work", view.metadataResidency.work.used)
	}
	if _, found, err := view.BeginWebhookAggregateRead(t.Context(), "missing"); err != nil || found {
		t.Fatal("missing workspace changed", found, err)
	}
	view.metadataResidency.work.limit = 1
	if _, _, err := view.BeginWebhookAggregateRead(t.Context(), ""); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("descriptor quota bypassed", err)
	}
}

func TestScaleP21WebhookJSONFreshObjectTraversalAndLargeValues(t *testing.T) {
	data := domain.Bootstrap{Settings: map[string]any{
		"first":  map[string]string{"id": "target", "teamId": "allowed"},
		"second": map[string]string{"id": "target", "teamId": "hidden"},
	}}
	view := scaleP21WebhookJSONView(t, data)
	read, _, err := view.BeginWebhookAggregateRead(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	seen := map[bool]bool{}
	for range 256 {
		got, err := read.TeamsAllowed(t.Context(), "target", []string{"allowed"})
		if err != nil {
			t.Fatal(err)
		}
		seen[got] = true
	}
	if len(seen) != 2 {
		t.Fatal("JSON object traversal cached a fixed duplicate-ID winner")
	}
	large := strings.Repeat("legal 界", 100000)
	largeView := scaleP21WebhookJSONView(t, domain.Bootstrap{Settings: map[string]any{
		"raw": json.RawMessage(`{"id":"target","teamId":"allowed","body":` + strconv.Quote(large) + `}`),
	}})
	largeRead, _, err := largeView.BeginWebhookAggregateRead(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := largeRead.TeamsAllowed(t.Context(), "target", []string{"allowed"}); err != nil || !got {
		t.Fatal("legal large raw record was lost", got, err)
	}
	largeRead.Close()
	if largeView.metadataResidency.work.used != 0 {
		t.Fatal("large raw JSON retained work")
	}
}

func TestScaleP21WebhookJSONWarmReloadAndBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("webhook-json-target-")
	for _, reload := range []bool{false, true} {
		var costs []int
		for _, size := range []int{16 << 10, 160 << 10} {
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21.webhook_json_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Settings = map[string]any{"id": target, "teamId": "allowed", "body": strings.Repeat("unrelated 界", size)}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			data, found := f.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("fixture workspace missing")
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			read, found, err := view.BeginWebhookAggregateRead(t.Context(), workspace)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			queries := 0
			peak := 0
			ctx := scaleP21HotWebhookContext{WithSQLStatementObserver(t.Context(), func(string) { queries++ }), func() {
				// This fixture is single-threaded. Err is also called while
				// admission holds its mutex, so the observer must not lock it.
				peak = max(peak, view.metadataResidency.work.used)
			}}
			for _, id := range []string{target, "missing"} {
				got, err := read.TeamsAllowed(ctx, id, []string{"allowed"})
				want := scaleP21WebhookJSONLiteral(t, data, id, []string{"allowed"})
				if err != nil || got != want {
					t.Fatalf("reload=%v size=%d got=%v want=%v err=%v", reload, size, got, want, err)
				}
			}
			read.Close()
			if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
				t.Fatal("routing queried SQL, decoded full metadata, or leaked admission", queries, view.metadataResidency.work.used)
			}
			costs = append(costs, peak)
			view.releaseMetadataLogicalSources()
		}
		t.Logf("reload=%v 1x/10x observed peak admitted work=%v (not heap)", reload, costs)
		if !reflect.DeepEqual(costs[:1], costs[1:]) {
			t.Fatal("unrelated body increased routing work", costs)
		}
	}
}
