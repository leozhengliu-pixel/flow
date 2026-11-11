# P01 measuring and permission baseline

P01 changes test instrumentation only. The seed and SQL probe are excluded from
production builds by `loadtest`; production routes, records, migrations and
business behavior are unchanged. `CHANGELOG.md` is the sole existing file outside
the package's ownership list, required by the shared delivery rules.

## Controls and compatibility

The existing `TestLoadUsers`, `TestLoadWriteDecodes`, `FLOW_LOAD_USERS`,
`FLOW_LOAD_DURATION`, `FLOW_LOAD_CHANGE_EVERY`, `FLOW_LOAD_ACCESS_EVERY`,
`FLOW_LOAD_WRITES`, `FLOW_LOAD_REPORT`, `FLOW_LOAD_PEAK_PROFILE` and profile
controls remain available. Existing report keys and units remain available.
`FLOW_LOAD_LEGACY=1` runs the original five-account `TestLoadUsers` verbatim,
including its original success-only freshness calculation; do not use that
calculation for an SLO decision.

All new controls are test controls, with no production defaults or rollout:

| Control | Default | Meaning |
| --- | --- | --- |
| `FLOW_LOAD_UNIQUE_USERS` | 5 | Real principals/sessions, independent of active tab count; 1..10000 |
| `FLOW_LOAD_METADATA_SCALE` | 1 | Metadata multiplier; falls back to the existing `FLOW_LOAD_SCALE` |
| `FLOW_LOAD_METADATA_MODE` | count | `count` multiplies record counts; `body` multiplies serialized body sizes |
| `FLOW_LOAD_METADATA_COUNT_SCALE` | mode-dependent | Explicit independent count multiplier |
| `FLOW_LOAD_METADATA_BODY_SCALE` | mode-dependent | Explicit independent body multiplier |
| `FLOW_LOAD_PERMISSION_MODE` | uniform | `unique` creates eleven private teams and distinct membership combinations |
| `FLOW_LOAD_REQUEST_TIMEOUT` | 2m | Complete request/body deadline |
| `FLOW_LOAD_DELIVERY_TIMEOUT` | 15s | Apply deadline; outstanding eligible recipients stay in the denominator |
| `FLOW_LOAD_APPLY_DELAY` | 0 | Delay actual simulated state installation, independently of receipt |
| `FLOW_LOAD_READ_RPS` | 0 | Additional scheduled small reads/s |
| `FLOW_LOAD_ISSUE_WRITE_RPS` | 0 | Scheduled issue edits/s |
| `FLOW_LOAD_METADATA_WRITE_RPS` | 0 | Scheduled metadata writes/s, replacing change-every scheduling |
| `FLOW_LOAD_BASE_URLS` | unset | Two external API URLs; generator is a separate process |
| `FLOW_LOAD_METRICS_URLS` | unset | Two test metric sources; required to claim external SQL/commit/CPU measurements |
| `FLOW_LOAD_FIXTURE` | unset | Private fixture file with actual identity, scope, record and seed information |
| `FLOW_LOAD_PREPARE` | off | Prepare that fixture using explicit throwaway test database settings |
| `FLOW_LOAD_TWO_PROCESS` | off | Launch two actual API subprocesses and verify distinct PIDs |
| `FLOW_LOAD_BROWSER_ACKS` | unset | Append-only browser apply acknowledgements; receipt never supplies these |
| `FLOW_LOAD_STRICT_PERMISSIONS` | off | Also fail on the documented pre-existing scoped-key defect |

Fractional count scales are useful for smoke tests and are reported as such.
The new load fixture includes real memberships, notifications, document history
and project/document/dependency relationships. Write-cost fixtures retain the
original collections and sizes at 1x, to keep their before/after comparison fair.
Seed fields omitted by older callers keep their old behavior.

## Measurement contracts

No shared architecture interface is implemented by P01; those belong to later
packages. New load-only store entry points are:

```go
func (s *SQLiteStore) SeedScaleP01Principals(ctx context.Context, workspace string, teams []string, count int, uniquePermissions bool) ([]ScaleP01Principal, error)
func (s *SQLiteStore) EnableScaleP01Probe(config DatabaseConfig) (*ScaleP01Probe, error)
func (p *ScaleP01Probe) Snapshot() (ScaleP01SQL, []ScaleP01Commit, int64)
func (p *ScaleP01Probe) SQLSnapshot() ScaleP01SQL
func ScaleP01RequestContext(ctx context.Context, requestID string) context.Context
func (p *ScaleP01Probe) BeginLoadWrite()
func (p *ScaleP01Probe) EndLoadWrite()
func (p *ScaleP01Probe) ActiveLoadWrites() int64
```

`SeedSyntheticMetadata(ctx context.Context, workspace string, spec SyntheticMetadata) error`
keeps its existing signature. `SyntheticMetadata` adds independent `CountScale`
and `BodyScale`, `Notifications`, `UserIDs` and `Relations`.

The driver records actual successful commits, including SQLite and prepared SQL.
Test request context associates a membership transaction's commit with its later
audit event, so access latency is not measured from the later audit commit.
Events and applies are joined by event ID and client ID, with duplicate protection
and support for a stream beating its writer's response. Eligibility is established
from the actual event permission policy before load, including document links.
Test-only request tracking keeps commit observation and SSE readers running while
timed-out API handlers finish. The drain is bounded by the request timeout plus
the delivery timeout; unfinished handlers make metrics explicitly incomplete.
The late-commit test forces an actual write after its caller's deadline and the
load window, and requires that commit and apply to remain in the report.

`routes.*.sloLatencyMs` and `commitToClientApply.sloLatencySeconds` include every
attempt/eligible recipient. Failures sort at positive infinity; a censored
percentile is JSON `null`, never zero. Legacy numeric freshness fields include a
timeout penalty and explicitly identify that treatment. Counts separately expose
missing receipt, late/unapplied delivery, request errors, timeouts and statuses.
Rolling fan-out histograms round upward by at most 0.8% or 1us and release per-client
acknowledgements after their deadline. The driver commit ring is capped at 10,000.

The default client is a Go simulation that parses and installs JSON or finishes
the required reload/visibility requests. It is not the real web client. Actual
browser metrics require `FLOW_LOAD_BROWSER_ACKS`: one JSON line after successful
state installation, `{eventId,clientId,appliedAt}`, with zero-based client indices
and an RFC3339 timestamp on the API host's wall clock. Write complete newline-ended
records promptly; partial trailing lines wait for the next poll. Delayed/future
or pre-commit acknowledgements do not satisfy the SLO. The report explicitly
labels absent browser instrumentation. No real browser result is inferred.

SQL metrics count driver attempts, including background work and errors, excluding
prepare/begin/commit. Execution timing excludes row iteration; its p95 is reported
as a conservative histogram interval. It is not database statement/transaction
latency. Database CPU, rows examined, temporary tables and lock/slow-query metrics
require an independent database-side collector and are reported unavailable here.
CPU uses process user/system time, normalized to four CPUs. In-process memory/CPU
includes both APIs and the generator; external reports separate replica metrics
from generator metrics and explicitly report sampled API peaks.

## Reproduction

Run every command through `~/.flow-local/heavy.sh`, one load at a time, checking
`memory_pressure` for at least 30% free first. From `api/`:

```sh
~/.flow-local/heavy.sh env TMPDIR=/tmp/codex-scale-p01 GOMAXPROCS=4 GOMEMLIMIT=1536MiB go test -p 1 -tags=loadtest ./internal/store ./cmd/server -run '^TestScaleP01' -count=1 -v

~/.flow-local/heavy.sh env TMPDIR=/tmp/codex-scale-p01 GOMAXPROCS=4 GOMEMLIMIT=1536MiB FLOW_LOAD_USERS=300 FLOW_LOAD_UNIQUE_USERS=300 FLOW_LOAD_METADATA_SCALE=1 FLOW_LOAD_DURATION=2m FLOW_LOAD_REPORT=/tmp/lead-verify/apimem/scale-baseline/300-1.json go test -p 1 -tags=loadtest ./cmd/server -run '^TestLoadUsers$' -count=1 -timeout=15m -v

~/.flow-local/heavy.sh env TMPDIR=/tmp/codex-scale-p01 GOMAXPROCS=4 GOMEMLIMIT=1536MiB FLOW_LOAD_WRITES=50 FLOW_LOAD_REPORT=/tmp/lead-verify/apimem/scale-baseline/write-cost.json go test -p 1 -tags=loadtest ./cmd/server -run '^TestLoadWriteDecodes$' -count=1 -timeout=15m -v
```

Run 800/2000 users by changing both user settings. For count 10x use
`FLOW_LOAD_METADATA_SCALE=10`; for body 10x additionally set
`FLOW_LOAD_METADATA_MODE=body`. Only explicitly reported combinations were run;
none establishes the programme's 30-minute MySQL capacity target.

For an isolated two-process smoke test use `FLOW_LOAD_TWO_PROCESS=1`, a short
duration and `FLOW_LOAD_METADATA_COUNT_SCALE=0.002`, running only
`^TestScaleP01TwoProcessLoad$`. SQLite/miniredis is the default. The same test reads
`FLOW_TEST_DATABASE_DRIVER`/`FLOW_TEST_DATABASE_URL` for throwaway MySQL/Postgres.
API subprocesses inherit the CPU/Go memory settings and are stopped by their own
PIDs. Resource-limited production-size MySQL/Redis infrastructure is separate.

For independently started APIs, first run `^TestScaleP01PrepareExternalFixture$`
with `FLOW_LOAD_PREPARE=1`, `FLOW_LOAD_FIXTURE` and explicit throwaway test database
settings. The fixture contains credentials and is private; never print it. Then
run `^TestLoadExternalUsers$` (documented plan alias) or
`^TestScaleP01ExternalUsers$` with matching user/scale settings,
`FLOW_LOAD_BASE_URLS` and the fixture. Every supplied session is preflighted against
both APIs. Optional `FLOW_LOAD_METRICS_URLS` must return the P01 test metric shape;
without a commit source the report explicitly cannot establish commit latency.
P01's metric endpoint exists only in its subprocess test mux, not in production
or OpenAPI. No live server is seeded implicitly by the generator.

## Permission and compatibility coverage

The matrix exercises admin, admin outside a private team, member, explicitly
granted guest, private outsider, child-team/private-ancestor inheritance, keys
unrestricted/selected-empty/selected-set, cross-workspace reads, old/stale slugs,
hidden project references/counts, and actor/payload suppression after revocation
for an already projected queued frame and durable replay. It compares serialized
fields without general null/empty normalization. Frozen list projection accounts
for the existing intentional list/detail field difference; bootstrap additionally
checks visibility and full detail fields. The fixture includes a linked private
issue and a nonzero count control. Full bootstrap derives its issue count from
visible issues, while current REST metadata/detail reads report the stored zero;
that route distinction is explicit in the oracle. The selected-key bootstrap team
reference shape is another distinct existing route contract.

Existing defect found: a selected-set key owned by an admin who can read a private
project can retain its hidden dependency ID in REST and the private issue's count
in bootstrap after key scope filtering. The baseline logs `BASELINE DEFECT` with
separate booleans for both findings; strict mode fails. This
is not fixed or silently normalized by P01. Do not claim the entire security M
matrix is clean or use these baseline passes to enable another package by default.

K coverage is wire-preserving (no production wire changes), legacy harness
reproduction, SQLite same-database replicas and real API subprocesses. The matrix
and load helpers expose the standard external database environment controls.
Docker is unavailable locally. MySQL/Postgres, actual old/new binaries, actual
web fallback, and real browser apply remain unverified locally.

## Results and command log

Results, command exits, source revisions and final hashes are recorded below after
verification. Raw reports/profiles/logs are under `/tmp/codex-scale-p01/`; the lead's
matrix reports are under `/tmp/lead-verify/apimem/scale-baseline/`.

Original source: `b49b6981053160f9de09a765b0cec4a119c7b5e3`.

| Original 2-minute run | 300 users | 800 users |
| --- | ---: | ---: |
| Unique principals | 5 | 5 |
| Peak heap in use MiB | 1118 | 1397 |
| Peak RSS MiB | 1262 | 1805 |
| Process allocation GiB | 74.365 | 135.050 |
| Bootstrap attempts | 1340 | 6351 |
| Bootstrap 503s | 852 | 5772 |
| Reported freshness p95 seconds | 6.164 | 6.004 |

The original freshness percentiles omit lost deliveries and are not SLO evidence.
The machine is shared, so process CPU contention and run-to-run variation apply.

Original 50-write costs (MiB/op): issue 51.178, comment 84.394, team 47.179;
the peer request after team rename is 376.416 MiB and one metadata decode. The
alternating team write is 388.512 MiB and 0.5 decodes/op. Raw original reports:
`before-300-1.json`, `before-800-1.json` under the task directory and
`write-cost-before.json` under the lead baseline directory.
