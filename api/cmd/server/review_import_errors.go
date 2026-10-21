package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"flow/api/internal/codehost"
)

// Machine-readable codes for what the pull/merge request import records, so
// clients can show the failure in the member's language. The message beside
// a code stays English diagnostic text. Parameters never carry credentials.
const (
	reviewImportCodeRateLimited      = "rate_limited"
	reviewImportCodeUpstreamError    = "upstream_error"
	reviewImportCodeAuthFailed       = "auth_failed"
	reviewImportCodeNotFound         = "repository_not_found"
	reviewImportCodeNoAccess         = "repository_no_access"
	reviewImportCodeNotConnected     = "not_connected"
	reviewImportCodeNoSource         = "no_source"
	reviewImportCodeRejected         = "request_rejected"
	reviewImportCodeNetwork          = "network_unreachable"
	reviewImportCodeTimeout          = "timeout"
	reviewImportCodeTooManyFiles     = "too_many_files"
	reviewImportCodeWorkspaceMissing = "workspace_not_found"
	reviewImportCodeUnknown          = "unknown"

	// Request errors of PATCH and POST /api/reviews/import.
	reviewImportCodeProviderInvalid    = "provider_invalid"
	reviewImportCodeRepositoryInvalid  = "repository_invalid"
	reviewImportCodeSourceNotConnected = "source_not_connected"
	reviewImportCodeTooManyRepos       = "too_many_repositories"
	reviewImportCodeAlreadyRunning     = "already_running"
	reviewImportCodeServerOff          = "server_off"
	reviewImportCodeWorkspaceOff       = "workspace_off"
	reviewImportCodeReadFailed         = "read_failed"
	reviewImportCodeSaveFailed         = "save_failed"
	reviewImportCodeSyncFailed         = "sync_failed"
)

// errReviewTooManyFiles: a request has more changed files than one sync reads.
var errReviewTooManyFiles = errors.New("too many changed files to sync")

// reviewImportNoSourceError: no connection reaches the repository.
type reviewImportNoSourceError struct{ provider string }

func (e *reviewImportNoSourceError) Error() string {
	return "no connected " + reviewHostLabel(e.provider) + " source reaches this repository"
}

// reviewImportClassify names the kind of a failed call and its parameters.
func reviewImportClassify(err error) (string, map[string]string) {
	params := map[string]string{}
	if status := reviewRemoteStatus(err); status > 0 {
		params["status"] = strconv.Itoa(status)
	}
	var limit *reviewImportRateLimit
	var noSource *reviewImportNoSourceError
	var netErr net.Error
	status := reviewRemoteStatus(err)
	limited, _ := reviewRemoteRateLimited(err)
	switch {
	case errors.As(err, &limit) || limited || status == http.StatusTooManyRequests:
		return reviewImportCodeRateLimited, params
	case errors.As(err, &noSource):
		return reviewImportCodeNoSource, map[string]string{"provider": reviewHostLabel(noSource.provider)}
	case errors.Is(err, errReviewTooManyFiles):
		return reviewImportCodeTooManyFiles, params
	case errors.Is(err, codehost.ErrNotConnected):
		return reviewImportCodeNotConnected, params
	case errors.Is(err, codehost.ErrNoAccess):
		return reviewImportCodeNoAccess, params
	case status == http.StatusNotFound || errors.Is(err, codehost.ErrNotFound):
		return reviewImportCodeNotFound, params
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return reviewImportCodeAuthFailed, params
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return reviewImportCodeRejected, params
	case status >= 500 || errors.Is(err, codehost.ErrTransient):
		return reviewImportCodeUpstreamError, params
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return reviewImportCodeTimeout, nil
	case errors.As(err, &netErr):
		return reviewImportCodeNetwork, nil
	}
	return reviewImportCodeUnknown, nil
}

// reviewImportStoredError is the last_error text of a backing-off item: the
// code, its parameters and the message as one JSON document (the column is
// text, so no migration is needed). Older rows hold the bare message.
type reviewImportStoredError struct {
	Code    string            `json:"code"`
	Params  map[string]string `json:"params,omitempty"`
	Message string            `json:"message"`
}

func reviewImportEncodeError(code string, params map[string]string, message string) string {
	stored := reviewImportStoredError{Code: code, Params: params, Message: message}
	for {
		raw, _ := json.Marshal(stored)
		if len(raw) <= 900 || len(stored.Message) <= 40 {
			return string(raw)
		}
		stored.Message = stored.Message[:len(stored.Message)/2]
	}
}

func reviewImportDecodeError(text string) (code string, params map[string]string, message string) {
	if strings.HasPrefix(text, `{"code":"`) {
		var stored reviewImportStoredError
		if json.Unmarshal([]byte(text), &stored) == nil && stored.Code != "" {
			return stored.Code, stored.Params, stored.Message
		}
	}
	return "", nil, text
}

// writeReviewImportError answers a request error with a machine code and
// parameters beside the English message.
func writeReviewImportError(w http.ResponseWriter, status int, code, message string, params map[string]string) {
	body := map[string]any{"error": message, "code": code}
	if len(params) > 0 {
		body["params"] = params
	}
	writeJSON(w, status, body)
}
