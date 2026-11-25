package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW44ChatFixture(t *testing.T) (*chatWorkflowFixture, *scaleP04Database, domain.Issue) {
	t.Helper()
	f := newChatWorkflowFixture(t)
	database := newScaleP04Database(t)
	f.s.store = database.repo
	if err := database.repo.EnsureChatIntegrationsSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, found := database.repo.BootstrapFor(database.key)
	if !found || len(data.Issues) == 0 {
		t.Fatal("owned chat fixture lacks an issue")
	}
	f.record.WorkspaceKey, f.record.ConnectionID = database.key, idgen.New("sw44-connection-")
	f.record.AppID, f.record.TenantKey = idgen.New("cli_sw44_"), idgen.New("sw44-tenant-")
	f.identity.UserID = data.Viewer.ID
	f.identity.TenantKey = f.record.TenantKey
	f.binding.ID, f.binding.ConnectionID, f.binding.TeamID = idgen.New("sw44-binding-"), f.record.ConnectionID, data.Issues[0].Team.ID
	if err := database.repo.PutChatIntegration(t.Context(), f.record); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := database.repo.DeleteConnectorSecret(ctx, chatCredentialID(f.record)); err != nil {
			t.Error("clean up owned chat credentials", err)
		}
		if err := database.repo.DeleteChatIntegration(ctx, database.key, f.record.ConnectionID); err != nil {
			t.Error("clean up owned chat integration", err)
		}
	})
	if err := database.repo.PutChatIdentity(t.Context(), database.key, f.record.ConnectionID, f.identity); err != nil {
		t.Fatal(err)
	}
	if err := database.repo.PutChatBinding(t.Context(), database.key, f.binding); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.credentials)
	if err := database.repo.PutConnectorSecret(t.Context(), chatCredentialID(f.record), raw, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.syncChatConnection(t.Context(), f.record, f.identity.UserID, "connected", "", "Chat delivery: previous failure"); err != nil {
		t.Fatal(err)
	}
	return f, database, data.Issues[0]
}

func scaleP21SW44Claim(t *testing.T, f *chatWorkflowFixture, issue domain.Issue, attempts int) domain.ChatJob {
	t.Helper()
	payload, _ := json.Marshal(chatNotificationJob{
		BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding),
		IssueID: issue.ID, Kind: "result", PersonalOpenID: f.identity.OpenID, Operation: "create",
	})
	job := domain.ChatJob{ID: idgen.New("sw44-job-"), WorkspaceKey: f.record.WorkspaceKey,
		ConnectionID: f.record.ConnectionID, Kind: "notification", DedupKey: idgen.New("sw44-dedup-"), Payload: payload}
	if added, err := f.s.store.EnqueueChatJob(t.Context(), job); err != nil || !added {
		t.Fatal("enqueue owned job", added, err)
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, job.Kind, 1, time.Now().Add(time.Second))
		if err != nil || len(jobs) != 1 {
			t.Fatal("claim owned job", attempt, jobs, err)
		}
		job = jobs[0]
		if attempt < attempts {
			if err := f.s.store.FinishChatJob(t.Context(), job, "pending", time.Now().Add(-time.Second), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	return job
}

type scaleP21SW44ChatResult struct {
	Status, LastError, HealthError string
	Attempts, Messages             int
	LeaseReleased, HealthDelivered bool
	Canceled                       bool
}

func scaleP21SW44Run(t *testing.T, mode string, bounded bool, jobs int) []scaleP21SW44ChatResult {
	t.Helper()
	f, database, issue := scaleP21SW44ChatFixture(t)
	if bounded {
		view, err := database.repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), database.key)
		if err != nil {
			t.Fatal(err)
		}
		f.s.store = view
	}
	outer, closeOuter := beginMetadataOutputReads(t.Context())
	defer closeOuter()
	outerScope := outer.Value(requestResourceReadsKey{}).(*requestResourceReads)
	outerClosed := 0
	if !registerRequestReadClose(outer, func() { outerClosed++ }) {
		t.Fatal("missing outer control scope")
	}
	var results []scaleP21SW44ChatResult
	var priorScope *requestResourceReads
	for index := range jobs {
		attempts := 1
		if mode == "exhausted" || mode == "processing-final" {
			attempts = 8
		}
		job := scaleP21SW44Claim(t, f, issue, attempts)
		ctx, cancel := context.WithCancel(outer)
		var scope *requestResourceReads
		closed, finishStatements, healthStatements, posts := 0, 0, 0, 0
		sentBefore := len(f.sent)
		transport := http.DefaultTransport
		http.DefaultTransport = chatWorkflowTransport(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || !strings.Contains(request.URL.Path, "/im/v1/messages") {
				return transport.RoundTrip(request)
			}
			posts++
			if bounded {
				current, ok := request.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
				if !ok || current == nil || current == outerScope || current == priorScope {
					t.Fatal("claimed job borrowed an outer or previous job's output scope")
				}
				if scope == nil {
					scope = current
					if !registerRequestReadClose(request.Context(), func() { closed++ }) {
						t.Fatal("job scope refused output before its platform operation")
					}
				} else if scope != current {
					t.Fatal("one claimed job replaced its scope between platform calls")
				}
				if f.s.store.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("actual point response was released before sending its result")
				}
			}
			switch mode {
			case "retry", "exhausted":
				response := chatTestResponse(http.StatusTooManyRequests, `{"code":99991400}`)
				response.Header.Set("Retry-After", "90")
				return response, nil
			case "platform-final":
				return chatTestResponse(http.StatusBadRequest, `{"code":230013}`), nil
			case "processing-final":
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":7}}`), nil
			case "cancel":
				cancel()
				return nil, context.Canceled
			default:
				return transport.RoundTrip(request)
			}
		})
		observed := store.WithSQLStatementObserver(ctx, func(query string) {
			if posts == 0 {
				return
			}
			finish := strings.HasPrefix(query, "UPDATE chat_jobs SET status=?")
			health := strings.Contains(query, "FROM chat_integrations")
			if !finish && !health {
				return
			}
			if finish {
				finishStatements++
			} else {
				healthStatements++
			}
			if bounded {
				if scope == nil || scope.closed || closed != 0 || f.s.store.MetadataWorkUsedForTesting() == 0 {
					t.Error("job output closed before health/FinishChatJob SQL", query, closed)
				}
			}
		})
		before := time.Now()
		err := f.s.processChatClaimedJob(observed, job)
		after := time.Now()
		http.DefaultTransport = transport
		cancel()
		if mode == "cancel" {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("parent cancellation was not returned", err)
			}
		} else if err != nil {
			t.Fatal("claimed job lifecycle failed", mode, bounded, err)
		}
		if bounded && (scope == nil || !scope.closed || closed != 1 || f.s.store.MetadataWorkUsedForTesting() != 0) {
			t.Fatal("job scope did not end with this claimed job", index, closed, f.s.store.MetadataWorkUsedForTesting())
		}
		if outerScope.closed || outerClosed != 0 {
			t.Fatal("one job completed the containing worker/request scope")
		}
		priorScope = scope
		result := scaleP21SW44ChatResult{Messages: len(f.sent) - sentBefore, Canceled: errors.Is(err, context.Canceled)}
		var runAt, lease int64
		rows := database.query(t, `SELECT status,attempts,run_at,lease_until,last_error FROM chat_jobs WHERE id=?`, job.ID)
		if !rows.Next() {
			rows.Close()
			t.Fatal("finished job disappeared")
		}
		if err := rows.Scan(&result.Status, &result.Attempts, &runAt, &lease, &result.LastError); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		result.LeaseReleased = lease == 0
		if err := database.repo.ReloadWorkspace(t.Context(), database.key); err != nil {
			t.Fatal("reload canonical job health", err)
		}
		data, found := database.repo.WorkspaceMetadataFields(database.key, "integrationConnections")
		if !found {
			t.Fatal("job health workspace disappeared")
		}
		for _, connection := range data.IntegrationConnections {
			if connection.ID == f.record.ConnectionID {
				result.HealthError = connection.LastError
				result.HealthDelivered = connection.LastDeliveryAt != nil
			}
		}
		wantStatus := "failed"
		switch mode {
		case "success":
			wantStatus = "completed"
			if !result.HealthDelivered || result.HealthError != "" || healthStatements == 0 {
				t.Fatal("successful send did not record and clear health", result, healthStatements)
			}
		case "retry":
			wantStatus = "pending"
			if runAt < before.Add(90*time.Second).UnixMilli() || runAt > after.Add(90*time.Second).UnixMilli() ||
				result.HealthDelivered || result.HealthError != "Chat delivery: previous failure" {
				t.Fatal("Retry-After or nonfinal health behavior changed", result, time.UnixMilli(runAt))
			}
		case "cancel":
			wantStatus = "processing"
		}
		if result.Status != wantStatus || result.Attempts != attempts || result.LeaseReleased != (mode != "cancel") ||
			finishStatements != 1 {
			t.Fatal("job finalization/attempt/lease changed", mode, result, finishStatements)
		}
		if mode == "platform-final" || mode == "exhausted" {
			if !strings.HasPrefix(result.HealthError, "Chat delivery: Chat platform request failed") || healthStatements == 0 {
				t.Fatal("final platform failure did not update health", result, healthStatements)
			}
		}
		if mode == "processing-final" && (!strings.HasPrefix(result.HealthError, chatProcessingErrorPrefix) ||
			result.LastError != result.HealthError || healthStatements == 0) {
			t.Fatal("error-derived processing health/message changed", result, healthStatements)
		}
		results = append(results, result)
	}
	closeOuter()
	if outerClosed != 1 {
		t.Fatal("outer control lifetime did not close exactly once", outerClosed)
	}
	return results
}

func TestScaleP21SW44ChatClaimedJobScopeLifecycle(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, mode := range []string{"success", "retry", "platform-final", "processing-final", "exhausted", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			jobs := 1
			if mode == "success" {
				jobs = 2
			}
			literal := scaleP21SW44Run(t, mode, false, jobs)
			bounded := scaleP21SW44Run(t, mode, true, jobs)
			if !reflect.DeepEqual(literal, bounded) {
				t.Fatal("extracted owned job differs from literal lifecycle", fmt.Sprint(literal), fmt.Sprint(bounded))
			}
		})
	}
}
