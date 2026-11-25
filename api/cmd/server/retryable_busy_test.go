package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/coordination"
	"flow/api/internal/store"
)

func TestIssueRecordsErrorMapsBusyTo503WithRetryAfter(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  int
		retry string
	}{
		{"cold slot busy", fmt.Errorf("query: %w", store.ErrColdMetadataBusy), http.StatusServiceUnavailable, "5"},
		{"workspace lock busy", fmt.Errorf("lock: %w", coordination.ErrWorkspaceLockBusy), http.StatusServiceUnavailable, "1"},
		{"work budget", store.ErrMetadataWorkBudget, http.StatusServiceUnavailable, "2"},
		{"handler deadline", fmt.Errorf("search: %w", context.DeadlineExceeded), http.StatusServiceUnavailable, "2"},
		{"other error stays 500", errors.New("boom"), http.StatusInternalServerError, ""},
		{"forbidden stays 403", store.ErrAuthForbidden, http.StatusForbidden, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			issueRecordsErrorFor(w, httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil), tc.err)
			if w.Code != tc.code || w.Header().Get("Retry-After") != tc.retry {
				t.Fatalf("status %d Retry-After %q, want %d %q: %s", w.Code, w.Header().Get("Retry-After"), tc.code, tc.retry, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	issueRecordsError(w, context.Canceled)
	if w.Body.Len() != 0 {
		t.Fatalf("a cancelled request must write nothing: %s", w.Body.String())
	}
}

func TestSyncChangesUnavailableCarriesRetryAfter(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/sync/changes?after=0", nil)
	w := httptest.NewRecorder()
	writeSyncChangesUnavailable(w, r, fmt.Errorf("journal: %w", store.ErrColdMetadataBusy))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("busy: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	w = httptest.NewRecorder()
	writeSyncChangesUnavailable(w, r, errors.New("journal unreadable"))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("other: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestWriteSyncErrorMapsBusy(t *testing.T) {
	logs := captureServerLogs(t)
	w := httptest.NewRecorder()
	writeSyncError(w, fmt.Errorf("synthetic credential record: %w", store.ErrColdMetadataBusy))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Retry-After"))
	}
	w = httptest.NewRecorder()
	writeSyncError(w, fmt.Errorf("synthetic credential record: %w", mysqlDataTooLong()))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected database error status %d", w.Code)
	}
	logged := logs.String()
	if strings.Contains(logged, "synthetic credential record") || strings.Contains(logged, "Data too long") ||
		!strings.Contains(logged, "cause=cold_slot_busy") || !strings.Contains(logged, "cause=mysql_1406") {
		t.Fatal("sync logs must record only safe cause codes")
	}
}

func TestMutationBusyPreservesPermanentRefusals(t *testing.T) {
	for _, err := range []error{store.ErrDatabaseWriteBusy, store.ErrRecordSpoolContended} {
		w := httptest.NewRecorder()
		respondMutation(w, err, http.StatusCreated, nil)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
			t.Fatalf("transient write refusal returned %d", w.Code)
		}
	}
	for _, err := range []error{store.ErrMetadataWorkBudget, store.ErrRecordSpoolBudget, context.DeadlineExceeded} {
		w := httptest.NewRecorder()
		if writeStoreBusy(w, err) || w.Header().Get("Retry-After") != "" {
			t.Fatal("classified a permanent or uncertain write failure as retryable")
		}
	}
}
