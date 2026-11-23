package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
)

func TestIssueQueryFailureLogsSafeDriverCode(t *testing.T) {
	logs := captureServerLogs(t)
	const privateValue = "private-fixture-marker"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/issue-records/{id}", func(w http.ResponseWriter, r *http.Request) {
		issueRecordsError(w, fmt.Errorf("projection %s: %w", r.URL.Query().Get("q"), &mysql.MySQLError{
			Number: 1054, Message: r.PathValue("id"),
		}))
	})
	r := httptest.NewRequest(http.MethodGet, "/api/issue-records/"+privateValue+"?q="+privateValue, nil)
	r.Header.Set("X-Request-ID", privateValue)
	w := httptest.NewRecorder()
	requestLog(mux, apiErrorEnvelope(mux)).ServeHTTP(w, r)
	body := decodeErrorBody(t, w)
	if w.Code != http.StatusInternalServerError || body["error"] != internalErrorMessage || body["requestId"] != privateValue {
		t.Fatalf("unexpected HTTP status or error envelope: %d", w.Code)
	}
	text := logs.String()
	cause := regexp.MustCompile(`msg="issue query failed" request_id=([0-9a-f]{32}) cause=mysql_1054`).FindStringSubmatch(text)
	if len(cause) == 0 || !strings.Contains(text, `msg="http request" request_id=`+cause[1]+` route="GET /api/issue-records/{id}"`) || strings.Contains(text, privateValue) {
		t.Fatal("failed query did not log a redacted driver code with a matching server request ID")
	}
}

func TestSyncChangesFailureLogsSafeDriverCode(t *testing.T) {
	logs := captureServerLogs(t)
	const privateValue = "private-fixture-marker"
	r := httptest.NewRequest(http.MethodGet, "/api/sync/changes?after="+privateValue, nil)
	r.Header.Set("X-Request-ID", privateValue)
	w := httptest.NewRecorder()
	requestLog(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSyncChangesUnavailable(w, r, &mysql.MySQLError{Number: 1054, Message: privateValue})
	})).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("sync projection returned %d, want 503", w.Code)
	}
	if text := logs.String(); !strings.Contains(text, "cause=mysql_1054") || strings.Contains(text, privateValue) {
		t.Fatal("sync projection did not log a redacted driver code")
	}
}

func TestIssueQueryConnectionFailureRetryClassification(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	for _, tc := range []struct {
		name, method string
		err          error
		status       int
	}{
		{"read with lost connection", http.MethodGet, mysql.ErrInvalidConn, http.StatusServiceUnavailable},
		{"read with refused dial", http.MethodGet, refused, http.StatusServiceUnavailable},
		{"read at server connection limit", http.MethodGet, &mysql.MySQLError{Number: 1040}, http.StatusServiceUnavailable},
		{"write with unknown outcome", http.MethodPatch, mysql.ErrInvalidConn, http.StatusInternalServerError},
		{"unattributed short read", http.MethodGet, io.ErrUnexpectedEOF, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, "/api/issue-records", nil)
			requestLog(nil, apiErrorEnvelope(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				issueRecordsError(w, tc.err)
			}))).ServeHTTP(w, r)
			retry := w.Header().Get("Retry-After")
			if w.Code != tc.status || (retry == "2") != (tc.status == http.StatusServiceUnavailable) {
				t.Fatalf("response status %d and Retry-After %q, want %d", w.Code, retry, tc.status)
			}
		})
	}

	t.Run("pre-write reload", func(t *testing.T) {
		logs := captureServerLogs(t)
		const privateValue = "private-fixture-marker"
		err := fmt.Errorf("reload %s: %w", privateValue, store.ErrDatabaseUnavailable)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/labels", nil)
		requestLog(nil, apiErrorEnvelope(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondMutation(w, err, http.StatusCreated, nil)
		}))).ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
			t.Fatal("a recognized pre-write failure must be retryable")
		}
		if text := logs.String(); !strings.Contains(text, "workspace reload failed before mutation") || strings.Contains(text, privateValue) {
			t.Fatal("the refused reload did not log a redacted failure")
		}
	})
}
