package config

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/net/http/httpguts"
)

type TelemetryConfig struct {
	Enabled, Frontend                  bool
	Endpoint                           string
	Environment                        string
	Interval, SlowRequest, SampleEvery time.Duration
	SampleRate, FrontendSampleRate     float64
	HighWater                          float64
	Thresholds                         [3]float64
	SnapshotDir, HeapProfileDir        string
	HeapProfileMaxBytes                int64
	SnapshotDisabled                   bool
	Targets                            [3]TelemetryTarget // metrics, traces, logs
	LegacyMetricsTemporality           string
	LegacyMetricsHistogram             string
}

type TelemetryTarget struct {
	URL, CAFile, CertFile, KeyFile string
	Headers                        map[string]string
	Timeout, Interval              time.Duration
	Gzip, Legacy                   bool
}

type fileTelemetry struct {
	enabled, allowHTTP                                      *bool
	endpoint, metricsEndpoint, tracesEndpoint, logsEndpoint *string
	compression                                             *string
	environment                                             *string
	interval, slowRequest, sampleEvery                      time.Duration
	sampleRate, frontendRate, highWater                     *float64
	frontend                                                bool
	headers                                                 map[string]string
	snapshot                                                *string
	heapProfiles                                            string
	thresholds                                              [3]float64
	heapProfileMaxBytes                                     int64
	tls                                                     TelemetryTarget
}

func telemetryFileSchema() *field {
	set := func(kind valueKind, apply func(*fileTelemetry, any)) *field {
		return &field{kind: kind, apply: func(f *File, v any) { apply(&f.Settings.telemetry, v) }}
	}
	text := func(apply func(*fileTelemetry, string)) *field {
		return set(kindString, func(f *fileTelemetry, v any) { apply(f, v.(string)) })
	}
	ratio := func(minimum float64, apply func(*fileTelemetry, *float64)) *field {
		return &field{kind: kindCustom, parse: func(p fileParser, n *yaml.Node, key string) error {
			v, err := strconv.ParseFloat(n.Value, 64)
			if (n.Tag != "!!int" && n.Tag != "!!float") || err != nil || math.IsNaN(v) || v < minimum || v > 1 {
				return p.errorf(n, key, "must be a number between %g and 1", minimum)
			}
			apply(&p.file.Settings.telemetry, &v)
			return nil
		}}
	}
	duration := func(apply func(*fileTelemetry, time.Duration)) *field {
		return set(kindDuration, func(f *fileTelemetry, v any) {
			d, _ := time.ParseDuration(v.(string))
			apply(f, d)
		})
	}
	return section(map[string]*field{
		"enabled":         set(kindBool, func(f *fileTelemetry, v any) { f.enabled = pointer(v.(bool)) }),
		"allowHttp":       set(kindBool, func(f *fileTelemetry, v any) { f.allowHTTP = pointer(v.(bool)) }),
		"endpoint":        text(func(f *fileTelemetry, v string) { f.endpoint = &v }),
		"metricsEndpoint": text(func(f *fileTelemetry, v string) { f.metricsEndpoint = &v }),
		"tracesEndpoint":  text(func(f *fileTelemetry, v string) { f.tracesEndpoint = &v }),
		"logsEndpoint":    text(func(f *fileTelemetry, v string) { f.logsEndpoint = &v }),
		"compression":     text(func(f *fileTelemetry, v string) { f.compression = &v }),
		"serviceName":     {kind: kindString},
		"environment":     text(func(f *fileTelemetry, v string) { f.environment = &v }),
		"headers":         {kind: kindCustom, parse: parseTelemetryHeaderFiles},
		"sampleRate":      ratio(0, func(f *fileTelemetry, v *float64) { f.sampleRate = v }),
		"interval":        duration(func(f *fileTelemetry, v time.Duration) { f.interval = v }),
		"slowRequest":     duration(func(f *fileTelemetry, v time.Duration) { f.slowRequest = v }),
		"timeout":         duration(func(f *fileTelemetry, v time.Duration) { f.tls.Timeout = v }),
		"tls": section(map[string]*field{
			"caFile":          text(func(f *fileTelemetry, v string) { f.tls.CAFile = v }),
			"certificateFile": text(func(f *fileTelemetry, v string) { f.tls.CertFile = v }),
			"keyFile":         text(func(f *fileTelemetry, v string) { f.tls.KeyFile = v }),
		}),
		"frontend": section(map[string]*field{
			"enabled":    set(kindBool, func(f *fileTelemetry, v any) { f.frontend = v.(bool) }),
			"sampleRate": ratio(0, func(f *fileTelemetry, v *float64) { f.frontendRate = v }),
		}),
		"diagnostics": section(map[string]*field{
			"highWater":      ratio(0.01, func(f *fileTelemetry, v *float64) { f.highWater = v }),
			"sampleInterval": duration(func(f *fileTelemetry, v time.Duration) { f.sampleEvery = v }),
			"thresholds":     {kind: kindCustom, parse: parseTelemetryThresholds},
			"snapshotDir":    text(func(f *fileTelemetry, v string) { f.snapshot = &v }),
			"heapProfileDir": text(func(f *fileTelemetry, v string) { f.heapProfiles = v }),
			"heapProfileMaxBytes": fileInt(1<<20, 64<<20, func(s *FileSettings, v int64) {
				s.telemetry.heapProfileMaxBytes = v
			}),
		}),
	})
}

func parseTelemetryThresholds(p fileParser, n *yaml.Node, key string) error {
	var thresholds [3]float64
	if n.Kind != yaml.SequenceNode || len(n.Content) != len(thresholds) {
		return p.errorf(n, key, "requires three increasing memory ratios")
	}
	for i, node := range n.Content {
		v, err := strconv.ParseFloat(node.Value, 64)
		if (node.Tag != "!!float" && node.Tag != "!!int") || err != nil || math.IsNaN(v) || v <= 0.05 || v > 1 || i > 0 && v <= thresholds[i-1] {
			return p.errorf(node, key, "requires three increasing memory ratios greater than 0.05 and at most 1")
		}
		thresholds[i] = v
	}
	p.file.Settings.telemetry.thresholds = thresholds
	return nil
}

func parseTelemetryHeaderFiles(p fileParser, node *yaml.Node, key string) error {
	if node.Kind != yaml.MappingNode || len(node.Content) > 16 {
		return p.errorf(node, key, "must map up to eight header names to absolute file paths")
	}
	files := make(map[string]string, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		name, file := strings.ToLower(node.Content[i].Value), node.Content[i+1]
		if !validTelemetryHeader(name) || files[name] != "" || file.Tag != "!!str" || !filepath.IsAbs(file.Value) {
			return p.errorf(file, key, "requires unique allowed header names and absolute file paths")
		}
		files[name] = file.Value
	}
	p.file.Settings.telemetry.headers = files
	return nil
}

func validTelemetryHeader(name string) bool {
	switch name {
	case "host", "cookie", "set-cookie", "content-type", "content-encoding", "content-length", "connection", "transfer-encoding", "proxy-authorization":
		return false
	}
	return httpguts.ValidHeaderFieldName(name)
}

func (f fileTelemetry) validate() error {
	if f.compression != nil && *f.compression != "none" && *f.compression != "gzip" {
		return errors.New("compression must be none or gzip")
	}
	for _, endpoint := range []*string{f.endpoint, f.metricsEndpoint, f.tracesEndpoint, f.logsEndpoint} {
		if endpoint != nil {
			if _, err := telemetryURL(*endpoint, f.allowHTTP != nil && *f.allowHTTP); err != nil {
				return err
			}
		}
	}
	if f.interval != 0 && (f.interval < time.Second || f.interval > 5*time.Minute) {
		return errors.New("interval must be between 1s and 5m")
	}
	if f.slowRequest > 10*time.Minute {
		return errors.New("slowRequest must be at most 10m")
	}
	if f.sampleEvery != 0 && (f.sampleEvery < 100*time.Millisecond || f.sampleEvery > 250*time.Millisecond) {
		return errors.New("diagnostics.sampleInterval must be between 100ms and 250ms")
	}
	if (f.snapshot != nil && *f.snapshot != "" && !filepath.IsAbs(*f.snapshot)) || (f.heapProfiles != "" && !filepath.IsAbs(f.heapProfiles)) {
		return errors.New("diagnostics directories must be absolute paths")
	}
	if f.tls.Timeout > time.Minute || (f.tls.CertFile == "") != (f.tls.KeyFile == "") {
		return errors.New("timeout must be at most 1m and TLS certificate/key files must be configured together")
	}
	return nil
}

func telemetryURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	if u.Scheme == "http" && !allowHTTP && !strings.EqualFold(u.Hostname(), "localhost") && !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("HTTP collector requires allowHttp for non-loopback hosts")
	}
	return u, nil
}

func readTelemetryHeaders(files map[string]string) (map[string]string, error) {
	if files == nil {
		return nil, nil
	}
	headers := make(map[string]string, len(files))
	for name, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			return nil, errors.New("server.telemetry.headers: could not open a header file")
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			file.Close()
			return nil, errors.New("server.telemetry.headers: invalid header file")
		}
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		v := strings.TrimSpace(string(data))
		if err != nil || len(data) > 4096 || v == "" || !httpguts.ValidHeaderFieldValue(v) {
			return nil, errors.New("server.telemetry.headers: invalid header file")
		}
		headers[name] = v
	}
	return headers, nil
}

func (c *Config) loadTelemetry(f fileTelemetry) (TelemetryConfig, error) {
	t := TelemetryConfig{
		Interval: 15 * time.Second, SampleRate: 0.1, FrontendSampleRate: 0.1,
		SlowRequest: time.Second, HighWater: 0.8, HeapProfileMaxBytes: 8 << 20,
		SampleEvery: 200 * time.Millisecond, Thresholds: [3]float64{.70, .85, .95},
	}
	var used []string
	legacy := func(name, replacement string) string {
		v := strings.TrimSpace(os.Getenv(name))
		if v != "" {
			for _, part := range used {
				if strings.HasPrefix(part, name+" -> ") {
					return v
				}
			}
			used = append(used, name+" -> "+replacement)
		}
		return v
	}
	enabled := legacy("FLOW_TELEMETRY_ENABLED", "server.telemetry.enabled")
	disabled := legacy("OTEL_SDK_DISABLED", "server.telemetry.enabled: false")
	on := strings.EqualFold(enabled, "true") || enabled == "1"
	off := strings.EqualFold(disabled, "true") || disabled == "1"
	t.Enabled = on && !off
	if f.enabled != nil {
		t.Enabled = *f.enabled
	}
	if t.Enabled {
		if err := f.validate(); err != nil {
			return t, fmt.Errorf("server.telemetry: %w", err)
		}
	}
	base := legacy("OTEL_EXPORTER_OTLP_ENDPOINT", "server.telemetry.endpoint")
	_ = legacy("OTEL_SERVICE_NAME", "collector resource configuration")
	_ = legacy("OTEL_RESOURCE_ATTRIBUTES", "collector resource configuration")
	t.Environment = legacy("FLOW_ENVIRONMENT", "server.telemetry.environment")
	if t.Environment == "" && on {
		t.Environment = "production"
	}
	if f.environment != nil {
		t.Environment = *f.environment
	}
	exportInterval := legacy("OTEL_METRIC_EXPORT_INTERVAL", "server.telemetry.interval")
	exportTimeout := legacy("OTEL_METRIC_EXPORT_TIMEOUT", "server.telemetry.timeout")
	t.LegacyMetricsTemporality = strings.ToLower(legacy("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "collector metrics pipeline"))
	t.LegacyMetricsHistogram = strings.ToLower(legacy("OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", "collector metrics pipeline"))
	_ = legacy("OTEL_METRICS_EXEMPLAR_FILTER", "privacy-filtered trace links")
	traceInterval := legacy("OTEL_BSP_SCHEDULE_DELAY", "server.telemetry.interval")
	traceTimeout := legacy("OTEL_BSP_EXPORT_TIMEOUT", "server.telemetry.timeout")
	_ = legacy("OTEL_BSP_MAX_QUEUE_SIZE", "bounded telemetry queues")
	_ = legacy("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "bounded telemetry queues")
	sampler := legacy("OTEL_TRACES_SAMPLER", "server.telemetry.sampleRate")
	samplerArg := legacy("OTEL_TRACES_SAMPLER_ARG", "server.telemetry.sampleRate")
	if f.endpoint != nil {
		base = *f.endpoint
	}
	t.Endpoint = base
	if f.interval != 0 {
		t.Interval = f.interval
	} else if millis, err := strconv.Atoi(exportInterval); err == nil && millis > 0 && millis <= 300000 {
		t.Interval = time.Duration(millis) * time.Millisecond
	} else if on {
		t.Interval = time.Minute
	}
	if f.sampleRate != nil {
		t.SampleRate = *f.sampleRate
	} else {
		if on || sampler != "" {
			t.SampleRate = 1 // the previous configuration sampled all new roots
		}
		switch sampler {
		case "always_off", "parentbased_always_off":
			t.SampleRate = 0
		case "traceidratio", "parentbased_traceidratio":
			if rate, err := strconv.ParseFloat(samplerArg, 64); err == nil && rate >= 0 && rate <= 1 {
				t.SampleRate = rate
			}
		}
	}
	t.Frontend = t.Enabled && f.frontend
	if f.frontendRate != nil {
		t.FrontendSampleRate = *f.frontendRate
	}
	if f.slowRequest != 0 {
		t.SlowRequest = f.slowRequest
	}
	if f.highWater != nil {
		t.HighWater = *f.highWater
	}
	if f.sampleEvery != 0 {
		t.SampleEvery = f.sampleEvery
	}
	if f.thresholds != [3]float64{} {
		t.Thresholds = f.thresholds
	}
	if f.snapshot != nil {
		t.SnapshotDir, t.SnapshotDisabled = *f.snapshot, *f.snapshot == ""
	}
	t.HeapProfileDir = f.heapProfiles
	if f.heapProfileMaxBytes != 0 {
		t.HeapProfileMaxBytes = f.heapProfileMaxBytes
	}
	var headers map[string]string
	if t.Enabled {
		var err error
		headers, err = readTelemetryHeaders(f.headers)
		if err != nil {
			return t, err
		}
	}
	for i, signal := range []string{"metrics", "traces", "logs"} {
		find := func(key, yaml string) (string, string) {
			common := legacy("OTEL_EXPORTER_OTLP_"+key, yaml)
			if i == 2 {
				return common, ""
			}
			return common, legacy("OTEL_EXPORTER_OTLP_"+strings.ToUpper(signal)+"_"+key, yaml)
		}
		get := func(key, yaml string) string {
			common, specific := find(key, yaml)
			if specific != "" {
				return specific
			}
			return common
		}
		specific := ""
		if i < 2 {
			specific = legacy("OTEL_EXPORTER_OTLP_"+strings.ToUpper(signal)+"_ENDPOINT", "server.telemetry."+signal+"Endpoint")
		}
		commonInsecure, signalInsecure := find("INSECURE", "server.telemetry.allowHttp")
		baseHeaders, signalHeaders := find("HEADERS", "server.telemetry.headers")
		ca := get("CERTIFICATE", "server.telemetry.tls.caFile")
		cert, signalCert := find("CLIENT_CERTIFICATE", "server.telemetry.tls.certificateFile")
		key, signalKey := find("CLIENT_KEY", "server.telemetry.tls.keyFile")
		// The legacy exporters selected complete pairs from a single scope.
		if signalCert != "" && signalKey != "" {
			cert, key = signalCert, signalKey
		} else if cert == "" || key == "" {
			cert, key = "", ""
		}
		compression := get("COMPRESSION", "server.telemetry.compression")
		legacyTimeout := get("TIMEOUT", "server.telemetry.timeout")
		_ = get("PROTOCOL", "OTLP/HTTP protobuf transport")
		if !t.Enabled || i == 2 && f.endpoint == nil && f.logsEndpoint == nil {
			continue
		}
		fromYAML := f.endpoint != nil
		if fromYAML {
			specific = ""
		}
		if override := []*string{f.metricsEndpoint, f.tracesEndpoint, f.logsEndpoint}[i]; override != nil {
			specific, fromYAML = *override, true
		}
		raw := base
		if specific != "" {
			raw = specific
		}
		if raw == "" {
			return t, errors.New("server.telemetry.endpoint (formerly OTEL_EXPORTER_OTLP_ENDPOINT) is required when telemetry is enabled")
		}
		// An explicit legacy HTTP URL (or INSECURE flag) was already an opt-in.
		var u *url.URL
		var err error
		if fromYAML {
			u, err = telemetryURL(raw, f.allowHTTP != nil && *f.allowHTTP)
		} else {
			// The old exporters used only the host, scheme and path.
			u, err = url.Parse(raw)
			if err == nil {
				u.User, u.RawQuery, u.Fragment, u.RawFragment, u.RawPath, u.ForceQuery = nil, "", "", "", "", false
				u, err = telemetryURL(u.String(), true)
			}
		}
		if err != nil {
			return t, errors.New("server.telemetry: invalid collector endpoint")
		}
		if !fromYAML {
			flag, flagErr := strconv.ParseBool(signalInsecure)
			if flagErr != nil {
				flag, flagErr = strconv.ParseBool(commonInsecure)
			}
			if flagErr == nil {
				if flag && u.Scheme == "https" {
					return t, errors.New("server.telemetry: a legacy HTTPS endpoint conflicts with its insecure flag")
				}
				if !flag {
					u.Scheme = "https"
				}
			}
		}
		if f.allowHTTP != nil {
			if _, err := telemetryURL(u.String(), *f.allowHTTP); err != nil {
				return t, fmt.Errorf("server.telemetry: %w", err)
			}
		}
		if specific == "" {
			u.Path, u.RawPath = path.Join(u.Path, "/v1/"+signal), ""
		} else if u.Path == "" {
			u.Path = "/"
		}
		tgt := TelemetryTarget{URL: u.String(), Headers: headers, Timeout: 5 * time.Second, Legacy: !fromYAML}
		if headers == nil && !fromYAML {
			if signalHeaders != "" {
				baseHeaders = signalHeaders
			}
			tgt.Headers = legacyTelemetryHeaders(baseHeaders)
		}
		if !fromYAML {
			tgt.CAFile, tgt.CertFile, tgt.KeyFile = ca, cert, key
			tgt.Gzip = compression == "gzip"
			tgt.Timeout = 10 * time.Second
			if millis, err := strconv.Atoi(legacyTimeout); err == nil && millis > 0 {
				tgt.Timeout = time.Duration(min(millis, 60000)) * time.Millisecond
			}
			if millis, err := strconv.Atoi(exportTimeout); i == 0 && err == nil && millis > 0 && millis <= 60000 {
				tgt.Timeout = min(tgt.Timeout, time.Duration(millis)*time.Millisecond)
			}
			if i == 1 {
				if f.interval == 0 {
					tgt.Interval = 5 * time.Second
					if millis, err := strconv.Atoi(traceInterval); err == nil && millis > 0 && millis <= 300000 {
						tgt.Interval = time.Duration(millis) * time.Millisecond
					}
				}
				if millis, err := strconv.Atoi(traceTimeout); err == nil && millis > 0 && millis <= 60000 {
					tgt.Timeout = min(tgt.Timeout, time.Duration(millis)*time.Millisecond)
				}
			}
		}
		if f.compression != nil {
			tgt.Gzip = *f.compression == "gzip"
		}
		if f.tls.CAFile != "" {
			tgt.CAFile = f.tls.CAFile
		}
		if f.tls.CertFile != "" {
			tgt.CertFile, tgt.KeyFile = f.tls.CertFile, f.tls.KeyFile
		}
		if f.tls.Timeout != 0 {
			tgt.Timeout = f.tls.Timeout
		}
		if tgt.Legacy && u.Scheme == "http" && (tgt.CAFile != "" || tgt.CertFile != "") {
			return t, errors.New("server.telemetry: a legacy HTTP endpoint cannot use TLS credentials")
		}
		t.Targets[i] = tgt
	}
	c.warnLegacyTelemetry(used)
	return t, nil
}

func legacyTelemetryHeaders(value string) map[string]string {
	var headers map[string]string
	for item := range strings.SplitSeq(value, ",") {
		key, text, ok := strings.Cut(item, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		decoded, err := url.PathUnescape(strings.TrimSpace(text))
		if ok && err == nil && validTelemetryHeader(key) && len(decoded) <= 4096 && httpguts.ValidHeaderFieldValue(decoded) {
			if headers == nil {
				headers = make(map[string]string)
			}
			headers[key] = decoded
		}
	}
	return headers
}

func (c *Config) warnLegacyTelemetry(used []string) {
	if len(used) > 0 {
		c.Warnings = append(c.Warnings, "Deprecated telemetry environment settings: "+strings.Join(used, "; "))
	}
}
