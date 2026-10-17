package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type changesResponse struct {
	Events         []domain.RealtimeEvent `json:"events"`
	Next           int64                  `json:"next"`
	Head           int64                  `json:"head"`
	HasMore        bool                   `json:"hasMore"`
	ResyncRequired bool                   `json:"resyncRequired"`
}

func TestRealtimeChangesAreFilteredPerViewerAndPaginated(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	reader, user := verifiedAuthClient(t, host.URL, "Changes viewer", "changes-viewer@example.test")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, reader, http.MethodPost, host.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	changes := func(client *http.Client, query string) changesResponse {
		return authRequest[changesResponse](t, client, http.MethodGet, host.URL+"/api/realtime/changes?"+query, nil, "test-workspace", http.StatusOK)
	}
	base := changes(reader, "since=0&limit=1").Head
	team := authRequest[domain.Team](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Changes private", "key": "CHP", "private": true}, "", http.StatusCreated)
	private := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "Private change", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	public := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "Public change", "teamId": repo.Bootstrap().Issues[0].Team.ID}, "test-workspace", http.StatusCreated)
	authRequest[domain.Issue](t, admin, http.MethodPatch, host.URL+"/api/issue-records/"+private.ID, map[string]string{"title": "Private edit"}, "test-workspace", http.StatusOK)

	asAdmin := changes(admin, "since="+strconv.FormatInt(base, 10))
	asReader := changes(reader, "since="+strconv.FormatInt(base, 10))
	if asAdmin.ResyncRequired || asReader.ResyncRequired || asAdmin.Head <= base || asReader.Head != asAdmin.Head {
		t.Fatalf("admin=%+v reader=%+v base=%d", asAdmin, asReader, base)
	}
	previous := base
	sawPrivate, sawPublic := false, false
	for _, event := range asAdmin.Events {
		if event.Seq <= previous {
			t.Fatalf("seq not increasing: %d after %d", event.Seq, previous)
		}
		previous = event.Seq
		sawPrivate = sawPrivate || event.AggregateID == private.ID
		sawPublic = sawPublic || event.AggregateID == public.ID
	}
	if !sawPrivate || !sawPublic {
		t.Fatalf("admin missed events: private=%v public=%v", sawPrivate, sawPublic)
	}
	for _, event := range asReader.Events {
		if event.AggregateID == private.ID {
			t.Fatalf("private team issue leaked to another member: %+v", event)
		}
		if event.Seq == 0 {
			t.Fatalf("event without cursor: %+v", event)
		}
	}
	if !slicesContainsAggregate(asReader.Events, public.ID) {
		t.Fatal("reader missed the public issue event")
	}
	if asReader.Next != asReader.Head {
		t.Fatalf("next=%d head=%d: a viewer who saw everything visible resumes at the head", asReader.Next, asReader.Head)
	}

	// Pages of one event reproduce the same stream with no gaps or repeats.
	var paged []int64
	cursor := base
	for rounds := 0; ; rounds++ {
		page := changes(admin, fmt.Sprintf("since=%d&limit=1", cursor))
		for _, event := range page.Events {
			paged = append(paged, event.Seq)
		}
		cursor = page.Next
		if !page.HasMore || rounds > 100 {
			break
		}
	}
	if len(paged) != len(asAdmin.Events) || cursor != asAdmin.Next {
		t.Fatalf("paged %v next=%d, want %d events next=%d", paged, cursor, len(asAdmin.Events), asAdmin.Next)
	}

	// Unknown and future cursors force a snapshot reload.
	for _, query := range []string{"since=" + strconv.FormatInt(asAdmin.Head+100, 10), "since=abc", ""} {
		if got := changes(reader, query); !got.ResyncRequired || got.Head != asAdmin.Head || len(got.Events) != 0 {
			t.Fatalf("%q => %+v", query, got)
		}
	}

	// Snapshot responses carry the cursor they were read at.
	request, _ := http.NewRequest(http.MethodGet, host.URL+"/api/bootstrap", nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := reader.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := response.Header.Get(changeCursorHeader); got != strconv.FormatInt(asAdmin.Head, 10) {
		t.Fatalf("bootstrap cursor header %q, want %d", got, asAdmin.Head)
	}
}

func slicesContainsAggregate(events []domain.RealtimeEvent, id string) bool {
	for _, event := range events {
		if event.AggregateID == id {
			return true
		}
	}
	return false
}

func TestRealtimeReplayAcceptsChangeCursors(t *testing.T) {
	hub := newRealtimeHub()
	for i := 1; i <= 3; i++ {
		hub.publish("w", domain.RealtimeEvent{ID: fmt.Sprintf("evt_%d", i), Seq: int64(i), Type: "issue.updated"})
	}
	channel, unsubscribe := hub.subscribeSince("w", "1")
	defer unsubscribe()
	if len(channel) != 2 || (<-channel).Seq != 2 {
		t.Fatal("a numeric cursor did not replay the events after it")
	}
	// Event ids keep working for clients that remember them.
	other, stop := hub.subscribeSince("w", "evt_2")
	defer stop()
	if len(other) != 1 {
		t.Fatalf("event id replay returned %d events", len(other))
	}
	missing, stopMissing := hub.subscribeSince("w", "99")
	defer stopMissing()
	if (<-missing).Type != "workspace.resync_required" {
		t.Fatal("an unknown cursor must request a resync")
	}
}

func TestEphemeralRealtimeEventKeepsPersistedAgentReplies(t *testing.T) {
	if ephemeralRealtimeEvent("agent_task.replied") {
		t.Fatal("agent_task.replied is persisted and must carry a cursor")
	}
	for _, eventType := range []string{"agent_task.updated", "agent_task.stream", "presence.updated", "loop_run.updated"} {
		if !ephemeralRealtimeEvent(eventType) {
			t.Fatalf("%s must stay ephemeral", eventType)
		}
	}
}

func TestRealtimeStreamReplaysNumericCursorFromDurableLog(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sse-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir(), realtime: newRealtimeHub()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	changes := func(since int64) changesResponse {
		return authRequest[changesResponse](t, admin, http.MethodGet, host.URL+"/api/realtime/changes?since="+strconv.FormatInt(since, 10), nil, "test-workspace", http.StatusOK)
	}
	base := changes(0).Head
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "Replay me", "teamId": repo.Bootstrap().Issues[0].Team.ID}, "test-workspace", http.StatusCreated)

	// The hub's ring was never fed (as after a restart), yet the cursor replays.
	request, _ := http.NewRequest(http.MethodGet, host.URL+"/api/realtime/events?since="+strconv.FormatInt(base, 10), nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := admin.Do(request.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "workspace.resync_required") {
			t.Fatal("durable cursor produced a resync")
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, issue.ID) {
			return
		}
	}
	t.Fatalf("replay never delivered the issue event: %v", scanner.Err())
}
