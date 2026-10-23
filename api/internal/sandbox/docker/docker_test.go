package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

var (
	_ sandbox.Provider = (*Provider)(nil)
	_ Client           = (*HTTPClient)(nil)
)

// fakeDaemon is an in-memory Docker Engine API (no real daemon is used).
type fakeDaemon struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer
	volumes    map[string]Volume
	images     map[string]bool
	pulls      []string
	creates    int
	runtimes   map[string]map[string]any
	internal   bool
	execExit   int
	resizes    []string
	// volumeFiles/volumeData hold what the archive API wrote into a volume
	// (path relative to the mount), keyed by volume name.
	volumeFiles map[string]map[string]*tar.Header
	volumeData  map[string]map[string]string
}

type fakeContainer struct {
	config ContainerConfig
	state  ContainerState
	files  map[string]*tar.Header
	data   map[string]string
}

func newDaemon() *fakeDaemon {
	return &fakeDaemon{containers: map[string]*fakeContainer{}, volumes: map[string]Volume{}, images: map[string]bool{}, runtimes: map[string]map[string]any{"runc": {}}, internal: true,
		volumeFiles: map[string]map[string]*tar.Header{}, volumeData: map[string]map[string]string{}}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func frame(stream byte, payload string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, payload...)
}

func matchesLabels(labels map[string]string, filters string) bool {
	var parsed map[string][]string
	_ = json.Unmarshal([]byte(filters), &parsed)
	for _, item := range parsed["label"] {
		key, value, _ := strings.Cut(item, "=")
		if labels[key] != value {
			return false
		}
	}
	return true
}

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/"+apiVersion)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	notFound := func(what string) { writeJSON(w, 404, map[string]string{"message": "No such " + what}) }
	switch {
	case path == "/_ping":
		_, _ = io.WriteString(w, "OK")
	case path == "/info":
		writeJSON(w, 200, Info{Runtimes: d.runtimes, DefaultRuntime: "runc"})
	case parts[0] == "networks":
		if parts[1] != "flow-sandbox" {
			notFound("network")
			return
		}
		writeJSON(w, 200, Network{Name: parts[1], Internal: d.internal})
	case path == "/images/create":
		image := r.URL.Query().Get("fromImage") + ":" + r.URL.Query().Get("tag")
		d.pulls = append(d.pulls, image)
		if strings.Contains(image, "missing") {
			writeJSON(w, 200, map[string]string{"error": "manifest unknown"})
			return
		}
		d.images[image] = true
		writeJSON(w, 200, map[string]string{"status": "done"})
	case path == "/containers/create":
		var config ContainerConfig
		_ = json.NewDecoder(r.Body).Decode(&config)
		name := r.URL.Query().Get("name")
		if _, ok := d.containers[name]; ok {
			writeJSON(w, 409, map[string]string{"message": "Conflict. The container name is already in use"})
			return
		}
		if !d.images[config.Image] {
			notFound("image")
			return
		}
		for _, mount := range config.HostConfig.Mounts {
			if _, ok := d.volumes[mount.Source]; !ok {
				notFound("volume " + mount.Source)
				return
			}
		}
		d.creates++
		d.containers[name] = &fakeContainer{config: config, state: ContainerState{Status: "created"}, files: map[string]*tar.Header{}, data: map[string]string{}}
		writeJSON(w, 201, map[string]string{"Id": name})
	case path == "/containers/json":
		list := []ContainerSummary{}
		for name, container := range d.containers {
			if matchesLabels(container.config.Labels, r.URL.Query().Get("filters")) {
				list = append(list, ContainerSummary{ID: name, Names: []string{"/" + name}, Labels: container.config.Labels, Created: 1760000000})
			}
		}
		writeJSON(w, 200, list)
	case parts[0] == "containers" && len(parts) >= 2:
		container, ok := d.containers[parts[1]]
		if !ok {
			notFound("container")
			return
		}
		action := ""
		if len(parts) > 2 {
			action = parts[2]
		}
		switch {
		case r.Method == http.MethodDelete:
			delete(d.containers, parts[1])
			w.WriteHeader(204)
		case action == "json":
			body := ContainerJSON{ID: parts[1], Name: "/" + parts[1], Created: "2026-10-18T11:00:00Z", State: container.state}
			body.Config.Labels = container.config.Labels
			writeJSON(w, 200, body)
		case action == "start":
			if container.state.Running {
				w.WriteHeader(304)
				return
			}
			container.state = ContainerState{Status: "running", Running: true, StartedAt: "2026-10-18T11:00:05Z"}
			w.WriteHeader(204)
		case action == "stop":
			container.state = ContainerState{Status: "exited", ExitCode: 143, FinishedAt: "2026-10-18T11:10:00Z"}
			w.WriteHeader(204)
		case action == "archive":
			if container.config.HostConfig.ReadonlyRootfs {
				// The containerd image store refuses copies into a read-only
				// container even when the path is on a volume.
				writeJSON(w, 400, map[string]string{"message": "container rootfs is marked read-only"})
				return
			}
			target := r.URL.Query().Get("path")
			volume := ""
			for _, mount := range container.config.HostConfig.Mounts {
				if mount.Target == target {
					volume = mount.Source
				}
			}
			reader := tar.NewReader(r.Body)
			for {
				header, err := reader.Next()
				if err != nil {
					break
				}
				data, _ := io.ReadAll(reader)
				if volume != "" {
					if d.volumeFiles[volume] == nil {
						d.volumeFiles[volume], d.volumeData[volume] = map[string]*tar.Header{}, map[string]string{}
					}
					d.volumeFiles[volume][header.Name], d.volumeData[volume][header.Name] = header, string(data)
					continue
				}
				container.files[target+"/"+header.Name] = header
				container.data[target+"/"+header.Name] = string(data)
			}
			w.WriteHeader(200)
		case action == "logs":
			_, _ = w.Write(append(frame(1, "out line\n"), frame(2, "err line\n")...))
		case action == "exec":
			writeJSON(w, 201, map[string]string{"Id": "exec1"})
		default:
			w.WriteHeader(400)
		}
	case parts[0] == "exec" && len(parts) == 3 && parts[2] == "start":
		var body struct{ Tty bool }
		_ = json.NewDecoder(r.Body).Decode(&body)
		hijacker := w.(http.Hijacker)
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_, _ = buffered.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		_ = buffered.Flush()
		go func() {
			defer conn.Close()
			input, _ := io.ReadAll(buffered)
			if body.Tty {
				_, _ = conn.Write([]byte("tty:" + string(input)))
			} else {
				_, _ = conn.Write(append(frame(1, "stdout:"+string(input)), frame(2, "stderr")...))
			}
		}()
	case parts[0] == "exec" && len(parts) == 3 && parts[2] == "resize":
		d.resizes = append(d.resizes, r.URL.Query().Get("w")+"x"+r.URL.Query().Get("h"))
		w.WriteHeader(201)
	case parts[0] == "exec" && len(parts) == 3 && parts[2] == "json":
		writeJSON(w, 200, ExecInspect{ExitCode: d.execExit})
	case path == "/volumes/create":
		var body struct {
			Name   string
			Labels map[string]string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		volume := Volume{Name: body.Name, Labels: body.Labels, CreatedAt: "2026-10-18T11:00:00Z"}
		d.volumes[body.Name] = volume
		writeJSON(w, 201, volume)
	case path == "/volumes":
		list := []Volume{}
		for _, volume := range d.volumes {
			if matchesLabels(volume.Labels, r.URL.Query().Get("filters")) {
				list = append(list, volume)
			}
		}
		writeJSON(w, 200, map[string]any{"Volumes": list})
	case parts[0] == "volumes" && len(parts) == 2:
		volume, ok := d.volumes[parts[1]]
		if !ok {
			notFound("volume")
			return
		}
		if r.Method == http.MethodDelete {
			delete(d.volumes, parts[1])
			w.WriteHeader(204)
			return
		}
		writeJSON(w, 200, volume)
	default:
		w.WriteHeader(404)
	}
}

func testConfig() config.CodingConfig {
	return config.CodingConfig{
		RuntimeURL: "http://flow:8080", InstanceID: "flow", Image: "flow-sandbox:1",
		Sizes:    map[string]config.SandboxSize{"medium": {CPU: "2", Memory: "16Gi", EphemeralStorage: "40Gi", PIDs: 8192}},
		Timeouts: config.CodingTimeouts{StopGrace: time.Minute, MaxSession: 4 * time.Hour},
		Snapshot: config.CodingSnapshotConfig{Strategy: sandbox.StrategyArchive},
		Docker:   config.DockerSandboxConfig{Network: "flow-sandbox", Runtime: "runsc"},
		Egress:   config.CodingEgressConfig{ProxyURL: "http://flow-egress:3128"},
	}
}

func newProvider(t *testing.T, mutate func(*config.CodingConfig)) (*Provider, *fakeDaemon) {
	t.Helper()
	daemon := newDaemon()
	server := httptest.NewServer(daemon)
	t.Cleanup(server.Close)
	client, err := NewHTTPClient("tcp://" + strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	c := testConfig()
	if mutate != nil {
		mutate(&c)
	}
	provider, err := NewWithClient(sandbox.Dependencies{Config: c}, client)
	if err != nil {
		t.Fatal(err)
	}
	return provider, daemon
}

func spec(id string) sandbox.SandboxSpec {
	return sandbox.SandboxSpec{ID: id, Kind: sandbox.KindSession, SessionID: "sess1", WorkspaceKey: "acme", Token: "fcs_secret", Resources: sandbox.Resources{Size: "medium"}, ProxyURL: "http://flow-egress:3128"}
}

func TestBuildContainerSecurity(t *testing.T) {
	provider, _ := newProvider(t, nil)
	cfg, err := provider.BuildContainer(spec("s1"))
	if err != nil {
		t.Fatal(err)
	}
	host := cfg.HostConfig
	if cfg.User != "1000:1000" || !host.Init || host.Privileged || !host.ReadonlyRootfs {
		t.Fatalf("cfg = %+v", cfg)
	}
	if !slices.Equal(host.CapDrop, []string{"ALL"}) || !slices.Equal(host.SecurityOpt, []string{"no-new-privileges:true"}) {
		t.Fatalf("security = %+v", host)
	}
	if host.PidsLimit != 8192 || host.Memory != 16<<30 || host.MemorySwap != host.Memory || host.NanoCPUs != 2e9 {
		t.Fatalf("limits = %+v", host)
	}
	if host.Runtime != "runsc" || host.NetworkMode != "flow-sandbox" || host.RestartPolicy.Name != "no" || host.Tmpfs["/tmp"] == "" {
		t.Fatalf("host = %+v", host)
	}
	targets := []string{}
	for _, mount := range host.Mounts {
		if mount.Type != "volume" {
			t.Fatalf("only named volumes, never bind mounts: %+v", mount)
		}
		targets = append(targets, mount.Target)
	}
	if !slices.Equal(targets, []string{"/workspace", "/home/flow", "/var/run/flow"}) {
		t.Fatalf("mounts = %v", targets)
	}
	for _, env := range cfg.Env {
		if strings.Contains(env, "fcs_secret") {
			t.Fatal("the token must never be an env var")
		}
	}
	if !slices.Contains(cfg.Env, "FLOW_RUNNER_MODE=session") || !slices.Contains(cfg.Env, "HTTPS_PROXY=http://flow-egress:3128") {
		t.Fatalf("env = %v", cfg.Env)
	}
	if !slices.Equal(cfg.Entrypoint, []string{"flow-runner"}) || !slices.Equal(cfg.Cmd, []string{"session"}) {
		t.Fatalf("command = %v %v", cfg.Entrypoint, cfg.Cmd)
	}
	if cfg.Labels[sandbox.LabelManagedBy] != "flow" || cfg.Labels[sandbox.LabelSandboxID] != "s1" {
		t.Fatalf("labels = %v", cfg.Labels)
	}
	archived := spec("s1")
	archived.Workspace.ArchiveURL = "http://flow/archive"
	cfg, _ = provider.BuildContainer(archived)
	if cfg.Entrypoint[0] != "/bin/sh" || !strings.Contains(cfg.Entrypoint[2], "flow-runner restore || exit 86") || cfg.Labels[labelRestore] != "1" {
		t.Fatalf("restore entrypoint = %v", cfg.Entrypoint)
	}
	if _, err := NewWithClient(sandbox.Dependencies{Config: func() config.CodingConfig {
		c := testConfig()
		c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
		return c
	}()}, nil); err == nil {
		t.Fatal("volumesnapshot is unsupported on docker")
	}
}

func TestDockerLifecycle(t *testing.T) {
	provider, daemon := newProvider(t, nil)
	ctx := context.Background()
	status, err := provider.StartSandbox(ctx, spec("s1"))
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != sandbox.PhaseRunning || status.ProviderRef != "flow-cs-s1" || status.ID != "s1" || status.StartedAt == nil {
		t.Fatalf("status = %+v", status)
	}
	if len(daemon.pulls) != 1 {
		t.Fatalf("a missing image is pulled once: %v", daemon.pulls)
	}
	// The token lands in the token volume through a helper container (the
	// read-only sandbox container refuses archive copies), which is removed.
	header := daemon.volumeFiles["flow-cs-s1-token"]["token"]
	if header == nil || header.Uid != 1000 || header.Mode != 0o400 || daemon.volumeData["flow-cs-s1-token"]["token"] != "fcs_secret" {
		t.Fatalf("token file = %+v", header)
	}
	if _, ok := daemon.containers["flow-cs-s1-token-writer"]; ok {
		t.Fatal("the token writer must be removed after the copy")
	}
	if daemon.creates != 2 {
		t.Fatalf("one sandbox container and one token writer: creates=%d", daemon.creates)
	}
	for _, volume := range []string{"flow-ws-s1", "flow-home-s1", "flow-cs-s1-token"} {
		if _, ok := daemon.volumes[volume]; !ok {
			t.Fatalf("volume %s missing", volume)
		}
	}
	if _, err := provider.StartSandbox(ctx, spec("s1")); err != nil || daemon.creates != 2 {
		t.Fatalf("restart must adopt: %v creates=%d", err, daemon.creates)
	}

	// Exec without and with a TTY.
	var stdout, stderr bytes.Buffer
	daemon.execExit = 3
	result, err := provider.Exec(ctx, "s1", sandbox.ExecRequest{Command: []string{"cat"}, Stdin: strings.NewReader("hi"), Stdout: &stdout, Stderr: &stderr})
	if err != nil || result.ExitCode != 3 || stdout.String() != "stdout:hi" || stderr.String() != "stderr" {
		t.Fatalf("exec = %+v %v %q %q", result, err, stdout.String(), stderr.String())
	}
	stdout.Reset()
	resizes := make(chan sandbox.TerminalSize, 1)
	resizes <- sandbox.TerminalSize{Width: 100, Height: 30}
	if _, err := provider.Exec(ctx, "s1", sandbox.ExecRequest{Command: []string{"bash"}, TTY: true, Stdin: strings.NewReader("ls"), Stdout: &stdout, Resize: resizes}); err != nil || stdout.String() != "tty:ls" {
		t.Fatalf("tty exec = %v %q", err, stdout.String())
	}

	logs, err := provider.Logs(ctx, "s1", sandbox.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(logs)
	logs.Close()
	if string(data) != "out line\nerr line\n" {
		t.Fatalf("logs = %q", data)
	}

	objects, err := provider.ListManaged(ctx, sandbox.ListFilter{SandboxID: "s1"})
	if err != nil || len(objects) != 4 {
		t.Fatalf("managed = %+v %v", objects, err)
	}
	if err := provider.StopSandbox(ctx, "s1", sandbox.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := provider.StopSandbox(ctx, "s1", sandbox.StopOptions{}); err != nil {
		t.Fatalf("stop is idempotent: %v", err)
	}
	if len(daemon.containers) != 0 || len(daemon.volumes) != 0 {
		t.Fatalf("left: %d containers %d volumes", len(daemon.containers), len(daemon.volumes))
	}
	if status, _ := provider.Status(ctx, "s1"); status.Phase != sandbox.PhaseMissing {
		t.Fatalf("status after stop = %+v", status)
	}
}

func TestDockerStatusAndErrors(t *testing.T) {
	provider, daemon := newProvider(t, nil)
	ctx := context.Background()
	if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "v1"}); err != nil {
		t.Fatal(err)
	}
	s := spec("s2")
	s.Workspace.VolumeID = "v1"
	if _, err := provider.StartSandbox(ctx, s); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		state  ContainerState
		labels map[string]string
		phase  sandbox.Phase
		reason sandbox.Reason
	}{
		{ContainerState{Status: "created"}, nil, sandbox.PhaseStarting, ""},
		{ContainerState{Status: "exited", ExitCode: 0}, nil, sandbox.PhaseSucceeded, ""},
		{ContainerState{Status: "exited", ExitCode: 137, OOMKilled: true}, nil, sandbox.PhaseFailed, sandbox.ReasonOOMKilled},
		{ContainerState{Status: "exited", ExitCode: 86}, map[string]string{labelRestore: "1"}, sandbox.PhaseFailed, sandbox.ReasonInitFailed},
		{ContainerState{Status: "exited", ExitCode: 86}, nil, sandbox.PhaseFailed, sandbox.ReasonExitNonZero},
		{ContainerState{Status: "removing"}, nil, sandbox.PhaseFailed, sandbox.ReasonUnknown},
	}
	for _, tc := range cases {
		container := ContainerJSON{Name: "/flow-cs-x", State: tc.state}
		container.Config.Labels = tc.labels
		status := provider.containerStatus(container)
		if status.Phase != tc.phase || status.Reason != tc.reason {
			t.Errorf("%+v: got %s/%s", tc.state, status.Phase, status.Reason)
		}
	}
	// Stopping keeps an explicit workspace volume (it belongs to the caller).
	if err := provider.StopSandbox(ctx, "s2", sandbox.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := daemon.volumes["flow-ws-v1"]; !ok {
		t.Fatal("an explicit workspace volume must survive stop")
	}
	// A missing workspace volume is not found; an unpullable image is ErrImagePull.
	s.ID, s.Workspace.VolumeID = "s3", "nope"
	if _, err := provider.StartSandbox(ctx, s); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing volume = %v", err)
	}
	s = spec("s4")
	s.Image = "registry/missing:1"
	if _, err := provider.StartSandbox(ctx, s); !errors.Is(err, sandbox.ErrImagePull) {
		t.Fatalf("missing image = %v", err)
	}
	if _, err := provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "x"}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("snapshots = %v", err)
	}
	if err := wrap("op", &APIError{Status: 500}); !errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("5xx must be transient: %v", err)
	}
	if err := wrap("op", &APIError{Status: 400}); errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("400 must not be transient: %v", err)
	}
	// Foreign objects are never deleted.
	daemon.volumes["foreign"] = Volume{Name: "foreign", Labels: map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "other"}}
	if err := provider.DeleteManaged(ctx, sandbox.ManagedObject{Type: TypeVolume, Name: "foreign", Labels: daemon.volumes["foreign"].Labels}); err != nil {
		t.Fatal(err)
	}
	if _, ok := daemon.volumes["foreign"]; !ok {
		t.Fatal("a foreign volume was deleted")
	}
}

func TestDockerPreflight(t *testing.T) {
	provider, daemon := newProvider(t, nil)
	results := map[string]sandbox.PreflightResult{}
	for _, result := range provider.Preflight(context.Background()) {
		results[result.Name] = result
	}
	if !results["docker"].OK || results["runtime"].OK || !results["network"].OK || results["network"].Warning {
		t.Fatalf("results = %+v", results)
	}
	daemon.runtimes["runsc"] = map[string]any{}
	daemon.internal = false
	results = map[string]sandbox.PreflightResult{}
	for _, result := range provider.Preflight(context.Background()) {
		results[result.Name] = result
	}
	if !results["runtime"].OK || !results["network"].Warning {
		t.Fatalf("results = %+v", results)
	}
}

func TestDemux(t *testing.T) {
	var out, errOut bytes.Buffer
	stream := append(frame(1, "a"), frame(2, "b")...)
	if err := demux(bytes.NewReader(stream), &out, &errOut); err != nil || out.String() != "a" || errOut.String() != "b" {
		t.Fatalf("demux = %v %q %q", err, out.String(), errOut.String())
	}
	if err := demux(bytes.NewReader(stream[:5]), &out, &errOut); err == nil {
		t.Fatal("a truncated frame must fail")
	}
}
