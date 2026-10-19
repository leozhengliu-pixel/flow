package gitlab

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

	"flow/api/internal/codehost"
)

const (
	requestTimeout    = 30 * time.Second
	responseBodyLimit = 8 << 20
	errorBodyLimit    = 2 << 10
)

// apiClient calls the GitLab REST API v4 with one token.
type apiClient struct {
	http   *http.Client
	base   string
	token  string
	bearer bool // OAuth tokens use Authorization: Bearer, access tokens PRIVATE-TOKEN
}

// response is a decoded API response.
type response struct {
	Status   int
	Header   http.Header
	NextPage string
}

// do performs one request. path starts with "/" and is relative to /api/v4
// (already escaped). Non-2xx responses become *Error wrapping a codehost
// sentinel; out is decoded on success when non-nil.
func (c *apiClient) do(ctx context.Context, method, path string, payload any, out any) (response, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	endpoint := c.base + "/api/v4" + path
	requestLine := method + " " + redactQuery(path)
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return response{}, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return response{}, &Error{Kind: codehost.ErrNotConnected, Message: "The GitLab URL is invalid.", Request: requestLine}
	}
	if c.bearer {
		request.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		request.Header.Set("PRIVATE-TOKEN", c.token)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Flow-Coding-Sessions")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(ctxErr, context.Canceled) {
			return response{}, ctxErr
		}
		return response{}, &Error{Kind: codehost.ErrTransient, Message: "Could not reach GitLab at " + c.base + ".", Request: requestLine}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, responseBodyLimit))
	result := response{Status: resp.StatusCode, Header: resp.Header, NextPage: strings.TrimSpace(resp.Header.Get("X-Next-Page"))}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, c.apiError(resp, requestLine, raw)
	}
	if readErr != nil {
		return result, &Error{Kind: codehost.ErrTransient, Message: "GitLab's response was cut off.", Status: resp.StatusCode, Request: requestLine}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return result, &Error{Kind: codehost.ErrTransient, Message: "GitLab returned an invalid response.", Status: resp.StatusCode, Request: requestLine}
		}
	}
	return result, nil
}

func (c *apiClient) apiError(resp *http.Response, requestLine string, raw []byte) error {
	snippet := string(raw)
	if len(snippet) > errorBodyLimit {
		snippet = snippet[:errorBodyLimit]
	}
	if c.token != "" {
		snippet = strings.ReplaceAll(snippet, c.token, "[redacted]")
	}
	kind := classify(resp.StatusCode)
	message := gitlabMessage(raw)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		message = MessageTokenRevoked
	case resp.StatusCode == http.StatusForbidden && message == "":
		message = "The GitLab token does not have permission for this request."
	case resp.StatusCode == http.StatusNotFound && message == "":
		message = "GitLab could not find the resource, or the token cannot see it."
	case resp.StatusCode == http.StatusTooManyRequests:
		message = "GitLab is rate limiting Flow. Flow retries automatically."
	case resp.StatusCode >= 500:
		message = fmt.Sprintf("GitLab returned HTTP %d. Flow retries automatically.", resp.StatusCode)
	case message == "":
		message = fmt.Sprintf("GitLab returned HTTP %d.", resp.StatusCode)
	}
	if c.token != "" {
		message = strings.ReplaceAll(message, c.token, "[redacted]")
	}
	if kind == nil {
		kind = errAPI
	}
	return &Error{Kind: kind, Message: message, Status: resp.StatusCode, Request: requestLine, Body: snippet}
}

// errAPI marks a non-retryable API rejection (400, 409, 422, …). It maps to
// the internal code unless the caller handles it (e.g. pullRequestFailed).
var errAPI = errors.New("gitlab: request rejected")

// ErrRejected reports whether err is a non-retryable GitLab API rejection
// (validation error); B maps it to pullRequestFailed while publishing.
func ErrRejected(err error) bool { return errors.Is(err, errAPI) }

// gitlabMessage extracts GitLab's {"message": …} / {"error": …} text.
func gitlabMessage(raw []byte) string {
	var payload struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	switch value := payload.Message.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	return strings.TrimSpace(payload.Error)
}

func redactQuery(path string) string {
	if index := strings.Index(path, "?"); index >= 0 {
		return path[:index]
	}
	return path
}

func statusOf(err error) int {
	var gitlabErr *Error
	if errors.As(err, &gitlabErr) {
		return gitlabErr.Status
	}
	return 0
}

func pageNumber(cursor string) int {
	page, err := strconv.Atoi(strings.TrimSpace(cursor))
	if err != nil || page < 1 {
		return 1
	}
	return page
}
