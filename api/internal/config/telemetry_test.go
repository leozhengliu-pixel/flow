package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelemetryLegacyEndpointsAndWarning(t *testing.T) {
	t.Setenv("FLOW_TELEMETRY_ENABLED", "TRUE")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test:4318/prefix/")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://metrics.test:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://traces.test:4318/custom/?ignored=fixture-secret")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer%20fixture-secret,x-common=fixture-common")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer%20fixture-trace")
	t.Setenv("FLOW_ENVIRONMENT", "staging")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_COMPRESSION", "gzip")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "7000")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "3000")
	t.Setenv("OTEL_BSP_EXPORT_TIMEOUT", "1200")
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
	c := Config{}
	got, err := c.loadTelemetry(fileTelemetry{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Frontend || got.Interval != 7*time.Second || got.SampleRate != .25 ||
		got.Targets[0].URL != "http://metrics.test:4318/" || got.Targets[1].URL != "http://traces.test:4318/custom/" ||
		got.Targets[2].URL != "" || got.Targets[2].Headers != nil || got.Environment != "staging" {
		t.Fatal("legacy enablement, sampling or signal path semantics changed")
	}
	if len(got.Targets[1].Headers) != 1 || got.Targets[1].Headers["authorization"] != "Bearer fixture-trace" ||
		got.Targets[0].Headers["x-common"] != "fixture-common" || !got.Targets[0].Gzip ||
		got.Targets[1].Interval != 3*time.Second || got.Targets[1].Timeout != 1200*time.Millisecond {
		t.Fatal("legacy signal-specific transport configuration changed")
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "OTEL_EXPORTER_OTLP_ENDPOINT -> server.telemetry.endpoint") ||
		strings.Contains(c.Warnings[0], "fixture-") || strings.Contains(c.Warnings[0], "collector.test") {
		t.Fatal("deprecation warning did not contain only variable names and replacements")
	}
}

func TestTelemetryYAMLPrecedenceAndValidation(t *testing.T) {
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://legacy.test:4318/private")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer%20fixture-legacy")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("FLOW_ENVIRONMENT", "staging")
	file := filepath.Join(t.TempDir(), "authorization")
	if err := os.WriteFile(file, []byte("Bearer fixture-new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	parse := func(body string) (fileTelemetry, error) {
		f, err := ParseFile("test.yaml", []byte("server:\n  telemetry:\n"+body))
		if err != nil {
			return fileTelemetry{}, err
		}
		return f.Settings.telemetry, nil
	}
	yaml, err := parse(`    enabled: true
    endpoint: https://new.test:4318/otel
    environment: canary
    tracesEndpoint: https://traces.test/explicit
    compression: gzip
    interval: 10s
    sampleRate: 0
    frontend: {enabled: true, sampleRate: 0.5}
    diagnostics: {sampleInterval: 150ms, thresholds: [0.6, 0.8, 0.9], snapshotDir: ""}
    headers:
      Authorization: ` + file + "\n")
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	got, err := c.loadTelemetry(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.Frontend || got.SampleRate != 0 || got.FrontendSampleRate != .5 || got.Environment != "canary" ||
		got.Interval != 10*time.Second || !got.SnapshotDisabled || got.SampleEvery != 150*time.Millisecond ||
		got.Thresholds != [3]float64{.6, .8, .9} || got.Targets[0].URL != "https://new.test:4318/otel/v1/metrics" ||
		got.Targets[1].URL != "https://traces.test/explicit" || got.Targets[1].Headers["authorization"] != "Bearer fixture-new" ||
		!got.Targets[2].Gzip {
		t.Fatal("YAML did not override legacy options")
	}
	for _, input := range []string{
		"    endpoint: http://collector.test:4318\n",
		"    endpoint: https://user:fixture-secret@collector.test\n",
		"    endpoint: https://collector.test/path?token=fixture-secret\n",
		"    headers: {Authorization: fixture-secret}\n",
		"    diagnostics: {sampleInterval: 5ms}\n",
		"    diagnostics: {thresholds: [0.7, 0.6, 0.9]}\n",
	} {
		if _, err := parse("    enabled: true\n" + input); err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("invalid telemetry config was accepted or echoed")
		}
	}
	yaml, err = parse("    enabled: false\n    endpoint: http://collector.test:4318\n    headers: {Authorization: /missing-telemetry-test-file}\n")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_SDK_DISABLED", "")
	if got, err = c.loadTelemetry(yaml); got.Enabled || err != nil {
		t.Fatal("explicit YAML off did not bypass credential loading")
	}
}

func TestTelemetryEndpointValidationFollowsEffectiveEnablement(t *testing.T) {
	file, err := ParseFile("flow.yaml", []byte("server:\n  telemetry:\n    endpoint: http://collector.test:4318\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	c := Config{}
	if got, err := c.loadTelemetry(file.Settings.telemetry); err != nil || got.Enabled {
		t.Fatal("disabled telemetry validated an unused collector")
	}
	t.Setenv("OTEL_SDK_DISABLED", "")
	if _, err := c.loadTelemetry(file.Settings.telemetry); err == nil {
		t.Fatal("legacy enablement bypassed YAML endpoint validation")
	}
}

func TestTelemetryMixedConfigurationKeepsLegacySampler(t *testing.T) {
	t.Setenv("FLOW_TELEMETRY_ENABLED", "")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test:4318")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	c := Config{}
	got, err := c.loadTelemetry(fileTelemetry{enabled: pointer(true)})
	if err != nil || !got.Enabled || got.SampleRate != 0 {
		t.Fatal("YAML enablement lost the legacy sampler")
	}
}

func TestTelemetryLegacyClientCertificatesKeepTheirKeys(t *testing.T) {
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector.test:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/common.crt")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/common.key")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_CLIENT_CERTIFICATE", "/metrics.crt")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_CLIENT_KEY", "/metrics.key")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE", "/partial.crt")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY", "")
	var c Config
	got, err := c.loadTelemetry(fileTelemetry{})
	if err != nil {
		t.Fatal(err)
	}
	if m, tr := got.Targets[0], got.Targets[1]; m.CertFile != "/metrics.crt" || m.KeyFile != "/metrics.key" ||
		tr.CertFile != "/common.crt" || tr.KeyFile != "/common.key" {
		t.Fatal("signal-specific TLS configuration split a certificate pair")
	}
}
