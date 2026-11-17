package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW209Stream struct {
	bytes.Buffer
	ctx  context.Context
	work *store.MetadataJSONRead
}

func (b *scaleP21SW209Stream) Write(part []byte) (int, error) {
	if err := b.work.ReserveEncoded(b.ctx, b.Len()+len(part)); err != nil {
		return 0, err
	}
	return b.Buffer.Write(part)
}

func (b *scaleP21SW209Stream) WriteString(part string) (int, error) {
	if err := b.work.ReserveEncoded(b.ctx, b.Len()+len(part)); err != nil {
		return 0, err
	}
	return b.Buffer.WriteString(part)
}

func scaleP21SW209Native(t *testing.T, source *store.SQLiteStore, workspace string) domain.Bootstrap {
	t.Helper()
	if !source.BoundedMetadataEnabled() {
		data, found := source.WorkspaceMetadataFields(workspace, "projects", "projectUpdates", "trash", "issueSlas", "slaEvents")
		if !found {
			t.Fatal("missing literal ticker output")
		}
		return data
	}
	read, found, err := source.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	headers, found, err := source.BeginAdvancedScheduleRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("missing installed ticker headers", found, err)
	}
	defer headers.Close()
	data := headers.Data
	for _, name := range []string{"trash", "issueSlas", "slaEvents"} {
		work, err := source.BeginMetadataJSONRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		buffer := &scaleP21SW209Stream{ctx: t.Context(), work: work}
		if err := read.StreamFieldJSON(t.Context(), name, buffer); err != nil {
			work.Close()
			t.Fatal(err)
		}
		if err := work.ReserveDecode(t.Context(), buffer.Bytes(), nil); err != nil {
			work.Close()
			t.Fatal(err)
		}
		switch name {
		case "trash":
			err = json.Unmarshal(buffer.Bytes(), &data.Trash)
		case "issueSlas":
			err = json.Unmarshal(buffer.Bytes(), &data.IssueSLAs)
		case "slaEvents":
			err = json.Unmarshal(buffer.Bytes(), &data.SLAEvents)
		}
		work.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func scaleP21SW209Headers(data domain.Bootstrap) domain.Bootstrap {
	result := data
	if data.Projects != nil {
		result.Projects = make([]domain.Project, len(data.Projects))
	}
	for index, project := range data.Projects {
		result.Projects[index] = domain.Project{ID: project.ID, CreatedAt: project.CreatedAt,
			UpdateCadence: project.UpdateCadence, UpdateSchedule: project.UpdateSchedule,
			Status: project.Status, Health: project.Health}
	}
	if data.ProjectUpdates != nil {
		result.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	}
	for parent, updates := range data.ProjectUpdates {
		var next []domain.ProjectUpdate
		if updates != nil {
			next = make([]domain.ProjectUpdate, len(updates))
		}
		if len(updates) > 0 {
			next[0] = domain.ProjectUpdate{CreatedAt: updates[0].CreatedAt,
				DueAt: updates[0].DueAt, Missing: updates[0].Missing}
		}
		result.ProjectUpdates[parent] = next
	}
	return result
}

func scaleP21SW209ProjectSpools(t *testing.T, source *store.SQLiteStore, workspace string) map[string]*store.RecordSpool {
	t.Helper()
	result := map[string]*store.RecordSpool{}
	var literal domain.Bootstrap
	var read *store.InstalledMetadataRead
	if source.BoundedMetadataEnabled() {
		var found bool
		var err error
		read, found, err = source.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("missing installed project field source", found, err)
		}
		defer read.Close()
	} else {
		var found bool
		literal, found = source.WorkspaceMetadataFields(workspace, "projects", "projectUpdates")
		if !found {
			t.Fatal("missing literal project field source")
		}
	}
	for _, field := range []string{"projects", "projectUpdates"} {
		spool, err := store.NewRecordSpool()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := spool.Close(); err != nil {
				t.Error(err)
			}
		})
		if read != nil {
			err = read.StreamFieldJSON(t.Context(), field, spool)
			if err == nil {
				_, err = io.WriteString(spool, "\n")
			}
		} else if field == "projects" {
			err = json.NewEncoder(spool).Encode(literal.Projects)
		} else {
			err = json.NewEncoder(spool).Encode(literal.ProjectUpdates)
		}
		if err != nil {
			t.Fatal("stream complete native project field", field, err)
		}
		result[field] = spool
	}
	return result
}

func scaleP21SW209ExactSpools(t *testing.T, field string, left, right *store.RecordSpool) {
	t.Helper()
	a, err := left.Stat()
	if err != nil {
		t.Fatal(err)
	}
	b, err := right.Stat()
	if err != nil || a.Size() != b.Size() {
		t.Fatal("complete native field byte lengths differ", field, a.Size(), b, err)
	}
	x, y := make([]byte, 32<<10), make([]byte, 32<<10)
	for offset := int64(0); offset < a.Size(); {
		count := int(min(int64(len(x)), a.Size()-offset))
		if _, err := left.ReadAt(x[:count], offset); err != nil {
			t.Fatal(err)
		}
		if _, err := right.ReadAt(y[:count], offset); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(x[:count], y[:count]) {
			t.Fatal("complete native field bytes differ", field, offset)
		}
		offset += int64(count)
	}
}

func scaleP21SW209MetadataRows(t *testing.T, f scaleP21SW10Fixture, workspace string) []any {
	t.Helper()
	rows := f.reader.query(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
	defer rows.Close()
	var result []any
	for rows.Next() {
		var field, key string
		var order int
		var raw json.RawMessage
		if err := rows.Scan(&field, &key, &order, &raw); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{field, key, order, raw})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW209MetadataRoot(t *testing.T, f scaleP21SW10Fixture, workspace string) json.RawMessage {
	t.Helper()
	rows := f.reader.query(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing canonical workspace metadata root")
	}
	var raw json.RawMessage
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW209ReloadSource(t *testing.T, f scaleP21SW10Fixture, source *store.SQLiteStore) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	f.reader.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, workspace).Close()
	query := `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`
	if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
		query = strings.Replace(query, "?", "$1", 1)
	}
	if _, err := f.reader.reader.ExecContext(t.Context(), query, workspace); err != nil {
		t.Fatal(err)
	}
	decodes := source.WorkspaceMetadataDecodes()
	if err := source.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal("advanced scheduler SQL source reload", err)
	}
	if !source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() <= decodes {
		t.Fatal("literal scheduler reload did not load its SQL source")
	}
	if source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() != decodes {
		t.Fatal("bounded scheduler reload fully hydrated workspace metadata")
	}
}

func TestScaleP21SW209AdvancedScheduleTickerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f := scaleP21SW10Setup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	// The initial invitation fixture has not installed every loader default.
	// Capture its normalized state once, before either differential leg.
	scaleP21SW209ReloadSource(t, f, f.repository)
	baseline, found := f.repository.BootstrapFor(workspace)
	if !found || len(baseline.Issues) == 0 {
		t.Fatal("ticker fixture requires a stored issue")
	}
	anchor := time.Now().UTC().Truncate(time.Minute)
	if baseline.NotificationPreferences == nil {
		baseline.NotificationPreferences = map[string]domain.NotificationPreferences{}
	}
	// Reload normalizes missing user defaults. Seed those defaults explicitly
	// so both legs start with the same durable rows and captured clock.
	for _, user := range baseline.Users {
		if _, found := baseline.NotificationPreferences[user.ID]; !found {
			preferences := defaultPreferences(user.ID)
			preferences.UpdatedAt = anchor
			baseline.NotificationPreferences[user.ID] = preferences
		}
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("sw209-")
	type scenario struct {
		name string
	}
	for _, test := range []scenario{
		{"missing"}, {"due-soon"}, {"current-archive"}, {"inbox-off-noop"},
		{"expired-document"}, {"sla-breach"}, {"sla-paused"}, {"sla-removed"},
		{"member-reminder-fanout"}, {"sla-subscriber-fanout"},
		{"sla-many-unique-rules"}, {"sla-duplicate-winning-rules"},
		{"duplicate-updates"}, {"duplicate-large-map"}, {"duplicate-large-inline"}, {"cancelled"}, {"missing-workspace"},
	} {
		for _, reload := range []bool{false, true} {
			type actorCase struct {
				name string
				user domain.User
				key  *domain.APIKey
			}
			var actors []actorCase
			for _, principal := range f.principals {
				actors = append(actors, actorCase{name: principal.name + "/session", user: principal.user})
				if test.name == "missing" || test.name == "sla-breach" {
					for _, credential := range []struct {
						name string
						key  domain.APIKey
					}{
						{"unrestricted", domain.APIKey{ID: "sw209-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
						{"selected-empty", domain.APIKey{ID: "sw209-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
						{"selected-set", domain.APIKey{ID: "sw209-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
					} {
						key := credential.key
						actors = append(actors, actorCase{name: principal.name + "/" + credential.name, user: principal.user, key: &key})
					}
				}
			}
			for principalIndex, principal := range actors {
				combinedGate := test.name == "duplicate-large-map" || test.name == "duplicate-large-inline"
				if combinedGate && principalIndex != 0 {
					continue
				}
				// Body peak proof is independent of actor permissions; the
				// ordinary table still runs every inherited/key actor case.
				sizes := []int{32}
				if principalIndex == 0 && (test.name == "missing" || test.name == "due-soon" || combinedGate) {
					sizes = append(sizes, 5<<20)
				}
				smallPeak := 0
				for _, bodyBytes := range sizes {
					t.Run(fmt.Sprintf("%s/reload=%t/%s/body=%d", test.name, reload, principal.name, bodyBytes), func(t *testing.T) {
						var outcomes []any
						var projectSpools []map[string]*store.RecordSpool
						var metadataPreimages [][]any
						for _, source := range []*store.SQLiteStore{f.repository, f.probe} {
							for _, target := range []*store.SQLiteStore{f.repository, f.probe} {
								target.SetWebhookSink(nil)
								target.SetRealtimeSink(nil)
							}
							restore := store.ForceFullMutationsForTesting(true)
							err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw209_seed", "", nil, func(data *domain.Bootstrap) error {
								if err := json.Unmarshal(saved, data); err != nil {
									return err
								}
								for index := range data.Projects {
									data.Projects[index].UpdateCadence, data.Projects[index].UpdateSchedule = "none", nil
								}
								project := domain.Project{ID: prefix + "project", Name: "Advanced schedule", Description: strings.Repeat("p", bodyBytes),
									CreatedAt: anchor.Add(-72 * time.Hour), UpdateCadence: "default", Health: "onTrack",
									Lead: &data.Viewer, MemberIDs: []string{data.Viewer.ID}, Status: domain.ProjectStatus{Type: "started"}}
								if test.name == "duplicate-large-inline" {
									project.ID = prefix + strings.Repeat("p", 192-len(prefix))
								}
								if test.name == "member-reminder-fanout" {
									project.MemberIDs = nil
									for _, recipient := range f.principals {
										project.MemberIDs = append(project.MemberIDs, recipient.user.ID, recipient.user.ID)
									}
								}
								if test.name == "due-soon" {
									project.CreatedAt = anchor.Add(-12 * time.Hour)
								}
								if test.name == "current-archive" {
									project.CreatedAt = anchor
								}
								if test.name == "inbox-off-noop" {
									project.Health = "noUpdate"
									preferences := defaultPreferences(data.Viewer.ID)
									preferences.UpdatedAt = anchor
									preferences.Inbox.Enabled = false
									data.NotificationPreferences[data.Viewer.ID] = preferences
								}
								data.Projects = append(data.Projects, project)
								data.Settings["projectUpdates"] = map[string]any{"cadenceDays": 1, "reminders": true, "missingNotifications": true}
								data.Settings["sla"] = map[string]any{"enabled": strings.HasPrefix(test.name, "sla-")}
								update := domain.ProjectUpdate{ID: prefix + "update", ProjectID: project.ID, Body: strings.Repeat("u", bodyBytes),
									CreatedAt: project.CreatedAt, User: data.Viewer}
								data.ProjectUpdates[project.ID] = []domain.ProjectUpdate{update}
								if test.name == "duplicate-updates" || combinedGate {
									data.ProjectUpdates[project.ID] = append(data.ProjectUpdates[project.ID], update)
								}
								if test.name == "current-archive" {
									data.Notifications = append(data.Notifications, domain.Notification{ID: prefix + "reminder", RecipientID: data.Viewer.ID,
										ProjectID: project.ID, Type: "projectUpdateReminder", Actor: data.Viewer, CreatedAt: anchor.Add(-time.Hour), UpdatedAt: anchor.Add(-time.Hour)})
								}
								if test.name == "inbox-off-noop" {
									due := project.CreatedAt.AddDate(0, 0, 1)
									data.ProjectUpdates[project.ID][0].DueAt, data.ProjectUpdates[project.ID][0].Missing = &due, true
								}
								if test.name == "expired-document" {
									payload, _ := json.Marshal(domain.Document{ID: prefix + "document", CollaborationID: prefix + "generation"})
									data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "document",
										ResourceID: prefix + "document", Payload: payload, DeletedAt: anchor.Add(-90 * 24 * time.Hour), ExpiresAt: anchor.Add(-time.Hour)})
									data.Comments[prefix+"document"] = []domain.Comment{{ID: prefix + "comment", Body: "Expired thread", User: data.Viewer}}
								}
								if strings.HasPrefix(test.name, "sla-") {
									if test.name == "sla-subscriber-fanout" {
										data.Issues[0].SubscriberIDs = nil
										for _, recipient := range f.principals {
											data.Issues[0].SubscriberIDs = append(data.Issues[0].SubscriberIDs, recipient.user.ID, recipient.user.ID)
										}
									}
									issue := data.Issues[0]
									rule := domain.SLARule{ID: prefix + "rule", Enabled: true, TeamIDs: []string{issue.Team.ID}, TargetMinutes: 60}
									if test.name == "sla-paused" {
										rule.PauseStatuses = []string{issue.State.ID}
									}
									if test.name == "sla-removed" {
										rule.Enabled = false
									}
									data.SLARules = []domain.SLARule{rule}
									if test.name == "sla-many-unique-rules" {
										for index := range 64 {
											other := rule
											other.ID = fmt.Sprintf("%sother-rule-%d", prefix, index)
											data.SLARules = append(data.SLARules, other)
										}
									}
									if test.name == "sla-duplicate-winning-rules" {
										paused := rule
										paused.PauseStatuses = []string{issue.State.ID}
										data.SLARules = append(data.SLARules, paused, rule)
									}
									data.IssueSLAs = []domain.IssueSLA{{ID: prefix + "sla", IssueID: issue.ID, RuleID: rule.ID,
										Status: "active", DueAt: anchor.Add(-2 * time.Hour), StartedAt: anchor.Add(-3 * time.Hour)}}
								}
								return nil
							})
							restore()
							if err != nil {
								t.Fatal(err)
							}
							if combinedGate {
								rows := f.reader.query(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
								if !rows.Next() {
									rows.Close()
									t.Fatal("combined duplicate gate lost seeded root")
								}
								var raw []byte
								if err := rows.Scan(&raw); err != nil {
									rows.Close()
									t.Fatal(err)
								}
								if err := rows.Err(); err != nil {
									rows.Close()
									t.Fatal(err)
								}
								rows.Close()
								var root map[string]json.RawMessage
								if err := json.Unmarshal(raw, &root); err != nil {
									t.Fatal(err)
								}
								var manifest map[string]string
								if err := json.Unmarshal(root["_flowCollections"], &manifest); err != nil {
									t.Fatal(err)
								}
								want := "map"
								if test.name == "duplicate-large-inline" {
									want = ""
								}
								if manifest["projectUpdates"] != want {
									t.Fatal("combined duplicate fixture has wrong canonical shape", manifest["projectUpdates"], want)
								}
							}
							if reload {
								scaleP21SW209ReloadSource(t, f, f.repository)
							}
							if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
								t.Fatal(err)
							}
							if reload && source.BoundedMetadataEnabled() {
								scaleP21SW209ReloadSource(t, f, source)
							}
							before := scaleP21SW59SQL(t, f)
							metadataPreimages = append(metadataPreimages, scaleP21SW209MetadataRows(t, f, workspace))
							if len(metadataPreimages) == 2 && !reflect.DeepEqual(metadataPreimages[0], metadataPreimages[1]) {
								t.Fatal("literal and bounded ticker legs began with different canonical metadata", scaleP21M2Diff(metadataPreimages[0], metadataPreimages[1]))
							}
							got := scaleP21SW59Outcome{}
							source.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.Events = append(got.Events, event) })
							captureRealtime := func(_ string, event domain.RealtimeEvent) {
								raw, err := json.Marshal(event)
								if err != nil {
									t.Error(err)
								}
								got.Realtime = append(got.Realtime, json.RawMessage(raw))
							}
							source.SetRealtimeSink(captureRealtime)
							if source != f.repository {
								f.repository.SetRealtimeSink(captureRealtime)
							}
							peak, statements, writes := 0, 0, 0
							ctx := f.literalRequest(principal.user, principal.key, nil).Context()
							ctx = store.WithSQLStatementObserver(ctx, func(query string) {
								statements++
								peak = max(peak, source.MetadataWorkUsedForTesting())
								lower := strings.ToLower(strings.TrimSpace(query))
								if (strings.HasPrefix(lower, "insert") || strings.HasPrefix(lower, "update") || strings.HasPrefix(lower, "delete")) &&
									(strings.Contains(lower, "issue_records") || strings.Contains(lower, "workspace_metadata_records") || strings.Contains(lower, "workspace_content_records")) {
									writes++
								}
							})
							if test.name == "cancelled" {
								var cancel context.CancelFunc
								ctx, cancel = context.WithCancel(ctx)
								cancel()
							}
							key := workspace
							if test.name == "missing-workspace" {
								key = prefix + "missing"
							}
							started := time.Now().UTC()
							(&server{store: source, uploadPath: t.TempDir()}).maintainAdvancedSchedulesAt(ctx, key, func() time.Time { return anchor })
							finished := time.Now().UTC()
							source.SetWebhookSink(nil)
							source.SetRealtimeSink(nil)
							f.repository.SetRealtimeSink(nil)
							if source.MetadataWorkUsedForTesting() != 0 {
								t.Fatal("ticker leaked operation ownership", source.MetadataWorkUsedForTesting())
							}
							got.Rows = scaleP21SW59SQL(t, f)
							native := scaleP21SW209Native(t, source, workspace)
							projectSpools = append(projectSpools, scaleP21SW209ProjectSpools(t, source, workspace))
							if combinedGate {
								projectID := prefix + "project"
								if test.name == "duplicate-large-inline" {
									projectID = prefix + strings.Repeat("p", 192-len(prefix))
								}
								var selected *domain.Project
								for index := range native.Projects {
									if native.Projects[index].ID == projectID {
										selected = &native.Projects[index]
										break
									}
								}
								updates := native.ProjectUpdates[projectID]
								if selected == nil || selected.Health != "noUpdate" || len(updates) != 2 ||
									updates[0].DueAt == nil || !updates[0].Missing || updates[1].DueAt != nil || updates[1].Missing {
									t.Fatal("combined duplicate gate lost occurrence order or Health/DueAt/Missing mutation")
								}
								if !source.BoundedMetadataEnabled() && (len(selected.Description) != bodyBytes ||
									len(updates[0].Body) != bodyBytes || len(updates[1].Body) != bodyBytes) {
									t.Fatal("combined duplicate gate has wrong literal body lengths")
								}
							}
							metadata := scaleP21SW209MetadataRows(t, f, workspace)
							headers := scaleP21SW209Headers(native)
							got.Response, err = json.Marshal(map[string]any{"metadata": metadata, "metadataRoot": scaleP21SW209MetadataRoot(t, f, workspace), "projects": headers.Projects,
								"projectUpdates": headers.ProjectUpdates, "trash": native.Trash, "issueSlas": native.IssueSLAs, "slaEvents": native.SLAEvents})
							if err != nil {
								t.Fatal(err)
							}
							value := scaleP21SW59Normalized(t, got, before, started, finished)
							ids := map[string]string{}
							for index, event := range native.SLAEvents {
								ids[event.ID] = fmt.Sprintf("<sla-event:%d>", index)
							}
							var normalize func(any, string) any
							normalize = func(value any, field string) any {
								switch item := value.(type) {
								case string:
									if replacement, found := ids[item]; found {
										return replacement
									}
									if clock, err := time.Parse(time.RFC3339Nano, item); err == nil && !clock.Before(started) && !clock.After(finished) {
										return "<generated-clock>"
									}
								case map[string]any:
									for key, child := range item {
										if strings.EqualFold(key, "seq") && fmt.Sprint(child) != "0" {
											item[key] = "<committed-cursor>"
										} else {
											item[key] = normalize(child, key)
										}
									}
								case []any:
									for index := range item {
										item[index] = normalize(item[index], field)
									}
								}
								return value
							}
							outcomes = append(outcomes, normalize(value, ""))
							noMutation := test.name == "inbox-off-noop" || test.name == "cancelled" || test.name == "missing-workspace"
							if noMutation && len(got.Events) != 0 || !noMutation && len(got.Events) == 0 {
								t.Fatal("ticker fixture did not exercise its expected mutation decision", test.name, len(got.Events))
							}
							if source.BoundedMetadataEnabled() {
								if smallPeak == 0 {
									smallPeak = peak
								}
								if peak > smallPeak+(2<<20) {
									t.Fatal("immutable project/update bodies changed ticker peak", smallPeak, peak)
								}
							}
							t.Logf("bounded=%t SQL=%d record_writes=%d peak_work=%d body_bytes=%d",
								source.BoundedMetadataEnabled(), statements, writes, peak, bodyBytes)
						}
						for _, field := range []string{"projects", "projectUpdates"} {
							scaleP21SW209ExactSpools(t, field, projectSpools[0][field], projectSpools[1][field])
						}
						if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
							if !reflect.DeepEqual(metadataPreimages[0], metadataPreimages[1]) {
								t.Log("metadata fixture preimages differ", scaleP21M2Diff(metadataPreimages[0], metadataPreimages[1]))
							}
							t.Fatal("advanced ticker changed exact SQL/native/publication output", test.name, reload, principal.name, bodyBytes, scaleP21M2Diff(outcomes[0], outcomes[1]))
						}
					})
				}
			}
		}
	}
}
