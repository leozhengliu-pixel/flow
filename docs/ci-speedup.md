# CI Speedup

The baseline uses the ten newest completed, non-cancelled `push` runs of
`ci.yml` on `main`, measured on November 27, 2026. Run `46050994013` supplied
the shard timing artifacts. GitHub's job payload does not expose a queued
timestamp, so queue delay is the interval from the first observed job start to
each later job start.

| Measure | Median before |
| --- | ---: |
| First job start to last job end | 16,175 s (269.6 min) |
| First job start to last job start | 15,992 s (266.5 min) |
| Accumulated job wall time | 183,359 s (3,056.0 runner min) |
| Jobs | 140 |

The measured run had these representative ranges.

| Job family | Jobs | Wall time | Queue offset |
| --- | ---: | ---: | ---: |
| API server shard | 44 | 17.2–39.6 min | 25.0–99.4 min |
| API store shard | 22 | 9.3–36.7 min | 101.4–131.3 min |
| PostgreSQL Scale server shard | 18 | 8.1–20.5 min | 0.1–24.4 min |
| MySQL Scale server shard | 18 | 8.0–30.5 min | 0.1–39.9 min |
| PostgreSQL Scale store shard | 6 | 6.0–12.9 min | 0.2–28.2 min |
| MySQL Scale store shard | 6 | 6.1–14.4 min | 0.2–34.2 min |
| Browser E2E shard | 4 | 29.7–63.3 min | 0–0.02 min |
| Web coverage job | 3 | 11.8–17.6 min | 0 min |

Queue offset is each job's start time minus the first observed job start in
run `46050994013`. GitHub's job payload does not expose a runner-queue
timestamp, so this includes workflow scheduling and runner availability.

The slowest completed test observations were these.

| Package | Test | Seconds |
| --- | --- | ---: |
| `./cmd/server` | `TestThousandIssueTriageConflictingWrite` | 1,099.79 |
| `./cmd/server` | `TestIssueWriteDifferential` | 909.54 |
| `./internal/store` | `TestProjectDisplayLegacyDifferential` | 909.50 |
| `./cmd/server` | `TestTriageSuggestionListReorderBulkDifferential` | 839.98 |
| `./cmd/server` | `TestTriageSuggestionListReorderBulkReloadedDifferential` | 829.09 |
| `./internal/store` | `TestAggregatePublicationDifferential` | 827.94 |

The refreshed `scripts/ci/go-test-timings.json` uses the median of the newest
three timing artifacts. The active SQLite plans now pack to 67,710 seconds for
`./cmd/server` and 28,547 seconds for `./internal/store`. The selected plans
use 28 server shards with a predicted maximum of 2,419 seconds and 14 store
shards with a predicted maximum of 2,039 seconds. PostgreSQL and MySQL use 12
server shards at 1,621 seconds maximum and 4 store shards at 1,283 seconds
maximum.

Every SQLite API shard keeps the complete active test list, including all Scale
differentials. On main, each shard runs its differential subset from a
race-enabled binary without atomic coverage instrumentation. The same shard's
ordinary tests run from a race-enabled, atomic-coverage binary. The coverage
profiles therefore come from the ordinary API tests and the other API packages,
while the differential paths retain their SQLite race signal. Pull requests
run the complete SQLite shard in plain coverage mode. The ordinary API tests
cover the package statements used by the differential paths, so removing atomic
instrumentation from the SQLite differential invocation does not remove a
coverage source. PostgreSQL and MySQL run the complete dialect selection on
every covered change. The external matrices use one plain precompiled test
binary per package for both databases.

The API test binaries are compiled once per package and mode with `go test -c`.
Every shard downloads the matching binary and selects its assigned tests with
`-test.run`. The PostgreSQL and MySQL matrices reuse one plain binary per
package. `go-test-binary.sh` preserves verbose output and emits timing events
for the existing timing refresh and coverage merge steps.

Each push selects one SQLite API mode. Main and nightly runs use race plus
atomic coverage for ordinary API tests, while pull requests use plain atomic
coverage. This avoids running a plain matrix and a race matrix for the same
push. PostgreSQL and MySQL remain separate because their differential behavior
is database-specific. The metadata upgrade assertions moved from one shard per
backend to one infrastructure invocation per backend, since they do not depend
on shard partitioning. SQLite differentials remain in the SQLite test list and
run under race without atomic instrumentation on main. Ordinary tests in the
same packages and the remaining API packages supply the merged coverage.

`scripts/ci/ci-scope.sh` applies these pull-request rules to `ci.yml`.

| Changed paths | Jobs selected |
| --- | --- |
| `api/**` including `api/go.mod` and `api/go.sum` | API, SQLite race/plain shards as applicable, PostgreSQL, MySQL, browser, and infrastructure |
| `web/**`, `scripts/generate-openapi.mjs`, `docs/openapi.json` | Web, SDK, and browser |
| `sdk/typescript/**` | SDK |
| `deploy/**`, `sandbox-runner/**` | Helm and sandbox |
| `.github/**`, `scripts/ci/**`, coverage scripts, Dockerfiles, or unknown root paths | Complete graph |
| `docs/**`, Markdown-only paths | No application jobs |

Pushes to `main`, scheduled runs, manual runs, workflow changes, and unknown
root changes use the complete graph. The nightly schedule is an additional
full push-style run. It exercises the race API graph, all three databases,
browser checks, security checks, Helm tests, and packaging checks regardless of
changed paths.

The existing `API` and `Browser E2E` aggregate checks remain required. Coverage
profiles still merge through `api-coverage`. The tracked known-failing list is
still checked for stale entries and only passing entries may be removed. The
PostgreSQL and MySQL metadata-upgrade checks run once per backend in
`infrastructure`, which keeps the original per-backend coverage without
running those two checks again in every Scale shard.
The separate Container and Security workflows remain enabled with their
existing build, gitleaks, CodeQL, dependency, and image checks.

Node modules, Go modules and Go build caches use keyed caches. The browser build
job builds the web bundle and both browser API binaries once, then the E2E,
performance, and security jobs download that artifact. Playwright browser
downloads use a package-lock keyed cache.

The projection below uses the measured shard maxima, the reduced matrix, the
shared binary and browser artifacts, and the observed queue contribution from
the ten-run baseline. It needs one post-merge main run for confirmation.

| Measure | Before median | Projected after |
| --- | ---: | ---: |
| Push jobs | 140 | 106 |
| SQLite API shards | 66 | 42 |
| Scale shards across PostgreSQL and MySQL | 48 | 32 |
| First job start to last job end | 269.6 min | about 145 min |
| Accumulated job wall time | 3,056 runner min | about 1,800 runner min |
