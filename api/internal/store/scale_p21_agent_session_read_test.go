package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21AgentSessionHeaders(data domain.Bootstrap, match AgentSessionMatch) []domain.AgentSession {
	var result []domain.AgentSession
	for _, session := range data.AgentSessions {
		if match.ID != nil && session.ID != *match.ID || match.UserID != nil && session.UserID != *match.UserID {
			continue
		}
		header := domain.AgentSession{ID: session.ID, UserID: session.UserID}
		if session.Messages != nil {
			header.Messages = []domain.AgentMessage{}
		}
		for _, message := range session.Messages {
			header.Messages = append(header.Messages, domain.AgentMessage{ID: message.ID, Status: message.Status, UpdatedAt: message.UpdatedAt})
		}
		result = append(result, header)
	}
	return result
}

func scaleP21AgentMessage(data domain.Bootstrap, sessionID, userID, messageID string) (domain.AgentMessage, bool) {
	for _, session := range data.AgentSessions {
		if session.ID == sessionID && session.UserID == userID {
			for _, message := range session.Messages {
				if message.ID == messageID {
					return message, true
				}
			}
		}
	}
	return domain.AgentMessage{}, false
}

func scaleP21AgentSessionEqual(t *testing.T, r *AgentSessionRead, data domain.Bootstrap, match AgentSessionMatch) {
	t.Helper()
	expected := scaleP21AgentSessionHeaders(data, match)
	first, found, err := r.FirstHeader(t.Context(), match)
	if err != nil || found != (len(expected) != 0) {
		t.Fatal("first session selection changed", found, err)
	}
	if found {
		if !reflect.DeepEqual(first.Data, expected[0]) {
			t.Fatal("first native header changed", first.Data, expected[0])
		}
		first.Close()
	}
	count := 0
	if err := r.VisitHeaders(t.Context(), match, func(header domain.AgentSession) error {
		if count >= len(expected) || !reflect.DeepEqual(header, expected[count]) {
			t.Fatal("ordered session header projection changed", count, header, expected)
		}
		count++
		return nil
	}); err != nil || count != len(expected) {
		t.Fatal("ordered session header count changed", err, count, len(expected))
	}
}

func TestScaleP21AgentSessionNativeOwnership(t *testing.T) {
	at := time.Now()
	data := domain.Bootstrap{AgentSessions: []domain.AgentSession{
		{ID: "duplicate", UserID: "owner", SlugID: "not-a-selector", DeletedAt: &at},
		{ID: "duplicate", UserID: "owner", Messages: []domain.AgentMessage{}},
		{ID: "duplicate", UserID: "other", Messages: []domain.AgentMessage{{ID: "message", Content: "wrong owner"}}},
		{ID: "duplicate", UserID: "owner", Title: "ignored", DeletedAt: &at, Messages: []domain.AgentMessage{
			{ID: "message", Role: "assistant", Content: "complete native message", Parts: []domain.AgentMessagePart{{ID: "part", Type: "text", Text: "part text", Offloaded: domain.OffloadedContent{"text": {Key: "not-hydrated-part", Size: 42}}}}, Mentions: []domain.AgentMention{{Type: "issue", ID: "issue", Label: "Issue"}}, Attachments: []domain.AgentMessageAttachment{{Name: "file", Size: 123, URL: "content://file"}}, DurationMS: 345, AnswerTo: "question", Feedback: &domain.AgentFeedback{Rating: "positive", Comment: "note", UpdatedAt: at}, CreatedAt: at, UpdatedAt: &at, Status: "streaming", Outcome: &domain.AgentReplyOutcome{Status: "interrupted", Reason: "worker_lost"}, WorkerCursor: &domain.AgentWorkerCursor{Run: "run", Attempt: 2, Seq: 9}, Offloaded: domain.OffloadedContent{"content": {Key: "not-hydrated-message", Size: 99, Summary: "summary"}}},
			{ID: "empty-parts", Parts: []domain.AgentMessagePart{}},
			{ID: "nil-parts"},
			{ID: "", Status: "streaming", UpdatedAt: &at},
			{ID: "message\x00id", Content: "NUL"},
		}},
		{ID: "duplicate", UserID: "owner", Messages: []domain.AgentMessage{{ID: "message", Content: "wrong later message"}}},
		{ID: "", UserID: "", Messages: []domain.AgentMessage{{ID: "", Content: "empty selected"}}},
		{ID: "session\x00id", UserID: "owner\x00id", Messages: []domain.AgentMessage{{ID: "message\x00id", Content: "NUL selected"}}},
	}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, key := range []string{"physical", ""} {
		statements := 0
		r, found, err := view.BeginAgentSessionRead(WithSQLStatementObserver(t.Context(), func(string) { statements++ }), key)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		base := view.metadataResidency.work.used
		for _, pair := range [][2]string{{"duplicate", "owner"}, {"", ""}, {"session\x00id", "owner\x00id"}, {"not-a-selector", "owner"}, {"missing", "owner"}} {
			id, userID := pair[0], pair[1]
			scaleP21AgentSessionEqual(t, r, data, AgentSessionMatch{ID: &id, UserID: &userID})
			scaleP21AgentSessionEqual(t, r, data, AgentSessionMatch{ID: &id})
			scaleP21AgentSessionEqual(t, r, data, AgentSessionMatch{UserID: &userID})
		}
		scaleP21AgentSessionEqual(t, r, data, AgentSessionMatch{})
		for _, ids := range [][3]string{{"duplicate", "owner", "message"}, {"duplicate", "owner", "empty-parts"}, {"duplicate", "owner", "nil-parts"}, {"duplicate", "owner", ""}, {"", "", ""}, {"session\x00id", "owner\x00id", "message\x00id"}, {"duplicate", "owner", "missing"}} {
			expected, exists := scaleP21AgentMessage(data, ids[0], ids[1], ids[2])
			point, found, err := r.FirstMessage(t.Context(), ids[0], ids[1], ids[2])
			if err != nil || found != exists {
				t.Fatal("message occurrence selection changed", ids, found, err)
			}
			if found {
				if !reflect.DeepEqual(point.Data, expected) {
					t.Fatal("complete native message changed", ids, point.Data, expected)
				}
				point.Close()
				point.Close()
				if !reflect.DeepEqual(point.Data, domain.AgentMessage{}) {
					t.Fatal("closed message retained output")
				}
			}
		}
		if statements != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != base {
			t.Fatal("session selection decoded metadata or leaked", statements, view.metadataResidency.work.used, base)
		}
		id, userID := "duplicate", "owner"
		header, _, err := r.FirstHeader(t.Context(), AgentSessionMatch{ID: &id, UserID: &userID})
		if err != nil {
			t.Fatal(err)
		}
		message, _, err := r.FirstMessage(t.Context(), id, userID, "message")
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		r.Close()
		if header.Data.ID != id || !reflect.DeepEqual(message.Data, data.AgentSessions[3].Messages[0]) || view.metadataResidency.work.used == 0 {
			t.Fatal("point values did not survive parent close")
		}
		message.Data.Parts[0].Text = "caller mutation"
		header.Close()
		message.Close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("point lifetime leaked")
		}
		if _, _, err := r.FirstHeader(t.Context(), AgentSessionMatch{}); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed reader accepted selection", err)
		}
	}
	if _, found, err := view.BeginAgentSessionRead(t.Context(), "missing"); found || err != nil {
		t.Fatal("missing workspace changed", found, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginAgentSessionRead(ctx, "physical"); !errors.Is(err, context.Canceled) {
		t.Fatal("reader ignored cancellation", err)
	}
	r, _, err := view.BeginAgentSessionRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	base := view.metadataResidency.work.used
	ctx, cancel = context.WithCancel(t.Context())
	calls := 0
	if err := r.VisitHeaders(ctx, AgentSessionMatch{}, func(domain.AgentSession) error {
		calls++
		if view.metadataResidency.work.used <= base {
			t.Error("streamed header lacked an admitted owner")
		}
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) || calls != 1 || view.metadataResidency.work.used != base {
		t.Fatal("callback cancellation leaked or continued", calls, err)
	}
	sentinel := errors.New("stop callback")
	if err := r.VisitHeaders(t.Context(), AgentSessionMatch{}, func(domain.AgentSession) error { return sentinel }); !errors.Is(err, sentinel) || view.metadataResidency.work.used != base {
		t.Fatal("callback failure lost or leaked", err)
	}
	if _, _, err := r.FirstHeader(ctx, AgentSessionMatch{}); !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != base {
		t.Fatal("canceled point header lost or leaked", err)
	}
	if _, _, err := r.FirstMessage(ctx, "duplicate", "owner", "message"); !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != base {
		t.Fatal("canceled point message lost or leaked", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 200 << 10
	if _, _, err := r.FirstMessage(t.Context(), "duplicate", "owner", "message"); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != base {
		t.Fatal("partial complete-message budget failure lost or leaked", err)
	}
	view.metadataResidency.work.limit = limit
	r.Close()
	for _, budget := range []int{1, 130 << 10, 194 << 10} {
		view.metadataResidency.work.limit = budget
		r, _, err := view.BeginAgentSessionRead(t.Context(), "physical")
		if err == nil {
			_, _, err = r.FirstHeader(t.Context(), AgentSessionMatch{})
			r.Close()
		}
		if !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("partial header budget failure lost or leaked", budget, err)
		}
	}
	view.metadataResidency.work.limit = limit
	var jobs sync.WaitGroup
	for range 4 {
		jobs.Go(func() {
			for range 3 {
				r, found, err := view.BeginAgentSessionRead(t.Context(), "physical")
				if err != nil || !found {
					t.Error(found, err)
					return
				}
				message, found, err := r.FirstMessage(t.Context(), "duplicate", "owner", "message")
				if err != nil || !found || message.Data.Parts[0].Text != "part text" {
					t.Error("concurrent native message changed", found, err)
				}
				message.Close()
				r.Close()
			}
		})
	}
	jobs.Wait()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("concurrent readers leaked")
	}
}

func TestScaleP21AgentSessionWarmReloadAndPin(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	sessionID, userID := "session-selected", f.data.Viewer.ID
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	seed := func(size int, replacement bool) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "agentSessions", "documents"), workspace, "scale_p21.agent_sessions", "", nil, func(data *domain.Bootstrap) error {
			text := "selected"
			if replacement {
				text = "replacement"
			}
			data.AgentSessions = []domain.AgentSession{
				{ID: sessionID, UserID: userID, Title: strings.Repeat("ignored title ", size), DeletedAt: &at, Messages: []domain.AgentMessage{
					{ID: "unselected", Content: strings.Repeat("ignored message body ", size), Parts: []domain.AgentMessagePart{{Text: strings.Repeat("ignored part ", size)}}},
					{ID: "selected", Content: text, Parts: []domain.AgentMessagePart{}, CreatedAt: at, UpdatedAt: &at, Status: "streaming"},
				}},
				{ID: "unrelated", UserID: "other", Messages: []domain.AgentMessage{{ID: "unrelated", Content: strings.Repeat("ignored unrelated ", size)}}},
			}
			data.Documents = []domain.Document{{ID: "agent-unrelated-document", Content: strings.Repeat("ignored document ", size)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	expectedWork := map[bool]int{}
	for _, size := range []int{1000, 10000} {
		seed(size, false)
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			legacy, _ := f.WorkspaceMetadataFields(workspace, "agentSessions")
			statements := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
			r, found, err := view.BeginAgentSessionRead(ctx, workspace)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			scaleP21AgentSessionEqual(t, r, legacy, AgentSessionMatch{})
			readerWork := view.metadataResidency.work.used
			header, _, err := r.FirstHeader(ctx, AgentSessionMatch{ID: &sessionID, UserID: &userID})
			if err != nil {
				t.Fatal(err)
			}
			headerWork := view.metadataResidency.work.used
			message, found, err := r.FirstMessage(ctx, sessionID, userID, "selected")
			expected, _ := scaleP21AgentMessage(legacy, sessionID, userID, "selected")
			if err != nil || !found || !reflect.DeepEqual(message.Data, expected) {
				t.Fatal("reloaded complete message changed", found, err)
			}
			held := view.metadataResidency.work.used
			if expectedWork[reload] == 0 {
				expectedWork[reload] = held
			}
			if held != expectedWork[reload] || statements != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unselected bodies increased admission or decoded", held, expectedWork, statements)
			}
			header.Close()
			message.Close()
			r.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("warm/reloaded selection leaked")
			}
			t.Logf("body_repetitions=%d reload=%t reader_work=%d header_work=%d held_work=%d", size, reload, readerWork, headerWork, held)
		}
	}
	old, _ := f.WorkspaceMetadataFields(workspace, "agentSessions")
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := view.BeginAgentSessionRead(bound, workspace)
	if err != nil {
		t.Fatal(err)
	}
	seed(1000, true)
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	parent.Close()
	scaleP21AgentSessionEqual(t, r, old, AgentSessionMatch{})
	point, found, err := r.FirstMessage(t.Context(), sessionID, userID, "selected")
	if err != nil || !found || point.Data.Content != "selected" {
		t.Fatal("pinned reader replaced selected message", found, err)
	}
	r.Close()
	seed(10000, true)
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	if point.Data.Content != "selected" || view.metadataResidency.work.used == 0 {
		t.Fatal("pinned point lost parent lifetime")
	}
	point.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("pinned session read leaked")
	}
}
