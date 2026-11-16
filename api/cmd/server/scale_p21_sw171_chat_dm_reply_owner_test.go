package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW171ChatDMReplyIndependentOutputLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, mode := range []string{"completed", "caller-closed", "coordination-canceled", "platform-final"} {
		t.Run(mode, func(t *testing.T) {
			f, database, _ := scaleP21SW44ChatFixture(t)
			view, err := database.repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), database.key)
			if err != nil {
				t.Fatal(err)
			}
			f.s.store = view
			outer, closeOuter := beginMetadataOutputReads(t.Context())
			defer closeOuter()
			outerScope := outer.Value(requestResourceReadsKey{}).(*requestResourceReads)
			var outerClosed atomic.Int32
			if !registerRequestReadClose(outer, func() { outerClosed.Add(1) }) {
				t.Fatal("missing caller control owner")
			}
			parent, cancelParent := context.WithCancel(outer)
			defer cancelParent()
			coordination, cancelCoordination := context.WithCancel(t.Context())
			defer cancelCoordination()
			f.s.coordinationContext = coordination
			var previous *requestResourceReads
			replies := 1
			if mode == "completed" {
				replies = 2
			}
			for index := range replies {
				payload, err := json.Marshal(chatDMReplyJob{
					Command: chatInboundCommand{OpenID: f.identity.OpenID, ChatID: chatDMTestChat, MessageID: idgen.New("sw171-message-")},
					UserID:  f.identity.UserID, Workspace: database.key, ThreadKey: idgen.New("sw171-thread-"),
					Final: &chatDMReplyView{Status: "done", Text: "Stored SW171 reply"},
				})
				if err != nil {
					t.Fatal(err)
				}
				job := domain.ChatJob{
					ID: idgen.New("sw171-job-"), WorkspaceKey: database.key, ConnectionID: f.record.ConnectionID,
					Kind: chatDMReplyJobKind, DedupKey: idgen.New("sw171-dedup-"), Payload: payload,
				}
				if added, err := database.repo.EnqueueChatJob(t.Context(), job); err != nil || !added {
					t.Fatal("enqueue reply", added, err)
				}
				reached, release := make(chan context.Context, 1), make(chan struct{})
				var releaseOnce, captureOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				// Cleanup must unblock a platform call before joining the actual
				// reply goroutine, including when a test assertion fails.
				t.Cleanup(func() {
					cancelCoordination()
					unblock()
					chatDMReplies.Wait()
				})
				var mu sync.Mutex
				var scope *requestResourceReads
				var closes atomic.Int32
				finishSQL, releaseSQL, healthSQL := 0, 0, 0
				transport := http.DefaultTransport
				http.DefaultTransport = chatWorkflowTransport(func(request *http.Request) (*http.Response, error) {
					message := strings.Contains(request.URL.Path, "/im/v1/messages") &&
						(request.Method == http.MethodPost || request.Method == http.MethodPatch)
					if !message {
						return transport.RoundTrip(request)
					}
					current, _ := request.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
					if current == nil || current == outerScope || current == previous {
						t.Error("async reply borrowed its caller or previous reply owner")
					}
					captureOnce.Do(func() {
						mu.Lock()
						scope = current
						mu.Unlock()
						if !registerRequestReadClose(request.Context(), func() {
							if request.Context().Err() == nil {
								t.Error("reply owner closed before its delivery context was canceled")
							}
							closes.Add(1)
						}) {
							t.Error("reply owner refused output during delivery")
						}
						reached <- request.Context()
						<-release
					})
					if current != nil {
						current.mu.Lock()
						closed := current.closed
						current.mu.Unlock()
						if closed || closes.Load() != 0 {
							t.Error("reply output closed while platform delivery was active")
						}
					}
					if mode == "coordination-canceled" {
						return nil, context.Canceled
					}
					if mode == "platform-final" {
						return chatTestResponse(http.StatusBadRequest, `{"code":230013}`), nil
					}
					return transport.RoundTrip(request)
				})
				t.Cleanup(func() { http.DefaultTransport = transport })
				observed := store.WithSQLStatementObserver(parent, func(query string) {
					mu.Lock()
					defer mu.Unlock()
					if scope == nil {
						return
					}
					finish := strings.HasPrefix(query, "UPDATE chat_jobs SET status=?")
					release := strings.HasPrefix(query, "DELETE FROM chat_reply_leases")
					health := strings.Contains(query, "FROM chat_integrations")
					if !finish && !release && !health {
						return
					}
					if finish {
						finishSQL++
					}
					if release {
						releaseSQL++
					}
					if health {
						healthSQL++
					}
					scope.mu.Lock()
					closed := scope.closed
					scope.mu.Unlock()
					if closed || closes.Load() != 0 {
						t.Error("reply output closed before health/finish/release SQL", query)
					}
				})
				if err := f.s.startChatDMReplyJobs(observed, database.key); err != nil {
					unblock()
					chatDMReplies.Wait()
					t.Fatal("start async reply", err)
				}
				var delivery context.Context
				select {
				case delivery = <-reached:
				case <-time.After(10 * time.Second):
					t.Fatal("queued reply did not reach platform delivery")
				}
				if mode == "caller-closed" {
					cancelParent()
					closeOuter()
					if delivery.Err() != nil {
						t.Error("caller cancellation canceled the independent reply")
					}
				}
				if mode == "coordination-canceled" {
					cancelCoordination()
					select {
					case <-delivery.Done():
					case <-time.After(5 * time.Second):
						t.Error("server cancellation did not reach reply delivery")
					}
				}
				if closes.Load() != 0 {
					t.Error("reply owner closed before delivery returned")
				}
				unblock()
				chatDMReplies.Wait()
				http.DefaultTransport = transport
				mu.Lock()
				current, finished, released, health := scope, finishSQL, releaseSQL, healthSQL
				mu.Unlock()
				if current == nil {
					t.Fatal("reply had no independent output owner")
				}
				current.mu.Lock()
				closed := current.closed
				current.mu.Unlock()
				if !closed || closes.Load() != 1 || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("reply goroutine retained output after cancellation and heartbeat join", index, closed, closes.Load(), view.MetadataWorkUsedForTesting())
				}
				if registerRequestReadClose(delivery, func() { t.Error("closed reply owner accepted another output") }) {
					t.Error("finished reply accepted another output")
				}
				if mode != "caller-closed" && outerClosed.Load() != 0 {
					t.Error("reply closed its caller owner")
				}
				rows := database.query(t, `SELECT status FROM chat_jobs WHERE id=?`, job.ID)
				var status string
				if !rows.Next() {
					rows.Close()
					t.Fatal("reply job disappeared")
				}
				if err := rows.Scan(&status); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				rows.Close()
				want := "completed"
				if mode == "coordination-canceled" {
					want = "processing"
					if finished != 0 || released != 0 || health != 0 {
						t.Error("canceled reply acknowledged its job", finished, released, health)
					}
				} else {
					if mode == "platform-final" {
						want = "failed"
					}
					if finished != 1 || released != 1 || health == 0 {
						t.Error("reply did not complete health/finish/release lifecycle", finished, released, health)
					}
				}
				if status != want {
					t.Error("reply completion status", status, "want", want)
				}
				previous = current
			}
		})
	}
}
