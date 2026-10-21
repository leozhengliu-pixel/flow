package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
)

func TestReviewImportClassifiesEachFailureKind(t *testing.T) {
	timeout := &url.Error{Op: "Get", URL: "https://host.invalid/x", Err: &net.DNSError{IsTimeout: true, Err: "i/o timeout"}}
	unreachable := &url.Error{Op: "Get", URL: "https://host.invalid/x", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}
	cases := []struct {
		name   string
		err    error
		code   string
		params map[string]string
	}{
		{"rate limited answer", &reviewRemoteError{Provider: "github", Status: 403, RateLimited: true}, reviewImportCodeRateLimited, map[string]string{"status": "403"}},
		{"429", &reviewRemoteError{Provider: "gitlab", Status: 429}, reviewImportCodeRateLimited, map[string]string{"status": "429"}},
		{"budget stop", &reviewImportRateLimit{}, reviewImportCodeRateLimited, nil},
		{"5xx", &reviewRemoteError{Provider: "gitlab", Status: 503}, reviewImportCodeUpstreamError, map[string]string{"status": "503"}},
		{"revoked token", &reviewRemoteError{Provider: "github", Status: 401}, reviewImportCodeAuthFailed, map[string]string{"status": "401"}},
		{"forbidden", &reviewRemoteError{Provider: "github", Status: 403}, reviewImportCodeAuthFailed, map[string]string{"status": "403"}},
		{"not found", fmt.Errorf("pull request files: %w", &reviewRemoteError{Provider: "github", Status: 404}), reviewImportCodeNotFound, map[string]string{"status": "404"}},
		{"rejected", &reviewRemoteError{Provider: "github", Status: 422}, reviewImportCodeRejected, map[string]string{"status": "422"}},
		{"no access", codehost.ErrNoAccess, reviewImportCodeNoAccess, nil},
		{"not connected", codehost.ErrNotConnected, reviewImportCodeNotConnected, nil},
		{"no source", &reviewImportNoSourceError{provider: "gitlab"}, reviewImportCodeNoSource, map[string]string{"provider": "GitLab"}},
		{"too many files", fmt.Errorf("%w (read 3000 files)", errReviewTooManyFiles), reviewImportCodeTooManyFiles, nil},
		{"timeout", fmt.Errorf("repository API request failed: %w", timeout), reviewImportCodeTimeout, nil},
		{"deadline", fmt.Errorf("sync: %w", context.DeadlineExceeded), reviewImportCodeTimeout, nil},
		{"network", fmt.Errorf("repository API request failed: %w", unreachable), reviewImportCodeNetwork, nil},
		{"unknown", errors.New("something odd"), reviewImportCodeUnknown, nil},
	}
	for _, tc := range cases {
		code, params := reviewImportClassify(tc.err)
		if code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, code, tc.code)
		}
		for key, want := range tc.params {
			if params[key] != want {
				t.Errorf("%s: params[%s] = %q, want %q", tc.name, key, params[key], want)
			}
		}
		// Parameters never carry a URL or a credential.
		for _, value := range params {
			if strings.Contains(value, "://") {
				t.Errorf("%s: param holds a URL: %q", tc.name, value)
			}
		}
	}
}

func TestReviewImportStoredErrorKeepsCodeAndReadsOldRows(t *testing.T) {
	text := reviewImportEncodeError(reviewImportCodeUpstreamError, map[string]string{"status": "502"}, `gitlab: HTTP 502: "bad" gateway`)
	code, params, message := reviewImportDecodeError(text)
	if code != reviewImportCodeUpstreamError || params["status"] != "502" || message != `gitlab: HTTP 502: "bad" gateway` {
		t.Fatalf("decoded = %q %v %q", code, params, message)
	}
	long := reviewImportEncodeError(reviewImportCodeUnknown, nil, strings.Repeat(`"é`, 400))
	if len(long) > 1000 {
		t.Fatalf("encoded error is %d bytes; the store would cut it", len(long))
	}
	if code, _, _ := reviewImportDecodeError(long); code != reviewImportCodeUnknown {
		t.Fatal("a long message broke the stored JSON")
	}
	// A row from before codes holds the bare message.
	code, params, message = reviewImportDecodeError("github: HTTP 500: boom")
	if code != "" || params != nil || message != "github: HTTP 500: boom" {
		t.Fatalf("old row = %q %v %q", code, params, message)
	}
}

func TestReviewImportRecordsTheCodeOfEachRepositoryFailure(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusNotFound, reviewImportCodeNotFound},
		{http.StatusUnauthorized, reviewImportCodeAuthFailed},
		{http.StatusBadGateway, reviewImportCodeUpstreamError},
	} {
		fake := newFakeImportHost(t, true)
		fake.open(1, "Change 1", "change-1")
		fake.failing["/merge_requests"] = tc.status
		handler, _, _ := newReviewImportServer(t, fake, nil)
		approveImport(t, handler, "gitlab", "group/sub/app")
		result := requestJSON[reviewImportResult](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusOK)
		if len(result.Failures) != 1 || result.Failures[0].Code != tc.code || result.Failures[0].Params["status"] != fmt.Sprint(tc.status) || result.Failures[0].Message == "" {
			t.Fatalf("status %d: failures = %+v", tc.status, result.Failures)
		}
		status := requestJSON[reviewImportStatus](t, handler, http.MethodGet, "/api/reviews/import", nil, http.StatusOK)
		if len(status.Backoffs) != 1 || status.Backoffs[0].Code != tc.code || status.Backoffs[0].Params["status"] != fmt.Sprint(tc.status) || strings.HasPrefix(status.Backoffs[0].Message, "{") {
			t.Fatalf("status %d: backoffs = %+v", tc.status, status.Backoffs)
		}
		if status.Backoffs[0].BackoffUntil == nil || time.Until(*status.Backoffs[0].BackoffUntil) <= 0 {
			t.Fatalf("no backoff: %+v", status.Backoffs[0])
		}
	}
}

func TestReviewImportRequestErrorsCarryCodes(t *testing.T) {
	fake := newFakeImportHost(t, true)
	handler, _, _ := newReviewImportServer(t, fake, nil)
	bad := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/reviews/import", map[string]any{"repositories": []map[string]string{{"provider": "svn", "repository": "a/b"}}}, http.StatusBadRequest)
	if bad["code"] != reviewImportCodeProviderInvalid {
		t.Fatalf("provider answer = %+v", bad)
	}
	repositories := []map[string]string{}
	for i := 0; i <= reviewImportMaxApproved; i++ {
		repositories = append(repositories, map[string]string{"provider": "gitlab", "repository": fmt.Sprintf("group/app%d", i)})
	}
	many := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/reviews/import", map[string]any{"repositories": repositories}, http.StatusBadRequest)
	if many["code"] != reviewImportCodeTooManyRepos || many["params"].(map[string]any)["max"] != fmt.Sprint(reviewImportMaxApproved) {
		t.Fatalf("too many answer = %+v", many)
	}
	// Not turned on for the workspace yet.
	off := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/reviews/import", nil, http.StatusConflict)
	if off["code"] != reviewImportCodeWorkspaceOff {
		t.Fatalf("workspace off answer = %+v", off)
	}
}
