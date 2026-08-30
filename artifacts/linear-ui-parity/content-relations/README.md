# Linear content and relations UI audit

Audit date: 2026-08-30. Source: the signed-in `leozhengliu` Linear workspace in the user's Chrome. Screenshots were captured only after DOM and accessibility inspection.

## Verified entry points

| Capability | Linear route / entry | Observed UI | Flow implementation |
| --- | --- | --- | --- |
| Feed | `/leozhengliu/pulse/following`, `/popular`, `/all` | `For me`, `Popular`, `Recent`, subscription menu, custom views, empty state, `New update` | Existing Pulse routes and composer; persisted project/initiative updates |
| Post | Pulse `New update` | Dialog labelled `Create initiativeupdate`; source, health, close, rich editor, attachment, `Post update` | Existing Pulse composer and update cards |
| FeedItem | Pulse feed | Internal wrapper around project update, initiative update or post; no independent row settings page | Existing `buildPulseFeed`; no fabricated standalone page |
| Meeting | No user-facing entry found | GraphQL schema marks Meeting `[Internal]`; no accessible route or control in this workspace | API retained; no fabricated standalone page |
| Project history | `/project/aaa-035098cf30e4/activity` | Comment/Update tabs, composer, month grouping, audit entries | Existing project Activity page |
| Project relation | Project Activity → Open project details → `Add dependency` | Two ordered options: `Blocked by…`, `Blocking…`; dependency rows appear under Properties | Existing project sidebar dependency picker and persistent dependency sync |
| Initiative history | `/initiative/test-081735980f52/activity` and overview Activity panel | Comment/update stream and `See all` | Existing Initiative activity page |
| Initiative relation | No distinct relation control found | Initiative properties expose projects, owner, lead team, target and labels; no dependency menu in measured workspace | API retained; no unmeasured control added |
| Team documents | `/team/LEO/documents` | Team header tabs, `New document`, filter/display, Name/Created/Last edited/Owner columns | Added team Documents route and table |
| Document draft | Team documents → `New document` | Creation navigates directly into autosaving editor; no explicit Save draft button | Existing autosaving document editor; draft API stays internal |
| Document history | Document → `Edited Aug 30` → `Show document history` | Metadata popover then `Restore version for <title>` dialog; empty text `There is no history yet.` | Existing history dialog, revision preview and restore |
| Customer taxonomy | `/settings/customer-requests` | Inline status/tier lists, count, plus button, per-row menu, inline Name/Description/Create form | Existing taxonomy settings; detail picker now consumes persisted statuses/tiers |
| Customer request lifecycle | Customer detail request row menu | `Mark as important`, `Move to`, `Copy link`, create/edit/change customer/open issue, and destructive `Delete`; no Archive/Restore entry exists | Flow follows the measured row lifecycle; backend archive remains internal and no fabricated archive control is shown |
| Release notes | `/pipeline/test/changelog` | Missing notes empty state, release selector, `Create` action | Existing pipeline Changelog |
| Release note editor | `/pipeline/test/release/test3-.../release-notes` | Breadcrumb, tabs, title, released date, Agent action, `Release notes content` editor | Existing release detail notes editor and persisted `ReleaseNote` API |
| Team resources | `/team/LEO/overview` | `Team resources`, `Add resources`, `Add section`, empty copy; members and Go to links | Added team Overview, sections, pinned documents/links, move/remove/rename/delete |

## Measured states and evidence

- Pulse default/empty and create dialog: `linear/pulse-*`
- Project overview, actions, activity, details and dependency picker: `linear/project-*`
- Initiative overview/actions: `linear/initiative-*`
- Customer index/detail/settings and inline status creation: `linear/customer-*`
- Releases, pipeline, changelog and editor: `linear/release-*`
- Team overview/resources/documents and document history: `linear/team-*`, `linear/document-*`
- Computed geometry/style snapshots: `linear/*-metrics.json`

## Keyboard and state observations

- Menus and dialogs expose listbox/option or dialog semantics and close with `Escape`.
- Project dependency picker starts focused in `Dependencies…`; only `Blocked by…` and `Blocking…` are present.
- Customer status creation is inline, autofocuses `Name`, disables the add trigger while editing and offers `Cancel`/`Create`.
- Document editor uses contenteditable textboxes, autosaves, supports a metadata popover and opens version history as a modal.
- Release and team top navigation are real links, preserving deep links and browser history.

## Blocked or unavailable states

- `Post`, `FeedItem`, and `Meeting` are marked internal in Linear's public schema. Only Post/FeedItem surface indirectly in Pulse; Meeting had no user-facing entry. A fake Flow settings page was deliberately not added.
- Initiative relation creation was not visible in this account/data set, so the persisted API was not exposed through invented UI.
- A temporary customer and request were created and removed. The row, ordered menu, `Move to` submenu, display options, and destructive confirmation were measured. Linear exposed no archive/restore control.
- A temporary document was edited across two sessions. The populated history list, change highlighting, current/older version states, direct restore, success Toast, delete confirmation, and deleted state were measured.

## Test-data note

The temporary entities used the prefix `Flow parity audit 2026-08-30`:

- Customer suffix `6ed8777eb7ef`: its request was deleted first, then the customer was deleted. Reload verified the list count returned from 2 to 1.
- Document URL: `https://linear.app/leozhengliu/document/flow-parity-audit-2026-08-30-document-101e94a57580`.

The document was restored to an older version and deleted. Linear retains it in `Recently deleted` for 30 days; the detail page displayed `Deleted` and `Document deleted`. The earlier `untitled-d9b5be2b0565` returned `Document not found` before this run. Existing issue `LEO-5` was temporarily linked to the request; deleting the request removed that request link without deleting the issue.
