package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"flow/api/internal/codehost"
)

// APIError is a non-2xx GitHub response. It unwraps to the codehost error
// class (ErrTransient, ErrNoAccess, ErrNotFound) so callers and
// codehost.ErrorCode can classify it.
type APIError struct {
	Status  int
	Message string
	Class   error
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("github: HTTP %d", e.Status)
	}
	return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message)
}

func (e *APIError) Unwrap() error { return e.Class }

func isStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

var requestCounter, _ = otel.Meter("flow/coding").Int64Counter("flow_coding_codehost_requests_total",
	metric.WithDescription("Code host API requests by host, operation and status"))

// classify maps an HTTP status to a codehost error class.
func classify(response *http.Response, message string) error {
	switch status := response.StatusCode; {
	case status == http.StatusTooManyRequests || status >= 500:
		return codehost.ErrTransient
	case status == http.StatusForbidden && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "" || strings.Contains(strings.ToLower(message), "rate limit")):
		return codehost.ErrTransient
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return codehost.ErrNoAccess
	case status == http.StatusNotFound:
		return codehost.ErrNotFound
	default:
		return nil
	}
}

// appRequest calls the API authenticated as the App (JWT).
func (h *Host) appRequest(ctx context.Context, op, method, path string, body, out any) error {
	jwt, err := h.appJWT()
	if err != nil {
		return err
	}
	return h.do(ctx, op, method, h.apiURL+path, "Bearer "+jwt, body, out)
}

// tokenRequest calls the API with an installation or user token.
func (h *Host) tokenRequest(ctx context.Context, op, method, path, token string, body, out any) error {
	return h.do(ctx, op, method, h.apiURL+path, "token "+token, body, out)
}

// do performs one request. Idempotent requests (GET, PATCH of a body,
// DELETE) are retried once on a transient failure; POSTs are not, because
// their callers handle duplicates (PR creation looks up the head first).
func (h *Host) do(ctx context.Context, op, method, target, authorization string, body, out any) error {
	attempts := 1
	if method == http.MethodGet || method == http.MethodDelete {
		attempts = 2
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: %v", codehost.ErrTransient, ctx.Err())
			case <-time.After(300 * time.Millisecond):
			}
		}
		err = h.doOnce(ctx, op, method, target, authorization, body, out)
		if err == nil || !errors.Is(err, codehost.ErrTransient) {
			return err
		}
	}
	return err
}

func (h *Host) doOnce(ctx context.Context, op, method, target, authorization string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if strings.Contains(target, "/login/oauth/") {
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "flow-coding-sessions")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		h.count(ctx, op, "error")
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w: %s %s: %v", codehost.ErrTransient, method, op, err)
	}
	defer response.Body.Close()
	h.count(ctx, op, strconv.Itoa(response.StatusCode))
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("%w: read %s: %v", codehost.ErrTransient, op, err)
	}
	if response.StatusCode >= 300 {
		var payload struct {
			Message string `json:"message"`
			Errors  []struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(raw, &payload)
		message := payload.Message
		for _, item := range payload.Errors {
			if item.Message != "" {
				message += "; " + item.Message
			} else if item.Code != "" {
				message += "; " + item.Code
			}
		}
		return &APIError{Status: response.StatusCode, Message: message, Class: classify(response, message)}
	}
	if out == nil || len(raw) == 0 || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("github: decode %s: %w", op, err)
	}
	return nil
}

func (h *Host) count(ctx context.Context, op, status string) {
	if requestCounter == nil {
		return
	}
	requestCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("host", "github"), attribute.String("op", op), attribute.String("status", status)))
}
