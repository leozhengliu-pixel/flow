package opensandbox

// HTTP client for the OpenSandbox Lifecycle API (specs/sandbox-lifecycle.yml,
// info.version 0.1.0 at tag release-1.1.0). Only the subset Flow needs is
// modelled; unknown response fields are ignored and unknown states are
// handled by the caller (the spec says new states may be added).

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/config"
)

// Headers.
const (
	HeaderAPIKey      = "OPEN-SANDBOX-API-KEY"
	HeaderExecdToken  = "X-EXECD-ACCESS-TOKEN"
	HeaderRequestID   = "X-Request-ID"
	DefaultExecdPort  = 44772
	maxErrorBody      = 64 << 10
	maxListPages      = 100
	listPageSize      = 100
	userAgent         = "flow-opensandbox-provider"
	maxRetryAfterWait = 30 * time.Second
)

// Lifecycle states (SandboxState). Unknown values are possible.
const (
	StatePending    = "Pending"
	StateAllocated  = "Allocated"
	StateRunning    = "Running"
	StatePausing    = "Pausing"
	StatePaused     = "Paused"
	StateResuming   = "Resuming"
	StateStopping   = "Stopping"
	StateTerminated = "Terminated"
	StateFailed     = "Failed"
)

// ImageSpec is the sandbox image (ImageSpec).
type ImageSpec struct {
	URI  string     `json:"uri"`
	Auth *ImageAuth `json:"auth,omitempty"`
}

// ImageAuth are registry credentials.
type ImageAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// NetworkRule is one egress rule (NetworkRule).
type NetworkRule struct {
	Action string `json:"action"`
	Target string `json:"target"`
}

// NetworkPolicy is the egress policy (NetworkPolicy).
type NetworkPolicy struct {
	DefaultAction string        `json:"defaultAction,omitempty"`
	Egress        []NetworkRule `json:"egress,omitempty"`
}

// CreateRequest is CreateSandboxRequest (subset).
type CreateRequest struct {
	Image            *ImageSpec        `json:"image,omitempty"`
	Timeout          *int              `json:"timeout,omitempty"`
	ResourceLimits   map[string]string `json:"resourceLimits,omitempty"`
	ResourceRequests map[string]string `json:"resourceRequests,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Entrypoint       []string          `json:"entrypoint,omitempty"`
	NetworkPolicy    *NetworkPolicy    `json:"networkPolicy,omitempty"`
	Extensions       map[string]string `json:"extensions,omitempty"`
}

// Status is SandboxStatus.
type Status struct {
	State            string     `json:"state"`
	Reason           string     `json:"reason,omitempty"`
	Message          string     `json:"message,omitempty"`
	LastTransitionAt *time.Time `json:"lastTransitionAt,omitempty"`
}

// Sandbox is the Sandbox resource (and CreateSandboxResponse).
type Sandbox struct {
	ID         string            `json:"id"`
	Image      *ImageSpec        `json:"image,omitempty"`
	Status     Status            `json:"status"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Entrypoint []string          `json:"entrypoint,omitempty"`
	ExpiresAt  *time.Time        `json:"expiresAt,omitempty"`
	CreatedAt  time.Time         `json:"createdAt"`
}

// Endpoint is an access endpoint for a sandbox port.
type Endpoint struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type pagination struct {
	Page        int  `json:"page"`
	PageSize    int  `json:"pageSize"`
	TotalItems  int  `json:"totalItems"`
	TotalPages  int  `json:"totalPages"`
	HasNextPage bool `json:"hasNextPage"`
}

type listResponse struct {
	Items      []Sandbox  `json:"items"`
	Pagination pagination `json:"pagination"`
}

// APIError is a non-2xx response (ErrorResponse) or a transport failure.
type APIError struct {
	Op         string
	Status     int // 0 for transport errors
	Code       string
	Message    string
	RequestID  string
	RetryAfter time.Duration
	Err        error // transport error
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("opensandbox %s: %v", e.Op, e.Err)
	}
	text := fmt.Sprintf("opensandbox %s: HTTP %d", e.Op, e.Status)
	if e.Code != "" {
		text += " " + e.Code
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	if e.RequestID != "" {
		text += " (request " + e.RequestID + ")"
	}
	return text
}

func (e *APIError) Unwrap() error { return e.Err }

// Retryable reports a failure worth retrying for an idempotent call.
func (e *APIError) Retryable() bool {
	if e.Status == 0 {
		return !errors.Is(e.Err, context.Canceled)
	}
	return e.Status == http.StatusTooManyRequests || e.Status == http.StatusConflict || e.Status >= 500
}

// IsNotFound reports a 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Client talks to one OpenSandbox lifecycle server.
type Client struct {
	base           string // ends with /v1
	apiKey         string
	execdToken     string
	http           *http.Client
	requestTimeout time.Duration
	createTimeout  time.Duration
	retries        int
	// sleep waits between retries (tests make it instant).
	sleep func(ctx context.Context, d time.Duration) error
}

// BaseURL normalizes a server URL to its /v1 API base.
func BaseURL(raw string) string {
	raw = strings.TrimRight(raw, "/")
	if strings.HasSuffix(raw, "/v1") {
		return raw
	}
	return raw + "/v1"
}

// NewClient builds a client from FLOW_CODING_OPENSANDBOX_*.
func NewClient(c config.OpenSandboxConfig) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	if c.CAFile != "" || c.InsecureSkipVerify {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureSkipVerify} //nolint:gosec // explicit operator opt-in for development
		if c.CAFile != "" {
			pem, err := os.ReadFile(c.CAFile)
			if err != nil {
				return nil, fmt.Errorf("FLOW_CODING_OPENSANDBOX_CA_FILE: %w", err)
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("FLOW_CODING_OPENSANDBOX_CA_FILE: no PEM certificates in %s", c.CAFile)
			}
			tlsConfig.RootCAs = pool
		}
		transport.TLSClientConfig = tlsConfig
	}
	return newClient(c, &http.Client{Transport: transport}), nil
}

func newClient(c config.OpenSandboxConfig, httpClient *http.Client) *Client {
	requestTimeout, createTimeout := c.RequestTimeout, c.CreateTimeout
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	if createTimeout <= 0 {
		createTimeout = 10 * time.Minute
	}
	return &Client{
		base: BaseURL(c.URL), apiKey: c.APIKey, execdToken: c.ExecdToken, http: httpClient,
		requestTimeout: requestTimeout, createTimeout: createTimeout, retries: max(c.Retries, 0),
		sleep: sleepContext,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoff is the delay before retry n (0-based): 250ms·2ⁿ with jitter, at most 5s.
func backoff(n int) time.Duration {
	delay := 250 * time.Millisecond << min(n, 5)
	delay = min(delay, 5*time.Second)
	return delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
}

type request struct {
	op      string
	method  string
	url     string
	query   url.Values
	body    any
	out     any
	retry   bool
	timeout time.Duration
	headers map[string]string
	// raw sends an already encoded body with this content type.
	raw         []byte
	contentType string
}

func (c *Client) do(ctx context.Context, r request) error {
	attempts := 1
	if r.retry {
		attempts += c.retries
	}
	var payload []byte
	contentType := r.contentType
	switch {
	case r.raw != nil:
		payload = r.raw
	case r.body != nil:
		encoded, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("opensandbox %s: encode: %w", r.op, err)
		}
		payload = encoded
		if contentType == "" {
			contentType = "application/json"
		}
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = c.requestTimeout
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			wait := backoff(attempt - 1)
			var apiErr *APIError
			if errors.As(lastErr, &apiErr) && apiErr.RetryAfter > 0 {
				wait = min(apiErr.RetryAfter, maxRetryAfterWait)
			}
			if err := c.sleep(ctx, wait); err != nil {
				return lastErr
			}
		}
		lastErr = c.once(ctx, r, payload, contentType, timeout)
		if lastErr == nil {
			return nil
		}
		var apiErr *APIError
		if !errors.As(lastErr, &apiErr) || !apiErr.Retryable() || ctx.Err() != nil {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) once(ctx context.Context, r request, payload []byte, contentType string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := r.url
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, target, body)
	if err != nil {
		return &APIError{Op: r.op, Err: err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	c.authorize(req.Header)
	for key, value := range r.headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &APIError{Op: r.op, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(r.op, resp)
	}
	if r.out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(r.out); err != nil {
		return &APIError{Op: r.op, Err: fmt.Errorf("decode response: %w", err)}
	}
	return nil
}

func (c *Client) authorize(header http.Header) {
	if c.apiKey != "" {
		header.Set(HeaderAPIKey, c.apiKey)
	}
}

// responseError reads an ErrorResponse; non-JSON bodies become the message.
func responseError(op string, resp *http.Response) *APIError {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	apiErr := &APIError{Op: op, Status: resp.StatusCode, RequestID: resp.Header.Get(HeaderRequestID)}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Detail  any    `json:"detail"`
	}
	if json.Unmarshal(raw, &body) == nil && (body.Code != "" || body.Message != "") {
		apiErr.Code, apiErr.Message = body.Code, body.Message
	} else if json.Unmarshal(raw, &body) == nil && body.Detail != nil {
		// FastAPI HTTPException bodies: {"detail": {...}} or {"detail": "..."}.
		switch detail := body.Detail.(type) {
		case map[string]any:
			apiErr.Code, _ = detail["code"].(string)
			apiErr.Message, _ = detail["message"].(string)
		case string:
			apiErr.Message = detail
		}
	} else {
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	if len(apiErr.Message) > 2048 {
		apiErr.Message = apiErr.Message[:2048] + "…"
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
		apiErr.RetryAfter = time.Duration(seconds) * time.Second
	}
	return apiErr
}

func (c *Client) sandboxURL(id string, rest ...string) string {
	target := c.base + "/sandboxes/" + url.PathEscape(id)
	for _, part := range rest {
		target += "/" + part
	}
	return target
}

// MetadataQuery encodes a metadata filter: the metadata query parameter is
// itself a URL-encoded k=v&k2=v2 string (spec: GET /sandboxes, "metadata").
func MetadataQuery(metadata map[string]string) string {
	values := url.Values{}
	for key, value := range metadata {
		values.Set(key, value)
	}
	return values.Encode()
}

// List returns every sandbox whose metadata contains all of metadata,
// following pagination.
func (c *Client) List(ctx context.Context, metadata map[string]string) ([]Sandbox, error) {
	all := []Sandbox{}
	for page := 1; page <= maxListPages; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(listPageSize)}}
		if len(metadata) > 0 {
			query.Set("metadata", MetadataQuery(metadata))
		}
		var resp listResponse
		if err := c.do(ctx, request{op: "list sandboxes", method: http.MethodGet, url: c.base + "/sandboxes", query: query, out: &resp, retry: true}); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if !resp.Pagination.HasNextPage || len(resp.Items) == 0 {
			return all, nil
		}
	}
	return all, fmt.Errorf("opensandbox list sandboxes: more than %d pages", maxListPages)
}

// Get fetches one sandbox.
func (c *Client) Get(ctx context.Context, id string) (Sandbox, error) {
	var sandbox Sandbox
	err := c.do(ctx, request{op: "get sandbox", method: http.MethodGet, url: c.sandboxURL(id), out: &sandbox, retry: true})
	return sandbox, err
}

// Create provisions a sandbox. The server answers once it is running (or
// failed), so it gets the create timeout and is never retried here: the
// caller looks the sandbox up by metadata before creating again.
func (c *Client) Create(ctx context.Context, create CreateRequest) (Sandbox, error) {
	var sandbox Sandbox
	err := c.do(ctx, request{op: "create sandbox", method: http.MethodPost, url: c.base + "/sandboxes", body: create, out: &sandbox, timeout: c.createTimeout})
	return sandbox, err
}

// Delete terminates a sandbox; a missing sandbox is success.
func (c *Client) Delete(ctx context.Context, id string) error {
	err := c.do(ctx, request{op: "delete sandbox", method: http.MethodDelete, url: c.sandboxURL(id), retry: true})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Renew moves the absolute expiration to at.
func (c *Client) Renew(ctx context.Context, id string, at time.Time) (time.Time, error) {
	var resp struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}
	body := map[string]string{"expiresAt": at.UTC().Format(time.RFC3339)}
	err := c.do(ctx, request{op: "renew expiration", method: http.MethodPost, url: c.sandboxURL(id, "renew-expiration"), body: body, out: &resp, retry: true})
	return resp.ExpiresAt, err
}

// PatchMetadata applies a JSON merge patch to the sandbox's metadata (nil deletes a key).
func (c *Client) PatchMetadata(ctx context.Context, id string, patch map[string]*string) (Sandbox, error) {
	var sandbox Sandbox
	err := c.do(ctx, request{op: "patch metadata", method: http.MethodPatch, url: c.sandboxURL(id, "metadata"), body: patch, out: &sandbox, retry: true})
	return sandbox, err
}

// Endpoint resolves the endpoint of a sandbox port.
func (c *Client) Endpoint(ctx context.Context, id string, port int) (Endpoint, error) {
	var endpoint Endpoint
	err := c.do(ctx, request{op: "get endpoint", method: http.MethodGet, url: c.sandboxURL(id, "endpoints", strconv.Itoa(port)), out: &endpoint, retry: true})
	return endpoint, err
}

// Health checks GET /health on the server root (unauthenticated).
func (c *Client) Health(ctx context.Context) error {
	root := strings.TrimSuffix(c.base, "/v1")
	return c.do(ctx, request{op: "health", method: http.MethodGet, url: root + "/health"})
}

// ProxyURL is the server-proxied base URL of a sandbox port.
func (c *Client) ProxyURL(id string, port int) string {
	return c.sandboxURL(id, "proxy", strconv.Itoa(port))
}

// isTransport reports a network-level failure (connection refused, reset, timeout).
func isTransport(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == 0 {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
