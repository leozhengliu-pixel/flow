# Scale P08 Delivery

## 1. Files

Owned implementation files: `web/src/hooks/use-workspace-realtime.ts`, `web/src/lib/shared-realtime.ts`, `web/src/lib/realtime-event-queue.ts`, `web/src/lib/realtime-cache.ts`, `web/src/lib/navigation-cache.ts`, `web/src/lib/issue-detail-cache.ts`.

New package files: `web/src/lib/scale-p08.ts`, four `web/src/lib/scale-p08-*.test.ts` files, `web/src/hooks/scale-p08-workspace-realtime.test.ts`, `web/e2e/scale-p08-realtime.spec.ts`, `web/scale-p08.playwright.config.ts`, `api/cmd/server/scale_p08_realtime_test.go`, and this report.

Files outside my scope: `CHANGELOG.md` for the required Unreleased entry, and one assignment in `api/cmd/server/issue_request_access.go`. The auth-disabled branch previously lost the request's viewer when loading policy metadata, suppressing recipient notifications and actor-owned Agent pushes before they reached the browser. The prerequisite restores `cached.metadata.Viewer = authUser(r)` and is kept in a separate commit for review. No authenticated authorization, database schema, REST/SDK shape, or application component changes.

## 2. Flags

`VITE_APPLIED_REALTIME_CURSOR=true` enables acknowledged delivery. `VITE_BOUNDED_BROWSER_CACHE=true` enables the small navigation DTO and issue-record byte budget. Both default off. These are Vite build flags; rebuild without them to restore the existing client paths. The separate auth-disabled viewer correction remains independent of these web flags. The complete M differential across principals, database dialects and mixed API releases has not been proved, so neither flag qualifies for a default-on rollout.

Applied channels and checkpoints use viewer, workspace and local schema 2. Legacy tabs retain their original channel and storage namespace. Reloads do not resume from a disk-only checkpoint: a stream or a joining follower establishes a recovery barrier, then obtains a fresh server baseline. This intentionally costs an additional baseline read until the manifest adapter supplies an authoritative initialization boundary.

## 3. Contracts

```ts
type RealtimeAcknowledge = (() => void) & { pending?: () => boolean }
onMessage(message: RealtimeMessage, acknowledge: RealtimeAcknowledge): void
onAppliedCursor?(cursor: string): void

loadRealtimeCursor(workspaceKey: string, viewerId?: string, schema?: number): string | undefined
saveRealtimeCursor(workspaceKey: string, cursor: string, viewerId?: string, schema?: number): Promise<void>
```

The hook calls the completion barrier only after `await onRemoteSync(event)` succeeds. Ignored events complete their own slot; they cannot move past older pending work. Coalesced replacements retain all completions. Overflow and failed applies retain them through a successful resync. Excess unfinished transport deliveries invalidate old callbacks and require a new snapshot barrier. Disposal invalidates callbacks from the previous account or workspace.

Durable string sequences remain strings, including values beyond JavaScript's safe integer range. Safe legacy numeric sequences and opaque old stream IDs remain supported. Presence, connection markers, streaming task/coding/Loop signals and ephemeral notification creation do not become durable checkpoints. A follower captures its own baseline and never adopts another tab's saved frontier. Joining followers recover before acknowledging live traffic that might precede hello replay.

No new Go or HTTP contract is introduced. The existing SSE URL and payloads are retained; manifest and sync-v2 negotiation belong to later packages.

## 4. Tests

Thirty-one new unit cases cover five-second apply delays, self echoes before a write response, invalid JSON/envelopes/payloads, failed apply and recovery, coalescing/overflow completion barriers, stale replacements, account/workspace/schema isolation, exact string sequences, leader exit, late-join live/replay ordering, ignored side channels, queued permission/unshare/member revocation, cache budgets, open-editor retention, summary/body preservation, and retaining canonical counts/content flags when actor lists or bodies are omitted. The opt-in measurement test is an additional case.

The 27-file Wsync/Wcache run, extended with affected startup/entity/Agent/Loop cases, passed 278 tests after rebasing. The authenticated Ert case passed against the enabled production build. Initial G0 passed all three requested packages. A later G0 run reached an existing timestamp-suffix panic in `TestResourceProjectionHandoffKeepsPrivateTeamsPrivate` (`request_metadata_test.go:50` slices a suffix that can have fewer than three digits); the fixture is unchanged. That case then passed three targeted reruns, and the complete G0 rerun passed: store 83.423s, coordination 0.710s, server 311.410s. These are test durations, not API latency measurements.

`TestScaleP08AuthDisabledRealtimeRetainsViewer` first failed on the existing API with an empty viewer instead of `usr_admin`. It checks the access metadata and recipient notification/Agent visibility. It uses the existing fixture helper that also runs against `FLOW_TEST_DATABASE_DRIVER` / `FLOW_TEST_DATABASE_URL` when supplied.

The new Chromium spec creates its own workspace with `workspace-fixture`, uses a scratch API and Vite on 42832/42831 plus a scripted provider on 42833, and checks both tabs with one shared EventSource. It covers issue/project/document/inbox/Agent changes and follower promotion at 1440x900 light and 390x844 dark. It verifies issue/document bodies, the inbox unread title and both Agent replies remain present. The final run passed all four cases in 38.7s.

## 5. Commands And Exits

Build/test commands ran through `~/.flow-local/heavy.sh`. Validation used Node 24.19 because the installed dependencies require it; installation used the requested Node 24.7 path and completed with engine warnings. Exact expanded test arguments and exits are in `/tmp/codex-scale-p08/commands.tsv`; orchestration scripts and individual logs are beside it.

| Command | Exit / result |
| --- | --- |
| `npm ci` | 0 |
| `SCALE_P08_MEASURE=baseline npx vitest run --maxWorkers=1 src/lib/scale-p08-measure.test.ts` | 0, measured before implementation |
| Initial four new unit files | 1, navigation fixture put its large value in an omitted project field; corrected |
| Initial `npm run typecheck` | 2, test constructor/mock types and DTO assertions; corrected |
| Initial `npm run lint` | 1, measurement helper `this` alias; corrected |
| Wsync/Wcache plus related files, 27 files | 0, initially 273 and finally 278 cases |
| Ten cursor regression files after the join barrier | 0, 99 cases at that revision |
| `SCALE_P08_MEASURE=after npx vitest run --maxWorkers=1 src/lib/scale-p08-measure.test.ts` | 0, repeated with identical values |
| `npm run typecheck` | 0 |
| Full `npm run lint` | 0, including every repository gate |
| `go build -p 1 -o /tmp/codex-scale-p08/flow-api ./cmd/server` | 0 |
| Enabled `npm run build -- --outDir /tmp/codex-scale-p08/dist-final --emptyOutDir` | 0 |
| `npm run test:e2e:realtime -- --reporter=list` with the scratch binary and enabled build | 0 on every run, one authenticated case each |
| Initial new browser run | 1, inbox event/readiness failure; both leader-exit cases passed |
| Second new browser run | 1, recipient event still suppressed; both leader-exit cases passed |
| Desktop diagnostic browser run | 1, inbox passed; append-message fixture expected 201 instead of its existing 200 |
| Pre-prerequisite four-case browser run | 1, missing recipient/Agent events; identified the empty API viewer |
| Delivery lint | 0 |
| `go test -p 1 ./cmd/server -run '^TestScaleP08' -count=1 -v` before the prerequisite fix | 1, reproduced the empty API viewer |
| Prerequisite targeted Go checks | 0; old request-cache and live revocation cases plus `TestScaleP08*` |
| `go vet -p 1 ./...` | 0 |
| Rebuilt scratch API and prerequisite browser run | 0, 4/4 Chromium cases |
| First G0 after the prerequisite | 1, existing short timestamp-suffix fixture panic; store and coordination passed |
| Delivery browser, including unread title | 0, 4/4 cases |
| `go test -p 1 ./cmd/server -run '^TestResourceProjectionHandoffKeepsPrivateTeamsPrivate$' -count=3 -v` | 0, all three runs executed |
| Complete G0 rerun after the fixture failure | 0, all three packages passed |
| `GOMAXPROCS=2 GOMEMLIMIT=1536MiB go test -p 1 ./internal/store ./internal/coordination ./cmd/server -count=1 -timeout=20m` | 0 |
| `docker info --format '{{.ServerVersion}}'` | 1, daemon unavailable; no containers created |
| `git fetch origin main`, `git pull --rebase --autostash origin main`, `git diff --check` | 0; upstream already current |
| Dependency cleanup | Shell `rm -rf` was rejected before execution; removal with a verified worktree path through Node filesystem APIs completed with 0 |

No `-run` pattern matched zero tests. The Go prerequisite adds no concurrency. It receives focused tests, gofmt, full API vet and another G0 run. No server load or external-database capacity result is claimed.

## 6. Measurements

Starting commit: `c2798acd4dd44a3a601f7652feb5ccdef76aef23`. The same fake-timer harness and data were used before implementation and after it: a 5-second apply, 20 workspaces containing a 200 KiB project body each, and 100 issues containing 512 KiB bodies.

| Measurement | Before | After |
| --- | ---: | ---: |
| Checkpoints saved at 400ms, before apply finishes | 1 | 0 |
| Apply completion | 5,080ms | 5,080ms |
| Checkpoints saved after apply | 1 | 1 |
| Total navigation JSON, UTF-8 bytes | 4,162,530 | 53,130 |
| Retained full issue-record JSON, UTF-8 bytes | 52,509,590 | 16,277,966 |
| Retained full issue records | 100 | 31 |

Raw results: `/tmp/codex-scale-p08/baseline.json` and `after.json`. These are serialized cache measurements, not retained-heap, CPU, SQL or end-to-end latency measurements. Budget accounting conservatively charges the larger of UTF-16 and UTF-8 size plus an entry allowance.

## 7. M And K

P08-specific coverage proves viewer/workspace/schema separation, cancellation after account switches, permission/unshare/member revocation while apply is queued, and retaining the old protocol and disabled-flag behavior. Existing API permission and realtime-policy tests ran in G0. The authenticated browser case covers a second member, resource updates and a team-access change; the scratch five-surface run uses auth disabled as requested.

The new web client was exercised against the existing SSE/bootstrap server contract. No server or SDK wire shape changed. Summary merges explicitly preserve supplied count/content fields rather than deriving replacement values from omitted actor lists or bodies. Full new-path field differentials for admin outside a private team, guest grants, sub-team inheritance, all API-key scopes, old slugs, hidden actors/counts/references, mixed old/new replicas and all three database dialects are not claimed. This is why the flags remain off.

## 8. Limits And Screenshots

The 2 MiB navigation limit covers stored DTOs across workspaces and the memory fallback. Cached navigation is explicitly incomplete, contains no project/document bodies or Agent conversations, and cannot replace a fresh bootstrap. The 32 MiB issue-record budget evicts older bodies to marked summaries and drops evicted issue history when merging a directory. Open editor bodies are reserved first; an exceptionally large active record stays editable outside the background-cache budget. This does not bound a legacy bootstrap, current document/project state, or all foreground history. Later window/detail adapters remain necessary for total browser memory bounds.

Docker was unavailable, so this session did not run MySQL/Postgres containers or a mixed-release replica matrix. No visual controls, CSS or UI copy changed; the browser checks exercise existing controls and surfaces, rather than a new control-state design audit.

Screenshots are saved under `/tmp/codex-scale-p08/shots`. Every one of the 12 final captures was opened and inspected: content is present, texts fit, and the phone project drawer is closed to expose the overview. Desktop captures are 1440x900/light; phone captures are 390x844/dark. Failed inbox/Agent captures from earlier runs were also inspected and are retained only as debugging evidence under `browser-results*`, never as passing verification.

| Capture | What it shows | Reviewer route and action |
| --- | --- | --- |
| `desktop-issue.png` | Updated issue title and intact editable body | `/{workspace}/issue/BRO-1`; rename in the other tab |
| `phone-issue.png` | The same update and body in the phone issue layout | Same route at 390px; rename in the other tab |
| `desktop-project.png` | Updated project name in overview and breadcrumb | `/{workspace}/project/{slugId}/overview`; edit the name in the other tab |
| `phone-project.png` | Updated name in the phone overview, details drawer closed | Same route at 390px; click Close project details, then rename elsewhere |
| `desktop-document.png` | Updated document title with original body retained | `/{workspace}/document/{slugId}`; change the title in the other tab |
| `phone-document.png` | Updated title and retained document body on phone | Same route at 390px; change the title in the other tab |
| `desktop-inbox.png` | A new recipient notification row for the updated issue | `/{workspace}/inbox`; create a reminder elsewhere and unsnooze it |
| `phone-inbox.png` | The recipient notification in the phone inbox | Same route at 390px; create/unsnooze from the other tab |
| `desktop-agent.png` | First reply retained, second user message and second completed reply | `/{workspace}/agent/{slugId}`; send the second message from the other tab |
| `phone-agent.png` | Both completed replies and second message fit in the phone conversation | Same route at 390px; send the second message in the other tab |
| `desktop-leader-crash.png` | A document update after the original leader tab exits, body intact | Open the document in two tabs, close the first, then update from another window |
| `phone-leader-crash.png` | The surviving phone tab receives the post-exit title update | Same document route at 390px; close the first tab and update elsewhere |

The spec creates the workspace and resource slugs rather than relying on shared local data. Its source contains the exact API triggers. The final delivery captures use the same names and routes. No scratch server is left running after verification; the API/Vite/provider ports have no listeners. Installed web dependencies have been removed.

## 9. Review

Delivery branch: `review/scale-p08`. The prerequisite is commit `bfffeba1183b6476136b8bd25c9f1db5a135de96`; the web implementation is `a660757ad643a2efebceca99bf661244ca962280` before the final report commit. Final command outcomes and cleanup are recorded in the delivery message and local artifacts. Push only the review branch.
