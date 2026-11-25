package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW198SessionLifecycleNativeClosure(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 7, time.UTC)
	user := "owner"
	deleted := domain.AgentSession{ID: "session", UserID: user, DeletedAt: &at}
	first := domain.AgentSession{ID: "session", SlugID: "old", UserID: user, Messages: []domain.AgentMessage{}}
	second := domain.AgentSession{
		ID: "session", SlugID: "old", UserID: user, Title: "second\x00", UpdatedAt: at,
		Messages: []domain.AgentMessage{{
			ID: "message", Content: "complete\x00", Status: "streaming", UpdatedAt: &at,
			WorkerCursor: &domain.AgentWorkerCursor{Run: "run", Attempt: 2, Seq: 9007199254740993},
			Offloaded:    domain.OffloadedContent{"content": {Key: "never-hydrated", Size: 42}},
			Parts:        []domain.AgentMessagePart{{ID: "part", Type: "text", Text: "keep"}},
		}},
	}
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace"}, Viewer: domain.User{ID: user}, ViewerRole: "member",
		AgentSessions: []domain.AgentSession{
			deleted, first, second, {ID: "session", UserID: "other"},
			{ID: "", UserID: "", Messages: nil}, {ID: "nul\x00", UserID: user},
			{ID: "unrelated", UserID: user, Title: strings.Repeat("ignored", 500000)},
		},
		AgentActivities: []domain.AgentActivity{{ID: "old-activity", Body: "preserved"}},
	}
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	source, err := repo.retainMetadataLogicalSource(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, test := range []struct {
		name  string
		scope AgentSessionLifecycleScope
		want  []domain.AgentSession
	}{
		{"all-occurrences", AgentSessionLifecycleScope{ID: "session"}, data.AgentSessions[:4]},
		{"owner", AgentSessionLifecycleScope{ID: "session", OwnerID: &user}, data.AgentSessions[:3]},
		{"active-owner", AgentSessionLifecycleScope{ID: "session", OwnerID: &user, ExcludeDeleted: true}, data.AgentSessions[1:3]},
		{"old-slug", AgentSessionLifecycleScope{ID: "old", OwnerID: &user, MatchSlug: true, ExcludeDeleted: true}, data.AgentSessions[1:3]},
		{"slug-is-not-id", AgentSessionLifecycleScope{ID: "old"}, []domain.AgentSession{}},
		{"empty-id", AgentSessionLifecycleScope{ID: ""}, data.AgentSessions[4:5]},
		{"nul-id", AgentSessionLifecycleScope{ID: "nul\x00"}, data.AgentSessions[5:6]},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputs, err := repo.loadAgentSessionLifecycleInputs(t.Context(), source, test.scope)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inputs.data.AgentSessions, test.want) {
				t.Fatal("native selection/order changed", inputs.data.AgentSessions, test.want)
			}
			if repo.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("selected native output has no owner")
			}
			inputs.Close()
			inputs.Close()
			if repo.MetadataWorkUsedForTesting() != 0 || len(inputs.data.AgentSessions) != 0 {
				t.Fatal("selected native output leaked")
			}
		})
	}
	scope := AgentSessionLifecycleScope{ID: "session", OwnerID: &user, ExcludeDeleted: true, AppendActivities: true}
	inputs, err := repo.loadAgentSessionLifecycleInputs(t.Context(), source, scope)
	if err != nil {
		t.Fatal(err)
	}
	inputs.data.AgentSessions[1].Messages[0].Status = ""
	inputs.data.AgentSessions[1].Messages[0].UpdatedAt = nil
	inputs.data.AgentActivities = append(inputs.data.AgentActivities, domain.AgentActivity{ID: "new-activity", Body: "appended"})
	merged, err := inputs.merge(t.Context(), scope)
	if err != nil {
		inputs.Close()
		t.Fatal(err)
	}
	defer merged.Release()
	inputs.Close()
	for _, field := range []struct {
		name string
		want any
	}{
		{"agentSessions", func() []domain.AgentSession {
			expected := cloneBootstrap(data).AgentSessions
			expected[2].Messages[0].Status, expected[2].Messages[0].UpdatedAt = "", nil
			return expected
		}()},
		{"agentActivities", append(slices.Clone(data.AgentActivities), domain.AgentActivity{ID: "new-activity", Body: "appended"})},
	} {
		index, _, _ := metadataLogicalField(field.name)
		value, err := merged.fields[index].Value(t.Context(), 32<<20)
		if err != nil || !reflect.DeepEqual(value.Interface(), field.want) {
			t.Fatal("native occurrence merge or append changed", field.name, err)
		}
	}
	if repo.WorkspaceMetadataDecodes() != 0 || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("native session closure decoded metadata or leaked")
	}
}

func TestScaleP21SW198SessionLifecycleInputRelease(t *testing.T) {
	data := domain.Bootstrap{AgentSessions: []domain.AgentSession{{ID: "target", UserID: "owner", Messages: []domain.AgentMessage{{ID: "message", Content: "body"}}}}}
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	source, err := repo.retainMetadataLogicalSource(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	inputs, err := repo.loadAgentSessionLifecycleInputs(ctx, source, AgentSessionLifecycleScope{ID: "target"})
	if inputs != nil || !errors.Is(err, context.Canceled) || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled closure lost cancellation or leaked", err)
	}
	limit := repo.metadataResidency.work.limit
	for _, budget := range []int{1, 150 << 10, 300 << 10} {
		repo.metadataResidency.work.limit = budget
		inputs, err := repo.loadAgentSessionLifecycleInputs(t.Context(), source, AgentSessionLifecycleScope{ID: "target"})
		if inputs != nil || !errors.Is(err, ErrMetadataWorkBudget) || repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial session closure admission leaked", budget, err)
		}
	}
	repo.metadataResidency.work.limit = limit
	inputs, err = repo.loadAgentSessionLifecycleInputs(t.Context(), source, AgentSessionLifecycleScope{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	inputs.data.AgentSessions[0].Title = "outside lifecycle"
	if merged, err := inputs.merge(t.Context(), AgentSessionLifecycleScope{ID: "target"}); merged != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		if merged != nil {
			merged.Release()
		}
		t.Fatal("session lifecycle changed immutable fields", err)
	}
	inputs.Close()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed native session merge leaked")
	}
}

func TestScaleP21SW198SessionLifecycleSkippedTranscriptBound(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	var baselines [3]int
	for _, scale := range []int{1, 10} {
		body := strings.Repeat("inline user input ", scale*100000)
		data := domain.Bootstrap{
			Workspace: domain.Workspace{ID: "workspace"}, Viewer: domain.User{ID: "owner"},
			AgentSessions: []domain.AgentSession{{
				ID: "session", UserID: "owner", UpdatedAt: at,
				Messages: []domain.AgentMessage{
					{ID: "user", Role: "user", Content: body, CreatedAt: at},
					{ID: "reply", Role: "assistant", Content: "small", Status: "streaming", CreatedAt: at, UpdatedAt: &at},
				},
			}},
		}
		repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		source, err := repo.retainMetadataLogicalSource(t.Context(), "physical")
		if err != nil {
			t.Fatal(err)
		}
		owner, message := "owner", "reply"
		for index, scope := range []AgentSessionLifecycleScope{
			{ID: "session", OwnerID: &owner, FirstSession: true, HeaderOnly: true},
			{ID: "session", OwnerID: &owner, FirstSession: true, MessageID: &message},
			{ID: "session", StreamingMessages: true},
		} {
			inputs, err := repo.loadAgentSessionLifecycleInputs(t.Context(), source, scope)
			if err != nil {
				t.Fatal(err)
			}
			wantMessages := 1
			if scope.HeaderOnly {
				wantMessages = 0
			}
			if len(inputs.data.AgentSessions) != 1 || len(inputs.data.AgentSessions[0].Messages) != wantMessages {
				t.Fatal("skipped transcript entered callback input")
			}
			stamp := at.Add(time.Second)
			inputs.data.AgentSessions[0].LastReadAt = &stamp
			merged, err := inputs.merge(t.Context(), scope)
			if err != nil {
				inputs.Close()
				t.Fatal(err)
			}
			sessions, _, _ := metadataLogicalField("agentSessions")
			seen := false
			err = merged.fields[sessions].Children(t.Context(), func(_ reflect.Value, session metadataLogicalNode) error {
				messages, err := issueHotField(t.Context(), session, "Messages")
				if err != nil {
					return err
				}
				return messages.Children(t.Context(), func(index reflect.Value, node metadataLogicalNode) error {
					if index.Int() != 0 {
						return nil
					}
					content, err := issueHotField(t.Context(), node, "Content")
					if err != nil {
						return err
					}
					same, err := content.StringEqual(t.Context(), body)
					seen = same
					return err
				})
			})
			if err != nil || !seen {
				t.Fatal("untouched large user frame changed", err)
			}
			held := repo.MetadataWorkUsedForTesting()
			if scale == 1 {
				baselines[index] = held
			} else if held != baselines[index] {
				t.Fatal("skipped transcript growth increased admitted native work", index, held, baselines[index])
			}
			merged.Release()
			inputs.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("skipped transcript closure leaked")
			}
			t.Logf("scale=%d profile=%d native held=%d", scale, index, held)
		}
		source.Release()
	}
}
