# Linear Reviews vs Flow verification (2026-08-22)

Desktop measurements used the authenticated Linear workspace and the isolated
Flow audit server at `1470px` width. Mobile acceptance used `390 x 844`.

## Data and API gaps closed

| Capability | Previous Flow | Result |
| --- | --- | --- |
| Review domain | No Review entity | Persisted `CodeReview`, checks, files, events, reviewers, issues, branches and completion timestamps |
| Review mutation | None | Atomic title/status/favorite/draft/branch/reviewer/issue updates |
| Review decisions | None | Comment, approve and request-changes submissions with activity |
| Archived state | None | Closed and merged states, conflict protection, and reopen |
| GitHub | One generic toggle | Organization-scoped connection, settings, manage and disconnect |
| GitLab | Rejected by API | Token/self-hosted setup, safe token hint, settings, manage and disconnect |
| Connection identity | One provider row | Stable connection IDs and connection-specific mutation/delete |
| Routes | None | Canonical `/reviews`, `/reviews/created`, `/review/:slug[/review|/changes]` |

GitLab API tokens are accepted only by the connection mutation. Bootstrap and
integration list responses contain no token or secret hash; only `tokenHint`
is returned. Organization, repository, branch, review title, user and issue
names remain literal in translated surfaces.

## DOM measurement comparison

| Element | Linear measured | Flow measured / result |
| --- | --- | --- |
| Reviews list pane | `481.5px` | `482px` |
| Reviews heading | `x=262.5 y=22.5`, `13px/500`, `15.5px` high | `x=262 y=22.5`, `13px/500`, `15.5px` high |
| Header filter/display buttons | `28 x 28`, pill radius | `28 x 28`, pill radius |
| For you tab | `63.875 x 28`, padding `0 10` | `63.875 x 28`, padding `0 10` |
| Created tab | `66.625 x 28`, padding `0 10` | `66.625 x 28`, padding `0 10` |
| Review row | `40px`, radius `8` | `40px`, radius `8` |
| Filter portal | command surface, `228px`, radius `12`, z `600` | `228px`, radius `12`, z `600` |
| Display portal | `300px` wide | `300px` wide, z `600` |
| Detail tabs | `28px`, `12px/500`, padding `0 10` | Exact size, font and padding |
| Squash & merge | `132.727 x 28`, padding `0 10 0 8`, pill | `132.727 x 28`, same padding and radius |
| Submit review | `102.297 x 28`, `12px/500`, Linear menu shadow | Exact dimensions/font and matching two-part shadow |
| Review title | `32px` line box | `24px/32px`, same line box |
| PR actions portal | `211 x 381`, radius `12`, z `600` | `211 x 388.5`, radius `12`, z `600`; extra height retains separate URL/branch copy commands |
| GitLab token input | `608 x 32`, padding `6 12`, radius `8` | `611 x 32`, padding `0 12`, radius `8` within the same `640px` settings column |
| GitLab URL input | `608 x 32`, radius `8` | `611 x 32`, radius `8` |
| GitLab Connect | `77.586 x 32`, padding `0 12`, pill | `73.25 x 32`, content-width, padding `0 12`, pill |

## Interaction acceptance

- For you and Created routing, selected row and empty states.
- Field filter menu, Status/Author/Reviewer/Repository subviews, search,
  Quick-to-review and Missing-issue filters.
- Display grouping, ordering, closed window, drafts, team reviews and property
  toggles.
- Nested Escape returns to filter fields; a second Escape closes. Arrow keys
  cycle command menu items; Enter activates focused commands.
- Overview, Guide and Diff canonical routes survive refresh.
- Reviewer and multi-Issue selection persist after refresh.
- Comment, approve, close and reopen append or update persisted state.
- Checks portal, update branch, favorite, copy URL, copy branch, external host
  link, draft conversion, quick approve, merge and close are functional.
- GitHub organization connection and GitLab token/self-hosted connection both
  persist. Manage, provider configuration, disconnect confirmation, empty state
  and reconnect were exercised.

## Portal, theme and I18n

| Portal | Light | Dark | z-index |
| --- | --- | --- | --- |
| Review filter | white LCH surface, Linear three-part shadow | `lch(12.72 .85 272)`, dark menu shadow | `600` |
| Display options | white LCH surface | dark themed surface/text/border | `600` |
| PR actions | white LCH surface, radius `12` | dark themed surface/text/border | `600` |
| Checks | themed surface | themed surface | `600` |
| Reviewer/Issue picker | themed modal and overlay | themed modal and overlay | `701` |
| Submit review | themed modal and overlay | themed modal and overlay | `701` |
| Disconnect confirmation | themed modal and overlay | themed modal and overlay | `701` |

English and Simplified Chinese were switched through the workspace language
menu. Light and Dark were switched through Preferences. Dynamic code-host data
is rendered with literal-name boundaries and was unchanged in both locales.

## Responsive acceptance

At `390 x 844`, the Reviews list occupies the viewport without horizontal
overflow. Opening a review switches to a full-width detail view; the mobile
back button returns to the list. Overview metadata wraps without overlap,
Review/Guide/Diff remain reachable, the detail scrolls vertically, and Portal
content stays inside the viewport.
