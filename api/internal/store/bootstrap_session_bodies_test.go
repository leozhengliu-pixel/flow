package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW166Fixture() domain.Bootstrap {
	at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
	return domain.Bootstrap{
		Workspace: domain.Workspace{ID: "sw166-workspace", URLKey: "sw166-workspace"},
		AgentSessions: []domain.AgentSession{{
			ID: "duplicate", UserID: "owner", Title: "Retained session header", CreatedAt: at, UpdatedAt: at,
			Messages: []domain.AgentMessage{{
				ID: "duplicate-message", Role: "assistant", Content: "Native content <>&\xff", CreatedAt: at,
				Parts: []domain.AgentMessagePart{{
					ID: "duplicate-part", Type: "tool_call", Text: "Native part text",
					ToolCall: &domain.AgentToolCall{
						ID: "duplicate-call", Name: "search", Status: "completed", Error: "Native error",
						Arguments: json.RawMessage(` {"number":9007199254740993,"duplicate":1,"duplicate":2,"url":"/uploads/argument.txt"} `),
						Result:    json.RawMessage(` [9007199254740993,1e30,{"url":"/uploads/result.txt"}] `),
					},
				}},
			}},
		}},
	}
}

func scaleP21SW166Begin(t *testing.T, view *SQLiteStore, source *metadataLogicalSource, options bootstrapBodyProjectionOptions) *IssueBootstrapRead {
	t.Helper()
	body, err := view.beginBootstrapBodyProjectionWithOptions(t.Context(), source, options)
	if err != nil {
		t.Fatal(err)
	}
	read := &IssueBootstrapRead{Data: body.Data, body: body, owner: &ResourceRead{store: view}}
	t.Cleanup(read.Close)
	return read
}

func scaleP21SW166Exact(t *testing.T, data domain.Bootstrap, read *IssueBootstrapRead, rewrite ...MetadataJSONRewriter) {
	t.Helper()
	want, err := json.Marshal(data)
	if err != nil {
		t.Fatal("legacy fixture encoding failed", err)
	}
	want = append(want, '\n')
	var output bytes.Buffer
	if err := read.WriteJSON(t.Context(), &output, rewrite...); err != nil {
		t.Fatal("actual bootstrap writer failed", err)
	}
	if !bytes.Equal(want, output.Bytes()) {
		position := 0
		for position < min(len(want), output.Len()) && want[position] == output.Bytes()[position] {
			position++
		}
		t.Fatalf("session body codec differs at byte %d lengths=%d/%d legacy=%q bounded=%q",
			position, len(want), output.Len(), want[max(0, position-64):min(len(want), position+96)],
			output.Bytes()[max(0, position-64):min(output.Len(), position+96)])
	}
}

func TestScaleP21SW166BootstrapSessionBodyCodecs(t *testing.T) {
	for _, reloaded := range []bool{false, true} {
		for _, mode := range []string{
			"valid-raw", "nil-raw", "empty-raw", "null-raw", "partial-arguments", "partial-result",
			"utf8-escapes", "native-markers-duplicates", "large-content", "large-part-text", "large-raw",
		} {
			t.Run(fmt.Sprint("reloaded=", reloaded, "/", mode), func(t *testing.T) {
				data := scaleP21SW166Fixture()
				message := &data.AgentSessions[0].Messages[0]
				call := message.Parts[0].ToolCall
				switch mode {
				case "nil-raw":
					call.Arguments, call.Result = nil, nil
				case "empty-raw":
					call.Arguments, call.Result = json.RawMessage{}, json.RawMessage{}
				case "null-raw":
					call.Arguments, call.Result = json.RawMessage("null"), json.RawMessage(" null ")
				case "partial-arguments":
					call.Arguments = json.RawMessage(` {"unfinished": [1,`)
				case "partial-result":
					call.Result = json.RawMessage("unfinished <>&\xff\nresult")
				case "utf8-escapes":
					text := strings.Repeat("x", 32767) + "\u4e2d\u6587\u2028\u2029 e\u0301 <>& \"\\\n\xff"
					message.Content, message.Parts[0].Text, call.Error = text, text, text
					call.Arguments = json.RawMessage(`{"escaped":"\u4e2d\u6587\u2028\u2029","number":9007199254740993}`)
				case "native-markers-duplicates":
					literal := bootstrapBodyMarkerPrefix + strings.Repeat("f", 32) + ":1"
					rawLiteral := `"` + bootstrapBodyRawMarkerPrefix + strings.Repeat("e", 32) + `:1"`
					message.Content, message.Parts[0].Text, call.Error = literal, literal, literal
					call.Arguments, call.Result = json.RawMessage(rawLiteral), json.RawMessage(rawLiteral)
					second := data.AgentSessions[0]
					second.Messages = append([]domain.AgentMessage(nil), second.Messages...)
					second.Messages[0].Content = "Distinct body under the same native IDs"
					data.AgentSessions = append(data.AgentSessions, second)
				case "large-content":
					message.Content = strings.Repeat("Opaque session content <>&. ", 100000)
				case "large-part-text":
					message.Parts[0].Text = strings.Repeat("Opaque session part text <>&. ", 100000)
				case "large-raw":
					call.Arguments = json.RawMessage(`{"text":"` + strings.Repeat("Opaque raw argument ", 120000) + `","number":9007199254740993}`)
				}
				if reloaded {
					// Exercise the supported saved transcript codec, including
					// AgentToolCall's partial-provider sanitization.
					raw, err := json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(raw, &data); err != nil {
						t.Fatal(err)
					}
				}
				view, source := scaleP21SW138Source(t, data)
				read := scaleP21SW166Begin(t, view, source, bootstrapBodyProjectionOptions{})
				scaleP21SW166Exact(t, data, read)
				read.Close()
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("session codec retained work or decoded whole metadata")
				}
			})
		}
	}
}

func TestScaleP21SW166BootstrapSessionDatabaseBodies(t *testing.T) {
	scaleP21SW143Setup(t)
	for _, reloaded := range []bool{false, true} {
		t.Run(fmt.Sprint("reloaded=", reloaded), func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			var held int
			for _, scale := range []int{1, 10} {
				body := strings.Repeat("Opaque session body <>&\u2028. ", scale*10000)
				if err := f.MutateWorkspace(t.Context(), workspace, "agent.session_updated", "sw166-session", nil, func(data *domain.Bootstrap) error {
					session := scaleP21SW166Fixture().AgentSessions[0]
					session.ID, session.UserID = "sw166-session", f.data.Viewer.ID
					message := &session.Messages[0]
					message.Content, message.Parts[0].Text, message.Parts[0].ToolCall.Error = body, body, body
					arguments, err := json.Marshal(map[string]any{"text": body, "number": json.Number("9007199254740993")})
					if err != nil {
						return err
					}
					message.Parts[0].ToolCall.Arguments, message.Parts[0].ToolCall.Result = arguments, arguments
					data.AgentSessions = []domain.AgentSession{session}
					at := session.CreatedAt
					if data.UserSettings == nil {
						data.UserSettings = map[string]domain.UserSettings{}
					}
					if data.NotificationPreferences == nil {
						data.NotificationPreferences = map[string]domain.NotificationPreferences{}
					}
					for _, user := range data.Users {
						settings, preference := defaultUserSettings(user.ID), defaultNotificationPreferences(user.ID)
						settings.UpdatedAt, preference.UpdatedAt = at, at
						data.UserSettings[user.ID], data.NotificationPreferences[user.ID] = settings, preference
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if reloaded {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer view.releaseMetadataLogicalSources()
				want, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				read, err := view.BeginIssueBootstrapRead(t.Context(), workspace, f.data.Viewer.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				work := view.MetadataWorkUsedForTesting()
				if held != 0 && held != work {
					t.Fatal("10x session bodies changed admitted headers", held, work)
				}
				held = work
				scaleP21SW143Compare(t, t.Context(), want, read)
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("database session body read retained work or decoded metadata")
				}
				t.Logf("body_scale=%d admitted_headers=%d", scale, work)
			}
		})
	}
}

func TestScaleP21SW166BootstrapSessionLifetime(t *testing.T) {
	data := scaleP21SW166Fixture()
	data.AgentSessions[0].Messages[0].Content = strings.Repeat("Streamed session content ", 100000)
	view, source := scaleP21SW138Source(t, data)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if body, err := view.beginBootstrapBodyProjection(canceled, source); body != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled session body projection escaped", err)
	}
	probe := scaleP21SW166Begin(t, view, source, bootstrapBodyProjectionOptions{})
	required := view.MetadataWorkUsedForTesting()
	probe.Close()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = required - 1
	body, err := view.beginBootstrapBodyProjection(t.Context(), source)
	view.metadataResidency.work.limit = limit
	if body != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("partial session header admission leaked", err)
	}
	for _, mode := range []string{"cancel", "writer-error", "quota", "close"} {
		t.Run(mode, func(t *testing.T) {
			read := scaleP21SW166Begin(t, view, source, bootstrapBodyProjectionOptions{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			refused := errors.New("session body destination refused")
			calls := 0
			writer := &scaleP21SW88Writer{write: func(raw []byte) (int, error) {
				calls++
				if mode == "cancel" {
					cancel()
				} else if mode == "writer-error" {
					return 0, refused
				}
				return len(raw), nil
			}}
			if mode == "quota" {
				view.metadataResidency.work.limit = view.MetadataWorkUsedForTesting()
			} else if mode == "close" {
				read.Close()
			}
			err := read.WriteJSON(ctx, writer)
			view.metadataResidency.work.limit = limit
			expected := context.Canceled
			switch mode {
			case "writer-error":
				expected = refused
			case "quota":
				expected = ErrMetadataWorkBudget
			case "close":
				expected = ErrBoundedMetadataCoverage
			}
			if !errors.Is(err, expected) || (mode == "quota" || mode == "close") && calls != 0 {
				t.Fatal("session stream failure changed", mode, calls, err)
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("session stream failure retained work")
			}
		})
	}
}

func TestScaleP21SW166BootstrapSessionUploadRewrite(t *testing.T) {
	data := scaleP21SW166Fixture()
	data.AgentSessions[0].Messages[0].Content = "/uploads/content.txt"
	data.AgentSessions[0].Messages[0].Parts[0].Text = "/uploads/part.txt"
	data.AgentSessions[0].Messages[0].Parts[0].ToolCall.Error = "/uploads/error.txt"
	view, source := scaleP21SW138Source(t, data)
	read := scaleP21SW166Begin(t, view, source, bootstrapBodyProjectionOptions{})
	calls := 0
	rewrite := func(ctx context.Context, source MetadataJSONSource, dst io.Writer) error {
		if !source.ContainsUploads() {
			t.Fatal("upload rewrite received a non-upload value")
		}
		raw, err := io.ReadAll(io.NewSectionReader(source, 0, source.Size()))
		if err != nil {
			return err
		}
		calls++
		_, err = dst.Write(bytes.ReplaceAll(raw, []byte(".txt"), []byte(".txt?signed=sw166")))
		return err
	}
	want, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.ReplaceAll(append(want, '\n'), []byte(".txt"), []byte(".txt?signed=sw166"))
	var output bytes.Buffer
	if err := read.WriteJSON(t.Context(), &output, rewrite); err != nil || !bytes.Equal(output.Bytes(), want) || calls != 5 {
		t.Fatal("session body upload rewrite differs", calls, err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("upload-rewritten session body retained work")
	}
}

func TestScaleP21SW166BootstrapStripUpdateSnapshots(t *testing.T) {
	data := scaleP21SW166Fixture()
	snapshot := &domain.PulseSnapshot{CapturedAt: time.Date(2026, 11, 15, 2, 3, 4, 0, time.UTC), Status: "Snapshot only"}
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{
		"duplicate": {{ID: "duplicate", Body: "Project update body", Snapshot: snapshot}, {ID: "", Snapshot: nil}},
	}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{
		"duplicate": {{ID: "duplicate", Body: "Initiative update body", Snapshot: snapshot}},
	}
	for _, strip := range []bool{false, true} {
		t.Run(fmt.Sprint("strip=", strip), func(t *testing.T) {
			view, source := scaleP21SW138Source(t, data)
			read := scaleP21SW166Begin(t, view, source, bootstrapBodyProjectionOptions{StripUpdateSnapshots: strip})
			want := data
			if strip {
				want.ProjectUpdates = map[string][]domain.ProjectUpdate{}
				for key, updates := range data.ProjectUpdates {
					want.ProjectUpdates[key] = append([]domain.ProjectUpdate(nil), updates...)
					for index := range want.ProjectUpdates[key] {
						want.ProjectUpdates[key][index].Snapshot = nil
					}
				}
				want.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
				for key, updates := range data.InitiativeUpdates {
					want.InitiativeUpdates[key] = append([]domain.InitiativeUpdate(nil), updates...)
					for index := range want.InitiativeUpdates[key] {
						want.InitiativeUpdates[key][index].Snapshot = nil
					}
				}
				if read.Data.ProjectUpdates["duplicate"][0].Snapshot != nil || read.Data.InitiativeUpdates["duplicate"][0].Snapshot != nil {
					t.Fatal("general bootstrap snapshot was materialized")
				}
			}
			scaleP21SW166Exact(t, want, read)
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
				t.Fatal("snapshot-stripped body projection retained output")
			}
		})
	}
}
