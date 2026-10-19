package docker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// apiVersion is the Docker Engine API version Flow speaks (Docker 20.10+:
// Init, Runtime, PidsLimit and Mounts are all available).
const apiVersion = "v1.41"

// APIError is a non-2xx answer of the Docker Engine API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: %d %s", e.Status, e.Message) }

func isStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// IsNotFound reports a 404 from the Engine API.
func IsNotFound(err error) bool { return isStatus(err, http.StatusNotFound) }

// IsConflict reports a 409 (a name already in use).
func IsConflict(err error) bool { return isStatus(err, http.StatusConflict) }

// Mount is a HostConfig mount.
type Mount struct {
	Type     string `json:"Type"`
	Source   string `json:"Source,omitempty"`
	Target   string `json:"Target"`
	ReadOnly bool   `json:"ReadOnly,omitempty"`
}

// HostConfig is the subset of the Engine API host config Flow sets.
type HostConfig struct {
	Init           bool              `json:"Init"`
	Privileged     bool              `json:"Privileged"`
	CapDrop        []string          `json:"CapDrop,omitempty"`
	SecurityOpt    []string          `json:"SecurityOpt,omitempty"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
	Tmpfs          map[string]string `json:"Tmpfs,omitempty"`
	Mounts         []Mount           `json:"Mounts,omitempty"`
	PidsLimit      int64             `json:"PidsLimit,omitempty"`
	Memory         int64             `json:"Memory,omitempty"`
	MemorySwap     int64             `json:"MemorySwap,omitempty"`
	NanoCPUs       int64             `json:"NanoCpus,omitempty"`
	Runtime        string            `json:"Runtime,omitempty"`
	NetworkMode    string            `json:"NetworkMode,omitempty"`
	IpcMode        string            `json:"IpcMode,omitempty"`
	RestartPolicy  struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
}

// ContainerConfig is the body of POST /containers/create.
type ContainerConfig struct {
	Image        string            `json:"Image"`
	User         string            `json:"User,omitempty"`
	Env          []string          `json:"Env,omitempty"`
	Entrypoint   []string          `json:"Entrypoint,omitempty"`
	Cmd          []string          `json:"Cmd,omitempty"`
	WorkingDir   string            `json:"WorkingDir,omitempty"`
	Labels       map[string]string `json:"Labels,omitempty"`
	StopSignal   string            `json:"StopSignal,omitempty"`
	StopTimeout  *int              `json:"StopTimeout,omitempty"`
	HostConfig   HostConfig        `json:"HostConfig"`
	AttachStdout bool              `json:"AttachStdout"`
	AttachStderr bool              `json:"AttachStderr"`
}

// ContainerState is the State of GET /containers/{id}/json.
type ContainerState struct {
	Status     string `json:"Status"` // created | running | paused | restarting | removing | exited | dead
	Running    bool   `json:"Running"`
	OOMKilled  bool   `json:"OOMKilled"`
	ExitCode   int    `json:"ExitCode"`
	Error      string `json:"Error"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
}

// ContainerJSON is GET /containers/{id}/json.
type ContainerJSON struct {
	ID      string         `json:"Id"`
	Name    string         `json:"Name"`
	Created string         `json:"Created"`
	State   ContainerState `json:"State"`
	Config  struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// ContainerSummary is one item of GET /containers/json.
type ContainerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Labels  map[string]string `json:"Labels"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
}

// Volume is a named volume.
type Volume struct {
	Name      string            `json:"Name"`
	Labels    map[string]string `json:"Labels"`
	CreatedAt string            `json:"CreatedAt"`
}

// Info is the subset of GET /info Flow checks.
type Info struct {
	ServerVersion  string                    `json:"ServerVersion"`
	Runtimes       map[string]map[string]any `json:"Runtimes"`
	DefaultRuntime string                    `json:"DefaultRuntime"`
	SecurityOpts   []string                  `json:"SecurityOptions"`
}

// Network is the subset of GET /networks/{id} Flow checks.
type Network struct {
	Name     string `json:"Name"`
	Internal bool   `json:"Internal"`
}

// ExecConfig is the body of POST /containers/{id}/exec.
type ExecConfig struct {
	Cmd          []string `json:"Cmd"`
	User         string   `json:"User,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	Tty          bool     `json:"Tty"`
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
}

// ExecInspect is GET /exec/{id}/json.
type ExecInspect struct {
	Running  bool `json:"Running"`
	ExitCode int  `json:"ExitCode"`
}

// LogsOptions selects container logs.
type LogsOptions struct {
	Follow bool
	Tail   int64
	Since  *time.Time
}

// Client is the Docker Engine API surface the provider uses. HTTPClient
// implements it over a unix socket or TCP; tests use fakes.
type Client interface {
	Ping(ctx context.Context) error
	Info(ctx context.Context) (Info, error)
	NetworkInspect(ctx context.Context, name string) (Network, error)
	ImagePull(ctx context.Context, image string) error
	ContainerCreate(ctx context.Context, name string, config ContainerConfig) (string, error)
	ContainerStart(ctx context.Context, id string) error
	ContainerInspect(ctx context.Context, id string) (ContainerJSON, error)
	ContainerStop(ctx context.Context, id string, timeout time.Duration) error
	ContainerRemove(ctx context.Context, id string) error
	ContainerList(ctx context.Context, labels []string) ([]ContainerSummary, error)
	ContainerLogs(ctx context.Context, id string, options LogsOptions) (io.ReadCloser, error)
	CopyToContainer(ctx context.Context, id, path string, archive io.Reader) error
	VolumeCreate(ctx context.Context, name string, labels map[string]string) (Volume, error)
	VolumeInspect(ctx context.Context, name string) (Volume, error)
	VolumeRemove(ctx context.Context, name string) error
	VolumeList(ctx context.Context, labels []string) ([]Volume, error)
	ExecCreate(ctx context.Context, containerID string, config ExecConfig) (string, error)
	ExecAttach(ctx context.Context, execID string, tty bool) (io.ReadWriteCloser, error)
	ExecResize(ctx context.Context, execID string, height, width uint16) error
	ExecInspect(ctx context.Context, execID string) (ExecInspect, error)
}

// HTTPClient talks to the Engine API without the Docker SDK.
type HTTPClient struct {
	base   string // scheme://host
	dial   func(ctx context.Context) (net.Conn, error)
	tls    *tls.Config
	client *http.Client
}

// NewHTTPClient connects to unix:///path, tcp://host:port, http(s)://host.
func NewHTTPClient(host string) (*HTTPClient, error) {
	parsed, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("FLOW_CODING_DOCKER_HOST: %w", err)
	}
	c := &HTTPClient{}
	var dialer net.Dialer
	switch parsed.Scheme {
	case "unix":
		path := parsed.Path
		if path == "" {
			return nil, fmt.Errorf("FLOW_CODING_DOCKER_HOST: unix socket path is empty")
		}
		c.base = "http://docker"
		c.dial = func(ctx context.Context) (net.Conn, error) { return dialer.DialContext(ctx, "unix", path) }
	case "tcp", "http":
		c.base = "http://" + parsed.Host
		c.dial = func(ctx context.Context) (net.Conn, error) { return dialer.DialContext(ctx, "tcp", parsed.Host) }
	case "https":
		c.base = "https://" + parsed.Host
		c.tls = &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}
		c.dial = func(ctx context.Context) (net.Conn, error) {
			return (&tls.Dialer{NetDialer: &dialer, Config: c.tls}).DialContext(ctx, "tcp", parsed.Host)
		}
	default:
		return nil, fmt.Errorf("FLOW_CODING_DOCKER_HOST: unsupported scheme %q (unix, tcp, http or https)", parsed.Scheme)
	}
	transport := &http.Transport{
		DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dial(ctx) },
		MaxIdleConns:    8,
		IdleConnTimeout: 30 * time.Second,
	}
	if parsed.Scheme == "https" {
		transport.DialContext = nil
		transport.TLSClientConfig = c.tls
	}
	c.client = &http.Client{Transport: transport}
	return c, nil
}

func (c *HTTPClient) url(path string, query url.Values) string {
	u := c.base + "/" + apiVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *HTTPClient) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	response, err := c.raw(ctx, method, path, query, body, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out)
}

func (c *HTTPClient) raw(ctx context.Context, method, path string, query url.Values, body any, contentType string) (*http.Response, error) {
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case io.Reader:
		reader = value
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
		contentType = "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, method, c.url(path, query), reader)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 && response.StatusCode != http.StatusNotModified {
		defer response.Body.Close()
		return nil, readAPIError(response)
	}
	return response, nil
}

func readAPIError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var message struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &message) != nil || message.Message == "" {
		message.Message = strings.TrimSpace(string(data))
	}
	return &APIError{Status: response.StatusCode, Message: message.Message}
}

func labelFilter(labels []string) url.Values {
	filters, _ := json.Marshal(map[string][]string{"label": labels})
	return url.Values{"filters": {string(filters)}}
}

func (c *HTTPClient) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

func (c *HTTPClient) Info(ctx context.Context) (Info, error) {
	var info Info
	err := c.do(ctx, http.MethodGet, "/info", nil, nil, &info)
	return info, err
}

func (c *HTTPClient) NetworkInspect(ctx context.Context, name string) (Network, error) {
	var network Network
	err := c.do(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &network)
	return network, err
}

// ImagePull pulls an image and waits for the pull to finish; errors are
// reported in the JSON progress stream, not the status code.
func (c *HTTPClient) ImagePull(ctx context.Context, image string) error {
	name, tag := image, ""
	if at := strings.LastIndex(image, "@"); at < 0 {
		if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
			name, tag = image[:colon], image[colon+1:]
		}
	}
	query := url.Values{"fromImage": {name}}
	if tag != "" {
		query.Set("tag", tag)
	}
	response, err := c.raw(ctx, http.MethodPost, "/images/create", query, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(response.Body)
	for {
		var message struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&message); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if message.Error != "" {
			return &APIError{Status: http.StatusInternalServerError, Message: message.Error}
		}
	}
}

func (c *HTTPClient) ContainerCreate(ctx context.Context, name string, config ContainerConfig) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	err := c.do(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, config, &created)
	return created.ID, err
}

func (c *HTTPClient) ContainerStart(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil, nil)
}

func (c *HTTPClient) ContainerInspect(ctx context.Context, id string) (ContainerJSON, error) {
	var container ContainerJSON
	err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil, &container)
	return container, err
}

func (c *HTTPClient) ContainerStop(ctx context.Context, id string, timeout time.Duration) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop", url.Values{"t": {strconv.Itoa(int(timeout / time.Second))}}, nil, nil)
}

func (c *HTTPClient) ContainerRemove(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), url.Values{"force": {"1"}, "v": {"0"}}, nil, nil)
}

func (c *HTTPClient) ContainerList(ctx context.Context, labels []string) ([]ContainerSummary, error) {
	query := labelFilter(labels)
	query.Set("all", "1")
	var list []ContainerSummary
	err := c.do(ctx, http.MethodGet, "/containers/json", query, nil, &list)
	return list, err
}

func (c *HTTPClient) ContainerLogs(ctx context.Context, id string, options LogsOptions) (io.ReadCloser, error) {
	query := url.Values{"stdout": {"1"}, "stderr": {"1"}}
	if options.Follow {
		query.Set("follow", "1")
	}
	if options.Tail > 0 {
		query.Set("tail", strconv.FormatInt(options.Tail, 10))
	}
	if options.Since != nil {
		query.Set("since", strconv.FormatInt(options.Since.Unix(), 10))
	}
	response, err := c.raw(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", query, nil, "")
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

func (c *HTTPClient) CopyToContainer(ctx context.Context, id, path string, archive io.Reader) error {
	response, err := c.raw(ctx, http.MethodPut, "/containers/"+url.PathEscape(id)+"/archive", url.Values{"path": {path}}, archive, "application/x-tar")
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (c *HTTPClient) VolumeCreate(ctx context.Context, name string, labels map[string]string) (Volume, error) {
	var volume Volume
	err := c.do(ctx, http.MethodPost, "/volumes/create", nil, map[string]any{"Name": name, "Labels": labels}, &volume)
	return volume, err
}

func (c *HTTPClient) VolumeInspect(ctx context.Context, name string) (Volume, error) {
	var volume Volume
	err := c.do(ctx, http.MethodGet, "/volumes/"+url.PathEscape(name), nil, nil, &volume)
	return volume, err
}

func (c *HTTPClient) VolumeRemove(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), url.Values{"force": {"1"}}, nil, nil)
}

func (c *HTTPClient) VolumeList(ctx context.Context, labels []string) ([]Volume, error) {
	var list struct {
		Volumes []Volume `json:"Volumes"`
	}
	err := c.do(ctx, http.MethodGet, "/volumes", labelFilter(labels), nil, &list)
	return list.Volumes, err
}

func (c *HTTPClient) ExecCreate(ctx context.Context, containerID string, config ExecConfig) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(containerID)+"/exec", nil, config, &created)
	return created.ID, err
}

// ExecAttach starts an exec and hijacks the connection for its streams.
func (c *HTTPClient) ExecAttach(ctx context.Context, execID string, tty bool) (io.ReadWriteCloser, error) {
	body, _ := json.Marshal(map[string]bool{"Detach": false, "Tty": tty})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/exec/"+url.PathEscape(execID)+"/start", nil), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "tcp")
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	if err := request.Write(conn); err != nil {
		stop()
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		stop()
		conn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols && response.StatusCode != http.StatusOK {
		defer conn.Close()
		stop()
		return nil, readAPIError(response)
	}
	return &hijacked{Conn: conn, reader: reader, stop: stop}, nil
}

// hijacked is an upgraded connection whose first bytes may already sit in
// the response reader's buffer.
type hijacked struct {
	net.Conn
	reader *bufio.Reader
	stop   func() bool
}

func (h *hijacked) Read(p []byte) (int, error) { return h.reader.Read(p) }

// CloseWrite half-closes the connection so the exec sees stdin EOF.
func (h *hijacked) CloseWrite() error {
	if closer, ok := h.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return nil
}

func (h *hijacked) Close() error {
	h.stop()
	return h.Conn.Close()
}

func (c *HTTPClient) ExecResize(ctx context.Context, execID string, height, width uint16) error {
	return c.do(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/resize", url.Values{"h": {strconv.Itoa(int(height))}, "w": {strconv.Itoa(int(width))}}, nil, nil)
}

func (c *HTTPClient) ExecInspect(ctx context.Context, execID string) (ExecInspect, error) {
	var inspect ExecInspect
	err := c.do(ctx, http.MethodGet, "/exec/"+url.PathEscape(execID)+"/json", nil, nil, &inspect)
	return inspect, err
}
