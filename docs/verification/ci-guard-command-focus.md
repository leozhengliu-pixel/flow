# CI Command Shortcut Focus

Run `41958540300` recorded a flaky failure in `desktop-shell-commands.spec.ts`:
Meta+K did not open the palette after hovering Search. Three unchanged local
confirmation runs passed, so those passes alone did not establish the cause.

The Agent page is lazy-loaded and intentionally focuses its empty composer on
mount. The test waited only for the sidebar, then clicked the header to clear
focus. A later composer mount could reclaim focus before Meta+K; the global
handler correctly skips editable targets. The test now waits for that intended
initial composer focus before its existing header click. Every palette, query,
shortcut-help and composer assertion remains intact, with unchanged timeouts
and zero retries. No product focus or shortcut behavior changes.

A temporary browser route held the actual Agent module, then released it after
the header click. The composer took focus, and the original Meta+K sequence
reproduced CI's exact missing-palette assertion (exit 1). Its screenshot was
opened and shows the caret in the composer with the Search tooltip visible.
With the corrected ordering, the same controlled case passed three times (exit
0), recording BODY as the shortcut target. The temporary spec is removed.

The first probe released the module after the tooltip was already open. The
app's overlay guard skipped autofocus, so that probe failed its focus setup
instead of reproducing CI. Its screenshot was also opened; it shows the loaded
Agent page and Search tooltip. Neither diagnostic screenshot is a visual pass:

- `results-41958540300-command-baseline/ci-guard-command.tmp-comma-327d6-after-a-delayed-agent-mount-chromium/test-failed-1.png`
- `results-41958540300-command-baseline2/ci-guard-command.tmp-comma-327d6-after-a-delayed-agent-mount-chromium/test-failed-1.png`

Paths are under `/tmp/codex-ci-guard/`. All browser runs use bundled Chromium,
an auth-disabled scratch API and private ports 24773/24780/24790 through the
machine-wide heavy lock. No production database, real model or user's browser
is used. Final checks all run through `~/.flow-local/heavy.sh` from `web/` and
exit 0:

| Command | Result |
| --- | --- |
| `npm run typecheck` | Explicit typecheck |
| `npm run lint` | Complete lint and every repository gate |
| `npx vitest run --maxWorkers=2 src/components/agent/agent-page-empty-state.test.tsx src/components/command/command-menu.test.tsx` | Two files, 42 tests |
| `npx playwright test --config=playwright.ci-guard.tmp.config.ts --project=chromium --retries=0 --repeat-each=3 --reporter=list e2e/desktop-shell-commands.spec.ts` | Complete spec three times, zero retries |

Logs end in `-41958540300-command.log`, except browser logs
`e2e-41958540300-command-{baseline,baseline2,fixed,final}.log`. The successful
final run produced no automatic screenshots. Harnesses stopped normally;
temporary configs and worktree dependencies are removed after delivery.
Product tooltip fixes remain published for lead review on `review/ci-guard`;
this test-only follow-up does not claim green main.
