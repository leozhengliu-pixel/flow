# Issues List Round Three

## Delivery

The review branch is `review/desktop-r3-issues-list`. The earlier filter geometry
commit `78ba1161` reached main before the owner changed the delivery rule. All
subsequent UI work is delivered through the review branch for the lead to merge.

The new state checks found desktop category labels and numeric comparisons being
truncated by a fixed submenu width. Those menus now size to their contents within
the existing 175px minimum and 395px maximum. Pending disabled filter actions use
the existing muted foreground, including when the row was selected immediately
before becoming disabled.

Selected chips retain their shared truncation behavior. Their value button now
exposes the complete selection through an accessible description and the existing
title-tooltip bridge. Opening the value picker also reveals the complete numeric
selection. No compact operator wording or chip geometry change was retained.

## State Matrix

The real app is exercised with independently created workspaces through
`web/e2e/workspace-fixture.ts`, an owned auth-disabled API, Vite, and bundled
Chromium. Each of these twelve combinations runs the same interactions:

| Viewport | Themes | Locales |
| --- | --- | --- |
| 1440 x 900 | dark, light | en-US, zh-CN |
| 390 x 844 | dark, light | en-US, zh-CN |
| 375 x 667 | dark, light | en-US, zh-CN |

| Changed control | States checked |
| --- | --- |
| Filter trigger and root menu | resting, hover, keyboard focus-visible, open |
| Root search and action rows | empty/typed search, open, hover/keyboard selection, pending disabled action |
| Status, people and label value menus | open, highlighted, unchecked and checked selections |
| Category and numeric submenus | empty input, typed input, pointer/keyboard highlight, applying a selection |
| Selected value button | resting, hover tooltip, keyboard focus-visible, open picker, checked value |

Search inputs and numeric choices have no disabled mode in their component
contract; that state is not fabricated. A scripted pending AI-filter response
exercises a real disabled action and proves a repeated click makes only one
request. No live model is called.

The fixture contains label `A`, the two-character CJK label `标签`, a 200-character
label, and the largest exact JavaScript integer `9,007,199,254,740,991`. The name
`林 舟` produces two CJK avatar initials. The auth-disabled server always reloads
its fixed local account identity, so a browser GET fixture changes only that
person's display text. Issues, labels, settings and selections use the real API.

The long label and phone chip may ellipsize. The full value remains available in
the tooltip, accessible description and selected-value picker. Numeric comparison
actions must show their complete operator and amount. Tests assert that their
glyph ranges fit, rather than asserting a fitted input or text width.

## Crops

Artifacts are under `/tmp/codex-desktop-r3-issues-list`:

- `control-crops`: individual state crops and computed-state JSON.
- `control-sheets`: four contact sheets per configuration, covering controls and
  chips, root/status menus, nested values, and value inspection.
- `control-states.md`: the gallery index and per-sheet observations.

Crops include padding around focus outlines. Small trigger crops are magnified
three times in the contact sheets; menu and text crops retain their native size.
Initial truncated-comparison captures and the discarded compact-operator
experiment are diagnostic failures, not passing verification.

## Checks And Limits

The state matrix has twelve passing configurations. Typecheck, full lint and the
124 focused component tests pass. Exact final commands and exit codes are retained
in the artifact report. All heavy jobs use `~/.flow-local/heavy.sh`.

The parity report uses the immutable reference captures and unchanged properties,
tolerances and coverage floors. The previous supplied count was 343 differences;
the font-metrics handoff reported 303. The current per-screen collection has ten
PASS and ten FAIL screens, with 158 differences. This includes corrected fixtures
and gate ownership, so it is not a pure CSS reduction metric.

The identifier evidence proves a query-wide allocation: PIC-2 has a 38px track in
a PIC-only list, while PIC-1 has a 50px track in a mixed list. FLO-1 and FLO-67 also
share the 50px track despite different glyph widths. Available captures do not
establish the rule for longer keys or three-digit identifiers. No universal 50px
minimum or fitted formula was committed.

Remaining failures concern that identifier track, display-menu layout, context
menu pointer state, and the view editor/header. Full measurements and individual
reasons are in `remaining-differences.md` beside the artifact report. No owner
decision, paid-plan change, new backend or new asset is requested.

Reviewer path: open a team's All issues route, choose Add filter, then inspect
Customers > Customer count with a long number. Apply a comparison, hover or focus
its value button, and open it. Repeat with Labels and Assignee in both themes and
languages. The automated test creates the short, long and CJK fixtures itself.
