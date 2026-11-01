# CI runtime investigation (2026-11-01)

The 15-minute target was **not achieved**. The final verified workflow passed in
16m03s, versus the successful main baseline's 30m45s. No tests, race detection,
coverage scopes, thresholds, or required status checks were removed.

## Measured runs

| Run | Result | Workflow wall-clock |
| --- | --- | --- |
| [Main baseline, b589d2a9](https://github.com/leozhengliu-pixel/flow/actions/runs/41281983784) | Passed | 30:45 |
| [Dispatch 1: scheduling and parser fix](https://github.com/leozhengliu-pixel/flow/actions/runs/41297410577) | Passed | 15:06 |
| [Dispatch 2: cache and fixture changes](https://github.com/leozhengliu-pixel/flow/actions/runs/41299216935) | Failed only the inherited document-tab browser assertion | 24:33 |
| [Dispatch 3: final changes, e369e54e](https://github.com/leozhengliu-pixel/flow/actions/runs/41302073912) | Passed | 16:03 |

The table below measures elapsed time from workflow creation until each group
finished, including runner queues and dependencies. All three dispatches were
used; there was no fourth dispatch after the final rebase onto main.

| Job group | Baseline | Final |
| --- | --- | --- |
| API server shards | 26:05 | 13:44 |
| API store shards | 30:18 | 15:15 |
| API including coverage and required aggregate | 30:44 | 16:03 |
| Web coverage gates | 13:49 | 13:54 |
| Browser E2E including security headers | 10:15 | 14:37 |
| API vet and partition checks | 7:29 | 5:43 |
| Remaining lint/build/compatibility/integration jobs | 3:41 | 13:47 |

The short checks intentionally finish later: they wait for API static checks so
the initial runnable CI set is 12 API tests, four browser shards, three web
coverage gates, and one API static job. The account still enforces its 20-job cap.

## Waste Removed

- The six-job API matrix cap forced two waves even after other jobs finished.
  All twelve API jobs are now eligible together; short checks start later.
- `setup-go` restored an immutable dependency-key cache and never saved newly
  compiled source or race/coverage variants on a cache hit. Source-keyed build
  caches now retain those variants, with a previous-source fallback and one
  writer per package group. Representative server build/list preparation fell
  from 3m23s to 8.5s. `-count=1` explicitly prevents cached test results from
  replacing execution.
- Every isolated database fixture regenerated the same bcrypt hash. The test
  fixture now computes its default-password hash once per process at the same
  default cost. Custom fixture passwords, production hashing, login verification,
  isolated databases, migrations, and server setup remain unchanged. Startup
  events showed the first fixture's hashing stage at 580ms, absent on later opens.
- The nested-count parity test took 593.85s and 788.27s in the two measured API
  passes, against a stale 456.56s weight. Its two-sample median, 691.06s, gives it
  a near-dedicated store shard instead of adding another 71 tests. The static
  check still partitions all 402 store tests exactly once. The scheduled
  three-successful-main-run median refresh remains in place.
- Raw `go test -json` events now survive as artifacts, including failures, so
  package durations, subtests, startup stages, and future flakes are inspectable.

The large parity matrix and its race-instrumented SQLite queries remain intact.
Most inspected sleeps were readiness polling or deliberate protocol timing
checks, not the minutes-long bottleneck. No sleeps or timeouts were increased.

## API Flake

Main run [41280064893](https://github.com/leozhengliu-pixel/flow/actions/runs/41280064893)
for e64f75a6 failed `TestParseAgentFileWithinBoundsConcurrentParses` with
`slots leaked: 1`. The parser sent its buffered result before a deferred semaphore
release. The receiver could return and inspect the slot before that defer ran;
there was no permanent leak. Normal completion and panic recovery now release
capacity before publishing the result. Cancellation still leaves a running
parser holding its slot until parsing actually finishes. The regression test
asserts immediate release after 100 consecutive parses, without retrying failures.

The second dispatch also exposed main's new browser assumption that document
tabs must fit at precisely 390px. The test now keeps its 390px reachability check
and exercises the expanded state at ample phone width. All 19 tests in that
spec passed locally in bundled Chromium, and the final browser shards passed.
No product UI changed.

## Remaining Limit

Other main runs and sibling workflows still share the account's runners. In the
final run, store shard 1 waited 6m21s, then ran in 8m54s. Dispatch 2's workflow
coverage job waited 10m41s before its 13m48s execution. More shards cannot remove
that account-level queue.

The web coverage jobs still each execute the full suite in about 13m50s. Browser
shard 1 took 14m29s: 1m54s API build, 9m54s browser tests, 1m35s security tests,
plus setup. Its slower final timing is included above, not hidden as noise.
The final Go coverage gates passed at 74.8% (floor 60%) and 84.2% for MCP/OAuth
(floor 75%). All three web coverage gates were unchanged and passed.

A reliable sub-15-minute result needs runner capacity reserved for the run, or
more work to reduce the remaining long jobs, such as one Vitest coverage
collection with all three existing scope gates. Neither isolation nor that
coverage redesign was verified here. This change stops at the measured result
rather than weakening checks or claiming an unmeasured 15-minute result.

## Local Verification

After the final rebase, all commands below exited 0 through the machine-wide
heavy-command lock. Go used `GOMAXPROCS=2`; browser tests used bundled Chromium,
no retries, the scratch API binary/database under `/tmp/codex-gap-ci`, and a
temporary Playwright config removed afterwards. The changed browser spec passed
all 19 tests. No application component changed, so no component Vitest file was
selected. No screenshots were taken.

```sh
# api/
go vet ./...
go test -p 1 -race -covermode=atomic ./cmd/server -run '^(TestReadFileParsesDocuments|TestReadFileCapsOutputAndRefusesZipBombs|TestAgentChatReadsAttachedFilesThroughTools|TestParseAgentFileWithinBoundsConcurrentParses|TestWorkspaceSettingsPersistence)$' -count=1
go test -p 1 -race -covermode=atomic ./internal/store -run '^(TestAuthTestFixtureKeepsPasswordCostAndIsolation|TestAccountAuthenticationAndSessionLifecycle|TestOpenSQLiteStartsWithoutFixtureWorkspace|TestSchemaMigrationsAreVersionedAndIdempotent)$' -count=1
# web/
npm run typecheck
npm run lint
npx playwright test e2e/mobile-docs.spec.ts --config=playwright.ci-local.config.ts --project=mobile-chromium
```

The parser regression also passed 20 repetitions with 100 release assertions
per repetition. The race/coverage shard partition check and YAML initial-job
budget check exited 0. The earlier profiling passes were targeted tests only,
not local full-suite runs.
