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

Final implementation: `faf954e0` on `c2798acd` (the added main commit changes CI
only; API code is identical to the original baseline's parent). The final
matrix uses the corrected handler drain. Earlier `pre-drain-*.json` files are
retained only for diagnosis and are superseded by the lead-directory reports.

| Corrected run | 300 / count 1x | 800 / count 1x | 300 / count 10x |
| --- | ---: | ---: | ---: |
| Distinct principals | 300 | 800 | 300 |
| Peak Go heap in use MiB | 1429 | 1425 | 12996 |
| Peak process RSS MiB | 1824 | 1910 | 4618 |
| Process allocation GiB | 129.187 | 81.401 | 34.330 |
| Observed SQL driver statements | 115396 | 283259 | 87841 |
| Eligible event-recipient deliveries | 2258 | 5006 | 300 |
| Applied within deadline | 1602 | 2597 | 0 |
| Missing receipt | 165 | 1465 | 0 |
| Unapplied or late (includes missing) | 656 | 2409 | 300 |
| Commit/apply SLO p95 and p99 | censored | censored | censored |
| Bootstrap 503s | 1273 | 6535 | 2678 |
| Writer failures / attempts | 2 / 11 | 4 / 11 | 11 / 11 |
| Outstanding API writes at final observation | 0 | 0 | 1 |
| Metrics complete | yes | yes | **no** |

The count-10x test completed on this 16 GB machine, but its observation drain
expired with one handler still active. Its commit denominator is therefore
incomplete, explicitly flagged in JSON. It is not a zero-loss result and cannot
support a capacity claim. The other reports also fail the delivery/admission
targets. These runs include the existing morning ramp during the first 20% of
the load window; not-yet-subscribed eligible clients remain in the denominator.
They do not isolate the programme's separate steady-state and cold-start SLOs.
Go's memory limit is soft; the large heap peak is reported rather than hidden.

No performance gain is claimed. The corrected matrix has more principals, richer
metadata, JSON application in the generator and complete failure accounting,
so it is not comparable to the original five-principal success-only run as an
optimization result. The legacy rerun and write-cost comparison use the original
fixture/method to check compatibility.

| 50-write allocation comparison, MiB/op | Before | After |
| --- | ---: | ---: |
| Issue writer | 51.178 | 55.541 |
| Comment writer | 84.394 | 84.420 |
| Team writer | 47.179 | 47.095 |
| Peer request after team write | 376.416 | 376.448 |
| Alternating team writer | 388.512 | 391.827 |

The after report also records SQL/CPU per phase. Issue/comment/team writer SQL
averages are 55.14/42/36.22 statements per operation; the peer request after a team
write averages 16.14. These include background work. The issue phase included
0.02 metadata decodes/op, versus zero before; alternating writes included 0.56,
versus 0.50 before. Such shared-process/background variation is visible, not
attributed to a P01 optimization. CPU and SQL before P01 were not instrumented,
so no before/after CPU or SQL reduction is asserted.

The exact legacy 300-user/5-principal rerun reports 1344 MiB peak heap in use,
1503 MiB RSS, 73.895 GiB allocated, 867 bootstrap 503s and a success-only freshness
p95 of 4.430s. The corresponding before values are 1118/1262 MiB, 74.365 GiB,
852 and 6.164s. Both use the same original method and neither establishes the SLO.

The real two-API-process smoke test passed with distinct API/generator PIDs and
separate replica SQL/CPU/memory samples. The race run enables that subprocess
scenario, in addition to all P01 unit/differential tests. The 2,000-user smoke
also passed its execution check with 2,000 principals, unique permission scopes,
602 MiB sampled peak heap, four writes and zero writer failures. Only 162 of 5004
eligible event-recipient applies met the deadline. Its deliberately small
`0.002` count scale and short cold ramp are not 2,000-user production capacity
evidence. Real browser apply remains unavailable in these runs.

## Files and tests

Existing files: `api/cmd/server/memory_load_test.go`,
`api/cmd/server/memory_write_load_test.go`, `api/internal/store/loadtest_seed.go`,
and the required `CHANGELOG.md` entry. New files are all named for P01:

- `api/cmd/server/scale_external_load_test.go`
- `api/cmd/server/scale_permission_matrix_test.go`
- `api/cmd/server/scale_p01_browser_acks_test.go`
- `api/cmd/server/scale_p01_fixture_test.go`
- `api/cmd/server/scale_p01_histogram_test.go`
- `api/cmd/server/scale_p01_late_commit_test.go`
- `api/cmd/server/scale_p01_measurement_test.go`
- `api/cmd/server/scale_p01_runner_test.go`
- `api/cmd/server/scale_p01_write_test.go`
- `api/internal/store/scale_p01_load_test.go`
- `api/internal/store/scale_p01_permission_test.go`
- `api/internal/store/scale_p01_probe.go`
- `api/internal/store/scale_p01_seed.go`
- `scale-p01-report.md`

New tests: `TestScaleP01PrincipalCounts`, `TestScaleP01MetadataScales`,
`TestScaleP01SQLProbeCommitAndRollback`, `TestScaleP01ReadProjectionBaseline`,
`TestScaleP01FailureDenominators`, `TestScaleP01HistogramAndCompaction`,
`TestScaleP01BrowserAcknowledgements`,
`TestScaleP01TimedOutWriteStillContributesItsCommit`,
`TestScaleP01PermissionMatrix`, `TestScaleP01DifferentialPreservesEmptyAndNull`,
`TestScaleP01ExternalUsers`, `TestScaleP01TwoProcessLoad`,
`TestScaleP01ReplicaProcess` and `TestScaleP01PrepareExternalFixture`.
`TestLoadExternalUsers` is the explicit alias required by the plan's command.
Expensive external/child/preparation tests skip unless enabled; the ordinary
prefix still runs real tests in both store and server, without zero matches.

## Verification commands and exits

All Go test/vet and load commands ran through `~/.flow-local/heavy.sh`, with
`GOMAXPROCS=4 GOMEMLIMIT=1536MiB`. Final verification is serialized inside
`/tmp/codex-scale-p01/verify-pipeline.sh` under that lock; it additionally sets
`TMPDIR=/tmp/codex-scale-p01`. The exact arguments and exit status for every final
stage are recorded in `/tmp/codex-scale-p01/commands.tsv`. Each stage's full output
is in the same directory as `<stage>.log`.

| Stage / command (from `api/`) | Exit |
| --- | ---: |
| Original `FLOW_LOAD_USERS=300 FLOW_LOAD_DURATION=2m ... go test -tags=loadtest ./cmd/server -run '^TestLoadUsers$' -count=1 -timeout=10m -v` | 0 |
| Original `FLOW_LOAD_USERS=800 FLOW_LOAD_LEGACY=1 FLOW_LOAD_DURATION=2m ... go test -tags=loadtest ./cmd/server -run '^TestLoadUsers$' -count=1 -timeout=10m -v` | 0 |
| Original `FLOW_LOAD_WRITES=50 ... go test -tags=loadtest ./cmd/server -run '^TestLoadWriteDecodes$' -count=1 -timeout=10m -v` | 0 |
| `final-p01`: `go test -p 1 -tags=loadtest ./internal/store ./cmd/server -run '^TestScaleP01' -count=1 -timeout=5m -v` | 0 |
| `final-vet`: `go vet ./...` | 0 |
| `final-vet-loadtest`: `go vet -tags=loadtest ./internal/store ./cmd/server` | 0 |
| `final-race`: `FLOW_LOAD_TWO_PROCESS=1 FLOW_LOAD_USERS=5 FLOW_LOAD_UNIQUE_USERS=5 FLOW_LOAD_METADATA_COUNT_SCALE=0.002 FLOW_LOAD_DURATION=5s FLOW_LOAD_CHANGE_EVERY=1s FLOW_LOAD_DELIVERY_TIMEOUT=2s FLOW_LOAD_REQUEST_TIMEOUT=10s ... go test -race -p 1 -tags=loadtest ./internal/store ./cmd/server -run '^TestScaleP01' -count=1 -timeout=10m -v` | 0 |
| `final-300-1`: 300 users/principals, scale 1, 2m, `go test -p 1 -tags=loadtest ./cmd/server -run '^TestLoadUsers$' -count=1 -timeout=15m -v` | 0 |
| `final-800-1`: 800 users/principals, scale 1, same command | 0 |
| `final-300-10`: 300 users/principals, scale 10 count mode, 2m, same test with `-timeout=20m` | 0 (incomplete metrics explicitly flagged) |
| `final-write`: 50 writes, `go test -p 1 -tags=loadtest ./cmd/server -run '^TestLoadWriteDecodes$' -count=1 -timeout=15m -v` | 0 |
| `final-legacy-300`: 300 users, legacy mode, 2m, `go test -p 1 -tags=loadtest ./cmd/server -run '^TestLoadUsers$' -count=1 -timeout=10m -v` | 0 |
| `final-2000-smoke`: 2000 users/principals, unique scopes, count 0.002, 10s, same test with `-timeout=5m` | 0 |
| `final-strict`: `FLOW_LOAD_STRICT_PERMISSIONS=1 go test -p 1 ./cmd/server -run '^TestScaleP01PermissionMatrix$' -count=1 -timeout=3m -v` | 1, expected pre-existing dependency/bootstrap-count defects |
| `docker-version`: `docker version --format '{{.Server.Version}}'` | 1, daemon unavailable |
| `final-prefix`: `go test -p 1 ./internal/store ./cmd/server -run '^TestScaleP01' -count=1 -timeout=5m -v` | 0 |
| `final-g0`: `go test -p 1 ./internal/store ./internal/coordination ./cmd/server -count=1 -timeout=20m` | 1, static SQL classifier false positive; server/coordination passed |
| `checked-p01`: tagged P01 prefix plus `TestEveryMetadataWriteStatementMovesTheMetadataRevision` | 0 |
| `checked-vet`: `go vet ./...` | 0 |
| `checked-vet-loadtest`: `go vet -tags=loadtest ./internal/store ./cmd/server` | 0 |
| `checked-race`: same two-process/P01 race command as above | 0 |
| `checked-g0`: same full G0 command as above | 0: store 81.886s, coordination 1.442s, server 316.770s |

Earlier development commands are retained in task logs rather than presented as
passing verification. `p01-tests-1/2/3`, `permission-4/5`, `p01-tests-6` and
`smoke-local` exited 1 while fixture expectations, imports and existing route
shape differences were being resolved; the first store prefix had zero matches
and was not counted as a pass. `permission-7`, `p01-tests-8`, `smoke-local-2`,
`smoke-local-final`, `external-smoke`, `p01-after-rebase`, the earlier write-after
run and all three pre-drain matrix runs exited 0. `p01-late-fix` and the first
pipeline attempt exited 1 while the meaningful linked-issue count fixture was
being added; the corrected final commands above supersede them. The separate
strict diagnostic exited 1 as expected. A queued G0 wrapper was stopped before
it acquired a lock or ran a test (137), then rescheduled at the pipeline's end.

`gofmt -w` was run on owned Go files after edits (final exit 0); an intermediate
unterminated string was corrected after exit 2. `git diff --check` exits 0.
Read-only `pwd`, `git status/log/diff/show/rev-parse`, `rg`, `sed`, `cat`, `jq`,
`ls`, `wc`, `ps`, `pgrep`, `date` and `memory_pressure` inspections were used.
Exploratory missing-file/glob/no-match probes returned 1 or 2 and were corrected;
none was treated as test evidence. No web, SDK, OpenAPI or browser command was
needed because their sources and production wire shapes did not change.

The first G0 failure was `TestEveryMetadataWriteStatementMovesTheMetadataRevision`:
its source scanner included build-tagged files and interpreted the probe's string
classifiers as incomplete executable writes. Commit `e76eeb56` uses SQL-token
comparisons instead, preserving instrumentation behavior without SQL-shaped
builder literals. The guard, all P01 tests, vet and race checks pass after that
correction. Corrected final command arguments/exits are additionally recorded in
`/tmp/codex-scale-p01/commands-final.tsv`. The raw baselines were collected on
`faf954e0`; the later classifier correction changes no business path or load mix.

## Delivery

Code commits: `578b6928`, `faf954e0`, `e76eeb56`. The verification report is a
separate documentation commit. Review ref: `refs/heads/review/scale-p01`.
The branch is rebased onto `origin/main`; the lead owns merging it. Production
data, deployed services, web/SDK sources and tracked `api/server` were not changed.
No browser screenshots were taken and no browser verification is claimed.

## Shared-database CI follow-up

The initial Infrastructure integration run `43099590098` failed PostgreSQL
fixture setup before MySQL could run. Its database already held unrelated users,
so the global test seed skipped creation of P01's assumed accounts. Following
P03 commit `dc963c61`, the untagged store and HTTP permission fixtures now register
and verify unique users, create an isolated workspace, and clean up only their
own workspaces and accounts. They open databases without the global test fixture.
The HTTP test stops its delivery scheduler before arranging test-owned data.

`TestScaleP01FixturePreservesExistingAccount` creates an unrelated account and
workspace, runs and cleans up a separate fixture, then verifies that the unrelated
credentials and workspace remain valid and that owned resources are gone. The
non-strict `BASELINE DEFECT` diagnostic is preserved unchanged. These changes
affect fixture setup and cleanup only; the saved load baselines above are retained.

Local reproduction runs the untagged P01 test set twice in separate Go processes
against `/tmp/codex-scale-p01/ci-followup/persistent.db`, using
`FLOW_TEST_DATABASE_DRIVER=sqlite FLOW_TEST_DATABASE_URL=<that path>` and
`go test -p 1 ./internal/store ./cmd/server -run '^TestScaleP01' -count=1 -timeout=5m -v`.
All heavy commands use `heavy.sh`, `GOMAXPROCS=4`, and `GOMEMLIMIT=1536MiB`.
Detailed command results and the new CI run are recorded in the follow-up logs.
The two persistent-file passes, tagged P01 tests, `go vet ./...`, and tagged
store/server vet all exited 0. Logs are under
`/tmp/codex-scale-p01/ci-followup/`; no saved capacity baseline was rerun or replaced.
