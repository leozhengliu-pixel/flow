# Linear vs Flow Releases measured comparison

Date: 2026-08-20

## Evidence and limits

- Linear values are live DOM measurements from the authenticated Chrome tab at
  `1470 x 693`, DPR `2`. The complete inventory is in `linear.md`.
- Flow values below are rendered DOM measurements from Chrome against
  `http://127.0.0.1:5175`, not source estimates.
- Linear's Chinese UI and mobile DOM were not reachable. Those combinations are
  therefore verified independently on Flow and are not presented as Linear
  parity.
- Linear production data was not mutated. States that required a destructive
  Linear mutation remain explicitly outside the comparison.

## Final geometry

| Element | Linear measured | Flow measured | Result |
| --- | --- | --- | --- |
| Desktop viewport | `1470 x 693` | `1470 x 693` | Same test frame |
| App sidebar | `244px` | `244px` | Match |
| Main frame | `x=244, y=8, 1218 x 649` | `x=244, y=8, 1218 x 649` | Match |
| Pipeline row | `x=244.5, y=128, 1217 x 44`, radius `8` | `x=244.5, y=128.5, 1217 x 44`, radius `8` | `0.5px` vertical delta |
| Release row | `1217 x 48`, radius `8` | `1217 x 48`, radius `8` | Match |
| Directory Display menu | `x=1152.5, y=92.5, 301 x 197` | `x=1152, y=92, 301 x 197` | `0.5px` position delta |
| Display compact submenu | `122 x 56.5` | `122 x 56.5` | Match |
| Pipeline options | `x=312, y=48, 247 x 229` | anchor-dependent `y=48`, `246.98 x 228.98` | Within `0.02px` size |
| Composer | `x=485, y=90, 500 x 298.49` | `x=485, y=90, 500 x 298.5` | Within `0.01px` |
| Stage menu | `175 x 177.5` | `175 x 177` | `0.5px` height delta |
| Custom calendar | `x=454, y=68.84, 562 x 486.5` | `x=454, y=68.8359, 562 x 486.5` | Match within rounding |
| Release detail | `x=1061.5, y=96, 400 x 560.5` | `x=1062, y=96, 400 x 561` | `0.5px` x/height delta |
| New pipeline content | Linear controls start near `x=549` | centered `x=533, width=640, height=604` | Flow page container measured; child controls below |
| Pipeline name input | `300 x 32` | `300 x 32` | Match |
| Type cards | `300 x 129.59` | `300 x 129.5` | `0.09px` height delta |
| Stage form area | `408.46px` wide | `408px` wide | `0.46px` delta |

## Typography, spacing, and motion

| Property | Linear measured | Flow measured/verified | Result |
| --- | --- | --- | --- |
| Header title | `13px`, weight `500` | `13px`, weight `500` | Match |
| Standard menu item | `32px`, padding `0 18px 0 14px`, `13px/19.5px` | Same | Match |
| Menu radius | `12px` | `12px` | Match |
| Composer radius | `22px` | `22px` | Match |
| Standard transition | `150ms` | `150ms` | Match |
| Composer easing | `cubic-bezier(.43,.07,.59,.94)` | Same | Match |
| Reduced motion | Not independently measured | Flow reduces animation to `1ms` | Flow accessibility extension |
| Pipeline row rest | transparent | transparent | Match |
| Row hover/focus | visible hover; exact focus token unavailable | hover and `focus-visible` surface | Behavior covered; no exact token claim |

## Menus and Portal layers

| Surface | Linear | Flow | Result |
| --- | --- | --- | --- |
| Selector/Display z-index | `500` | `500` | Match |
| Action/Stage/Date z-index | `600` | `600` | Match |
| Stacked calendar z-index | `701` | `701` | Match |
| Dark Portal background | `lch(12.72 .85 272)` | `lch(12.72 .85 272)` | Match |
| Dark Portal text | `lch(91.178 1.425 272)` | `lch(91.178 1.425 272)` | Match |
| Dark Portal border | `lch(25.68 1.93 272)` | `lch(25.68 1.93 272)` | Match |
| Dark Portal shadow | three layers at `.125` | same three layers | Match |
| Light Portal | light surface/border/shadow measured in Linear | Flow verified after changing Preferences to Light | Theme inheritance passed |

Portal content was checked outside its trigger subtree. It is not clipped by
the list or composer scroll containers. The calendar's Radix wrapper is fixed
independently of the quick menu: desktop `x=454, y=68.8359`; mobile `x=12,
y=12`. On mobile its surface is `366 x 486.5`, z-index `701`.

## Interaction comparison

| Workflow/state | Linear observed | Flow accepted | Result |
| --- | --- | --- | --- |
| Directory grouping/order/properties | Yes | Menus update the real view | Functional match |
| Nested submenu keyboard | Arrow navigation and Escape close chain | Arrow navigation and Escape close child + parent | Match |
| Pipeline favorite | Yes | Real favorite API; sidebar navigation works | Functional match |
| Create composer | Name, version, description, stage, date | Same plus functional Scope | Flow extension |
| Stage search | Search row plus four defaults | Search row, filtering, ArrowDown focus, Escape closes only menu | Match |
| Date quick choices | Custom, tomorrow, 1/2 weeks, 1 month | Same choices and optional clear date | Match plus clear action |
| Custom calendar | Two month grids, prev/next, Cancel/Save | Same; `562 x 486.5` | Match |
| Create release | Required fields and disabled state | Real API create; invalid/disabled and saving states verified | Functional match |
| Release detail URL | Dedicated route | stable `?release=` deep link; close restores pipeline URL | Same behavior, different URL scheme |
| Stage update | Stage menu | Real API update using `stageStatuses` | Functional match |
| Release notes | Rich editor and Agent action | textarea and real save action | Capability difference |
| Changelog | Missing-notes state and release notes | Released entries render notes; empty state supported | Functional, illustration differs |
| Archive | Dedicated archived release route | Active/archive view and restore action | Functional extension |
| Recently deleted | Linear pipeline trash observed | Releases and pipelines project into Flow trash | Functional extension |
| New pipeline | Full settings page | Full page; Cancel path and validation verified | Presentation match |
| CI access key | Existing pipeline settings | Admin-only one-time key rotation API | Model/API parity; settings surface owns UI |

No Releases button is decorative: header controls, display controls, favorites,
copy URL, settings navigation, archive, trash navigation, detail tabs, stage,
notes, scope, calendar, and pipeline editor controls all have implemented actions.

## Model and API comparison

Flow now represents:

- ordered release pipelines and stages;
- explicit `stageStatuses` independent of business stage names;
- scheduled/continuous type and production flag;
- team visibility, position/reorder, archive/delete/restore;
- releases with stage, status, version, description, dates, commit SHA, projects,
  issues, notes, frozen scope, archive and ordering;
- release and pipeline favorites;
- path filters, release note template/automation and one-time CI access keys.

Pipeline mutations and key rotation are admin-only. Releases feature gating
covers pipelines. Direct reads, bootstrap projections, writes, and trash restore
respect private-team visibility. The API test matrix covers admin, member,
guest, feature-disabled, private-team, invalid resource, lifecycle, archive,
trash, key hashing, and `stageStatuses` round trips.

## Locale, theme, and responsive acceptance

| Combination | Flow result |
| --- | --- |
| English + Dark + desktop | Passed for directory, list, composer, menus, calendar, detail and editor |
| English + Light + desktop | Passed for main surfaces and Portals |
| Chinese + Light + mobile `390 x 844` | Passed; document `scrollWidth=390` |
| Chinese + Dark | Passed for translated chrome and protected entity values |
| Mobile detail | `390px` wide, `800px` high at `y=44`; no horizontal overflow |
| Mobile calendar | `366 x 486.5` at `x=12, y=12`; no clipping or overflow |
| Mobile pipeline editor | main `386 x 808`; document `scrollWidth=390` |

Business values remained literal across locale changes, including
`Flow QA Releases`, `Released`, `版本 QA 1`, `v2026.08`, team names, issue
identifiers and issue titles. Only application chrome is translated.

## Honest non-parity items

- Linear Chinese and mobile reference DOM were unavailable, so no invented
  Linear values are reported for those modes.
- Flow release notes use a controlled textarea rather than Linear's rich editor
  and Agent action.
- Flow's changelog empty state uses a Lucide icon rather than Linear's measured
  `144.26 x 110` illustration.
- Linear's detail architecture exposes richer properties and issue-row metadata;
  Flow matches the aside geometry but intentionally has a smaller content model.
- The Flow new-pipeline team picker is inline rather than Linear's Portal menu.
- Linear trash restore for an already deleted release could not be measured
  without mutating Linear production data.
