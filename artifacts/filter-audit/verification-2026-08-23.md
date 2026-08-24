# Issue-list Filter verification - 2026-08-23

## Routes

| Surface | Linear | Flow |
| --- | --- | --- |
| My Issues | `/leozhengliu/my-issues/assigned` | `/cleantrack/my-issues/assigned` |
| Saved view | `/leozhengliu/view/:viewId` | `/cleantrack/view/:viewId` |
| Team/workspace issues | `/leozhengliu/team/:key/all` | `/cleantrack/team/:key/all` |
| Release issues | `/leozhengliu/pipeline/:pipeline/release/:release/issues` | `/cleantrack/pipeline/:pipeline/release/:release/issues` |

All Flow surfaces use `MyIssuesFilterMenu`; Release, Display and Filter open states are mutually exclusive.

## Root measurements

| Property | Linear | Flow |
| --- | --- | --- |
| Width | `191px` English; `207px` Chinese target | `191px` English; `207px` Chinese |
| Radius | `12px` | `12px` |
| Border | `.5px` | `.5px` |
| Search height | `36.5px` | `36.5px` |
| Option height | `32px` | `32px` |
| Separator height | `12px` | `12px` |
| z-index | `600` | `600` |
| Desktop viewport | `1470x693`, DPR 2 | `1470x693` fixture |

Linear and Flow root menus both contain 24 capabilities and measure `578px` high. Locale changes root width because Linear sizes the menu differently for English and Chinese content.

## Linear ordered root snapshot

`AI filter; Advanced filter; Status; Assignee; Agent; Agent Session; Creator; Priority; Labels; Relations; Suggested label; Dates; Project; Project properties; Initiative; Cycle; Added to cycle; Releases; Subscribers; External source; Auto-closed; Content; Links; Template`.

## Flow ordered root snapshot

`AI filter; Advanced filter; Status; Assignee; Agent; Agent Session; Creator; Priority; Labels; Relations; Suggested label; Dates; Project; Project properties; Initiative; Cycle; Added to cycle; Releases; Subscribers; External source; Auto-closed; Content; Links; Template`.

Every rendered item is enabled and has a filtering side effect. Recursive submenus cover Relations, Dates, Project properties and Releases. AI suggestions and Advanced field groups were exercised end to end.

## Value menus

Measured Linear widths were applied per field: Status `204`, Assignee/Creator/Subscribers `231`, Agent `190`, Priority/Releases `199`, Labels `263`, Relations `184`, Dates `216`, Project `395`, Cycle/Content/Links `175`. Search header, 32px rows, checkbox state and Portal shadows are shared.

## Evidence

- `dom/linear-root.json`, `dom/flow-root.json`
- `dom/linear-status.json`, `dom/linear-submenus.json`
- `dom/flow-keyboard.json`
- `screenshots/linear-root-light.png`, `flow-root-light.png`, `flow-root-dark.png`, `flow-root-mobile.png`
- `screenshots/overlay-root-light.png`, `diff-root-light.png`
- `model-api.json`, `state-edges.json`

## Matrix and blockers

- Flow Chinese/English: shared `t()` rendering; entity values remain untranslated.
- Flow Light/Dark: captured.
- Flow Desktop/Mobile: captured; mobile `390x844`, menu `207x522.5`, no viewport overflow.
- Linear Desktop Light: captured and DOM measured in signed-in Chrome.
- Linear Mobile and Dark: blocked. Chrome external-window viewport override did not change its actual `1470x693` viewport, and no theme preference was changed solely for evidence.
- Whole-page pixel difference is not a valid `<=1%` score because Linear and Flow use different issue data and Flow intentionally omits unsupported capabilities instead of showing fake controls.

The remaining semantic gap is Advanced Filter OR-group composition; the implemented advanced group uses AND semantics.

## Final rerun

- English root width corrected and remeasured at `191px` for both Linear and Flow.
- Light border corrected and remeasured at `.5px lch(91.9 0 282)` for both.
- Status submenu remeasured at `204x273.5px` for both.
- Todo selection removed Backlog/Canceled groups; the same filter bar and result survived reload.
- Escape closed all Portals after the close transition and restored focus to `Add filter`.
- Temporary Linear menus and Flow persisted fixture filters were cleared after verification.
- Production build, lint, picker architecture, design-token audit and `git diff --check` passed.
