# Telemetry

Flow exports OTLP/HTTP protobuf metrics, sampled traces, and diagnostic events
to an operator-configured collector. Backend telemetry and browser telemetry
each default to off. With the backend off, Flow leaves the HTTP handler
unwrapped and starts no sampler, exporter, or diagnostic worker.

## Enable

```yaml
server:
  telemetry:
    enabled: true
    endpoint: https://collector.example.test:4318
    headers:
      Authorization: /run/secrets/collector-authorization
    sampleRate: 0.1
    interval: 15s
    slowRequest: 1s
    frontend:
      enabled: true
      sampleRate: 0.1
    diagnostics:
      sampleInterval: 200ms
      thresholds: [0.70, 0.85, 0.95]
      snapshotDir: /var/lib/flow/telemetry
      heapProfileDir: /var/lib/flow/telemetry/profiles
```

Pass the YAML to `flow --config /path/to/flow.yaml`. The collector authorization
file holds the full header value, including the `Bearer` prefix if used.
At enabled startup, Flow reads the file once. The base endpoint appends
`/v1/metrics`, `/v1/traces`, and `/v1/logs` to its path. An explicit
`metricsEndpoint`, `tracesEndpoint`, or `logsEndpoint` supplies that signal's
complete URL, with `/` for an empty path. Flow accepts HTTPS and loopback HTTP.
Use `allowHttp: true` to opt in to plaintext on a private network. Endpoint URLs
exclude credentials, query strings, and fragments. Redirects fail the export.
The full options and ranges are in [Configuration](configuration.md#telemetry).

### Move from environment variables

Existing environment settings remain supported for at least one release.
Startup emits one warning with variable names and replacements. An explicit
YAML `enabled` overrides both legacy enablement variables. A YAML base endpoint
supersedes the old shared and signal-specific endpoints. Move header and TLS
settings when you migrate the endpoint. Flow keeps old transport credentials
scoped to targets that still use legacy endpoints.

| Existing input | YAML replacement or migration |
| --- | --- |
| `FLOW_TELEMETRY_ENABLED`, `OTEL_SDK_DISABLED` | `server.telemetry.enabled` |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_{METRICS,TRACES}_ENDPOINT` | `server.telemetry.endpoint`, `metricsEndpoint`, `tracesEndpoint`. Shared URL path prefixes and full per-signal URLs retain their old meanings. |
| `OTEL_EXPORTER_OTLP_{HEADERS,CERTIFICATE,CLIENT_CERTIFICATE,CLIENT_KEY}` and per-signal variants | `headers` with file paths and `tls.{caFile,certificateFile,keyFile}`. Legacy signal headers override common headers. |
| `OTEL_EXPORTER_OTLP_{INSECURE,COMPRESSION,TIMEOUT}` and per-signal variants | An `http://` endpoint with `allowHttp` where needed, `compression`, and `timeout`. Legacy URLs explicitly using HTTP continue to work. An insecure flag on an HTTPS endpoint is a configuration error. |
| `OTEL_METRIC_EXPORT_INTERVAL`, `OTEL_METRIC_EXPORT_TIMEOUT`, `OTEL_BSP_SCHEDULE_DELAY`, `OTEL_BSP_EXPORT_TIMEOUT` | `interval`, `timeout`. Existing metric and span processor duration variables use milliseconds. |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `sampleRate`, including 0 and 1. Incoming trace context and baggage are omitted. |
| `FLOW_ENVIRONMENT` | `environment`. Flow exports the scrubbed label as `deployment.environment` and `deployment.environment.name`. |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | Set custom resource labels on your collector. Flow exports `flow-api`, the source revision when available, and a random process-lifetime instance identifier. |
| `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`, `OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION` | Legacy HTTP and coding-session metrics honor the legacy metric preferences. The new Flow metrics use explicit histogram buckets and cumulative counters. |
| `OTEL_EXPORTER_OTLP_PROTOCOL`, span processor queue sizing, `OTEL_METRICS_EXEMPLAR_FILTER` | Flow exports protobuf over HTTP with bounded queues. Slow-request events link to retained traces. |

Legacy environment endpoints only receive metrics and traces. This also
applies to a shared endpoint that is overridden by both signal URLs.
Set a YAML `logsEndpoint` or shared `endpoint` to receive events.
Existing coding-session metrics retain their instrument names. The schema drops
open-ended dimensions such as models, upstream names, regions, and sandbox
size labels. Legacy HTTP metric names remain available when the metrics target
comes from the environment. They carry only registered routes, method, and
status code. Update dashboards that group by fields excluded by the privacy
schema. Privacy filtering applies regardless of configuration source.

## Metric and event catalog

These are OTLP names. Histograms emit bucket counts, observation counts, and,
where available, sums. The included Grafana dashboards use Prometheus-style
names, units, and labels. The `flow.status` label carries an HTTP status class,
browser outcome, or fixed failure reason, depending on the instrument.

| Metrics | Signal and dimensions |
| --- | --- |
| `flow.http.server.duration`, `.request.size`, `.response.size` | Seconds and byte histograms by registered `http.route`, `flow.method`, and `flow.status`. Sizes count body bytes read by the handler and written to its response. |
| `flow.http.server.in_flight`, `.body_in_flight_bytes`, `.busy`, `.unavailable` | In-flight requests by route and method, cumulative bytes read by requests still in flight, and HTTP 503 counters with and without `Retry-After` combined. `.busy` counts the subset with `Retry-After`. |
| `flow.db.statement.duration`, `flow.db.transaction.{retries,failures}` | SQL dispatch duration and query count by route and statement kind. Retry and failure counters identify route, database family, and classified conflict reason. |
| `flow.db.pool.{open,in_use,idle,max_open,waits,wait_ns}` | Connection counts, cumulative wait count and wait time by pool (`main`, `snapshots`, `reads`). |
| `flow.mcp.tool.duration`, `flow.background.job.duration`, `flow.external.call.duration` | Tool, job, and external call duration and count. Job kinds cover webhook, import, triage, recurring, and agent runs. |
| `flow.realtime.frame.size`, `flow.document.save.size`, `flow.document.request.limit_bytes` | Frame count and size by transport and safe message kind, save size by route, and document body limit. |
| `flow.process.heap.{live,inuse,released}_bytes`, `flow.process.go.{total,managed,limit,headroom}_bytes`, `flow.process.{stack_bytes,rss_bytes,goroutines}` | Process memory and goroutines. Heap live is live after the last GC, heap in-use includes unused space in allocated spans, and managed Go bytes subtract released memory. |
| `flow.process.gc.{cycles,pause}` | Cumulative GC count and pause histogram. |
| `flow.metadata.work.{used_bytes,limit_bytes,peak_bytes,waiting}`, `.kind.{used_bytes,peak_bytes,waiting}`, `.refusals` | Process work budget, operation-kind totals, and contended vs over-limit refusals. |
| `flow.memory.budget.{used_bytes,limit_bytes,peak_bytes}` | Policy residency, cached rows, image cache, source descriptors, source disk bytes, and realtime budget. Peaks are emitted where tracked. |
| `flow.cgroup.memory.{current_bytes,peak_bytes,max_bytes,high_bytes,peak_reset}`, `.events.{high,max,oom,oom_kill}`, `.psi.{some_avg10,full_avg10,some_us,full_us}` | Linux usage, limits, events, and pressure from the cgroup imposing the tightest hard memory limit. This can include sibling usage under a shared parent. PSI average is a percentage. Total stall count is in microseconds. v1 emits available equivalents. |
| `flow.browser.{lcp,inp,cls,ttfb,navigation.duration,long_task.duration,heap.size}` | Web vitals, page-load and router-paint timings, long tasks, and estimated JS heap by page family where supported. |
| `flow.browser.{api.requests,realtime.reconnects,local_store.failures,dropped}` | HTTP outcomes by server route pattern, reconnects by transport, storage failures by operation, and dropped browser records. Network failures use `unmatched`. |
| `flow.telemetry.{dropped_records,dropped_batches,dropped_series}` | Queue and export loss by signal, including `browser_logs` and `browser_metrics`. Rejected browser series carry kind `browser`. |

Frequently sampled gauges also emit `_min` and `_max` for the export window.
The set includes Go heap live/in-use, Go total/managed/headroom, goroutines,
RSS, cgroup current, work used/waiting, and body bytes in flight. Cgroup peak
comes from the kernel separately. A failed
metrics export carries its extrema forward into the next successful export.
Transient measurements can be absent before their first observation. Memory
threshold and restart events are sent as soon as the logs exporter is available.

| OTLP event | Contents |
| --- | --- |
| `flow.request.slow` | Route pattern, method, status class, duration, summed stage durations, and a trace link when retained. |
| `flow.memory.high_water` | Threshold name, sampled memory and work peaks, up to eight work holders by kind and route, up to eight active charged HTTP requests with method, body bytes, work bytes and elapsed time, and up to eight compiled goroutine frame counts. The request census tracks up to 512 charged requests at once. |
| `flow.memory.previous_process_unclean` | Timestamp and numeric snapshot from an unclean stop. Temporary collector failures trigger a retry. An explicit rejection leaves the file for the next startup. Confirm the cause with your runtime or orchestrator. |
| `flow.browser.js_error` | Server-keyed error fingerprint and up to five allowlisted built-asset basenames with line numbers. |

## Investigate a memory spike or kill

Set `GOMEMLIMIT` about 10-15% below the container's hard memory limit as a starting
point. Leave room for memory outside Go and watch the kernel's actual charges.
The high-frequency sampler checks the larger of managed Go memory and cgroup
current usage against the smaller available limit. It also checks metadata
work independently. A threshold re-arms after a
drop of up to five percentage points. Each tier has a five-minute capture
cooldown. A jump can select the highest eligible tier in one sample.

1. Compare `flow.process.heap.inuse_bytes_max`, `flow.process.go.managed_bytes_max`,
   `flow.metadata.work.used_bytes_max`, and `flow.cgroup.memory.peak_bytes`.
   The interval maxima retain sampled spikes between exports. The kernel peak
   can retain spikes that happened between samples.
2. Check cgroup `high`, `max`, `oom`, and `oom_kill` changes and PSI. The v2
   kernel peak resets after collection if the open file supports a reset. A
   `peak_reset` value of 0 means it is a cgroup-lifetime high-water mark.
   Cgroup metrics and current RSS come from Linux. Unsupported files are omitted.
3. Open `flow.memory.high_water`. Compare individual work holders with the total
   budget and look for one route dominating charged work. Check frame counts
   for blocked or concurrent operations. An application frame is preferred
   when available. Above 2,048 goroutines the frame census is omitted to keep
   capture bounded.
4. Preserve the previous-run snapshot and the profile in the replica's directory.
   The snapshot is rewritten atomically about every 12 seconds, about every
   second while warning pressure stays active, and on an eligible crossing.
   This preserves the last sampled values on an ordinary process kill. Keep
   the directory on persistent storage if it must survive a replaced Pod.
   Pending crash evidence lives in `memory.previous-*.json`. Flow retains up
   to four pending files plus the rolling file. If the pending slots are full
   and the rolling file records another unclean exit, Flow pauses rolling
   writes until the collector acknowledges that file's event.
5. Enable `heapProfileDir` on production replicas where there is a private
   writable diagnostics volume. Flow rotates three compressed heap profiles
   and one emergency goroutine profile. Each file is capped, and captures that
   would exceed the cap are discarded. The emergency profile is limited to
   snapshots with at most 4,096 goroutines. Inspect locally with
   `go tool pprof -top -inuse_space /path/to/heap-0.pprof` and
   `go tool pprof -top /path/to/goroutine-0.pprof`.

The events panel can show values like these during a document-save spike.

```text
flow.memory.high_water
flow.memory.threshold=emergency
flow.cgroup.memory.current_bytes=1016592384 flow.cgroup.memory.peak_bytes=1040187392
flow.cgroup.memory.max_bytes=1073741824 flow.metadata.work.used_bytes=369098752
flow.request.1.route=/api/documents/{id} flow.request.1.method=PATCH
flow.request.1.work_bytes=268435456 flow.request.1.body_bytes=25165824 flow.request.1.elapsed_ms=1840
flow.holder.1.kind=document_body flow.holder.1.route=/api/documents/{id}
flow.memory.previous_process_unclean flow.snapshot.time_unix_ms=1795824000000
```

This synthetic example points to a large in-flight save while the cgroup is
near its cap. Look at cgroup `oom_kill` or the Pod's termination reason to
confirm a kill, then correlate the holder with its heap profile and traces.

## Investigate slow requests and deploys

Use **Flow API Performance** in Grafana to rank APIs by p95. Change `0.95` to
`0.50` or `0.99` and compare the request rate so low-volume routes get context.
For Prometheus-compatible `le` buckets the query is:

```promql
topk(10, histogram_quantile(0.95,
  sum by (le, http_route, flow_method, flow_status) (
    rate(flow_http_server_duration_seconds_bucket{http_route!~"unmatched|/api/events|/api/realtime/(events|socket)|.*/stream|/api/coding/environments/.*/terminal"}[5m])
  )
))
```

Filter the slow event panel in Loki with
`{service_name="flow-api"} |= "flow.request.slow"`. Expand the record to see stage
times and follow its trace ID to Tempo, or use the same ID in an OTLP-backed
Jaeger deployment. In traces look at authentication, authorization and body
reads, installed metadata reads/selects/hydration, mutation lock/load/apply/
persist/commit, DB begin and SQL, work admission, fan-out, document save,
MCP, and external request spans. Repeated operations report their total duration
and count. The span shows the longest instance of each stage, with sanitized
operation, table, or host labels where available. Stage times can overlap.
Slow requests and HTTP 5xx responses qualify even at zero head sampling.

Compare `service_version` for the same `http_route`, `flow_method`, and
`flow_status` across revisions. Alongside latency, check `flow_http_server_busy_total`,
SQL statement counts and durations, transaction failures and retries, and DB pool
wait time. Short-lived 503s with `Retry-After` usually indicate a transient
pressure condition. A 503 without that header can be a request that exceeds a
hard budget. Stream lifetimes appear in HTTP metrics and are excluded from
slow-request events.

## Deployment example

The Compose stack includes an OpenTelemetry Collector, VictoriaMetrics, Tempo
with local storage, one Loki process, and Grafana with **Flow Memory** and
**Flow API Performance** provisioned. From `deploy/observability`, set
`GRAFANA_ADMIN_PASSWORD`, then run `docker compose up -d`. Grafana is on
`http://127.0.0.1:3300` and OTLP/HTTP on `http://127.0.0.1:4318`. Use that
collector URL for a Flow process on the host. For containers, attach Flow
to the same private Docker network and use `http://collector:4318` with
`allowHttp: true`, or place the listener behind your authenticated TLS proxy.

The service limits total 2 vCPUs and 3,840 MiB. Put Docker's volumes on a
dedicated 50 GB filesystem or volume quota. Retention is 14 days for metrics,
72 hours for traces, and 7 days for events. At roughly 300-400 users, start at
10% trace sampling and monitor ingestion and disk utilization. Data rates
depend on API traffic, browser sample rates, and route cardinality. The
Compose file, pipeline, and dashboards are in [deploy/observability](../deploy/observability).

### Kubernetes

The chart at `deploy/observability` installs the same five services with
ClusterIP access and 46 GiB of persistent storage. Use one installation per
namespace. Create the `flow-observability` namespace and a Secret named
`flow-grafana` with the `admin-password` key before installing.

```sh
helm upgrade --install flow-observability deploy/observability \
  --namespace flow-observability
kubectl --namespace flow-observability port-forward svc/grafana 3300:3000
```

Start with the [Flow Helm values example](../deploy/observability/helm-flow.yaml).
For the internal collector, set `telemetry.endpoint` to
`http://collector.flow-observability.svc.cluster.local:4318`, set `allowHttp`
to `true`, and leave `existingSecret` unset. Restrict collector access to
your Flow workloads. For a collector behind TLS and authentication, keep
the Secret mapping and set the endpoint to that listener.

The example's per-Pod diagnostics volume survives a container restart. Use
a replica-local persistent volume for snapshots that must survive a Pod
replacement. An existing observability installation can use a Prometheus
exporter or remote write for metrics, Jaeger or Tempo for traces, and Loki
for OTLP logs. Adapt metric names and resource label promotion to the receiver.

## Privacy and cost

Flow builds records from explicit schemas and fixed enumerations. Browser
batches use same-origin keepalive requests with an authenticated session and
omit the page referrer. The server checks the request's origin using the
configured proxy trust policy. It revalidates browser measurements against
its route and bundled asset catalogs, restricts request size, and rate-limits
intake without keeping per-client identity. Tabs stop reporting when the
intake responds with an expired session or reports that it is disabled.
Raw errors, URLs, credentials, and SQL text are excluded. Local profiles stay
on the replica.
Existing application logs are outside the OTLP pipelines.
See [Privacy](privacy.md#optional-telemetry).

The exporter holds up to 96 queued traces and 128 queued events. Metric
aggregation holds up to 8,192 request and operation series. Browser data has
its own limits of 32 queued errors and 1,024 series, and separate export
connections and backoff. Exports have a 4 MiB payload cap. When the
collector recovers, check the `flow.telemetry.dropped_*` counters for gaps.

The request benchmark uses a scratch SQLite database, an Apple M4, a local
OTLP receiver, and three rounds with the modes rotated. Each run times 300
requests per operation after warming up. The table reports medians of the
per-run results. Latencies include the loopback HTTP round trip.

| Tracing | Issue list p95 | Issue PATCH p95 | Document save p95 | CPU per 900 requests | Peak sampled RSS |
| --- | --- | --- | --- | --- | --- |
| Off | 2.83 ms | 5.72 ms | 9.65 ms | 3.31 s | 109.5 MiB |
| 10% | 2.55 ms | 5.64 ms | 9.65 ms | 3.31 s | 112.5 MiB |
| 100% | 2.67 ms | 5.39 ms | 8.95 ms | 3.31 s | 112.6 MiB |

RSS increased by about 3 MiB. Changes in latency and CPU between rounds
on the shared host exceeded the differences between modes. The fixture
used a 50-issue list, a small document, and a one-second export interval.
The sampler's runtime read, extrema, and threshold checks had a median
cost of **0.59 microseconds per sample** on macOS. On Linux arm64 with
cgroup v2 and RSS reads included, the median was **1.81 microseconds**.
Both fast-sample benchmarks had zero allocations. Reading the work
budget through its production lock measured a median of **0.019
microseconds** separately. These benchmarks exclude the once-per-second
kernel counter reads and occasional diagnostic captures.

The disabled work-admission benchmark on Apple M4 measures 456 B and seven
allocations per ordinary lease, compared with 496 B and eight allocations
before telemetry. Additional snapshot leases use 208 B and four allocations,
compared with 248 B and five allocations before telemetry.

To reproduce the measurements, build the API to
`/tmp/codex-telemetry/flow-api`, run
`node scripts/bench-telemetry.mjs /tmp/codex-telemetry/flow-api 900`, and run
`go test ./internal/telemetry -run '^$' -bench BenchmarkTelemetrySampler -benchmem`
and `go test ./internal/store -run '^$' -bench 'BenchmarkTelemetryWork(BudgetSnapshot|AdmissionOff)' -benchmem`
from `api/`. Use the machine-wide lock for these commands in shared worktrees.
