# Phone Fitting Follow-up

Audited `cbe2de3b` against `64db1c70`, including later replacements of its
selectors. Raw evidence is `/tmp/mobile-ref/<area>/<screen>-390.json`.
A measured rectangle is not a declaration that variable text must have a
fixed width. No expectations were edited.

## Rule Decisions

Paths below are relative to `web/src/components/`.

| Rule | What It Did | Decision And Evidence |
| --- | --- | --- |
| Agent title `275.69px`, actions at `323.69/355.69px`, hidden toolbar action | Fitted one title; hid Move to toolbar | Already removed/replaced before this task. Preserve the natural header and the working Move command in Chat options. Raw conversation title is `Create Invite Test team and add people`, width 242.69; Flow's prefixed title differs. |
| `documents/document-page.css`: metadata `width/min/max-width:360.19px` | Pinned owner/date metadata to one fixture | Removed. Raw panel rect `[0,41.67,360.19,182.5]` describes that content, not other names. Intrinsic width, minimum zero, viewport maximum; long owner browser tests at both phone widths. |
| `my-issues/my-issues.module.css`: overflow `74.96px` | Reserved the English `2 more` width for every count/locale | Removed. Raw label is 39.96px, padding 10px per side, gap 6px, icon 8px. Existing measurement rows now measure the actual rendered count. |
| `.actions>:nth-child(3)` | Hid My Issues Insights | Already replaced with named selectors and a Display options command. Removed the remaining redundant CSS hiding rules; Insights remains under Display options. |
| `team-overview/team-overview-page.tsx`: decrement visible count when active is last visible tab | Forced a fitting active tab into the overflow menu | Removed. Keep every measured tab that fits. The off-screen count and active-label measurements remain. Documents/Members navigation works with direct tabs or real overflow. |
| Project milestones `101.22px`, `94.66px` | Pinned issue count/progress and Add milestone to sampled text | Removed. Raw `0 issues   0 %` measures 101.22px; raw Add milestone button measures 94.66px. Neither is an invariant for other counts or translations. |
| Project milestone description `height:50px` | Clipped longer descriptions | Removed. Use the existing 50px minimum and a one-line editor minimum (`1lh`, 24px), rather than inheriting the generic editor's 60px minimum. Raw one-line layout is 24px text plus 10px/16px padding. Long description tests grow past 50px. |
| Project toolbar Insights `display:none` | Hid the standalone Insights trigger | Removed redundant CSS. The current component already routes phone users through Issues, Display options, Insights. |
| `.project-activity__agent {display:none}` | Hid Write with Agent for project updates | Removed. On phones the same handler/button follows the activity feed; desktop keeps its composer button. Verified opening the panel and receiving a scripted streamed answer at 375/390px; unit test applies a draft back into the composer. |
| `settings/mobile-notifications.css`: `nth-child(n)` height 79.5px | Forced two inbox rows to a sampled height | Removed; content determines height above the existing row minimum. Raw rows have 16px vertical padding and 8px copy/control gap. Stress tests lengthen each description. |
| `settings/mobile-profile.css`: heights 89.5/83.5/91.5/109px, second-row radius | Pinned five profile rows by position | Removed. Existing stacked copy/control flow remains. Long-copy tests verify rows grow; name/title editing and workspace access still work. |
| `settings/mobile-workspace.css`: positional heights 107/91.5/94/103/68px; label height 15.5px; nowrap fiscal title; description 187px/43.5px/29px | Pinned workspace copy, wrapping and rows to one workspace | Removed. Copy flexes in horizontal rows and is nonflexing only in semantically stacked field rows. Titles wrap and rows grow. |
| Workspace select width 104.5px | Forced the sampled month width on every select | Removed. Raw select is `[252.84,486,104.5,36]` with padding `6px 24px 6px 12px`; retain those control paddings/height, not sampled text width. |
| Workspace URL wrapper `width:100%`; URL `flex:0 1 256px` | Let the Edit URL action use the rest of the row | Kept. Raw wrapper is 324.67px, input 256px, Edit glyph at x333.34. This is a bounded text input, not a text label; its input scrolls and shrinks when necessary. Edit URL is keyboard-focusable and tested at both widths. |
| Workspace third-section description hidden; first two titles opacity .5 | Hid the Welcome message explanation and made working Welcome/Announcements controls look disabled | Removed both CSS rules. The explanation already exists on the Welcome message settings page; phones show it there rather than duplicating it in the navigation row. Browser test opens that page and reads the explanation. Announcements has no description span and still opens its configuration dialog. |
| `settings/settings-parity.css`: preferences first row hidden | Hid Language | Already moved after all reference preference content. Preserve the visible Language row there; browser tests change to zh-CN and back. No replacement hiding rule. |
| Preferences nth-child row/copy heights, <=380px exceptions, select widths 171/100.5/109.5/91/83px | Encoded English row ordering and wrapping | Removed the entire positional block, including later renumbering after Language moved. Intrinsic native selects, normal line boxes and row minimums remain. Long-copy and zh-CN tests pass without pinning heights. |
| `settings/mobile-teams.css`: `.settings-action:not(.primary){display:none}` | Hid Teams Export CSV | Removed. Render the duplicate desktop toolbar export only outside phones; preserve the existing same-permission Export CSV after the table. Verified real `teams.csv` downloads at 375/390px. |
| Members toolbar fixed height/row 36px, filter 132px, Invite 58.84px and left margin 18px | Fitted three controls to one locale | Removed the fixed sizes and margin. Search/filter are now one shrinking group with 6px gap; Invite is a separate intrinsic group. Raw toolbar declares gap 24px between those groups; Invite itself has margin zero. Desktop markup is unchanged. |
| Target commit's E2E coordinates, metadata width and 46px option assertions | Made tests depend on the fitting | Replace with containment, real menu navigation, scroll reachability and variable-content checks. Keep `button.team-home-tabs-overflow-pill`: it correctly excludes inert measurement spans, not features. Historical changelog text remains historical, not a layout contract. |

## Remaining Differences

- `feature`: none from the moved controls. Language and exports follow reference
  content; Agent writing follows the activity feed. Welcome-message explanation
  remains on its existing settings page.
- `data`: fixed-coordinate icon crops can hit text instead of icons when names
  differ. Raw project header is `Compare Test` (108.89px); Flow is
  `[mobile] Compare Test`. Raw Agent title is 242.69px; the two Flow fixtures
  use different prefixed titles. Team/project/initiative glyph colours, logo
  initials, status/assignee data, resources and active display options also
  differ. Full crop diagnostics are retained below, rather than fitting them.
- `real`: Preferences retains three gate differences: native Display names
  select x differs by approximately 1px at both widths; the 375px Interface
  card is 371.5px rather than 386px. The raw 390px select is 100.5px and copy
  line height 14.5px; the removed block explicitly manufactured the extra
  14.5px on narrower phones. No row-index correction or sampled select width
  was reintroduced. The text gate passes.
- `real`: Documents tabs-menu gate attempts an active overflow trigger that
  no longer exists when all three tabs fit. Both destinations are actual
  visible links; their E2E navigation passes. There is no equivalent menu
  element to remap honestly. The corresponding 390px raw file is absent.
- `real`: Project overview text gate chooses the raw outer Add customer request
  button (13.3333px/400/white) rather than its innermost span
  (13px/500/lightness 36.975). Flow's span already matches the latter. Do not
  change the product text to satisfy this container-text comparison.
- `real`: Existing pixel differences in issue filter separator, off-property
  chip and details/status crops are not claimed fixed by this removal task.
  Other colour diagnostics must not be mistaken for clean icon-gate passes.

No glyphs were changed by these removals. The existing glyphs implicated by
the title-dependent crop differences retain their reference paths:

| Icon | Reference Path Prefix | Flow Path Prefix | Result |
| --- | --- | --- | --- |
| Favorite | `M10.5193 4.98997` | `M10.5193 4.98997` | Same; title length moves the crop |
| Options | `M3 6.5a1.5` | `M3 6.5a1.5` | Same; title length moves the crop |
| Add | `M8.75 4C8.75` | `M8.75 4C8.75` | Same; adjacent resource data differs |

Reference hairlines and separators were retained; no border was replaced
with an inset shadow to manufacture a coordinate.

## Verification

Implementation commits after rebasing onto `71bcc7a6`:
`15a540f7` (metadata/tabs/tests), `5109263b` (milestones/Agent),
`a2727701` (settings/content flow).

Final checks, all through `~/.flow-local/heavy.sh`:

- `npm run typecheck`: exit 0.
- Full `npm run lint`: exit 0, including all design/control/motion checks.
- `npx vitest run --maxWorkers=2` with the 17 focused component files in
  `/tmp/codex-phone-unfit-2/checks.sh`: exit 0, 241 tests passed.
- `FLOW_MOBILE_API=http://127.0.0.1:18323 FLOW_E2E_API_URL=http://127.0.0.1:18323
  npx playwright test -c /tmp/codex-phone-unfit-2/playwright.config.mjs
  mobile-agent.spec.ts mobile-docs.spec.ts mobile-team-overview.spec.ts
  mobile-settings.spec.ts mobile-issues-list.spec.ts mobile-projects.spec.ts`:
  exit 0, 96 tests passed after rebase.
- Scratch API `GOMAXPROCS=2 go build -p 1 -o /tmp/codex-phone-unfit-2/api
  ./cmd/server`: exit 0. No Go source changed, so no Go tests/vet were needed.
- `git diff --check`: exit 0.

An earlier combined run had one transient project-status usage-count failure;
the final unchanged six-spec run passed all 96 tests. Initial stale-locator
and wrong-local-API-port runs are retained as failed attempts, not passes.

Own API/Vite: `127.0.0.1:18323` / `127.0.0.1:5483`, with an isolated SQLite
backup of the reference fixture database under `/tmp/codex-phone-unfit-2/`.
No shared reference data or production data was mutated. Every browser spec
creates its own workspace. No real model requests were necessary.

The members gate locator was updated to the same actual Search by name or
email input after grouping it; no other locator or expectation was changed.

At 1440x900, all 15 routes have identical visible main text and identical
visible heading/button/input/select rectangles before and after. The desktop
screenshots were also inspected. Phone checks cover 375/390px, long metadata
names and milestone descriptions, settings copy growth, and zh-CN.

Screenshots in `/tmp/codex-phone-unfit-2/shots/` were opened in contact sheets:

| Files | What They Show |
| --- | --- |
| `*-1440-before.png`, `*-1440-after.png` | Same Agent conversation/options, document view/metadata/list, My Issues, project overview/update/milestones, and six settings routes; desktop unchanged |
| `*-390-after.png` | Those 15 phone routes, with intrinsic metadata/tabs, growing settings rows and milestone copy, and the writing action after the feed |
| `preferences-{375,390}.png` | Natural preference wrapping before selecting Language |
| `language-zh-{375,390}.png` | Visible Language select after the reference content, with the UI actually switched to Chinese |
| `teams-export-{375,390}.png` | Export CSV visible after the team table; browser also verified the download filename |
| `activity-tools-{375,390}.png` | Update composer/feed followed by the visible Write with Agent control; 375px capture is scrolled to that control |
| `agent-working-{375,390}.png` | Open phone panel with a real scripted streaming answer, not a configuration error |

The first feature-capture attempt reused a workspace with a restored chat,
which intercepted a later click. It was a failed verification attempt, not a
pass; final feature captures use a separate workspace per width.

## Reviewer Clicks

Use any workspace in place of `:workspace` at 375/390px; compare at 1440px.

| Route | Clicks |
| --- | --- |
| `/:workspace/document/:document` | Edited date, inspect metadata with a long owner; Show document history |
| `/:workspace/my-issues/assigned` | More views, choose another view; Display options, Insights |
| `/:workspace/team/:team/documents` | Documents/Members tabs directly, or their menu when labels really overflow |
| `/:workspace/project/:project/overview` | Expand a milestone, enter a multiline description; inspect counts and Add milestone |
| `/:workspace/project/:project/activity` | Update, scroll after the feed, Write with Agent; inspect the response |
| `/:workspace/project/:project/issues` | Display options, Insights |
| `/:workspace/settings/account/preferences` | Scroll to Language, select Chinese, then English |
| `/:workspace/settings/teams` | Export CSV after the table; inspect downloaded teams.csv |
| `/:workspace/settings/workspace` | Edit URL, change fiscal month; Welcome message opens its explanation; Announcements opens its dialog |
| `/:workspace/settings/account/profile`, `.../notifications`, `/:workspace/settings/members` | Edit profile, open channels, filter/search/invite members; verify content is not clipped |

## Gate Output

The following appendix is generated from the final per-screen logs. It includes
all failures, not just the passing summary lines.

### agent / agent-home

```text
mobile-gate agent / agent-home: 44 element checks, 0 failures
text-gate agent / agent-home: 2 shared texts, 22 not shown in Flow there, 0 differences
icon-gate agent / agent-home: 13 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### agent / conversation

```text
mobile-gate agent / conversation: 58 element checks, 0 failures
text-gate agent / conversation: 8 shared texts, 19 not shown in Flow there, 0 differences
icon-gate agent / conversation: 15 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=null at 303,17,8,9 ref=M10.1611 0.314094L5.9946 refRGB=149,149,151 flowRGB=null shapeIoU=0
```

### agent / chat-options

```text
mobile-gate agent / chat-options: 26 element checks, 0 failures
text-gate agent / chat-options: 4 shared texts, 25 not shown in Flow there, 0 differences
icon-gate agent / chat-options: 12 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=177 at 333,15,14,14 ref=M10.5193 4.98997L9.46118 refRGB=149,149,151 flowRGB=252,252,252 shapeIoU=0.161
  COLOUR dist=null at 365,15,14,14 ref=M3 6.5a1.5 1.5 0 1 1 0 3 refRGB=255,255,255 flowRGB=null shapeIoU=0
```

### docs / document-view

```text
mobile-gate docs / document-view: 34 element checks, 0 failures
text-gate docs / document-view: 2 shared texts, 11 not shown in Flow there, 0 differences
icon-gate docs / document-view: 16 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### docs / document-metadata

```text
mobile-gate docs / document-metadata: 20 element checks, 0 failures
text-gate docs / document-metadata: 4 shared texts, 16 not shown in Flow there, 0 differences
icon-gate docs / document-metadata: 7 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### docs / documents-list

```text
mobile-gate docs / documents-list: 56 element checks, 0 failures
text-gate docs / documents-list: 5 shared texts, 27 not shown in Flow there, 0 differences
icon-gate docs / documents-list: 34 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=107 at 52,14,16,16 ref=sprite refRGB=226,82,19 flowRGB=254,107,119 shapeIoU=0.878
  COLOUR dist=104 at 45,207,14,14 ref=sprite refRGB=227,83,21 flowRGB=254,107,119 shapeIoU=0.405
```

### docs / documents-tabs-menu

```text
locator.click: Timeout 30000ms exceeded.
Error: ENOENT: no such file or directory, open '/tmp/mobile-ref/docs/documents-tabs-menu-390.json'
Error: ENOENT: no such file or directory, open '/tmp/mobile-ref/docs/documents-tabs-menu-390.json'
```

### docs / documents-list-selected

```text
mobile-gate docs / documents-list-selected: 28 element checks, 0 failures
text-gate docs / documents-list-selected: 10 shared texts, 26 not shown in Flow there, 0 differences
icon-gate docs / documents-list-selected: 18 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### docs / documents-filter-menu

```text
mobile-gate docs / documents-filter-menu: 24 element checks, 0 failures
text-gate docs / documents-filter-menu: 10 shared texts, 28 not shown in Flow there, 0 differences
icon-gate docs / documents-filter-menu: 11 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### docs / documents-display-options

```text
mobile-gate docs / documents-display-options: 28 element checks, 0 failures
text-gate docs / documents-display-options: 12 shared texts, 29 not shown in Flow there, 0 differences
icon-gate docs / documents-display-options: 13 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### issues-list / my-assigned

```text
mobile-gate issues-list / my-assigned: 46 element checks, 0 failures
text-gate issues-list / my-assigned: 4 shared texts, 26 not shown in Flow there, 0 differences
icon-gate issues-list / my-assigned: 26 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=46 at 338,271,18,18 ref=M12.927 16.371a1.47 1.47 refRGB=226,227,229 flowRGB=254,254,254 shapeIoU=0.626
```

### issues-list / my-created

```text
mobile-gate issues-list / my-created: 34 element checks, 0 failures
text-gate issues-list / my-created: 3 shared texts, 38 not shown in Flow there, 0 differences
icon-gate issues-list / my-created: 17 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=260 at 351,101,18,18 ref=M10.25 6.75C10.25 7.9926 refRGB=86,87,89 flowRGB=248,231,232 shapeIoU=0.141
  COLOUR dist=45 at 312,50,32,32 ref=text:display-button refRGB=149,149,151 flowRGB=121,127,179 shapeIoU=0.519
```

### issues-list / my-subscribed

```text
mobile-gate issues-list / my-subscribed: 36 element checks, 0 failures
text-gate issues-list / my-subscribed: 4 shared texts, 38 not shown in Flow there, 0 differences
icon-gate issues-list / my-subscribed: 23 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=null at 247,62,8,9 ref=M10.1611 0.314094L5.9946 refRGB=227,228,230 flowRGB=null shapeIoU=0
  COLOUR dist=45 at 312,50,32,32 ref=text:display-button refRGB=149,149,151 flowRGB=121,127,179 shapeIoU=0.519
```

### issues-list / my-activity

```text
mobile-gate issues-list / my-activity: 40 element checks, 0 failures
text-gate issues-list / my-activity: 4 shared texts, 44 not shown in Flow there, 0 differences
icon-gate issues-list / my-activity: 21 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=45 at 312,50,32,32 ref=text:display-button refRGB=149,149,151 flowRGB=121,127,179 shapeIoU=0.519
```

### issues-list / my-tabs-overflow

```text
mobile-gate issues-list / my-tabs-overflow: 32 element checks, 0 failures
text-gate issues-list / my-tabs-overflow: 6 shared texts, 26 not shown in Flow there, 0 differences
icon-gate issues-list / my-tabs-overflow: 17 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### issues-list / my-assigned-details

```text
mobile-gate issues-list / my-assigned-details: 32 element checks, 0 failures
text-gate issues-list / my-assigned-details: 9 shared texts, 28 not shown in Flow there, 0 differences
icon-gate issues-list / my-assigned-details: 16 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=45 at 353,99,14,14 ref=M8.75 4C8.75 3.58579 8.4 refRGB=52,52,53 flowRGB=26,26,27 shapeIoU=0.896
```

### issues-list / filter-menu

```text
mobile-gate issues-list / filter-menu: 26 element checks, 0 failures
text-gate issues-list / filter-menu: 17 shared texts, 65 not shown in Flow there, 0 differences
icon-gate issues-list / filter-menu: 19 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=55 at 17,209,358,12 ref=text:filter-separator refRGB=29,30,31 flowRGB=60,61,64 shapeIoU=0.918
```

### issues-list / filter-status

```text
mobile-gate issues-list / filter-status: 20 element checks, 0 failures
text-gate issues-list / filter-status: 12 shared texts, 57 not shown in Flow there, 0 differences
icon-gate issues-list / filter-status: 14 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### issues-list / display-options

```text
mobile-gate issues-list / display-options: 38 element checks, 0 failures
text-gate issues-list / display-options: 22 shared texts, 50 not shown in Flow there, 0 differences
icon-gate issues-list / display-options: 20 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=167 at 122,523,120,28 ref=text:property-chip-off refRGB=252,252,252 flowRGB=154,155,157 shapeIoU=0.022
```

### settings / account-preferences

```text
mobile-gate settings / account-preferences: 38 element checks, 3 failures
  FAIL 375 account-preferences.row-names-select x: want 241.84 got 240.84
  FAIL 375 account-preferences.card-interface h: want 386 got 371.50
  FAIL 390 account-preferences.row-names-select x: want 256.84 got 255.84
text-gate settings / account-preferences: 18 shared texts, 4 not shown in Flow there, 0 differences
icon-gate settings / account-preferences: 11 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### settings / account-profile

```text
mobile-gate settings / account-profile: 42 element checks, 0 failures
text-gate settings / account-profile: 13 shared texts, 5 not shown in Flow there, 0 differences
icon-gate settings / account-profile: 16 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### settings / account-notifications

```text
mobile-gate settings / account-notifications: 50 element checks, 0 failures
text-gate settings / account-notifications: 12 shared texts, 12 not shown in Flow there, 0 differences
icon-gate settings / account-notifications: 13 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### settings / workspace-general

```text
mobile-gate settings / workspace-general: 40 element checks, 0 failures
text-gate settings / workspace-general: 13 shared texts, 8 not shown in Flow there, 0 differences
icon-gate settings / workspace-general: 16 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=69 at 34,174,32,32 ref=text:logo-swatch refRGB=105,105,106 flowRGB=146,145,144 shapeIoU=0.485
```

### settings / teams

```text
mobile-gate settings / teams: 48 element checks, 0 failures
text-gate settings / teams: 10 shared texts, 11 not shown in Flow there, 0 differences
icon-gate settings / teams: 22 icons in layout boxes, 1 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=285 at 166,284,16,16 ref=M6.75194 10.6239C6.41861 refRGB=86,87,89 flowRGB=252,252,252 shapeIoU=0.139
```

### settings / members

```text
mobile-gate settings / members: 42 element checks, 0 failures
text-gate settings / members: 12 shared texts, 25 not shown in Flow there, 0 differences
icon-gate settings / members: 21 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### settings / nav

```text
mobile-gate settings / nav: 34 element checks, 0 failures
text-gate settings / nav: 45 shared texts, 7 not shown in Flow there, 0 differences
icon-gate settings / nav: 21 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### settings / nav-lower

```text
mobile-gate settings / nav-lower: 36 element checks, 0 failures
text-gate settings / nav-lower: 20 shared texts, 32 not shown in Flow there, 0 differences
icon-gate settings / nav-lower: 0 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### projects / project-overview

```text
mobile-gate projects / project-overview: 56 element checks, 0 failures
text-gate projects / project-overview: 17 shared texts, 25 not shown in Flow there, 1 differences
  "Add customer request" y=645: size 13.3333px vs 13px, weight 400 vs 500, lightness 100 vs 37
icon-gate projects / project-overview: 42 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=237 at 149,384,16,16 ref=M3 6.5a1.5 1.5 0 1 1 0 3 refRGB=86,87,89 flowRGB=223,224,226 shapeIoU=0.217
  COLOUR dist=133 at 131,464,16,16 ref=M8.75 4C8.75 3.58579 8.4 refRGB=149,149,151 flowRGB=225,226,228 shapeIoU=0.163
```

### projects / project-properties

```text
mobile-gate projects / project-properties: 58 element checks, 0 failures
text-gate projects / project-properties: 26 shared texts, 63 not shown in Flow there, 0 differences
icon-gate projects / project-properties: 45 icons in layout boxes, 5 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=231 at 155,351,14,14 ref=sprite refRGB=227,83,21 flowRGB=94,106,209 shapeIoU=0.427
  COLOUR dist=147 at 153,422,16,16 ref=sprite refRGB=247,200,193 flowRGB=207,94,99 shapeIoU=0.858
  COLOUR dist=125 at 234,422,16,16 ref=M8.75 4C8.75 3.58579 8.4 refRGB=153,153,156 flowRGB=225,226,228 shapeIoU=0.188
  COLOUR dist=46 at 57,351,120,15 ref=text:teams-label refRGB=151,151,154 flowRGB=122,128,182 shapeIoU=0.55
  COLOUR dist=117 at 57,423,120,15 ref=text:initiatives-label refRGB=247,200,193 flowRGB=167,135,138 shapeIoU=0.716
```

### projects / project-activity

```text
mobile-gate projects / project-activity: 40 element checks, 0 failures
text-gate projects / project-activity: 7 shared texts, 24 not shown in Flow there, 0 differences
icon-gate projects / project-activity: 30 icons in layout boxes, 0 colour differences (core RGB distance > 40, or ink on one side only)
```

### projects / project-updates

```text
mobile-gate projects / project-updates: 42 element checks, 0 failures
text-gate projects / project-updates: 11 shared texts, 26 not shown in Flow there, 0 differences
icon-gate projects / project-updates: 33 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=133 at 176,15,14,14 ref=M10.5193 4.98997L9.46118 refRGB=149,149,151 flowRGB=225,226,228 shapeIoU=0.163
  COLOUR dist=135 at 210,15,14,14 ref=M3 6.5a1.5 1.5 0 1 1 0 3 refRGB=149,149,151 flowRGB=226,227,229 shapeIoU=0.171
```

### projects / project-milestones

```text
mobile-gate projects / project-milestones: 36 element checks, 0 failures
text-gate projects / project-milestones: 17 shared texts, 20 not shown in Flow there, 0 differences
icon-gate projects / project-milestones: 24 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=133 at 176,15,14,14 ref=M10.5193 4.98997L9.46118 refRGB=149,149,151 flowRGB=225,226,228 shapeIoU=0.163
  COLOUR dist=135 at 210,15,14,14 ref=M3 6.5a1.5 1.5 0 1 1 0 3 refRGB=149,149,151 flowRGB=226,227,229 shapeIoU=0.171
```

### projects / project-issues

```text
mobile-gate projects / project-issues: 28 element checks, 0 failures
text-gate projects / project-issues: 6 shared texts, 10 not shown in Flow there, 0 differences
icon-gate projects / project-issues: 23 icons in layout boxes, 2 colour differences (core RGB distance > 40, or ink on one side only)
  COLOUR dist=133 at 176,15,14,14 ref=M10.5193 4.98997L9.46118 refRGB=149,149,151 flowRGB=225,226,228 shapeIoU=0.163
  COLOUR dist=135 at 210,15,14,14 ref=M3 6.5a1.5 1.5 0 1 1 0 3 refRGB=149,149,151 flowRGB=226,227,229 shapeIoU=0.171
```
