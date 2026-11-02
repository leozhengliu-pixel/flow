# Desktop Font Metrics

## Font Identity

The loaded upright face in both apps and `web/InterVariable.woff2` in the
[official Inter 4.1 distribution](https://github.com/rsms/inter/releases/tag/v4.1)
have the same SHA-256:
`693b77d4f32ee9b8bfc995589b5fad5e99adf2832738661f5402f9978429a8e3`.
The embedded version is `Version 4.001;git-9221beed3`; its axes are `opsz`
(14-32, default 14) and `wght` (100-900, default 400). No font replacement,
subset conversion, or font copied from the reference is required.

The reference upright face URL ends in `/fonts/InterVariable.woff2?v=4.1`;
Flow loads `/fonts/InterVariable.woff2`. Both declare weights 100-900 and
`font-display: swap`. The reference excludes two emoji codepoints from its
Unicode range; neither occurs in the measured strings. Byte identity rules
out a different Inter version or glyph subset for this investigation.

Inter is distributed under the SIL Open Font License 1.1. The official
copyright and license accompany the existing font in
`web/public/fonts/LICENSE.txt`.

## Shaping Evidence

Measurements used real Chromium at 1440x900, scale factor 2, after
`document.fonts.ready`. Temporary spans copied the computed typography of
the actual workspace label and issue identifier; they had no padding,
border, fixed width, or transform and were removed after measurement.

| Setting | Reference workspace | Flow before | Reference identifier | Flow before |
| --- | --- | --- | --- | --- |
| Size / weight | 13px / 550 | same | 13px / 450 | same |
| Tracking | -0.1px | same | -0.26px | same |
| Features | `"calt"` | normal | `"calt", "cpsp", "tnum"` | normal |
| Numeric variant | normal | normal | normal | tabular-nums |
| Kerning / optical sizing | auto / auto | same | auto / auto | same |
| Variation settings | `"opsz" 28` | normal | normal | normal |
| Ligatures / stretch | normal / 100% | same | normal / 100% | same |
| Text rendering | auto | same | optimizelegibility | auto |

The workspace's explicit optical axis accounts for the entire width
difference. Automatic optical sizing at 13px uses the face's minimum axis
value, 14. No stylistic set or `cv*` feature is requested by either element.

The identifier already used tabular numbers in Flow. With that held
constant, `cpsp` adds 0.609375px to `FLO-53`, `FLO-11`, `FLO-99`, and `PIC-2`.
Toggling `calt` alone does not change these measurements. Numeric-only
`0123456789` also stays unchanged when toggling capital spacing.

## Column Limit

The reference `FLO-53` glyph run measures **48.9296875px**, while its parent
grid cell measures **50px**. The row's authored inline grid declaration
explicitly contains `[identifier] 50px`; its glyphs do not intrinsically
occupy 50px. The corresponding `PIC-2` run is 37.5234375px in a 38px column.

Capital spacing explains part of the title displacement, but it cannot
explain the remaining 1.0703125px between a natural 48.9296875px column and
the reference's authored 50px column. The rule used to select that column
width remains unknown. No fixed identifier width, rounding formula,
padding, or title offset is inferred from those samples.

## Reproduction

Read-only reference measurements and local browser evidence are under
`/tmp/codex-font-metrics`. The reference tab was created for this task and
closed afterwards. No issue shortcuts, app writes, or settings changes
were used in the reference.

The durable browser regression uses its own workspace through
`web/e2e/workspace-fixture.ts`. The desktop gate uses its existing repaired
fixtures and immutable reference captures. Phone checks use the existing
sweep with this worktree's dependencies, a free port, and a separate log.

## Results

The product change consists of two declarations, both restricted to desktop
widths of at least 1024px: `"opsz" 28` on the sidebar workspace name and
`"calt","cpsp","tnum"` on list identifiers. No global font metrics changed.

| Measurement | Before | After | Reference |
| --- | ---: | ---: | ---: |
| Workspace `flow-app` glyph width | 55.664063 | 52.531250 | 52.531250 |
| Workspace menu width | 109.664063 | 106.531250 | 106.531250 |
| Identifier `FLO-53` glyph width | 48.320313 | 48.929688 | 48.929688 |
| Identifier `PIC-2` glyph width | 36.914063 | 37.523438 | 37.523438 |
| Ordinary row title x | 390.820 | 391.430 | 392.500 |

Ten strings were measured in each element's typography: `flow-app`,
`FLO-53`, `FLO-11`, `FLO-99`, `PIC-2`, `Inbox`, `My issues`, `Settings`,
`0123456789`, and `AVATAR`. All 20 final Flow measurements match the
reference within 0.02px. Evidence: `reference-probe.json`,
`reference-features.json`, `flow-before.json`, and `flow-after.json` under
`/tmp/codex-font-metrics`.

The full 128-screen desktop gate changed from **904 to 840 differences**.
All 64 workspace-width failure entries disappeared and no new failure
entries appeared. Both runs retain 33 PASS, 75 FAIL, 14 INCONCLUSIVE, and
2 SKIPPED screens; both exit 1. This is a comparison of the same shared
base fixtures, not a claim that the unrelated desktop failures are fixed.

The repaired issue-list harness from the supplied report changed from
343 to 303 differences: all 40 workspace differences are resolved.
Its final states are 6 PASS, 13 FAIL, and 1 INCONCLUSIVE. The repaired
issue-detail harness remains 20 PASS / 6 FAIL, with 19 differences. Its
eight ordinary row title offsets improve from 1.680px to 1.070px early.
Both repaired gates exit 1. The repaired project gate also ran: 223
differences, 11 FAIL / 1 INCONCLUSIVE / 1 SKIPPED, exit 1.

Of the 93 entries originally grouped as identifier-column differences,
92 ordinary row titles improve from 1.680px to 1.070px early and remain
strict failures. The other entry is a `my-created` parent-trail match for
`REL test A blocker`: reference x=533.840px, before x=540.156px, after
x=540.766px. That entry was already late and is not explained by the
1.680px identifier-column hypothesis. No offset was added to conceal it.

## Verification

All heavy commands ran through `~/.flow-local/heavy.sh`.

- `npm ci` in this worktree: exit 0.
- `GOMAXPROCS=2 go build -p 1 -o /tmp/codex-font-metrics/flow-api ./cmd/server`
  from `api/`: exit 0. No Go source or API shape changed.
- `npm run typecheck` and full `npm run lint` from `web/`: exit 0 each.
- `npx vitest run --maxWorkers=2` on the seven component files below:
  7 files, 62 tests, exit 0.
- Bundled Chromium, both modified specs (`desktop-sidebar.spec.ts` and
  `desktop-team-issues-topbar.spec.ts`): 4 tests, exit 0. Temporary config:
  `/tmp/codex-font-metrics/playwright.config.mjs`. Each test creates its own
  workspace using the repository fixture.
- Scratch API plus Vite browser review and all 20 width assertions:
  exit 0. Workspace menu opens and closes; the 390x844 list has no
  document overflow and identifiers retain normal feature settings.

The targeted component files are `layout/sidebar.test.tsx`,
`layout/sidebar-item-menu.test.tsx`, `layout/sidebar-team-menu.test.tsx`,
`layout/sidebar-pulse.test.tsx`, `my-issues/my-issues-list.test.tsx`,
`my-issues/my-issues-estimates.test.tsx`, and
`my-issues/issue-row-actions.test.tsx`, all under `web/src/components`.

The full phone sweep used `FLOW_SWEEP_WEB` for this worktree,
`FLOW_SWEEP_BASE=http://127.0.0.1:5579`, and
`FLOW_SWEEP_LOG=/tmp/codex-font-metrics/phone-sweep.log` with
`bash /tmp/lead-verify/sweep.sh`. Exit 0; final line:

```text
screens run 83 of 83, passing 83
```

## Visual Review

Every final screenshot was opened and inspected. All are under
`/tmp/codex-font-metrics/shots`:

- `desktop-list.png`: readable workspace name, identifiers, and issue
  titles in the desktop list, with no overlapping text.
- `desktop-workspace-menu.png`: settled, opaque workspace menu below the
  corrected label. The initial capture caught the opening fade and was
  rejected; the final capture waits for animations to finish.
- `phone-list.png`: readable phone rows and toolbar with no horizontal
  overflow. Phone typography is unchanged.

For human review on the existing repaired fixtures:

1. Open `/desktop3-issues-list-all-issues/team/PIC/all` at desktop width.
   Inspect the top-left `flow-app` label and click it to open Workspace Menu.
2. Open
   `/desktop3-issue-detail-r2-rows-1793592208201/team/FLO/active`.
   Inspect the `FLO-53` identifier and its title; right-click its title to
   compare the row-menu screen. The remaining 1.070px column difference
   is deliberately visible.
