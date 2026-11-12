# Changelog

All notable changes to Flow will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]
- Record hydration temporary files share a process-wide 256 MiB byte budget (`FLOW_RECORD_SPOOL_MAX_BYTES`, `0` streams directly). Compatibility responses and canonical records continue through bounded streaming when the budget or temporary volume is unavailable; signed bootstrap URLs are rewritten during encoding. The hydration flag remains off by default.
- Team-restricted API keys now intersect their owner's access for resource references and counts across REST, bootstrap and MCP reads. Project dependencies, initiative and document links, issue hierarchy and cycle links, progress history, directory totals, cycle capacity and customer demand no longer expose other teams. Stored webhook deliveries are projected again for the reading key. Permission baseline tests enforce this by default.
- Optional `FLOW_SYNC_V2=on` shares versioned realtime sources and validated frames across viewers. Negotiated summaries and bounded delta responses preserve legacy envelopes and require a covered transaction journal before rollout; transport budgets remain active when disabled.
- Optional `FLOW_RECORD_HYDRATION=on` adds bounded metadata record scopes that preserve unloaded values, streams nested legacy JSON and signed file URLs, and releases compatibility-download snapshots before sending to slow clients. It defaults off pending handler migration and rollout validation.
- Add opt-in sync manifests and bounded, permission-bound project/initiative record reads. `FLOW_SYNC_MANIFEST` and `FLOW_INDEXED_READS` default off; unready catalogs retain legacy reads, filters, counts and pagination tokens.
- Sync record summaries, details, filters and manifest actor references share the credential team-scope policy; private references, customer demand and stored progress aggregates stay outside restricted-key responses.
- Issue list Dates filters: "Custom date or timeframe…" opens a date dialog with day, month, quarter, half-year and year views. A day can be matched on, before or after it, and a period is matched within its span. The field also reads typed dates such as "May 2027", "Q4" and "05/20/2027". Clicking a chip's date reopens the dialog. Each preset shows how many loaded issues it matches, and Canceled date joins the Dates menu.
- Optional `VITE_SYNC_V2=true` enables versioned manifest adapters for issue and document detail routes only when the server advertises `sync-v2`. Other surfaces and older servers keep the legacy bootstrap. Record details load in bounded batches, authorization changes cancel old reads, and unaffected workspace indexes are reused.
- Added recipient inbox state contracts and opt-in web overlays for unread counts and targeted inbox refreshes. Team-restricted credentials use the notification list's indexed access projection for counts and changed IDs. The capability remains disabled until all notification writers, snooze expiry and API replicas have complete commit and recipient projection coverage; missing capabilities preserve the existing inbox refresh behavior.
- Added opt-in API scale baselines with distinct principals, separate metadata count/body growth, an external two-process generator, commit/apply delivery accounting, SQL/CPU measurements, and permission differential coverage with isolated accounts and workspaces on shared test databases.
- Session activity timestamps now default to one refresh per minute across API replicas, while authentication, expiry and revocation are checked on every request. Set `FLOW_SESSION_TOUCH_INTERVAL=0` to restore an activity write on every request; a positive Go duration up to `60s` allows a shorter interval.
- Optional `FLOW_NARROW_MUTATION_INPUTS=on` reduces metadata copied for issue edits, comments and team edits while retaining the existing transactions and side effects. It defaults to off.
- Issue edits and comments copy smaller callback inputs by default; `FLOW_NARROW_MUTATION_INPUTS=off` restores the legacy path. Tests compare narrowed callbacks against full owned inputs automatically, and `FLOW_NARROW_MUTATION_VERIFY=1` enables that comparison with full-result fallback for production canaries. Team edits retain their existing allocation cost and legacy path.
- Auth-disabled development servers retain the active viewer when filtering realtime events, so recipient inbox and Agent updates reach open tabs.
- Optional browser scalability controls checkpoint realtime only after successful application, preserve completion barriers through coalescing and recovery, and isolate checkpoints by viewer, workspace and schema. Navigation summaries are limited to 2 MiB and cached issue details use a 32 MiB byte budget, preserving the open editor; both controls default off pending the complete rollout matrix.
- Wire sync capability advertisement to manifest/read opt-ins, journal-backed changes, certified collections and complete core data. Activation compatibility and rollback checks cover older clients, older servers and mixed replicas. P08, P10 and P13 defaults remain off pending the deferred load gate.
- With bounded browser caches opted in, realtime recovery restores the small navigation cache only after its fresh baseline completes. A later revocation prevents an older recovery read from restoring that cache.
- Applied realtime creation echoes keep a new Agent chat in its initiating panel, so a project's "Write with Agent" action does not open a second toolbar chat. Session updates and remote creations continue to apply.
- API scalability: project and initiative detail reads use a small permission context resolved once per request and copy only the requested record and its references. `FLOW_RECORD_ACCESS` defaults to `on`; opt-in `shadow` compares with the existing projection, and `off` restores it. Permanent permission differentials run on SQLite, PostgreSQL and MySQL.
- Realtime transport memory covers shared payloads, replay, queued events, dequeued batches and writes. Web heartbeats negotiate empty 204 responses and keep the initial/SSE roster; legacy callers and `FLOW_PRESENCE_MINIMAL=off` keep the 200 roster response. Collaboration recovery uses bounded SQL pages without temporary files, retains the 2 MiB frame limit, and follows compaction and generation changes. Event replay reads byte-bounded pages of up to 64 records.
- Storage: validate catalog page indexes after statistics collection at 23,210 and 232,100 records, including archive filters and continuation pages; reject explicit sorting on SQLite, MySQL and PostgreSQL.
- Storage: add a rebuildable shadow metadata catalog with bounded summary pages, record batches and resumable 128-record backfills. Existing readers and writers remain authoritative; the catalog does not advertise writer coverage.
- Security: realtime events no longer reach people who cannot read what they describe. Events other than issue, comment, document, project and initiative events (which are projected per viewer) used to go to every member of the workspace with their payload, often the whole record or the change's input: another member's personal view, a private team's labels, statuses, templates, release pipelines, SLA rules and renames, customers (to guests), webhooks, API keys, passkeys, integrations and other administration, workspace invitations (with their addresses). Each event now follows the read rule of the record it names, as the API applies it (personal and team views, team privacy, guests, API key team limits, admin-only settings): a viewer who may read the record gets a bare signal (type, id and the fields the web app patches locally) and reloads it; anyone else gets nothing, or a signal without the record's id when the change may have taken it out of their reach (a team membership, a view or template moved into a private team) or the record was deleted. Administration events reach workspace admins and whoever made the change, personal records only their owner. Replays from `/api/realtime/changes`, a resumed stream and events from other replicas are filtered the same way, and the payload they carry is never forwarded as is.
- Agent replies keep what happened to them: each reply records an outcome (completed, failed, interrupted or stopped, with a reason), and failed, interrupted and stopped replies no longer record a "completed" activity; an empty reply is not a failure by itself. Like the reference app, a stopped reply shows no badge or Retry, an interrupted one shows no error row and its chat reads "Stopped responding" (or "Didn't start" when it never reached a worker, with Retry only when no write tool ran), a failed read step keeps its done label, and a failed write step says what did not happen ("Couldn't update issue AGE-1") instead of "retried". A metadata refresh no longer blanks an open conversation (a summary never replaces its full messages), a reply whose stream drops follows the server's reply instead of resending, the floating Agent panel follows the same rules, and agent-worker tool writes carry an Idempotency-Key. Loops: a run without its required output reads "Required output missing" and a finished run without a reply shows an empty state.
- Team trees match the reference's sizing, order and phone drawer. A team menu (Parent team in Create team, the issue Team property, the create-issue team, the invite-members team select) hugs its widest row at a whole-pixel width (the rows rounded up, 175px at least, 400px rows at most, plus the 1px border), where the Team property and create-issue menus reserve the selected team's tick column on every row; long team names no longer truncate at 175px or 200px. Sibling teams are listed alphabetically in every menu, picker and Settings > Teams (at every nesting level); the app sidebar and the settings sidebar list a team's sub-teams newest first. New teams hide "Initiatives" in their team sidebar by default (the "Hide initiatives in the team sidebar" setting starts on). The phone settings drawer joins sibling sub-teams with one continuous line and an elbow per row, and the settings sidebar team rows keep their icon and name as the first two spans.
- Sibling sub-teams hang from one continuous tree line. In every team picker and team filter (issue Team property, create-issue team, Parent team, project Teams and invite-members team pickers, project and issue Team filters) a sub-team followed by a later sibling keeps its 1px guide for the full row height, so only the last child ends in the elbow; deeper trees carry each ancestor's guide past its whole subtree. Settings › Teams draws the branch from the row (it was clipped inside the name cell): the line runs into the next row while a sibling follows and ends in the shared curve otherwise. The desktop settings sidebar lists sub-teams under their parent, inset 22px (18px more per level), on the same continuous line with a stub below the parent.
- Open clients no longer download the whole workspace metadata after every realtime change. Document edits (autosave included), document comments, project and initiative comments, updates, milestones and resources, and team renames are applied in place from the event: the server sends each viewer the document, project or initiative as their own workspace data shows it (a project's links to a private team or project the viewer cannot see are left out, as is its issue count) plus a document's comments or a project's updates for those events, and a later copy never replaces a newer one. Editing a document no longer reloads the workspace after each save either. Only changes to what someone may see (team membership, team privacy, document permissions, a project or initiative moved between teams) reload the metadata, once per burst. Drafts and AI conversations reach only their author (their events used to carry the content to every member, each of whom reloaded the whole workspace), a deleted project or initiative now reaches the clients holding it, and code review, dashboard, post and meeting events no longer make every client reload the whole workspace.
- Open clients no longer download the whole workspace metadata after every realtime change. Document edits (autosave included), document comments, project and initiative comments, updates, milestones and resources, and team renames are applied in place from the event: the server sends each viewer the document, project or initiative as their own workspace data shows it (a project's links to a private team or project the viewer cannot see are left out, as is its issue count) plus a document's comments or a project's updates for those events, and a later copy never replaces a newer one. Editing a document no longer reloads the workspace after each save either. Only changes to what someone may see (team membership, team privacy, document permissions, a project or initiative moved between teams) reload the metadata, once per burst. Drafts and AI conversations reach only their author (their events used to carry the content to every member, each of whom reloaded the whole workspace), a deleted project or initiative now reaches the clients holding it, and code review, dashboard, post and meeting events no longer make every client reload the whole workspace. The realtime streams of a server share that work: each event's projections are built once (the first stream builds, the others wait for it) and written to every stream without being encoded again, the project and initiative visibility index is built once per workspace copy instead of once per stream and event, a presence heartbeat that changes nothing no longer goes to every stream (each one used to filter and re-encode the whole roster), a stream skips presence rosters a later one replaces, and a stream that falls more than 1 MiB behind gets a resync instead of holding the backlog. With 300 users on one replica the streams stopped falling behind (2,329 resyncs, and the bootstraps they caused, down to none) and changes reach everyone within a second.
- Open clients no longer download the whole workspace metadata after every realtime change. Document edits (autosave included), document comments, project and initiative comments, updates, milestones and resources, and team renames are applied in place from the event: the server sends each viewer the document, project or initiative as their own workspace data shows it (a project's links to a private team or project the viewer cannot see are left out, as is its issue count) plus a document's comments or a project's updates for those events, and a later copy never replaces a newer one. Editing a document no longer reloads the workspace after each save either. Only changes to what someone may see (team membership, team privacy, document permissions, a project or initiative moved between teams) reload the metadata, once per burst. Drafts and AI conversations reach only their author (their events used to carry the content to every member, each of whom reloaded the whole workspace), a deleted project or initiative now reaches the clients holding it, and code review, dashboard, post and meeting events no longer make every client reload the whole workspace. The realtime streams of a server share that work: each event's projections are built once (the first stream builds, the others wait for it) and written to every stream without being encoded again, the project and initiative visibility index is built once per workspace copy instead of once per stream and event, a presence heartbeat that changes nothing no longer goes to every stream (each one used to filter and re-encode the whole roster), a stream skips presence rosters a later one replaces, and a stream that falls more than 1 MiB behind gets a resync instead of holding the backlog. With 300 users on one replica the streams stopped falling behind (2,329 resyncs, and the bootstraps they caused, down to none) and changes reach everyone within a second. A workspace's presence roster goes out at most once a second per server (a burst of people opening the app sends one roster, not one per person, to every stream), and expired presence is cleaned up once per workspace every 15 seconds instead of once per open stream.
- Resource requests that build the viewer's whole workspace projection (project, initiative, document and other resource routes, the public API records, and handlers that read the projection) are bounded like the bootstrap, by a gate of their own so page loads and resource reads never wait on each other: `FLOW_RESOURCE_READ_CONCURRENCY` (default 6) concurrent projections per server, `FLOW_RESOURCE_READ_QUEUE_LIMIT` (128) waiting reads and `FLOW_RESOURCE_READ_QUEUE_TIMEOUT` (15s). A read beyond that gets 503 with Retry-After, which the web client retries (up to three tries for any read); a write waits for its turn, and a request whose client leaves gives its place up. A request holds its slot until it ends, and requests that build no projection never wait.
- A large workspace no longer exhausts API memory under concurrent use. Notification and realtime routes stop copying the whole workspace metadata (one notification list used to copy it twice); a resource route builds the viewer's metadata projection once per request instead of in both the authorization and the handler (read requests, issue create and update, and the issue-record sub-routes); the paged bootstrap is limited to `FLOW_METADATA_READ_CONCURRENCY` (default 4) concurrent builds per server with a bounded queue (`FLOW_METADATA_READ_QUEUE_LIMIT`, 64; `FLOW_METADATA_READ_QUEUE_TIMEOUT`, 20s) that answers 503 with Retry-After, a write deadline (`FLOW_METADATA_READ_WRITE_TIMEOUT`, 2m) and a response streamed one element at a time (byte-identical to before); and the web client retries a busy bootstrap after its Retry-After and coalesces the metadata reloads realtime events trigger (one at a time per workspace, spread across tabs). Servers sharing a database keep their workspace copy while its stored metadata revision is unchanged instead of decoding the whole workspace again before every write and on every peer event, a reload that outlasts the request that started it completes in the background instead of being discarded, a peer event reload no longer times out after 5s and makes every client resync at once, and an issue edit no longer deep-copies the stored projects and trash. On a synthetic workspace with 42 MiB of metadata and 40 concurrent clients, peak heap fell from about 4 GiB to 1.2-1.4 GiB.
- A document created from the Agent page opens from the reply's link and is created once. Document events now reach everyone who may open the document (they were dropped for every viewer), the agent's own writes reach the tab that asked (it used to discard them as its own echo), and a document page whose document the tab has not loaded yet fetches it and shows "Document not found" only when the server has no such document. A reply stream gets a keep-alive comment every 15 seconds, so a reverse proxy's idle timeout no longer closes it while the model is thinking; when the connection does drop, the Agent page follows the reply on the server instead of putting the message back with a Retry that ran the whole request again (two identical documents). A write call that reaches Flow twice (repeated in a provider stream, replayed from the stored conversation, or arriving while the first still runs) returns the first run's result instead of writing again.
- Every team picker lists sub-teams as the shared tree: the create-issue team picker (name and key, no search, tick on the current team), the create-team Parent team picker, the project Teams picker and invite-members team picker, and the Team value lists of the issue and project filter menus now indent a sub-team 24px under its parent with the same rounded connector. The tree is one shared rule keyed on the menu's `tree` flag (the per-menu override block is gone). The project filter offers Teams once a workspace has more than one team, and a short team list in a filter has no search.
- The sidebar team tree nests to any depth like the reference: a team's rows start 19px in at the first sub-level and 16px further at each deeper level (links 16px beyond their heading), and every sub-team's elbow and connector hang from its own parent's guide line instead of all landing on the outermost one.
- The issue Team property menu lists teams as a tree like the reference: a sub-team sits indented 24px per level under its parent with a thin rounded connector hanging from the row above, instead of a "Parent > child" breadcrumb row. The menu hugs its rows (175px minimum), team icons are 14px in an 18px cell, and the selected tick is muted. The phone sheet keeps the breadcrumb rows.
- Desktop issue tabs expose their labels consistently, Insights and initiative controls use the measured shared colours and chart spacing, release pipeline search has a complete accessible label, and team header glyphs keep the correct inset. Initiative keyboard hints stay inline on phones and the introduction is localized. Integrations discovery includes the featured services and Slack intake while preserving Feishu and Lark. Usage & limits shows each workspace's real 30-day Loop run total with a working management link.
- Browser E2E specs follow the current reference structure: a fresh workspace's Integrations page starts at the discovery block (no Enabled group until something is installed), and the phone Help panel's "Send flare" report names the page kind, never the path or workspace key.

- Sub-teams in the sidebar now nest inside their parent team: a sub-team's heading and links sit below the parent's own links (indented 19px, its links 16px further), a thin connector hangs from the parent's heading beside every sub-team and turns into an elbow at each sub-team heading, and the sub-team keeps its own collapse state, so collapsing the parent hides only the parent's links and leaves its sub-teams visible. The team chevron is centred in its box (it sat 4px too far right), the team actions glyph box is 12x14, the phone drawer's team title ends where the always-visible actions button starts, collapsed team rows keep their rounded corners on the phone, and the light theme's team headings, section labels and hover colours follow the reference.
- Settings › Administration has its own Permissions page (between Members and Security) holding the workspace permission settings that used to sit in Security: new user invitations, team creation, workspace initiatives, labels, templates, pinned views, loops and agent guidance, plus API key creation and data import. Settings › Teams nests sub-teams under their parent with a collapse toggle, Usage & limits links its Coding sessions and Loops features, the SLA row is titled "Enable SLAs" and the phone SLA page fills the screen like the other settings pages, and the desktop Integrations page drops the empty "Enabled" block and lists integrations in category groups of description cards.
- The Help menu is 270px wide with 6px spacer rows instead of padding, drops its duplicate "Search for help" row, and gains "Send flare" (opens a pre-filled GitHub issue with your browser and page) and "Community" (GitHub Discussions); the workspace menu no longer draws a divider between Settings and "Invite and manage members" and keeps its padding at 0 in the light theme.
- The sidebar's personal section now lists My issues before Reviews by default (a saved copy of the old default counts as never customised, a rearranged order is kept); the Try section offers Import issues on desktop too, suggests Cycles only when the first team has cycles on, and "Connect GitHub" opens the GitHub integration settings instead of a toast.
- The Agent home shows a "Connect GitHub" chip under the composer while no GitHub connection exists; it opens the GitHub integration settings.
- Team home: the team name field fits its text, the right column lists Members, Go to (Connect channel, Team settings, Triage, Issues, Cycles when on, Initiatives, Projects, Views) and then Sub-teams in that order, sub-team rows look like the Go to links, and the top bar sits on the same half pixel as the reference. The empty Releases page's Documentation button is 28px high.

- The issues toolbar now orders its buttons Filter, Display options, Insights, Details. The team issue list's filter menu leaves out Cycle, Added to cycle, External source and Customers while there is nothing to pick from, shows the whole menu (flush under its button) when it fits the window, and names its panel "Menu". Display options names its two sort buttons "Group ordering" and "View ordering", lists Release before Estimate, offers the Creator chip (an avatar of the issue's creator in the row), keeps Release whenever releases are on, and leaves out Customers and Customer revenue while the workspace has no customers.
- Insights: a new view's report is Issue count by Status, segmented by Priority; the introduction and the report share one card (12px introduction, a Documentation button with an icon, a "Close" button), table headers use the regular weight, column widths follow the text exactly, the legend shows each status icon, and the footer button is named "Set default for everyone".
- A team with cycles switched off opens "This team has no cycles." on desktop; the empty Triage inbox reads "Nothing to triage" with a ringed illustration and its star is named "Add to favorites"; the empty Initiatives page's shortcut chip uses 11px key caps and no longer shows the details toggle while there are no initiatives.
- The light theme now uses the reference colours throughout: sidebar section headings, team names and workspace name use the sidebar palette (one step darker than the page), primary text is the base label colour with list titles in the title colour, the page frame, triage frame and settings frame use the shared frame border, issue-list group labels, the open workspace and help triggers and the help menu border match.
- Changing your password with a wrong current password now answers 400 "The current password is incorrect" (and a too-short new password a 400 with its reason) instead of a 500; the wrong password is still audited as a failure.
- The welcome video player's elapsed and duration labels have a fixed width, so the seek bar sits at exactly the same place on every platform instead of shifting by a hundredth of a pixel with the font's text measurement.

- Create a new team (Settings › Teams › Create team, ⌘K, the teams directory and a team's Create sub-team) is now a page of the settings layout instead of a separate screen with its own sidebar: the same settings sidebar with Teams highlighted, the pinned Back link (it returns to where you came from), page header, cards, rows, inputs and buttons as the other settings pages, on desktop and phones. The form follows the reference: Icon & Name (icon and colour picker joined to the name field), Identifier (suggested from the name until you edit it, with the reference's messages for a missing, duplicate or taken value), Parent team (a searchable picker; sub-teams copy their parent's settings, so Copy from team is fixed to the parent), Team access (Public to workspace, Restricted to a private parent's members, Private to team members), Timezone (a searchable list of every time zone, defaulting to the one most of your teams use) and Copy settings from existing team. Creating a team opens its settings.
- Team settings now follow the reference's Business workspace page for page. The overview has the "Teams" crumb, lists Access and permissions with General and Members (it used to follow the Danger zone), and every row has its own icon (statuses, estimates, triage, cycles, chat notifications, team agents, loops, project updates, thread summaries, project statuses, hierarchy); the Danger zone buttons hang 6px past the row edge and page titles start 1.44px left of the column like the reference. Team General puts Name and Identifier in 200px fields (the icon is fused to the name field and tinted with the team colour), the description is one text area, Timezone shows "GMT-8:00 – Pacific Time - Los Angeles" style names, and Team initiatives and Other close the page; "Create issues by email" moved to the Automations page. Team members is the workspace table (sortable Name, Email and Role columns, full-width rows that open the member's profile, a Sub-teams column and a 32px row menu). Team hierarchy has a docs link, a Parent team select and a Subteams card (add, and a row per sub-team). In the settings drawer a team's entry is current only on its overview, sub-teams are inset 22px with a stub and elbow connector, and the team crumb's glyph is 18px.
- Team settings sub-pages (Access and permissions, Estimates, Issue statuses, Triage, Cycles, Automations, Slack, Team agents, Agent skills, Agent connectors, Project updates, Resolved thread summaries, Team project statuses, Recurring issues) now follow the reference page by page: titles and lead sentences with a quiet "Docs ↗" link, wording and banners for teams that have sub-teams, a dimmed switched-off section, icon tiles on link rows, hover-only status row menus, a Triage group and an Issue ordering setting on the issue statuses page, a team hierarchy page with a parent select and a sub-teams list, estimates with the scale values beside the select and options hanging on a line, Automations with email intake, Git connections, Auto-close issues and Auto-archive with 1/3/6/9/12 month selects, empty rows with their add action on the agent pages, and a stacked layout with full-width selects on a phone. The default status of an issue workflow is chosen in the status editor instead of a separate pill.
- New teams start with Triage switched on, as in the reference; existing teams keep their setting.

- The command menu panel now clips its overflow instead of hiding it, so opening it and hovering a row can no longer scroll the panel by half a pixel and shift every row 1px left (it landed at the reference position only on some opens).
- Every member who can see a project now sees its connected Lark or Feishu groups in the project sidebar (the group's name, or "N groups"), not only the team's owners and workspace administrators, who were the only ones the binding list was returned to; the others saw "Connect group". A new read-only endpoint, `GET /api/integrations/{provider}/chat/projects/{projectId}/groups`, lists the names of the active groups bound to a project for anyone who can see it (404 otherwise; a private team's group stays hidden from those outside the team) and says whether the viewer may change them. Members who can't open the group dialog read-only, with the groups listed and a note that only the project's team owners change them; with no group connected they see "No Lark group" instead of a connect action. An initiative's group dialog likewise lists its active groups by name for everyone who can see the initiative (not for one a private team leads). Binding configuration stays with those who manage it.
- On a phone, the SLAs settings page now scrolls 32px past its last rule card, matching the reference, instead of the 96px the other settings pages leave.
- The "Show all notifications" button under an empty unread inbox list is 1px wider (it has the 0.5px transparent border the other pill buttons have), matching the reference.
- The inbox welcome notification now plays Flow's own 38-second product walkthrough (create an issue, move it on the board, ask the Flow Agent) in a player with play, volume, seek, speed, download, picture-in-picture and full-window controls, English captions and keyboard shortcuts. It never autoplays and shows its poster until played. The video (`web/public/media/`, H.264 and VP9, under 1 MB each) is rendered from Flow's own UI by `web/scripts/render-intro-video.mjs`; the welcome text now uses the reference sizes.
- Changing a workspace's URL no longer leaves data behind under the old URL key. The rename now moves, in one transaction, the rows of every table keyed by the URL key (before, agent and coding sessions, loops, search documents, API keys, uploads indexes, offloaded content and others stayed behind: offloaded document versions and long agent replies showed as empty, and a later workspace purge did not find those rows), JSON naming the workspace (OAuth grants, agent tasks), app developer token references and the workspace's sealed connector credentials; offloaded content and coding harness state objects are copied to the new key before it commits. A test fails when a new table keyed by the URL key is not moved. A URL key that still holds rows can no longer be taken by a rename or a new workspace (409), and a workspace whose purge started cannot be renamed. Renames are recorded (`workspace_key_renames`) and the storage maintenance pass moves rows written under the previous key by work still running during the rename. Data migration 1040 repairs earlier renames from the domain events that recorded the previous keys, and offloaded bodies a workspace refers to are copied back from keys no workspace has. See [Renaming a workspace URL](docs/operations.md#renaming-a-workspace-url).
- Deleting a workspace now deletes it: after a grace period (`FLOW_WORKSPACE_DELETION_GRACE_DAYS`, `retention.workspaceDeletionDays`, default 30 days) in which admins can still cancel, the storage maintenance pass purges the workspace permanently, with every row it owns in every table (including domain events, search documents, agent and coding sessions, chat, SCIM, webhook and audit stream secrets, OAuth grants issued for it, recently viewed items and search history), the files its records use (attachments, assets, logos, coding artifacts, sandbox logs and snapshot archives, cached Pulse audio), the document versions and agent transcript parts in the content store, the sealed credentials of its integrations and connectors, and its cache entries. A purge interrupted by a restart finishes on the next pass, and the instance keeps a receipt (`workspace_deletion_receipts`: IDs, times and counts per table, no personal data) and logs each purge. Once a purge has started the deletion can no longer be canceled (409). The delete and cancel endpoints and the workspace access status report `deletionScheduledFor`. `DeleteWorkspace` in the store now removes every workspace table, and a test fails when a new table with a workspace column is not covered. See [Workspace deletion](docs/operations.md#workspace-deletion); backups keep the data until they expire, so align their retention.
- Purging an item from Recently deleted (by hand or when its retention ends) now also deletes the attachment files of the purged issue, document, project or customer request that no remaining issue or trash entry attaches; deleting a customer does the same for its requests. Before, the files stayed in storage.
- Secrets at rest are always encrypted with `FLOW_CONNECTOR_SECRET_KEY` (AES-256-GCM, bound to their row). Workspace integration OAuth access and refresh tokens (GitHub, GitLab, Jira, Slack, Figma and the other OAuth providers) move out of the workspace document into the encrypted credential store; a connection now only reports `oauthAuthorized` and its expiry, and every refresh rotates the stored tokens with a compare-and-swap. Data migration 1025 moves existing tokens and removes token copies from the domain event history (`previous_values`). Webhook, application webhook and audit log stream signing secrets are no longer stored in plain text when no key is configured: creating or rotating them, and starting an integration OAuth flow, fail with `503` ("Configure FLOW_CONNECTOR_SECRET_KEY …"), and plain-text secrets left by earlier releases are encrypted at the first start with a key (until then they keep signing and each start logs how many remain). The key can now be rotated without reconnecting anything: list the old key in the new `FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS` and each replica re-encrypts stored secrets under the new key at startup. Both variables accept `_FILE`, and a malformed key stops the server at startup. **Upgrade note:** set `FLOW_CONNECTOR_SECRET_KEY` before upgrading; without it, migration 1025 drops the plain-text integration tokens and those integrations ask to be reconnected. See [Secrets at rest](docs/configuration.md#secrets-at-rest).
- Every path that sends workspace content to a model now honours the workspace AI policy (HIPAA mode, the Flow Agent switch, and the guest rule). The AI filter (`POST /api/ai/issue-filter`), which sent member names, labels, projects and cycles to the model without checking it, now answers `403` like the Agent routes, and the web app hides the AI filter when the workspace turns AI off or HIPAA mode on. Triage Intelligence falls back to its heuristic, loop descriptions and output contracts are not read, comment actions and delegated agent sessions check again when they run, agent-worker runs queued before AI was turned off are failed when claimed, and coding sessions refuse to start, continue, restart or be approved (`403`, code `aiDisabledByPolicy`) while the coding LLM proxy refuses each model request of a running sandbox. Media transcription (`transcribe_media`), web search and URL fetching were already checked on every tool call. `docs/configuration.md` lists every entry point, and a test fails when a new model call is added without the check.
- The workspace region is now what it says: a label declared at creation. `POST /api/workspaces` stores it in lower case and accepts only the listed regions; `PATCH /api/workspaces/{key}` refuses to change it (`400`; sending the current value is still accepted). Settings › Workspace no longer suggests it controls data residency, and `docs/configuration.md` explains how to keep a workspace's data in a region: deploy Flow, its database, object storage and model providers there.
- The audit log is an append-only, tamper-evident table with time-based retention. Entries are written in the same transaction as the change they describe, numbered per workspace and hash-chained; `GET /api/workspace/audit-log/verify` (admins) and `server audit-verify` (operators) report any entry that was changed, removed or inserted. Instead of the latest 1,000 entries, the log keeps 180 days by default (`FLOW_AUDIT_LOG_RETENTION_DAYS`, `0` keeps everything), pruned oldest first by the maintenance pass; a purged workspace's entries are kept until they age out the same way (see [Workspace deletion](docs/operations.md#workspace-deletion)). Entries now carry `actorType` (user, API key, OAuth application, SCIM, agent or system), `actorId`, the client address (truncated by default, `FLOW_AUDIT_LOG_IP`), the browser and OS, `result`, `seq` and `hash`, and never keep password, token, secret or message body fields. New events: sign-ins (successful and refused, per workspace of the account or the instance log for unknown emails), sign-outs, session revocations, password changes and resets, passkey enrolment and removal and second-factor checks, API key creation, rotation and revocation, admin role changes, OAuth application grants and revocations, export and migration bundle downloads, and every write the Flow Agent makes with Flow's tools. `GET /api/workspace/audit-log` keeps its response and filters, adds `format=csv`, and returns the newest 1,000 matching entries as an array. Existing entries move into the table on upgrade (data migration 1030), the audit log stream continues from where it stopped, and the bootstrap no longer embeds the log (the Audit log page reads the API).
- Personal data rights. **Download your data** (Account › Security & access › Your data, or `POST /api/account/data-exports`) prepares a zip of JSON files with your profile, sign-in methods (no secrets or identity provider claims), sessions and devices, linked chat accounts, personal and code host connections (no tokens), API key and passkey metadata, authorized applications, settings, notifications, search history, recently viewed, your private Flow Agent chats, and the issues, comments and documents you created that you can still access; only you can download it, for 7 days, at most 3 requests a day, and each request is audited. **Delete account** (Account › Security & access › Danger zone, or `DELETE /api/account`, confirmed with your email) deletes your personal data in every workspace at once and keeps the content you wrote, shown as "Deleted user" (same id, no name, email or avatar; mentions too; stored copies of your name and email in records and retained events are scrubbed, and the audit log keeps your entries under the pseudonymous identity without your name, email, address or browser while its hash chain still verifies, each rewrite recorded as a `personal_data_redacted` entry); your export archives are deleted with the account, and a purged workspace deletes its members' archives; it is refused while you are the only owner or admin of a workspace. Workspace admins can **Erase personal data** of a current or former member (Settings › Members, or `POST /api/workspaces/{key}/members/{userId}/erase`): an account that belongs only to that workspace is deleted as a whole, otherwise only that workspace's data is erased and the account can no longer join it. SCIM `DELETE` still only deprovisions. See [docs/privacy.md](docs/privacy.md). Database: schema migration 24 adds `personal_data_exports` and `auth_users.deleted_at`.

- The Flow Agent can file requests from a Lark or Feishu group: in a bound group that takes requests, ask it (`@Flow 把上面这条提成需求`, `@Flow file this as a request`) and it writes the request from the conversation, shows it on an approval card in the thread that only you can approve, and then files it exactly as `@Flow 提需求` does: as you, in the team's triage, with the same request record, link back to the group, thread and confirmation card. It counts as a write in the write ledger on both agent runtimes, and is offered only in such groups: not in direct messages, on the Agent page, in unbound groups or groups without request intake. Flow's public MCP tools are unchanged.

- Talk to the Flow Agent in Lark and Feishu groups: mentioning the bot in a bound or unbound internal group runs the agent as the member who asked, with exactly their permissions, limits, tool budget, write ledger and approvals, and answers in one streaming card in the mention's thread. Replies in that thread continue it (without a mention when the app has `im:message.group_msg`), each turn in its speaker's own Agent chat, listed on the Agent page as **Lark · *group*** / **飞书 · *group***. A bound group gives the agent its team and projects as the default scope; the agent also gets the thread and the group's recent messages as context when the app may read them. Approval cards in the thread can only be answered by the member who asked; link cards, workspace choices and notices stay private. A plain mention no longer makes a request or a help card (help and a bare mention still send the help card, `提需求` still makes a request), so the group binding editor drops the "Treat every mention as a request" toggle (the API still accepts `everyMention`, without effect), and the group help card says you can mention the bot to ask or get things done.

- Browser E2E expectations follow the round 3 inbox and integrations passes: the notification snooze list has seven presets (including "This evening") in a 306px panel with a 279px field, the actions menu shows two two-key shortcuts, and the featured integration card keeps its own mark colour; the snooze specs run on a fixed 09:00 clock.

- Team settings header: "Team overview" is a button (like the reference) with the reference's 6px/8px padding and chevron gap, and the settings sidebar search field is named "Search…" instead of "Search settings".

- Desktop parity round 4: the agent chat switcher popover is a flat run of 32px chat rows between 6px spacers with no padding (a lone group has no heading, "New chat" only shows while a chat is open, rows highlight only after the pointer or keyboard moves), and the team retire row reads "Prevent creating and updating issues in this team while preserving all historical data".

- A project connected to a Lark or Feishu group shows it in the project sidebar: the group's name (or "N groups" when several are bound) instead of "Connect group", refreshed when the group dialog closes; revoked bindings no longer count.
- The issue list filter value menu rounds its content-sized width up to a whole pixel, so the widest label no longer loses a fraction of a pixel to its ellipsis.
- Desktop parity round 3 for issue pages and lists: the sidebar Current and Upcoming cycle links carry the row padding and column gap on the row itself, the issue list filter value menu rounds its content-sized width to whole pixels, the bulk action bar count uses regular weight with a tabular number, and an app-authored comment's "via <app>" continues the name in the 500 weight and tertiary ink.
- Third desktop pass on the agent area: the Agent home keeps the starter examples above the recent chats until three chats exist (the chat list now follows the examples in the page), the Loops run views (Overview and Run history tabs, run rows) are real links with the reference's 0.5px pill border, the Loops table headers sit flush with their columns and read "Runs (30d)", inline code keeps its text in an inner span with the reference's -0.1px tracking (a size-adjusted face scales the system monospace to the reference's 0.6em advance), italic text uses the real Inter italic face, the coding session bar and chat header sit at the reference offsets, session prompt avatars and the agent avatar slot use the shared avatar metrics, issue activity shows the session card at the width of the comment composer, and a hovered sidebar section header (Workspace, Your teams) turns white on a raised row.
- Third desktop pass on inbox, shell, settings, projects and documents: the notification snooze list offers "This evening" (18:00, until 17:00), is 306px wide, opens with its first preset highlighted and keeps the parent "Snooze" row lit; the inbox actions menu is 181px wide with 6px spacer rows, offers "Delete all read" (⇧⌫) and only lists "Mark all as read" while something is unread; the Add filter menu is 207px wide, opens with no row highlighted and is no longer modal, so the toolbar button keeps its hover fill while it is open; the workspace menu is a fixed 225px with the reference's 8px/6px label, shortcut and chevron spacing; the integrations feature card uses a light illustration-style backdrop with dark copy; a new project view's card floats 8px inside the frame, its breadcrumb separator and Copy URL button match the reference, the timeline layout carries Today and zoom in the view toolbar (the Insights toggle stays on the saved-view toolbar), and timeline bar names are 13px/500; the document history list keeps the reference's 23.337fr share.

- Third desktop pass on inbox, shell, settings, projects and documents: the notification snooze list offers "This evening" (18:00, until 17:00), is 306px wide, opens with its first preset highlighted and keeps the parent "Snooze" row lit; the inbox actions menu is 181px wide with 6px spacer rows, offers "Delete all read" (⇧⌫) and only lists "Mark all as read" while something is unread; the Add filter menu is 207px wide, opens with no row highlighted and is no longer modal, so the toolbar button keeps its hover fill while it is open; the workspace menu is a fixed 225px with the reference's 8px/6px label, shortcut and chevron spacing; the integrations feature card uses a light illustration-style backdrop with dark copy; a new project view's card floats 8px inside the frame, its breadcrumb separator and Copy URL button match the reference, the timeline layout carries Today and zoom in the view toolbar, and timeline bar names are 13px/500; the document history list keeps the reference's 23.337fr share.
- Second desktop pass on the shell and documents areas: the sidebar team menu opens 4px under its more button and the open trigger keeps the regular text colour, the create-issue composer only offers the Cycle chip for teams that have cycles turned on and names its property chips like the reference ("Change status", "Change priority. No priority is selected", ...), the team key in its header is bright, the document options menu is a fixed 264px list with spacer rows, document and team documents avatars use the white initials colour, the subscribers popover lays out the agent badge on the name's baseline, the document history dialog leaves a third of the free height above it and splits its panel 23:7, and the team documents toolbar row is 43.5px tall.
- Match the desktop issue page to the reference: the reply row under a comment is a one-line composer with attach and send buttons, comment times keep the lighter 13px ink and an 8px gap, an issue's first event reads "created", the right-click row menu lists "Remove" after "Mark as", menu rows keep their natural width so the widest row's shortcut keys sit on the reference's sub-pixel, the due date and remind me panels keep device-pixel positions and never wrap their widest row, menus built on the row context menu snap to whole pixels, the selected triage row brightens its meta line, and triage creator avatars use the 9px/400 initials.
- Match the desktop issue lists to the reference: board columns follow the workflow (Backlog first, then Todo, In Progress, Done) and list every sub-issue as its own card sorted with the others, the columns sit 348px apart, the display options panel leaves out the Creator chip on desktop, an unnamed cycle shows its number on the row chip, the filter submenu sits 1px further from its menu with the count text 0.25px lower and a bright marker on the row that owns it, the Documentation pill and the 180px-minimum row context menu use the reference surfaces, a folded list group keeps the 40px group pitch, a sub-issue row's parent crumb follows the title flush, and the new issue view defaults to Save to Workspace with a text separator in its breadcrumb. The empty Assigned tab shows the reference illustration with a Create new issue button, and the "No issue details" text sits in its own span.
- Paint initials avatars like the reference: a person, app or agent without a picture and a workspace without a logo get one of 108 colours (36 hues 10° apart at LCH lightness 40, 55 or 70, chroma 60) derived from their id, replacing the four-colour palette, so the colour is the same on every surface and two people with the same name keep their own. The profile, comment, Pulse, review, mention, presence, customer owner, AI settings and views page avatars that painted a fixed colour now use it too. New workspaces no longer store a colour, and a data migration clears the colour every workspace used to be created with (`#5E6AD2`); a colour set through the API still wins.
- Keep the desktop display options panel, the document info and subscribers popovers and the notification snooze submenu on the device-pixel position the layout gives them (half pixels at 2x) like the reference, which only snaps its menus and pickers to whole pixels; the triage panel's command buttons sit at the end of its 44px header.
- Match the desktop sidebar team menu and workspace menu to the reference: menu rows use an 8px icon gap, "Leave team…" has no icon column, the open team row's more button keeps no extra fill, and both panels have no padding of their own (6px spacer rows open and close the list).
- Match the desktop keyboard shortcuts panel to the reference: the title is a content-sized heading centred in the 24px row it shares with the close button (half a pixel lower), keeping the 71px search start.
- Match the desktop command menu to the reference: the query row and the list lay out at the panel's full 720px border-box width (rows and shortcut hints sit 1px further right) and the list is the panel height minus the 46px query row.
- Desktop Reviews onboarding: the secondary pills (Learn more, Connect GitLab) use the toolbar pill fill and carry no ring in dark, like the reference.
- Desktop Pulse view tabs: tab labels use the normal line height so the text sits at the reference offset inside the 28px pill.
- Desktop inbox filter menu: the Add filter menu is modal, so toolbar buttons behind it no longer take hover while it is open, and the shortcut key hint uses the reference border colour.
- Match the desktop agent area to the reference more closely: the Loops editor title is 24px/600 and its trigger sentence, Add connector and Team access controls use the reference ink, the date field drops its calendar icon and the interval field sizes to its value with a chevron, the loop hub composer spaces its actions 12px apart, loop run tabs and lists draw their dividers as hairlines (so nothing shifts by half a pixel), the run page's "This Loop is disabled" chip uses the pill background, and the Loops list template cards show the trigger label and use the reference action labels (two new templates: "Update issue priority based on demand" and "Project hygiene"; the catalog now has eight).
- Give the session page's chat column the reference's drag handle ("Resize agent chat", 360px minimum, remembered per browser, ArrowLeft/ArrowRight to resize), keep its copy button 12px below the last reply, name the agent send button "Send message", let the agent chat switcher popover open 4px below its button with the reference's border and a 320px content width, and show inline code in issue text with the reference's 7.5% tint.

- Match the desktop documents area to the reference more closely: the document selection toolbar matches the reference ("Move selection to new document" now lives in the ⌘K command menu while the editor holds a text selection), an open subscribers popover keeps the bell highlighted, the history version rows and the comment composer (placeholder colour, 12px inset, send button overhang) use the reference's measurements, the documents list keeps a 2px gap above a group that follows rows and centres each document glyph in its button, the slash menu hangs 4px under its chip with 5px separator margins, and the insert menu draws the reference's glyphs. The document menu no longer shows a trailing separator when Delete is not offered.
- Follow the reference's issue-list defaults: a view that stores no group order (a new view draft, team lists) groups statuses In Progress, Todo, Backlog, Triage, Done, Canceled, while saved views keep the order they stored; the issue label picker lists labels already in use under "Frequently used" and the rest under "Workspace labels", puts selected labels first with a divider and no heading, drops the headings while searching, and starts its heading flush with the top of the list
- Rebuild the project's "connect Feishu/Lark group" dialog on the reference's project Slack window: a 700px dialog (13px radius, 20px padding) with the chat app's mark, title, the project's icon and name, a one-line description, a divider, a "Project notifications" switch section, the group picker right under it, the notification options as 32px checkbox rows, and the Feishu-only settings (team, default template, card language, group actions, requests) two to a line, so the form is short; it opens straight on the form when no group is connected yet, and is a near full-width dialog with native selects on phones.

- Fix the project's "connect Feishu/Lark group" dialog: its select menus opened under the dialog overlay, so the group, team, template and language options could not be clicked, and picking a team dropped the project from the binding's scope (a project dialog now starts on its only shareable team and keeps the project). The form is rebuilt in the project notification panel's system: each row has its label on the left and a 24px pill select on the right (described rows put the select under the copy), with the panel's checkbox rows, native selects on phones and real-click tests on desktop and at 390px.

- Match the desktop projects area to the reference more closely: the projects list uses the 48px Lead track, a centred header title and a 4px add-view gap; the display menu aligns to its button; the initiatives list uses the 6px column gap and toolbar button fill; the project header, properties panel, milestones header, project and milestone menus, activity composer and issue toolbar follow the measured geometry and colours; the initiative detail tracking and value colours match; and an empty cycles list shows the bare title.

- Match the desktop settings pages to the reference more closely: buttons inside cards and rows and select triggers use the settings control ink (and light-theme select triggers keep the same 30px height as dark), the Docs links end in the arrow with a 4px gap, the label pages header actions sit level with the title 8px apart, the members role column header and Export CSV, the teams grid columns, the notifications status dot, the preferences emoji sample, the integrations cards and the team hub title use the reference geometry.

- Move skills out of a dedicated button in the agent composer (Agent page, floating panel, loop run reply and the project creation assistant): the Skills button and the attach button are replaced by one "+" menu (File with its key hint, and a Skill submenu listing the newest skill first, with a search field once there are more than three and "Create skill" only while there are none), and typing "/" at the start of a message or after a space opens a Skills section (the newest four, or every match once something is typed) with Insert media and Attach files, plus a hover card for the focused skill. A chosen skill becomes a chip in the message, so a follow-up message can run a skill too (the server now accepts `skillIds` on a follow-up and adds them to the conversation); phones get the same menus as the shared command sheet.
- Rebuild Settings › Issues › SLAs on the reference's layout: a header with the Docs link, one card with the SLAs switch (and the work week while SLAs are on), then "Automation rules" as one card per rule reading "When Priority is Urgent / Then Add SLA of 24 hours from escalation" or "Remove SLA", with edit and delete buttons on hover (always shown on touch), drag or Alt + arrow keys to reorder, the rules greyed out while SLAs are off, and a rule dialog with conditions (priority, team, status, label, project), the action, the duration in minutes, hours, days or weeks, and calendar or business time. The rule list and dialog read in Chinese. A workspace that never chose keeps showing SLAs on when it already has rules, so the page and the SLA display option agree with what the server does.

- Give SLA automation rules an action and an order: a rule can now add an SLA or remove it, rules are evaluated top to bottom and the first enabled rule an issue matches decides its SLA (the way the reference set works: Urgent and High get a deadline, Medium, Low and No priority have it removed), an issue that matches again after its SLA was removed starts a new clock, new rules go last, and `POST /api/sla-rules/reorder` saves a new order. Rules saved before this keep working as rules that add an SLA, but an issue that matched several of them now follows only the first.
- Match the desktop issue lists, boards and views to the reference: list headers keep their insets on one inner row, group titles and the bulk bar use the reference's text colour, selected rows tint their date, board columns leave 8px under the sticky header, and the Views page and triage list use the reference's insets and control backgrounds.
- Open the filter menus with no highlighted row (arrow keys or the pointer highlight one), keep a hovered row's chevron muted, keep the Display options and Add filter buttons highlighted while their menu is open, and tighten the display menu's section captions and the filter value rows' count hints.
- Tidy the new-view editor to the reference (dim name placeholder, 10px resource tabs, 90px header block, create button colour), and add a New issue action to the My issues header on desktop.

- Offer "This evening" and "In 3 hours" in the issue Remind me menu at any hour before 17:00 and 15:00 (the reference shows them at 00:22 and 05:43 too), in the order of the reference list.
- Size the issue options menu and its submenus to their rounded content width (no fixed 318px Remind me or 232px due date width), so key hints and dates line up with the reference.
- Open desktop menus, pickers, popovers and tooltips on whole pixels (the nearest pixel of their unrounded position) instead of half pixels at 2x, as the reference does.
- Set desktop description text at the face's automatic optical size instead of a forced 14, which made links 0.3px wider than the reference.
- Give the issue's parent link the reference's transparent half-pixel border so its identifier and title sit 0.5px further right.
- Size the property pickers' search shortcut keycaps with a 1.1 line height (17.1px tall) like the reference.
- Match the issue sidebar's empty label and project triggers to the reference: the button keeps the primary ink with only its placeholder muted, 6px between icon and text, the hover and open background of the other triggers, the same left edge (so the labels popup opens 1px further right), and the accessible name "Add labels".
- Space the issue header's identifier from its title by 0.43em (5.6px at 13px) instead of 6px, which moves the favourite and options buttons 0.4px left.
- Start the issue activity rows 0.5px further left (the whole row, text included, rather than only the icon), so relation links and times line up with the reference.
- Draw avatar initials in plain white instead of the theme's accent label colour, as the reference does.
- Say why a loop's outputs could not be read: the "Couldn't determine outputs automatically" notice in the loop editor and on a run now adds what happened (the model returned only reasoning, the reply hit the length limit, an empty or withheld or unreadable reply, a tool call written as text, outputs in the wrong format, no model configured, or a failed or timed-out request) and what to do, in English and Chinese. The loop API serves it as `outputContract.fallbackReason` (`class`, `explanation`); contracts saved before show the generic notice alone, and no model output or reasoning is ever shown.

- Make the Helm chart's `config.file` `server:` settings take effect: for a variable the chart sets itself (`config.appUrl`, log level and format, database pool, Redis, S3, chat platform, coding values via `server.env`, ...) the chart now writes the file's value instead of its own, while `config.env`, `extraEnv` and secrets still win over the file. Settings that follow from the Pod (`httpAddr`, database and storage drivers, Redis mode, image and volume paths, the trusted CA file) fail rendering with the chart value to use instead. Deployments without `config.file` render byte-identically, and the example config now works as `config.file`.
- Let the config file's `defaults.workspace` set a new workspace's workflow statuses (`workflowStates`: name, type, colour, description, position, default, and the reserved duplicate status) and its labels and label groups (`labels`, `labelGroups`), shared by its teams like the built-in set. Every status type must be present, names are unique, colours are `#RRGGBB` (or `lch()` for statuses), and errors name the file, line and key; existing workspaces and teams created in them keep their statuses and labels. A new team now starts in its workspace's default status instead of always Backlog.
- Add `FLOW_DEFAULT_LANGUAGE` (`en-US` or `zh-CN`), the environment form of the config file's `defaults.user.language` for deployments without a file: it sets new members' language and the sign-in pages' default, accepts only supported languages, and wins over the file.
- Match the desktop sidebar's help button to the reference: a 25 by 24px quiet button with 8px radius and 4px side padding at 15px from the sidebar edge (the main and settings sidebars alike), in the muted sidebar colour in both themes instead of a circled outline.
- Keep the team issue list header's New issue action 8px from the frame edge instead of 7.5px.
- Centre sidebar team names in their 28px row by their own line box (0.25px lower), and size the editor selection toolbar's shortcut keys with a 1.1 line height so each key is 21.1px tall.
- Show the reference's hover hints on issue list group headers (T with "to collapse group" and Option-click with "to collapse all" on the chevron, "Add issue…" on the plus), make T collapse or expand the group under the pointer, give the Insights button a tooltip, and size the Time in status hover card (9px/8px padding, tooltip colours, 8px below the status icon) like the reference.
- Match the agent skill menus to the reference: right-click and the "…" button now share one 175px menu (Edit, Move to team, Use in chat, Delete) with a team submenu that moves a personal skill to a team, 68px skill rows with the 4px title gap and 24px menu button, and the "Discard changes?" dialog with Cancel and Confirm when leaving an edited skill.
- Match the composer's skill list to the reference's skill menu (plain rows 14px in, 175px wide with the submenu shadow) and keep "Create skill" for when there are no skills, and let the settings sidebar link read "Join or create a team" while there are teams to join.
- Match the Lark/Feishu group dialog and the project notification panel to the reference's notification panel (334.21px wide, 8px radius, 16px sections split by .5px dividers, 12px titles and options on 14.5px lines, 14px checkboxes, 24px pill buttons that grow to 28px on touch screens), make the dialog's notification options that panel's checkboxes, show the chat platform's mark in the panel's chat row, open the person card from an issue activity avatar above it with a 32px avatar and 400-weight initials like the reference, and drop the avatar's inset ring inside the card.
- Pick avatar initials like the reference: a single name shows its first two letters ("Hui" is HU), two words show one letter each, three or more show the first word and the last two ("Flow QA OAuth app" is FOA), and an email uses its local part.
- Size three-letter and "WW" avatar initials like the reference's avatar tiers (three letters 4px at 14px, 5px at 16, 18 and 20px, 7px at 24px, 9px at 32px; "WW" 5, 6, 7, 9 and 12px) instead of squeezing them to fit the avatar.
- Start team and workspace issue lists and new view drafts ordered by Priority (Manual stays the board default), show triage issues in a new view draft, and keep a draft's display from reading or overwriting the All issues display.
- Fix the project and initiative Lark/Feishu group dialog: selects and switches now render as Flow's shared select and switch (no oversized chevrons or square checkboxes), rows follow the settings layout, and the form scrolls inside the dialog on desktop and phone.
- Match avatar initials to the reference: one size-driven font size for every avatar (8px at 14px, 9px at 16, 18 and 20px, 11px at 24px, 14px at 32px) and a weight inherited from the row instead of a bold 600 or 500, so issue replies, property pickers, settings lists and panels read the same everywhere.
- Read an optional YAML configuration file (`FLOW_CONFIG_FILE` or `--config`, mountable from a Kubernetes ConfigMap) with a `server:` section for the non-secret server settings and a `defaults:` section for new workspaces, teams and members: language, team estimates, cycles, triage, auto-close, workspace feature switches and permissions, theme, home view and notification defaults. Environment variables still win over the file, defaults apply only to objects created afterwards, and unknown keys, wrong types, unsupported values and secrets stop startup with the file, line and key. The sign-in pages use the configured language for browsers that have not chosen one, and the Helm chart renders `config.file` into its own ConfigMap mounted at `/etc/flow`, rolling Pods when it changes. The server now embeds the time zone database, so IANA time zones validate in the minimal container image too.
- Align the desktop settings sidebar's "Back to app" link: it sits 0.25px higher and the chevron keeps its own 6px gap, so the link is 1px wider and the search field below stays put.
- Draw the saved view details panel cards with one real border and the shared card shadow (no double outline in the light theme), match the reference tab strip and row geometry, and show deactivated, SCIM-provisioned or unknown assignees by name or "Unknown user" instead of their raw IDs.
- Default the Workspace sidebar to Initiatives, Projects, Views and Loops with Customers, Members, Releases and Teams under More (saved orders are kept), and make sidebar rows colour text and icon alike at rest, hover and current page in both themes, with keyboard focus drawing only the ring.
- Match the sidebar More menu's rows, glyph slots, muted icon colour, hover, separator and light-theme border and shadow to the current design, use one filled glyph family for Asks and Customize sidebar, and keep Customize sidebar on one line.
- Refuse SAML responses whose assertion lacks a Subject, a NameID in a user format, a bearer confirmation or Conditions, comes from another issuer, uses the artifact binding or carries SAML elements in a foreign XML namespace, so a malformed signed assertion can no longer crash the sign-in handler or name a different user (GO-2026-6909).
- Organize team settings into Issues, Projects, Workflows, and AI sections with live counts and direct hierarchy and estimate settings links on desktop and phone.
- Check the phone Documents display popover's exact height after the shared property-chip spacing correction.
- Preserve fractional content insets in phone issue action sheets when the browser rounds hairline borders.
- Wait for the filter submenu's opening animation before checking exact avatar glyph containment in browser tests.
- Show uploaded pictures and application icons, not just initials, for team members, audit log actors, dashboard owners, document collaborators, project update authors and your own profile picture.
- Report a loop output-contract read whose model reply held only reasoning, nothing, or cut-off arguments by that class instead of a JSON error, retry it once with a class-specific hint, and keep the class in the fallback contract.
- Accept signed Feishu and Lark card clicks whatever unit their request timestamp uses, check the signature before the timestamp, name each timestamp refusal with safe diagnostics, and answer a refused card click with an error toast instead of an HTTP error.
- Preserve parent-trail line boxes and shared date insets in phone issue cards.
- Align phone review author names with the shared identity row's text track.
- Preserve the empty description line when centering phone OAuth application counts.
- Guard phone inbox filter and action glyph insets across both themes, languages and phone widths.
- Verify phone due-date submenu typography and exact issue-options search insets.
- Keep phone browser assertions aligned with complete inbox content tracks, the issue header hairline and natural review control text widths.
- Preserve the document directory's inner team-name weight on phones.
- Require the phone project Milestones section's exact position after the properties margin correction.
- Show each person's uploaded avatar or own initials colour, and each team's configured icon and colour, throughout filter menus, filter chips and people action menus on desktop and phone.
- Preserve the empty description slot in phone webhook rows.
- Match phone code-chip line boxes with a local monospace fallback that preserves the primary font's ascent and descent.
- Use the compact phone gap between group estimate icons and totals, replacing the cycle-only value offset.
- Include the member group divider in phone label centering.
- Align phone display tabs and document property gaps with their shared control insets.
- Use shared sidebar section spacing above phone team rows.
- Remove fitted phone activity offsets and restore subscription-button padding.
- Center phone sub-issue progress within the header's explicit grid track.
- Match phone issue-title paragraph tracking.
- Include the phone issue header's hairline in its box while retaining the content origin.
- Reserve the same border space in every phone cycle metric row.
- Preserve inline text baselines in phone issue summaries and document subscriber badges.
- Retain the transparent inner frame around phone initiative updates.
- Match phone label controls' hairline borders and intrinsic text alignment.
- Preserve the minimum name-column widths in phone member and label tables.
- Keep phone browser checks aligned with searchable Team moves and exact document subscriber-chip insets after entry animations.
- Wait for the agent composer's initial focus before testing the global command shortcut, so lazy page loading cannot steal the intended test focus.
- Center the phone composer's create-more controls on their text line.
- Align the phone composer team icon slot while preserving the chip's text spacing.
- Fill phone priority picker icon slots without shifting their centers.
- Keep phone command, help and property sheet content inside the hairline inset with consistent search widths and list padding.
- Wait for the phone context menu's empty state to clear before checking that its unfiltered rows return.
- Measure subscription and issue property sheets after their entry animations finish, preserving exact browser geometry assertions.
- Check desktop tooltip keycaps on both Mac and Linux, preserving exact spacing while waiting for the tooltip's opening animation.
- Run the phone inbox action geometry check at the strict phone gate's native scale and require exact content widths after the shared sheet sizing correction.
- Write Feishu and Lark personal notifications from the notification itself in the member's Flow language, with display names, the team's state names and times in the member's time zone; message-shortcut forms, request notes and group help no longer mix languages.
- Choose a group binding's **Card language** (English, Chinese or the platform default) in the group binding editor.
- Redesign Feishu and Lark link previews: compact cards per link type (issues and comments, projects and updates, initiatives, documents, cycles, teams, views, the workspace) in the viewer's language, a neutral "Flow link" line for anything the viewer or group may not see, and refreshed previews when the item changes.

- Accept Feishu and Lark card clicks in the legacy card request format as well as `card.action.trigger` (plain or encrypted, either signature scheme), so Assign to me, status, priority and Comment no longer fail with HTTP 401 when the app sends legacy callbacks.
- Recognize create commands in rich text messages and with full-width punctuation or no spaces, and log a secret-free reason code for every chat callback rejected or message ignored, and for every direct message answered without the Flow Agent.
- Read workspace metadata for chat workflows in process, so direct messages, card actions and group commands keep working in workspaces whose bootstrap exceeds the internal response limit; an internal failure now fails the job at once, tells the member and shows on the integration page.
- Show on the Feishu and Lark settings page which inbound message permissions the app has not been granted.

- Mirror comments written through MCP and agent tools (new comments, replies, edits and deletions) into Feishu and Lark issue threads once each: MCP comment writes now record the same events as the REST routes of their resource, on that resource and naming the comment.
- Show the update itself on Feishu and Lark project update cards (health, author, posting time in the team's time zone and the body as card markdown with a link to the update), redraw the card when the update is edited and retire it when the update is deleted.
- Write Feishu and Lark group cards, summaries and comment sync messages in one language, the group binding's (`language`, en-US or zh-CN) or the platform's default, and name people by display name.
- Preserve shared phone sheet geometry while matching labels at the strict gate's native hairline scale; cover panel and notification content positions.
- Retain capital spacing and tabular numerals in phone triage identifiers.
- Align phone breadcrumb glyphs to the text baseline and center status glyphs in their slots.
- Center phone milestone and composer controls in complete rows while retaining adjacent spacing.
- Use complete icon slots and consistent spacing in phone project controls.
- Match the phone team heading weight to the surrounding header labels.
- Preserve phone document filter-chip borders and full menu content insets.
- Keep the phone document history title and restore action on their natural flex rows.
- Align phone document subscriber checkbox and badge columns with their text.
- Include the separator hairlines in phone document metadata row flow.
- Keep phone filter and subscriber search content at the viewport inset width inside hairline borders.
- Preserve the inline text baseline of counts in phone filter rows.

- Align phone review metadata rows with the byline gap and shared icon insets.
- Align phone review narration code chips to the surrounding text top while preserving the panel's hairline insets.
- Let the phone review readiness action size itself from its full label.
- Preserve phone heading and row text tracks when font metrics round to half pixels.
- Keep phone review counts aligned with tabular digit metrics.
- Anchor the phone notification copy strip below its full header height.
- Use symmetric phone inbox filter insets and account for hairline borders in notification action content sizing.
- Keep the phone review agent header and transcript inside the panel's hairline border.
- Keep browser checks aligned with the shared desktop toolbar palette, Auto-closed filter inventory and composer position, using an isolated workspace for shell commands.
- Offer comment action tools in Agent chat, translate their Chinese step subjects, and synchronize the web tool fixture and exact MCP inventory contracts.
- Keep phone menu content widths independent of hairline border rounding, including notification action rows and dividers.
- Finish closing the desktop workspace menu and return focus even when an exit animation completion event is lost, preserving its divider and row spacing.
- Keep ordinary tooltips from blocking nearby controls or page shortcuts, retain the selected view-details fill with custom tooltips, and verify tooltip keycaps on both Mac and Linux.
- Restore shared phone picker content widths and consistent search/list insets while preserving Team filter row sizing.
- Cover the MCP count filter tree with a matching team predicate in the schema fast-path check.
- Give a favorited initiative star the removal label and tooltip, and cover the sidebar shortcuts, assignee profile links and filled star in isolated browser workspaces.

- Offer joining or creating a team beneath Your teams when no teams are joined, respecting team creation and membership permissions and translating the empty prompt.

- Share assignee profile cards across issue rows, boards and details, with visible profile and team links, live presence, known local time and out-of-office status; keep inactive people identifiable without active links.

- Show the sidebar Issues shortcut card on hover and keyboard focus, with shared tooltip styling and pointer dismissal.
- Align desktop issue filters with the toolbar popovers and end constrained filter menus at a partial row to show that more options can be scrolled into view.
- Keep Auto-closed available in the ordinary desktop issue filter menu.
- Use the title editor's optical font size for skill names and their placeholders.
- Protect unsaved skill edits when leaving through navigation, browser history, or a reload.
- Open the same Edit, Use in chat, and Delete actions from a skill row's right-click and button menus.

- Give shared desktop toolbar icon pills their measured resting, hover and open fills separately from text tabs.
- Size the desktop issue document and property rail with the shared container grid, so their gutters and columns adapt together as the pane changes width.
- Keep the labels sheet's half-pixel content alignment across browser scales without widening other phone menus.
- Move issues from the desktop or phone Team property, issue options, row menus or the shared Command-Shift-M shortcut. Search team paths and keys, show the current selection, hide forbidden destinations, and confirm moves with the destination name and updated identifier.
- Move issues from the desktop Team property, issue options, row menus or the shared Command-Shift-M shortcut. On phones, change the team from issue options without adding a chip to the property row. Search team paths and keys, show the current selection, hide forbidden destinations, and confirm moves with the destination name and updated identifier.
- Remove an issue's project and milestone on a team move when the project does not include the destination team; retain projects shared with that team.
- Put open issues moved by a non-member into the destination team's triage, while retaining closed states and the existing team-move event path.
- Reset phone drawer navigation when focus returns to its pinned header, including on reopen, while preserving the hardware-keyboard skip link.
- Show the shared computer artwork on the unavailable Shortcuts page and center not-found content without the regular settings top padding.
- Give pale workflow status icons their own light-surface lightness ceiling while keeping saved colors and the project palette intact.
- Align the document subscribers popover with the header's half-pixel button geometry.
- Use the shared 13px date-command typography in the phone issue options Due date submenu.
- Cover phone parent issue rows with labels and sub-issue counters at both phone widths, themes, and languages, including the avatar inset and full pill containment.
- Match label picker checkbox and dot geometry, reveal empty checks during keyboard navigation, and let phone picker rows and composer search fields fill their shared surface.
- Wait for the label picker's opening animation before hovering its rows in the shared menu browser checks, preserving exact phone geometry assertions.
- Suggest next steps on issue comments with Flow Agent. Personal Agent settings control suggestions; editors can accept status, assignee, priority, label, sub-issue and link proposals or dismiss them. Acceptance records normal issue activity and resolves once across concurrent clients.

- Match the phone issue property sheets to the shared sheet: 13px rows, a heading chip that names the issue ("Assigned users" for assignees), cycle rows with their dates and state, "Workspace labels", the static status glyphs and a due date list that opens as a sheet with the cycle ends. Also draw the not-found page with the phone header and drawer button in the interface language, send `/settings/account` to the profile, show the Documents crumb on phone team documents, fill the project details toggle while the panel is open, translate the "search" tool row and read chat history times in Chinese like every other relative time.
- Show Agent personalization skills as name and created/updated rows with a row menu (Edit, Use in chat, Delete) instead of raw Markdown, and open each skill in the shared rich-text editor so headings, bold, lists, code and links render and save back as Markdown with their line breaks intact; the skill page asks before discarding edits and matches the phone layout.
- Add searchable multi-team search filters, editable chips, shared links, and Chinese copy, and keep long picker values and team keys within their rows.

- Validate exact Team predicates in issue queries and support the same filter trees in MCP issue list, count, and search, with team-scoped workspace search results.

- Verify compact composer and clipboard toast styles at the shared 880px boundary, and isolate the desktop route matrix by viewport so its full coverage does not share one test deadline.
- Check unsafe translation placeholder replacements per source file so the full repository scan stays within individual test deadlines in coverage runs.
- Require archived issues to be restored before changing their content or properties, make their editors and property controls read-only, and offer Restore in issue options.
- Match the desktop project timeline to the reference: the axis uses 11px labels centred on weekly ticks (every other week is numbered once ticks sit under 35px apart, so Quarter zoom no longer crowds) with month and week tick marks, month names that no longer stick to the pane edge, no rule under the axis, dashed month lines through the axis and a fading today line; a bar's name now slides along its bar clear of the names column so a long-running project stays readable; each project row shows its health, status, priority and lead buttons after the name; the names column covers the whole pane; bars use the measured fill, border and shadow; the priority icon bars sit 1px higher.
- Match tooltips to the reference: 28.1px surfaces with the key caps 8px after the label and 5px from the edge, labels at weight 450, shortcuts in title tooltips drawn as key caps without wrapping, sidebar link tooltips beside the row ("Go to inbox" with G then I, the team's name with Ctrl ⇧ 1), and tooltips on Add Filter, Show display options, Open details, the favorite star, notification settings, New issue, the team crumb, Help, the Agent dock and a row's priority and dates. Keep the agent session chat at 560px wide, anchor the agent error row's icon and Retry to its first line behind a styled Details toggle, and keep the issue chip's identifier on one line in the floating agent panel.
- Fix uploaded OAuth application icons not loading with authentication on: an uploaded icon now records the workspace and admin that own it, is readable by the members of the application's workspace and of the workspaces that installed it (any signed-in account for a public application, whose consent screen anyone may see), and is served inline with its upload's media type, `nosniff`, a sandboxed no-script Content-Security-Policy and `Cross-Origin-Resource-Policy: same-origin`. Application logos under `/uploads/` must be icons their own workspace uploaded, and the consent screen shows uploaded icons.
- Fix uploaded workspace logos not loading with authentication on: a logo records its workspace and is readable by the workspace's active members while it is the workspace's logo; invitation and invite-link previews return it as a 15-minute signed URL for people who have not joined yet, and `logoUrl` accepts only the workspace's own uploaded logo. Icons and logos uploaded before owners were recorded get an owner at startup from the application or workspace using them.

- Match the desktop issue list to the reference: one identifier column across the whole list, the Cycle glyph on the Cycle and Added to cycle filters, right-aligned submenu markers, muted row context menu glyphs with fixed Status, Due date, Move and Unsubscribe shapes, display options that start with Show matching nested sub-issues and Due date, Cycle and Pull requests on, leave out Cycle, Estimate, SLA, Release and My activity date when the issues cannot have them, and sit on normal line height with the reference's bottom padding, a divider after Settings in the workspace menu, the 750 by 262 create issue composer on the reference's 12px insets, and the Activity heading 18px under the sub-issue list.
- Verify team sort announcements in both directions, hold the actual subscription save request while checking disabled menu states, and assert the phone startup shell geometry independently of the desktop sidebar.
- Fix regressions: restore the phone team settings page padding and width, keep empty columns on project, cycle and My issues boards in the Hidden columns panel unless "Show empty columns" is on, list the phone cycle figures as label and value rows so "Days remaining" no longer wraps, insert names containing `$&` or `$'` into translated text as written, space the inbox row Snooze key and submenu triangle 6px apart, draw the workspace menu submenu triangle like every other menu, keep the issue composer title at the measured offset without padding, and restore the 120px Lead column on the projects list.

- Keep the desktop sidebar and page controls docked above 880px, using one shared CSS and React layout boundary so 1024px no longer mixes a drawer with desktop controls.
- Give light-theme project property pills the same shared mobile fill and border as sibling issue properties.

- Keep phone issue labels rounded and ellipsized within their slots, and preserve Error session pills and full assignee avatar clusters in My issues and team lists.

- Wrap long project names in the editable overview title and commit with Enter or blur.

- Keep Issued keys descriptions inside their card on phone and dim the default home-view suffix in the open menu.

- Check the desktop label picker's exact 60px text offset after its opening animation finishes.
- Share 1px keyboard focus rings across controls, with inset navigation rings and spaced checkbox and toggle rings.
- Expose Skip to content to hardware keyboards on phones, with Enter moving focus into the main content.
- Start the slow workspace hint's 0.8s fade 8.8s after DOM readiness and preserve its clock when React takes over the startup screen.

- Show cycle dates in the interface language, use tabular numerals for cycle percentages, align the cycle page breadcrumb and item list with the directory, keep the phone cycle insights panel inside the viewport, translate the Loops template cards, and stop partial translations of sentences that end in "issue views".

- Align browser regressions with label checkbox opacity states and searchable phone subscription submenu navigation.
- Show an Offline pill in the sidebar header in place of the workspace name while the browser is offline, draw the first-load surface as the app shell (blank sidebar column, main card and the Flow mark) instead of a full-window splash, and add a "Skip to content" link as the first Tab stop on desktop.

- Give primary buttons a hover fill (Create issue), stop disabled comment buttons from painting hover, and draw sidebar, header and bottom-bar focus rings as a 1px accent ring (none on section toggles and team rows).
- Show whole-character initials for emoji-led names, colour every person avatar by user id, truncate long assignee names in the issue property column, and mention people written next to CJK text or with one-character names.
- Match the desktop issue board to the reference: card and pill fills, hover fill and default cursor, a sticky column header over a column panel, sub-issue progress as an inline pill, the shared column menu surface, no top padding, and rows that fit their content.

- Match the issue display menu to the reference: wider layout tabs, flat controls, the 10x23 select chevron, tighter section captions and an 8px-inset footer; the Groups section is phone-only.

- Restore the submenu triangle stylesheet so the triangle is right-aligned in every shared menu, and show the phone Display dot only when the options differ from the view defaults.

- Open search date presets before a custom date dialog, with two desktop calendars, typed periods, all five precisions and draft Apply or Cancel actions.

- Apply issue-menu surface tokens at the shared component rule so phone shadows and rounded submenu headers remain intact.

- Recompute shared search date ranges when fiscal settings arrive and discard invalid date chips with their requests.

- Keep the project date picker at its full calendar height, fit all seven weekday columns inside its padding, and keep phone popovers within the viewport.

- Share English and Chinese typed-date parsing across snooze, reminders, due dates and command pages, preserving IME confirmation.

- Use the same first-weekday preference for Pulse groups and calendars, including Tuesday through Friday.

- Restore the desktop issue-options due-date submenu width and hidden shortcut, and size search date filter menus to their content with date precisions on one row.
- Use the shared settings search magnifier and match its visible icon fill in both themes.

- Keep the shortcut search's neutral border and restore its accent focus outline.

- Match the desktop chat history icon to the sidebar navigation foreground.

- Restore the layered gradient outline on the phone agent watermark.

- Balance agent starter descriptions and use the shared Loops glyph.

- Restore the issue title baseline and dim the empty phone assignee chip.

- Align the command input hint and use the matching issue and label command glyphs.

- Align keyboard shortcut rows and the search caret with the shared panel typography.

- Align settings directory groups, resting sort arrows, search icons and search placeholder colors.

- Keep directory sorting on supported columns with consistent ascending and descending indicators.

- Match regular dark toggle thumbs to the shared 14px geometry without a shadow.

- Open phone command sheets without selecting the first row until pointer, keyboard or search input.

- Keep Chinese phrases whole: verb catch-alls no longer leave an untranslated noun in a translated sentence, and picker labels, retire-team copy, row dates and status group names read in Chinese. A coverage test now fails when a `t()` key has no Chinese entry.
- List cycles on a timeline on desktop, as on the phone: dates and dots on a rail, a Current/Upcoming status pill, a capacity ring and the scope in aligned columns, and the started cycle opened with its chart and the Scope, Started and Completed legend. Hovering the chart reads a day's figures.

- Rebuild the cycle details card around a Progress section (legend, chart, one set of breakdown tabs with how much of each row is done) and a Planning section, and show the cycle icon and a switcher chevron in the breadcrumb.

- Draw each weekend of a cycle chart as one hatched band in the track colour instead of one thin, bright stripe set per day, and put the phone cycle dates against the rail.

- Align the document metadata and subscribers popovers, the document history selection, the submenu triangle and row icons of the inbox and document menus, the snooze field, the inbox filter icons, review descriptions and phone review tabs, and the project update composer tab ring with the measured reference.
- Keep issue display selects on one line, show supported nesting choices, and hide unchanged view-default actions.
- Match issue filter and row-menu glyphs, restore the sub-issue activity divider, and keep pale status rings visible in light themes.
- Retain empty issue-board columns in the collapsible hidden-column panel, including server-backed boards.
- Restore project row hover and selection paint, respect ungrouped project displays on desktop, and reveal empty target-date controls on interaction.
- Match document heading interaction states and tinted groups, and use natural actor spacing and property-row geometry in initiative details.

- Restore avatar sizes and fit initials using measured text bounds, including wide Latin and CJK pairs.

- Match workspace, team, label and phone context menu interaction states, and complete Chinese navigation labels.

- Preserve phone issue breadcrumbs, wrapped project activity and composer spacing, and muted reviewer prompts.

- Restore review issue link-type choices and persist Resolves, Contributes and Related associations with each linked issue.

- Match subscribed document rows to the shared menu highlight color and phone inset in pointer and keyboard states.

- Translate command navigation and saved-chat feedback labels into Chinese.

- Use shared phone menu geometry and submenu navigation for issue, team, project and Skills controls.

- Restore shared inset menu highlights, chip hover feedback and perceptible button hover in both themes.

- Reserve separate label checkbox and icon tracks and reveal unchecked subscription boxes on highlight.

- Let filter value menus size to their labels within the shared maximum width.

- Keep small avatar initials on one line and preserve avatar proportions beside long member names.
- Share issue title and sub-issue label styling and remove special due-date menu sizing and hidden shortcuts.

- Use the shared phone menu surface for issue options and its submenus.

- Honor every first-weekday preference in settings and calendar controls.

- Add drafted search date filters with on/before/after comparisons, five date precisions, Cancel and Apply, using existing API range filters.

- Keep phone property submenus above their parent overlay so grouped project labels and Back remain tappable.

- Let team label headings wrap beside actions and keep their breadcrumb in normal phone layout.

- Place the phone team overview shortcut below wrapping team titles.

- Keep team member CSV export available on phones.

- Reveal label row actions on hover, keyboard focus and while their menu is open.

- Restore desktop display choices for release, SLA and personal activity dates.

- Offer Release in issue options before a workspace has releases or pipelines.

- Parse Chinese snooze examples and report invalid dates in both notification snooze menus.

- Keep review onboarding features reachable at narrow desktop widths.

- Separate the property and value in phone inbox filter search results with a chevron.

- Update desktop browser checks for plain board issue counts and the dedicated MCP connector settings route.

- Check numeric filter readability with realistic counts while allowing extreme values to ellipsize, and close only open filter menus in browser state checks.

- Repaint phone cycle timeline charts immediately when the resolved interface theme changes.

- Keep document names unchanged in subscriber chips when the interface language is Chinese.

- Let document action menus size to their command labels and inherit the shared menu row height.

- Restore People with access in phone document command menus for owners and administrators.

- Preserve rich text, hyperlink targets, and nested block structure when moving a document selection to a new document.

- Preserve unrelated document content when a selection changes during a move, track intervening edits safely, and keep the removal undoable.
- Preserve explicitly chosen team workflow colors across issue, composer and board glyphs, including choices matching legacy defaults.

- Fill the team favorite star when its saved selection is active.

- Retain tool targets and search details in expanded Agent work on phones.

- Restore the shared Display options keyboard focus ring on phones.

- Keep phone search calendars outside the clipped filter menu so every day is visible and clickable as soon as the calendar opens.

- Use light-theme backgrounds and ink throughout the phone settings drawer, including selected navigation and blurred search text.

- Route MCP connectors to a dedicated settings page with connector controls, selected navigation and a phone selector that stays below its label.

- Preserve authored email hyperlinks in Agent messages while leaving bare user email addresses as plain text.

- Show real board estimate totals, including complete server buckets and each team's unestimated-issue policy, while keeping issue counts distinct.

- Use the estimate triangle on phone board cards without fabricated cycle progress.

- Apply team summary selections as Team filters, repair earlier invalid selections, and retain matching issues after reload.

- Skip punctuation in shared workspace initials, preserving readable phone Settings avatars and whole CJK and accented characters.
- Keep desktop filter categories and numeric comparisons readable within the existing submenu width limit, expose complete selected filter values through tooltips and accessible descriptions, and distinguish pending disabled filter actions.
- Fix reviewed phone regressions across Inbox, Reviews, Pulse, projects and team Documents: compact filter searches and keyboard focus, canonical issue links, translated command search, functional review issue menus, persisted reactions and attachment uploads, state-colored icons, stable counts, readable long names and drafts, chronological activity months, light controls, single-line team tabs, visible menu focus and unclipped filter dividers.

- Check desktop issue-filter search visibility, non-overlap and filtering without a platform-specific input width.

- Let team owners manage their chat group bindings through workspace authorization, and use the documented Feishu PKCE token exchange with separate OAuth failure reasons, translations that survive dictionary loading, and secret-free stage diagnostics.

- Align desktop issue-filter menu width, window-edge spacing, search hint gap, and submenu overlap with the shared menu measurements.

- Keep workspace switches on their selected workspace by resolving home-page redirects only from that workspace's snapshot, with browser coverage for menu actions, shortcuts and Chinese avatars across desktop and phone layouts.

- Restore visible workspace-menu chevrons, inset rounded menu hovers and a bounded workspace switcher; keep Chinese and mixed-script workspace initials centered without clipping across shared avatars.

- Suppress repeated chat message shortcuts for a full elapsed minute, including taps across a wall-clock minute boundary.

- Guard phone triage age readability, non-overlap and alignment across the week-to-month text transition without platform-specific glyph widths.

- Align desktop project team controls and section spacing with the shared overview measurements.

- Keep the shared desktop project Lead track compact and stable when opening display options; match desktop document title metrics, let document action menus grow with translated content, and use the measured milestone row inset.

- Align desktop project header content insets and cycle breadcrumb separators with their visible chrome.

- Align desktop cycle directory contrast and breadcrumb spacing, keep open project action triggers unfilled, and clarify milestone submenu indicators.

- Match desktop project metadata, resource spacing, activity tabs, and issue-intro keycaps without changing phone layouts.

- Keep agent approval, change-state and feedback saves and release access-key bookkeeping off issue and discussion hydration.

- Keep migration preview/mapping/invitation bookkeeping, token-rotation results and sync-group decisions on metadata-only writes while preserving source bundles and audit entries.

- Save document attachments, intake address edits, application secret rotations and review writeback results without hydrating unrelated issue records.

- Save Loop output contracts in a narrow transaction over the Loop and its versions without loading issue or discussion collections; late model results still fill matching versions without changing the edited Loop or person-chosen contracts, and unchanged saves write nothing.

- Align desktop initiative overview heading insets, property icons, resource and reaction controls, and project toolbar open states while preserving phone controls.

- Keep phone Snooze, issue-row, Move to, and Labels submenus inside the viewport, with nested levels replacing their parent and a Back action; preserve desktop cascades.

- Keep the desktop project timeline grid and today line extended through the viewport when only a few projects are shown.

- Verify desktop text shaping, equal-width digits, and workspace-name containment without platform-specific glyph-width snapshots.

- Check desktop issue picker readability and search behavior across platforms without fitting tests to intrinsic input widths.

- Keep desktop issue project search on screen when a long project menu opens above its trigger, scrolling the options within the available space.

- Match desktop initiative directory column labels, title contrast, and update-count hit areas without changing phone styles.

- Correct desktop workspace-name optical sizing and issue-identifier capital spacing using the existing Inter font.

- Remove the unselected initiative column gutter from project lists and use the standard lead width while keeping display-property toggles aligned.

- Align desktop project timeline month labels, project icon slots, and the today marker while retaining timeline zoom and centering behavior.

- Match desktop issue team-selection groups and compact label-chip insets, and honor subscriber avatar images.

- Match desktop issue selected identifiers, bulk-command surfaces, summary-tab labels and open-picker states, and align view-editor action groups while preserving phone spacing.

- Align desktop triage row hit areas, toolbar gaps and empty-selection action surfaces while preserving phone rows.

- Align desktop project and initiative icon surfaces with the shared page icon radius, padding, and tint.

- Restore desktop issue-row subscriptions and keep relation removal after the standard action groups.

- Align desktop new-view header actions, icon surface and form foregrounds, and keep the applied-filter hairline at its measured tone.

- Keep desktop project and initiative rail surfaces and project metadata at their intended foreground and border colors while preserving custom theme palettes.

- Align desktop issue row-menu shortcut slots and submenu list insets, and restore the coding tools action icon.

- Align desktop initiative sidebar property columns, empty-value labels, and compact picker typography without changing phone controls.

- Match desktop issue-filter highlighted labels, submenu markers, shortcut padding and count insets while preserving phone pickers.

- Match the desktop coding-session copy control foreground to its shared text token.

- Match desktop issue identifier tracking and saved-view directory name contrast while preserving phone typography.

- Align desktop triage property chips and document insets, and preserve label and empty project control contrast while their menus are open.

- Keep desktop loop picker entity names at the primary foreground while retaining muted team keys and phone styling.

- Correct desktop issue parent typography, property insets, sub-issue icon sizing, and due-date submenu search presentation.

- Match desktop issue details-toggle fills and applied-filter borders and add-control padding without changing phone controls.

- Keep the selected property option highlighted when a picker opens or its options finish loading.

- Give desktop notification menus a separate list inset and snooze separator while preserving phone menus and dated snooze actions.

- Align desktop project and initiative description editing areas while keeping their text aligned with the content column.

- Let desktop inbox action menus size to their labels and shortcuts without a negative shortcut offset.

- Align desktop shell navigation insets, issue navigation targets and sidebar search tooltip weight while preserving phone styles.

- Use the muted desktop estimate placeholder when an issue has no estimate, retaining the selected-value and interactive foregrounds.

- Align desktop board identifiers, dates and parent-trail typography with the shared metadata foreground and measured separator size.

- Keep desktop inbox filter triggers transparent away from hover and give selected filter labels and icons the highlighted foreground.

- Align desktop issue activity and comment avatar typography without changing phone avatars or the user colour palette.

- Align desktop saved-view headings, metadata, favorite controls and summary tabs with their measured presentation.

- Align desktop member and team directory search fields, sort labels, role text, and filter spacing without changing phone controls.

- Match desktop settings docs links, row actions, security session text, and team overview copy to the shared settings typography and spacing.

- Align desktop integration discovery borders, category badges, feature copy and carousel controls, keeping navigation arrows outside the tab list.

- Align desktop property search-header spacing and shortcut padding while preserving phone pickers.

- Match desktop issue-board title contrast and linked team breadcrumb foregrounds without changing phone styles.

- Align desktop milestone menu text weight and highlighted text and icons with the shared action menu tokens.

- Record the desktop document parity closeout, with each residual comparison classified and coverage limits kept explicit.

- Scope the desktop document subscriber badge audit to its named user and measure metadata alignment in one animation frame, avoiding ambiguous badges and transient geometry failures.

- Align desktop issue-list disclosure controls, group and row text, and My issues toolbars while preserving the Insights action and phone layouts.

- Align desktop label table columns, sort affordances, search surfaces and editable label names without changing phone tables.

- Align desktop project display chips, list foregrounds and table captions with the shared panel surface, leaving phone styles unchanged.

- Match desktop Loop schedule sentence typography without changing phone controls.

- Keep desktop Pulse empty-state illustration, copy and actions in separate, consistently spaced groups without changing the phone view.

- Align desktop Loop foregrounds, category tabs, table gutters and run controls with shared surface rules, preserving phone layouts.

- Group desktop editor selection actions after the formatting divider, align slash-command and comment controls, and match document-list and history-author spacing while preserving phone layouts.

- Match desktop Agent example spacing and skill-menu outlines, and align coding-session prompts, copy controls and typography without changing phones.

- Match desktop board column gutters, status-icon sizing and hidden-column typography without changing phone boards.

- Align desktop document metadata, subscriber labels and history surfaces with their shared foreground and border rules, without changing phone layouts.

- Align desktop inbox frame and menu borders, shortcut labels and read-notification text with the shared surface palette.

- Align desktop issue document and property-rail inset ownership and the open options-button surface without changing phone rules.

- Keep desktop personal settings actions transparent and aligned, with a compact Customize label and descriptive accessible name.

- Match desktop issue reply placeholder typography and comment action padding without changing phone controls.

- Restore desktop Reviews onboarding content, preview and integration actions with Chinese translations while keeping the phone connection view.

- Match the desktop issue header foreground, create-button inset and toolbar spacing while preserving compact layouts.

- Keep desktop inbox header insets on its inner toolbar while preserving the title's text bounds and phone layout.

- Match desktop toast dismiss padding and muted foreground without changing phone controls.

- Match Agent starter examples to project planning, backlog research and automated Loop prompts, with Chinese translations.

- Align desktop issue creation control colours, title and team breadcrumb spacing, and icon-button padding while retaining phone styles.

- Match desktop command and shortcut-help keycap typography, group-heading padding and muted labels without changing phone controls.

- Add the desktop backlog introduction, scoped issue creation and documentation action, with Chinese copy and unchanged phone empty states.

- Use the measured desktop workspace-name typography and foreground without changing the phone workspace switcher.

- Use the shared muted foreground for desktop sidebar team headings while retaining the highlighted team-menu hover state and phone styles.

- Match desktop project sidebar labels, disclosure spacing and icon-button padding to the measured panel controls without changing phone layouts.

- Align desktop issue property controls with the shared property-rail inset without changing phone layouts.

- Restore issue identifiers in desktop activity links and align issue title tracking, sub-issue controls, subscriber surfaces and the floating command strip.

- Update browser inventories for expanded desktop shortcut help and the four default My issues tabs, while checking the shared view remains available.

- Keep the desktop M M shortcut on triage's duplicate workflow so it records the relation, changes status and advances the queue.

- Check desktop triage selection and document column alignment without sampled pixel positions or private browser emulation.

- Wire desktop entity-navigation pickers and issue keyboard controls, expand shortcut help without changing phone content, and send performance reports to a real support destination.

- Keep the desktop session list free of the extra Refresh action, move additional authentication policies below workspace management, and restore the API community link and measured intro spacing without changing phones.

- Restore desktop Reviews onboarding actions and title icon, and the measured Pulse empty-state artwork; retain phone layouts and both code-host connections.

- Align shared desktop sidebar colours, header inset ownership, icon-button padding and settings table controls while preserving phone metrics.

- Size desktop document metadata intrinsically and align its author columns, restore history preview spacing and action fill, and keep advanced insert blocks searchable without expanding the default document menu.

- Align desktop issue and view navigation, filter categories, and default display-property choices while retaining phone layouts and saved selections.

- Keep automatic similarity suggestions off desktop issue pages and triage peeks, and omit empty release actions without changing phone screens.

- Finish desktop Agent skill-menu sizing, interactive loop owners, run menus and instruction disclosure, and compact loop editor controls without changing phone layouts.

- Measure phone resource-tab overflow with its complete menu controls and available toolbar space; restore the full animated-images preference description on phones.

- Align desktop inbox and team title insets, board header controls, saved-view counts and creation borders, and the team creation accessible label.

- Restore secondary desktop loop schedule, hub, table and run composer metrics, plus run-specific prompts and capability labels.

- Remove unreferenced desktop settings measurement scripts.

- Run all API CI shards in one twenty-runner wave, retain source-keyed race/coverage builds, and reuse the test fixture's full-cost password hash without changing production hashing or coverage gates.
- Release file-parser capacity before returning completed results so back-to-back reads cannot falsely report an occupied slot.
- Verify both compact and expanded document-tab navigation without assuming a font-dependent 390px fitting breakpoint.

- Remove paid-plan badges, billing settings and unsupported credit-purchase controls; keep usage tracking and instance spend limits available.

- Run desktop spacing browser checks with the repository's project context and assert declared hairlines instead of device-scale-dependent coordinates.

- Let document selection actions and Agent history ages size to their rendered text in every language.

- Separate the document label and title naturally in phone subscriber chips.

- Preserve the base branch's text padding before the phone review direction arrow.

- Align phone document metadata values using the widest translated label instead of a sampled label width.

- Preserve the phone composer's dim Labels pill while its picker is open and restore the review chat author header's top padding.

- Let phone settings rows and selects grow with their actual content, and retain language, export and onboarding controls outside the reference content.

- Size phone milestone text intrinsically and keep project update Agent writing reachable after the activity feed.

- Remove sampled phone metadata and overflow widths; let team tabs use the space their rendered labels actually need.

- Shard browser CI across four isolated runners within the twenty-job limit, preserve the required Browser E2E check and per-shard reports, avoid duplicate device scenarios and unnecessary app reloads, and wait for notification and animation readiness instead of fixed sleeps.
- Remove desktop hairline spacing compensation, restore the Agent composer grid and footer rhythm, and align document sort/icon targets and settings rows with their declared spacing.

- Let phone chat actions follow short titles while long titles truncate within the viewport.

- Use natural icon spacing in phone review and cycle controls, and let issue composer toolbars contain their controls and bottom padding.

- Align phone preference action hit areas with other settings actions and use the standard icon gap for new webhooks.

- Use consistent phone team-tab padding and toolbar gaps, and keep restore-only document history controls on a compact toolbar while history navigation can still wrap.

- Preserve the milestone list's bottom inset and the inline text baseline of the phone project activity link.

- Keep phone chat actions right-aligned for every title and prevent opening Chat options from scrolling the header sideways.

- Group phone saved-view identities around a centered avatar frame without changing desktop spacing.

- Constrain long project and milestone names inside phone issue properties instead of letting their nested rows overflow.

- Group the phone Agent header inside its page header, restore activity hit-area padding without changing row pitch, and make document layout checks wait for settled dialogs and real tab overflow.

- Keep phone review Guide after Overview and Diff, move initiative selection and row actions into the existing context menu, and remove duplicate chat-menu padding.

- Move the phone conversation's toolbar action into Chat options and keep feedback after the reference chat commands.

- Move phone issue prompt and coding-tool actions into Work on issue in the existing issue options menu, keeping the three copy icons compact.

- Match main issue lists on touch phones by leaving sub-issue selection targets usable without painting per-row checkboxes; keyboard focus still reveals them.

- Keep selected documents' Ask agent action in the phone Actions menu rather than widening the bulk toolbar.

- Let the phone document version selector fit its text and align the history header, preview border and restore action with their content insets.

- Anchor phone document metadata at the viewport edge when needed, with content-sized owner and history controls.

- Keep the phone document header's navigation, breadcrumbs and actions in their natural compact groups without changing desktop layout.

- Remove fabricated phone billing offers, invoices and balance placeholders; show the workspace's actual credits and the empty invoice state.

- Match phone API settings documentation links, OAuth application actions, card spacing and full-width key policy controls without changing desktop layouts.

- Match workspace project status category spacing and action sizes on phones without changing desktop layouts.

- Match the phone team triage header and two-line issue rows, with working navigation and list-to-detail transitions.

- Match the phone composer's assignee panel, grouped agent and team-member rows, and compact typography while preserving desktop pickers.

- Match phone Projects toolbar sidebar glyphs and use viewport-width filter and display panels without changing desktop layout.

- Match phone Views directory controls, workspace group headers, and saved-view row spacing without changing desktop layout.

- Match phone Search header spacing, tab pills and glyphs, and use the shared phone panel for search filters.

- Desktop issue row menus now include working Convert to and Move actions, and display options use the heavier list, board, sorting and select glyphs without changing phone layouts.

- Use the measured solid border and border-box sizing for phone initiative update cards.

- Refine initiative phone update-card strokes, comment controls and compact activity timestamps.

- Match initiative overview spacing, property pills, header controls and latest update cards on phones without changing desktop layout.

### Fixed
- Keep the document owner button's hover padding out of phone metadata column sizing, so the editor-to-time gap stays consistent.
- Shrink phone document parent breadcrumbs and their separator together, preserving the title's natural share of the header.
- Let phone chat titles use the available header space while keeping adjacent favorite and options controls inside the viewport.
- Center the desktop Inbox welcome document within symmetric insets, and keep isolated browser checks aligned with adaptive tabs, supported workspace commands and native border rounding.
- Keep initiative selection/actions and the document-selection Agent action reachable on phones.
- Restore phone review Guide navigation and Latest, let loop names flex, and remove fake welcome-video controls and the unsupported desktop-app download link.
- Share content-sized phone member-table columns so names retain available space and row menus remain visible without hover.
- Keep project Agent writing and cycle/project insights reachable on phones, and size cycle names, milestone counts and team tab overflow from their content.
- Restore phone language, team security, project-status navigation and CSV exports; let personal settings grow with their copy instead of row-number dimensions.
- Remove the invisible duplicate composer assignee and three-label positioning; allow phone issue titles and property chips to grow without colliding with the footer.
- Restore phone issue work tools, insights, summary dimensions and display settings; remove activity offsets, position-specific board padding and fixed tab-count widths.
- Let document breadcrumbs, metadata, history actions and inline comment composers size from their content instead of reference-specific widths and offsets.
- Replace fitted Agent header and menu coordinates with normal flow; keep phone chat actions and desktop skills reachable as content grows.
- Match the phone issue actions menu's filter, border, shadow, row spacing, dividers and pull request glyph without changing desktop menus.
- Match the phone composer's status picker typography, selection highlight, and workflow glyphs without changing desktop pickers.

- Isolate the phone agent-panel seed behind an explicit development flag, link its real issue, and match the panel surface, pressed agent toggle and tool-row spacing without changing desktop layout.
- Keep the Chinese Agent label consistent across filters, settings and OAuth applications with one shared translation.
- Stop background delivery writes before checking no-op API requests, and align browser assertions with the saved control and shortcut references.
- Preserve fractional compact project-picker text metrics and avoid an unused desktop Members scrollbar gutter on Linux.
- Match phone Search empty-state illustration colour and explanatory text spacing without changing desktop layout.
- Apply compact picker text rendering to native buttons, exercise navigation shortcuts independently, and keep phone filter tests scoped to their viewport and current panels.
- Use isolated workspace fixtures and relative API routes throughout issue-list browser specs so repeated local runs do not collide with earlier test data.
- Match the phone Help panel's chip, filter, support rows and glyphs, and close the drawer when opening it without changing desktop menus.
- Match phone Teams filters, scrollable columns, team counts and expandable child rows without changing the desktop table.
- Refine phone workspace fiscal controls, initials, description wrapping, and onboarding text tones.
- Match the phone workspace members directory, search, role filter, invite control and grouped rows without changing desktop settings.
- Restore the phone workspace trigger's open pill and the keyboard-selected workspace-menu row highlight.
- Stack workspace general fields on phones, refine time and region spacing, and keep announcement settings accessible without changing desktop layout.
- Match desktop project and initiative add-view glyphs and empty initiative property labels, lower the initiative description placeholder, and verify sortable team settings rows without changing phone layouts.
- Match phone workspace-menu labels, panel sizing, both separators and the drawer inbox glyph, and clear the filter when switching to the workspace list without changing desktop menus.
- Match the desktop issue comment placeholder to the saved reference glyph color while preserving the phone placeholder token.
- Restore the phone cycles timeline, date rail, burndown chart and scope statistics without changing the desktop directory.
- Match My issues phone filter-menu glyphs, row typography and full-width group dividers without changing desktop menus.
- Restore the Open issue command picker, its O then I shortcut, and its searchable shortcut-help entry.
- Restore the desktop document comment send button and tinted avatar, dim issue comment placeholders, update expanded event labels, and use small menu chevrons and the inbox tray-delete icon without changing phone layouts.
- Restore the priority inbox control on phones and refine account notification rows, status dots, spacing and navigation glyphs without changing desktop settings.
- Skip leading punctuation in shared profile and user-avatar initials, and translate the team overview and message settings labels into Chinese.
- Restore the phone profile avatar and stacked fields, compact email action, and workspace access spacing without changing desktop settings.
- Restore visible desktop profile field borders and settings search text, use the shared Create team wording, and align remaining settings controls and section spacing without changing phone layouts.
- Align desktop project timeline axes, zoom controls, empty issue states, activity month headings, property menus and initiative tables; restore team context and empty-cycle navigation without changing phone screens.

- Restore desktop initiative detail columns, the latest saved update card and actions, browser titles, and project-count progress rings while retaining phone layouts and team controls.

- Restore desktop project milestone chips, honor ungrouped rows and display properties, align list columns and compact date controls, and correct overview property spacing without changing phone layouts.

- Desktop issue activity uses plain issue-title links, omits redundant parent history, and expands consecutive event groups. Compact project rows reclaim unused columns, comment actions and replies use the measured sizes, and date/reminder menus include end-of-week and three-hour presets without changing phone layouts.
- Phone history and display popovers paint a true half-pixel edge, chat switcher times align to the right inset and compact older weeks without changing sub-day units, and inactive display layout pills retain their ring. Desktop layouts and timestamps are unchanged.
- Desktop issue display options expose every property without fixed-height clipping, retain collapsible group controls, and use the shared sub-team filter with compact Reset and team-default actions. Group context menus collapse, select complete groups and hide them; bulk Actions show the command glyph, and new-view fields use the brighter title and full-width description. Phone sheets are unchanged.
- Phone navigation drawers retain a visible compose fill, the team header and child spacing, indented cycle labels, and a dismissible Import issues entry without changing the desktop sidebar.
- Phone initiative lists use compact touch-sized tabs and actions, full-width name and health rows, and matching navigation and health glyphs without changing desktop layouts.
- Desktop Inbox's unread-empty title and ghost action use the measured vertical placement, normal text line box and medium action weight without changing the phone empty state. Snooze inputs and date columns use the measured desktop insets.
- Desktop workspace-welcome documents use the compact Flow mark, quieter body type and a bounded video placeholder so Resources stays in view, with working Flow documentation and support links. Pulse empty states retain left-aligned content and correctly styled desktop actions. Inbox preview Copy URL actions retain the workspace route, and review links no longer silently do nothing.
- Phone document header menus use a searchable, scrollable command panel with touch-sized rows and matching action order; desktop menus are unchanged.
- Desktop Inbox row actions show the complete Delete label and a small snooze chevron. Keyboard snooze now includes the same dated options, Custom icon and editable time as the pointer menu, at the matching desktop width.
- Phone document pages retain the project breadcrumb and edited date beside touch-sized actions, align the title and writing column, and use the matching navigation glyph without changing desktop layouts.
- Phone project descriptions keep paragraphs, headings and all list levels unclipped with aligned markers; resource-add controls and the first-update card use the matching spacing, hairline and compose glyph size without changing desktop layouts.
- Phone team tabs and document actions retain visible hairline rings, the settings drawer has a thin rounded edge, and the selected issue layout pill uses its ring token correctly; desktop styling is unchanged.
- Desktop issue pages use the corrected content/properties split, owning-team control and 32px property rhythm, narrower short project menus with type-to-search, left-opening label menus, and aligned triage peek insets. Team moves are available from the desktop action menu, and relation removal loads targets omitted by paged workspace data. Phone layouts are unchanged.
- Desktop settings show account email availability, installed integration cards and discovery, team overview icons and live summaries, the settings help and plan footer, sortable team columns, label usage columns, and full-size notification and session icon tiles without changing phone settings layouts.
- Desktop shell menus use compact text line boxes and measured workspace-menu spacing; shortcut search keeps a neutral focus border and now lists the complete general/navigation set, clipboard confirmations use the raised surface and copy glyph, command search keeps matching commands ahead of entity results, and the fullscreen issue command's V shortcut now opens the expanded composer. Phone styling and shortcuts are unchanged.
- Command-menu search keeps its result group registered while queries change, avoiding a deferred sorting crash. Desktop issue tabs, the sidebar plan control, and phone hidden-board counts retain fractional font metrics on Linux. Browser fixtures select their own workspace with the supported header, phone navigation checks run as separate workflows instead of exhausting a shared deadline, and service-worker retirement checks avoid racing app registration.
- Phone Agent chat switching shows a visible history filter, calendar-week groups, taller rows and full-width hairline separators in a viewport-sized popover; desktop history menus are unchanged.
- Desktop issue lists use compact status tracks, right-aligned dates, cycle metadata pills, a single estimate/count group metric and persistent dim disclosure carets; parent progress remains visible while children are nested. Team breadcrumbs and compact filter Save/Clear controls match the desktop spacing; saved-view summaries include working Teams breakdowns, estimate totals and matching tab fills without changing phone layouts.
- Phone settings search uses the measured placeholder tone and hairline edge, the shared Agent/history footer stays hidden on settings routes, and team overview tab fills no longer brighten their rings; desktop layouts are unchanged.
- Phone issue activity centers avatar initials, dims and ends its connector at the last icon, matches the comment placeholder colour, and aligns compact timestamps; blocking events now say "marked this issue as blocking" in the shared desktop and phone history, with updated Chinese copy.
- Phone issue activity aligns subscriber controls, event links and timeline spacing, and the empty comment composer uses the matching attachment and send glyphs without changing desktop layouts.
- Phone settings navigation restores the opening section's vertical inset and muted help glyph without changing the scrolled drawer viewport or desktop layout.
- Desktop issue boards show a full hidden-column panel with persistent hide/restore controls; filtered empty lists show the number of hidden issues and a working clear action. Saved issue views open their remembered details panel by default on desktop and include unassigned issues and complete server-side breakdown counts. Enabled triage queues retain their two-pane layout and show the team glyph and compact row metrics without changing the disabled state.
- Phone navigation labels, team glyphs, floating help control and backdrop now match the drawer styling; project customer-request labels and team document headers use their intended phone font weights without changing desktop layouts.
- Desktop issue view pills no longer accumulate border-width drift; the add-view glyph and issue composer's team text, action glyphs, and undivided footer match the desktop shell styling without changing phone controls.
- Phone Pulse, Reviews and project pages retain fractional font metrics on Linux instead of shifting tabs and actions; browser menu and shell checks create their own workspaces and use the API's normalized workspace keys.
- The Inbox no longer shows notifications from an older navigation cache: the snapshot keeps inbox rows out of storage, reconciles them from the notifications API, and a cancelled or partial bootstrap stream is retried instead of leaving deleted rows on screen. The full bootstrap also omits deleted and archived notifications, and both workspace header spellings select the same workspace.
- Large MySQL workspaces list, sort, search and count issues without full-table scans: priority sorting, triage filters, saved views, cycle capacity and cycle history use matching composite indexes, plain totals use the maintained issue counters, and the activity-to-cycle index keeps the cycle graph from parsing every activity row.
- The MCP `count_issues` team fast path is no longer disabled by the injected tool base URL, so it reads the maintained team counters instead of counting the whole issue table.
- Search's date filters open their calendar inside the filter submenu instead of
  dismissing it, on desktop and phone widths.
- MCP rejects unknown cycle references, reports missing issues as caller errors, and keeps malformed JSON-RPC params distinct from recoverable tool argument errors. Internal-error logs retain diagnostic details without exposing them in responses.
- Production builds keep small math fonts as same-origin files for strict CSP; filter dialogs have accessible names, and streaming assertions wait for frame-batched rich text.
- Estimate icons now use the same filled-left triangle for every value and
  property button, with a separate broken outline for "No estimate" menu options.
- On phones, issue details wrap their properties, expose due dates and related-issue navigation, keep the copy controls visible, and use touch-sized issue action menus without repeating the desktop properties rail.
- On phones, the issue header keeps the reference app's breadcrumb and action positions, the property pills share one hairline and padding so a due date reads like the other properties, and the related-issues pill uses the relationship glyph already used by the activity stream, and the header menu plus reaction/attachment glyphs use the muted phone icon palette.
- On phones, the Reviews page has the reference app's combined 87.5px header and tab strip, Created count badges, and the centered empty-state illustration; desktop layouts are unchanged.
- Phone pill tabs (Reviews, My issues, Projects) clip their fill to the padding box, so the shared hairline ring matches the reference edge instead of lightening the fill.

### Security

- Upgrade `github.com/go-jose/go-jose/v4` to v4.1.6 to address panics in JSON
  and CBC-HMAC processing and denial of service when parsing RSA JWKs used by
  OIDC discovery and token verification.
- Issue relationship filters and MCP sub-issue counts now exclude archived
  children and children outside the viewer's team and permission scope.
- Upgrade `golang.org/x/net` to v0.62.0 to address HTML parser vulnerabilities;
  coding-runtime OpenPGP signing uses the maintained `github.com/ProtonMail/go-crypto`
  fork instead of the deprecated `golang.org/x/crypto/openpgp` package.

### Added

- Browser coverage in throwaway workspaces for Triage accept, decline, duplicate (3 and M M), snooze, next/previous navigation, and editor/dialog shortcut guards.
- Phone-sized app frames fill the viewport, with matching navigation targets,
  searchable workspace menus and modal property pickers; the issue composer
  uses phone-sized controls without changing desktop layouts.

- Common MCP and agent issue tools use bounded catalog projections and indexed issue point reads; team counts use maintained counters instead of scanning the full issue table.
- Agent replies now recover from empty or malformed streamed tool arguments without breaking saved chats; chat errors use translated messages with Details and Retry. Sending follows the new turn, finished steps omit running summaries, and loop drafts appear after their work header. The loop builder keeps to the requested scope and document-target guidance is clearer.

- Agent issue and cycle listing now supports an explicit `all` cycle filter, compact relationship counts and relationship-presence filters, avoiding per-issue detail fan-out; the tool harness uses a valid page location.

- The `hasSubIssues` issue-list filter now checks the indexed child parent IDs directly, so it stays current through issue mutations without a workspace-wide attribute re-index.

- Agent tool calls now share reference, localized status/priority, and relative-date normalization across chat and MCP, with actionable resolution errors; the opt-in streaming regression harness is in `scripts/agent-tool-error-harness.py`.

- Agent and loop-builder progress now uses friendly, retry-aware steps, language-matched draft copy, optimistic loop creation, and weekday-aware schedules; issue field aliases are normalized before tool validation.

- Loops verify described issue targets before writing, cache semantic checks,
  and enforce team and named-target scope on status updates. Changed
  instructions suggest a fresh output contract for per-field review while
  runs keep the person's outputs. The document picker searches indexed titles
  and content and highlights matching snippets.

- CI uses three store test shards, refreshes timing weights in a weekly pull
  request, and builds the browser-test API before the server readiness timeout.
- Security browser coverage for server-fetched X/Figma and GitHub/GitLab cards,
  service worker updates and kill switches, plus opt-in real-provider embeds.
- Block LaTeX formulas in shared issue, document and comment editors, with
  source editing, KaTeX previews, formula actions and Markdown/collaborative
  history support. Inline dollar-delimited text stays literal.
- Requests from Lark and Feishu group messages: in a group bound to a team
  whose binding takes requests (**Requests** in the binding editor, with the
  reaction, request template and priority), a member turns a message into a
  request by explicitly asking the bot (`@Flow 提需求 …`), adding the
  binding's reaction, or **Submit as request** in the
  message shortcut's form. The request is an Ask (source `feishu` or
  `lark`) with its issue in the team's triage, created as the member's
  linked Flow account with their permissions; unlinked members get the link
  card and members who cannot create issues in the team are told privately.
  One message carries one request (`chat_intake_requests`, migration 22).
  The message's thread follows the request: the confirmation card, replies
  as comments (when the binding allows updates), Flow comments, a note on
  status changes and a reply with the outcome when the request is closed.
- Chat with the Flow Agent in a direct message with the workspace's Lark or
  Feishu bot. It runs as the linked member with exactly their Flow
  permissions, rate limits, tool budget and approvals, on either agent
  runtime; each conversation is an Agent chat marked **Lark** or **飞书** on
  the Agent page. The answer streams into one card, replies continue the
  chat, held calls get an Approve / Reject card, and images and files become
  attachments. `new issue` / `新建事项`, `my issues` / `我的事项`,
  `inbox` / `收件箱` and pasted Flow links answer with cards; a reply to a
  personal notification card comments on its issue. Members linked in
  several workspaces of the tenant pick one (`switch workspace` /
  `切换工作区` changes it). Unlinked accounts get a card to link; unlinking
  stops a running reply. Secrets are hidden in what the bot sends, and
  workspaces with sensitive data protection send no content. The app needs
  the `im:message`, `im:message.p2p_msg:readonly` and `im:resource`
  permissions (see `docs/feishu-lark.md`).
- Lark and Feishu issue threads: everything a group hears about one issue
  goes into one message thread (its first card, or the member's create
  command, is the root; a deleted root starts a new thread). Flow comments
  are posted into the thread and their edits and deletions reflected there;
  a reply in the thread becomes a Flow comment written as the member's
  linked account, with their own permissions and only when the binding
  allows updating issues. Unlinked members get one card to link their
  account; imported replies are never posted back. The resource-to-thread
  mapping is stored (`chat_threads`, `chat_thread_items`).
- Lark and Feishu team bindings cover all of the team's projects, one
  project or a chosen set of projects (**Projects**, a multi-select in the
  binding editor; `projectIds` in the API). Every project must belong to the
  team and disclose no private team; deliveries and group actions stay in
  the set. Existing single-project bindings become one-project sets.
- Initiatives connect directly to a Lark or Feishu group from their
  notifications and actions menus, whether or not a team leads them. The
  initiative's owner or a workspace administrator authorizes it, with the
  team bindings' group checks, an authorization record and audit entries;
  the group gets the initiative's update cards and never a private team's
  content.
- Lark and Feishu: the **创建事项** / **Create issue** message shortcut
  creates an issue from any message. A private form card (team, title and
  description prefilled from the message with a link to its chat, assignee,
  priority) creates it as the member's linked Flow account; the issue card is
  then replied under the message. Bound groups keep their create option and
  team scope; unlinked members get a card to link their account.
- Lark and Feishu link previews: Flow issue, project, document and
  initiative links expand into a preview card for each viewer, filtered by
  their own Flow access. Private team content expands in a group only when
  the group is bound to that team; nothing expands with sensitive data
  protection on.
- Lark and Feishu issue cards (group and personal notifications) have
  **Assign to me**, status and priority controls next to **Open in Flow**.
  Clicks run as the clicking member with their permissions and the group
  binding's update option, the card is redrawn in place, and refusals arrive
  privately in Chinese or English with a stable code.
- The MCP attachment tools (`prepare_attachment_upload`,
  `create_attachment_from_upload`, `create_attachment`, `save_attachment`)
  attach files and links to a document as well as an issue: pass exactly one
  of `issue` or `document` (a document you can edit). Document files are
  recorded on the document and served to everyone who can see it; the body
  is left alone unless `insert: "append"` adds the file at the end. Every
  result includes a ready `markdown` snippet (`![title](url)` for images,
  `[title](url)` otherwise) to place with `save_document`, and
  `delete_attachment` also removes a document's attachment. On issues,
  `insert: "none"` keeps a file out of the description (the default still
  embeds it).
- `save_document` content turns a line that holds only an image
  (`![alt](url)`), a video (`::video[title](url)`) or a file card
  (`::file[title](url)`) into that block in the editor.
- `FLOW_CHAT_PLATFORM=lark|feishu` (Helm `integrations.chat.platform`) makes
  Lark or Feishu the deployment's first-class chat platform. The team home
  shows **Connect Lark group** (飞书 in Chinese for Feishu), the team's
  notifications page lists and binds its groups, project menus and the
  project details sidebar open the project's groups, project and initiative
  update settings, the sidebar and directory team menus, onboarding and the
  integration catalog point to it. The default (`slack`) is unchanged.
- Private teams can be shared with a Lark or Feishu group: the team's owner
  authorizes a binding to one internal group, choosing the notification
  types, project and whether the group may create or update issues. The
  binding adds no team membership and no Flow access for anyone; updates go
  to that group only, group actions use each member's own Flow permissions,
  and it stops once its authorizer can no longer administer the team. Team
  owners bind their own teams (public ones too) from the team's
  notifications page; only they and workspace administrators can change,
  revoke or remove a team's bindings. Every binding records who authorized
  it and when, **Revoke** keeps it listed as revoked, and binding changes are
  audited.
- Lark and Feishu groups can receive initiative updates of the initiatives
  their team leads.
- Personal Lark and Feishu notifications: a member who linked their account
  turns them on in **Personal > Notifications**, chooses the notification
  types as for email and desktop, and receives them as a direct message from
  the app's bot. Messages are sent only while the integration is healthy and
  for content the member can still open; unlinking or leaving the workspace
  stops them, and an account outside the app's availability range is shown
  as such on the notifications row.
- With `FLOW_AGENT_RUNTIME=worker`, Flow Agent chat replies run on the agent
  worker (`agent-worker`) instead of inside the API. The reply is the same
  chat message as before: saved while it is written (a reload or another tab
  shows it streaming), with its tool calls, results and change cards, and
  saved in its final form when the run ends. Tools set to always ask wait on
  the reply for approval and the run continues once every call is answered;
  questions with options end the reply as before; Stop ends the run, a
  connector question waiting in it included. The reply keeps the tool budget
  and the check that reported outcomes come from real tool results. A reply
  whose worker stopped responding is restarted when nothing was written, and
  otherwise shown as interrupted with Retry. The default
  (`FLOW_AGENT_RUNTIME=inprocess`) is unchanged.
- Chat replies on the agent worker no longer wait forever: a reply no worker
  takes within `FLOW_AGENT_WORKER_CLAIM_TIMEOUT` (default 2 minutes) ends with
  "No agent worker is available; try again or contact your admin" and its run
  is cancelled. Calls waiting for approval are rejected after 30 minutes and
  the reply goes on, as in-process, and the step limit
  (`FLOW_AGENT_MAX_TOOL_TURNS`) counts the whole reply, also after approvals.
- Chats with sent files run on the agent worker too
  (`FLOW_AGENT_RUNTIME=worker`): images reach the model as image inputs
  (read from storage when the run starts, with the same size and type checks),
  other files as the note the model opens them by, and `read_file` /
  `transcribe_media` work on the conversation's own files as in-process. The
  worker also starts with the tool groups of the chat's surface (an issue,
  project or document chat), as in-process.
- Loop builder conversations run on the agent worker too
  (`FLOW_AGENT_RUNTIME=worker`): the draft and its files reach the model, the
  chip questions show on the reply and wait for the answer (Stop cancels
  them), the words before a question stay in the reply, and `save_loop` rows
  read "Updated workflow definition draft" or "Created automation" as before.
- Flow Agent mentions in comment threads and issues delegated to Flow Agent
  run on the agent worker (`FLOW_AGENT_RUNTIME=worker`). A delegated issue
  that asks for code changes still enters Coding (in the environment its
  repository names) or reports the blocker, as in-process. The thread shows
  the same activities as
  before (the first look at the issue at once, each tool call, the streamed
  text and the answer); questions with options and calls waiting for approval
  end the run and the answer continues it, with the same tool and step
  budgets, connector budget and checks of what the answer reports. A run no
  worker takes within `FLOW_AGENT_WORKER_CLAIM_TIMEOUT` ends the session with
  "No agent worker is available".
- Loop runs execute on the agent worker too (`FLOW_AGENT_RUNTIME=worker`).
  The run page is unchanged: steps, tool calls, the output contract and its
  refusals, document writes, `finish_run` reports and the acceptance check are
  recorded as before, every tool call goes through the same scope and
  contract checks, and the turn, tool-call and time budgets, Cancel and the
  per-workspace limit apply across API servers. Replies on a run's page still
  run in the API.
- Flow Agent on the agent worker uses the web as in-process: chats and
  comment threads get `fetch_url`, and `web_search` when a search provider is
  configured, while the workspace allows web access (turning it off takes
  effect mid-run); without a search provider the worker uses the model
  provider's hosted search on OpenAI Responses and Anthropic Messages
  (`FLOW_AGENT_NATIVE_WEB_SEARCH`), shown as a web search step, and drops it
  when the provider refuses it.
- The workspace Invite dialog (Settings → Administration → Members → Invite)
  has an **Add to team** field for every role. Pick one or more teams (a
  searchable multi-select; optional for Members and Admins, required for
  Guests) and the invitee joins them when they accept. The invitation API
  refuses teams the inviter could not add people to directly (a private team
  they are not on, or one whose member management is closed to them) with 403;
  workspace admins may use any team.
- Security response headers on every response (pages, static files, API,
  health, errors, redirects, downloads, streams): `X-Content-Type-Options`,
  `Referrer-Policy`, a `Permissions-Policy` that keeps only what the web app
  uses, `frame-ancestors` with `X-Frame-Options`, HSTS when HTTPS is
  confirmed (TLS, or `X-Forwarded-Proto` from a proxy trusted with
  `FLOW_TRUST_PROXY_HEADERS`), and a Content Security Policy that starts in
  report-only mode (`FLOW_SECURITY_CSP=enforce` to block). Each part has its
  own `FLOW_SECURITY_*` setting and `FLOW_SECURITY_HEADERS_SKIP` leaves
  headers to a gateway; Helm `securityHeaders` and `trustProxyHeaders`. See
  [Security response headers](docs/configuration.md#security-response-headers).
- Agent sessions can be archived as a whole: **Archive session…** in a
  session's "…" menu (and `POST /api/agent-tasks/{id}/archive`) hides the
  session and all of its messages, people's replies included, from the
  activity for everyone, live and after a reload. A running session is stopped
  first and late activity cannot bring it back; its log, audit entry, reports,
  attachments, pull requests and linked issues are kept. Only the person who
  started it or a workspace admin can archive it, and archiving twice changes
  nothing. MCP adds `list_agent_sessions`, `get_agent_session` and
  `archive_agent_session`. See [Application agents](docs/application-agents.md#archive-a-session).
- SCIM provisioning: ETags (`If-Match`/`If-None-Match`), `sortBy`/`sortOrder`,
  `POST /Bulk`, `POST /Users/.search` and `/Groups/.search`, the enterprise
  User extension and profile attributes, audit log entries for every
  provisioning change, a "When a user is deprovisioned" setting (suspend or
  remove from the workspace), team group mapping on the identity provider
  page, and "Managed by SCIM" members whose role, name, email and status
  change only in the identity provider.
- Settings › Account › Connected accounts: Google Calendar (members show as
  out of office from their calendar's out-of-office events) and Notion (link
  previews of issues and projects) connections, configured with
  `FLOW_GOOGLE_OAUTH_CLIENT_ID` / `_SECRET` and `FLOW_NOTION_CONNECTION_URL`.
- `POST /api/workspace/documents/{id}/revisions/import` (workspace admins):
  adds a preserved earlier body of a document to its version history without
  changing the document. See [Operations](docs/operations.md#re-adding-a-lost-document-version).
- Coding sessions: Pi as a fourth agent (harness `pi`, pinned 1.4.0 in the
  sandbox image), using the OpenAI-compatible upstream with its own model list
  `FLOW_CODING_MODELS_PI` (Helm `coding.llm.models.pi`, empty by default) and
  `contextWindow` from `FLOW_CODING_MODEL_INFO`.
- Loop outputs say where comments, status updates and issues go: the
  triggering item, issues matching a description, a named project or
  initiative, a team. A run's writes elsewhere are refused with a tool error,
  and only writes on target count as the loop's output. Title templates of
  new documents and issues may hold placeholders the run fills (`{topic}`,
  declared with what fills it) next to `{date}`, `{week}`, `{month}`,
  `{year}`, `{team}` and `{project}`; the run passes them as `titleFields`
  and a title that does not fit the template is refused. An existing
  document the instructions do not name ("the team doc") must be chosen in
  "What this loop will produce" (or created there); until then runs may read
  but write to no existing document and need review with
  `failureReason: needs_target`. The editor's document picker searches the
  workspace's documents on the server.

### Changed

- Agent chat streams batch answer tokens to animation frames, preserve a reader's
  manual scroll position, show a quiet shimmering thinking row without a spinner,
  and persist generated titles off the reply's critical path.

- Nested issue counts use existing indexes for project, creator, cycle and
  customer groups, and project, subscriber and sparse-attribute filters.
  Search matches are read once; substring filters scan their team scope once.

- PostgreSQL change-log numbering assigns a committed batch, advances its
  cursor and retires the queue in one statement after acquiring the numbering
  lock. Multi-event writers also enqueue their events in bounded batches.

- An issue created with the Lark or Feishu message shortcut in a bound group
  keeps the source message as its thread root, so the creation reply and
  later notifications continue that thread (and *issue created* posts no
  separate card). In direct messages, a pasted Flow link gets no bot preview
  card once the platform previews links for the app, and an Agent chat held
  in a direct message is marked read for the member, so it no longer shows
  as unread on the Agent page.
- `prepare_attachment_upload` returns the file's stable URL as `assetUrl`
  (`…/uploads/asset_…`) and its path as `url`, next to the one-time
  `uploadUrl` for the `PUT`. Before, `assetUrl` repeated the upload URL.
  `create_attachment_from_upload` accepts either value, so clients that pass
  the old `assetUrl` keep working; clients that sent the `PUT` to `assetUrl`
  must use `uploadUrl`.
- Record ids Flow generates have the shape
  `<prefix><19 digits>_<10 characters>` (a zero-padded creation time in
  nanoseconds and a random part) instead of `<prefix><digits>`, so ids made
  at the same instant on several servers no longer collide. Ids of one kind
  still sort by creation time; existing ids keep their shape. Integrations
  that match ids against a pattern such as `prefix_\d+$` must treat them as
  opaque strings; see [Record ids](docs/api.md#record-ids). The
  webhook-disabled notification, a document's baseline version and a reset
  custom email intake address no longer reuse a value built from the clock
  second or the document's save time.
- On a Lark or Feishu deployment (`FLOW_CHAT_PLATFORM`), the remaining
  chat platform copy names it instead of Slack (飞书 in Chinese for
  Feishu): the read-only notice of a team's notifications, the initiative
  updates description, settings search and the legacy integrations list.
  The custom view menu's Slack-only notifications item shows on Slack
  deployments only.
- Lark and Feishu tenant authorization is automatic. The tenant administrator
  approves it on the chat platform by approving and publishing the app
  version in the admin console; once the App ID and App Secret obtain a
  tenant token and the bot's info is readable, Flow marks the tenant
  authorized (recording when, the tenant key and the administrator who
  configured the connection). Nobody signs in for it in Flow and there is no
  tenant authorizer to designate, and the app no longer needs the contact
  scopes. Flow re-checks on **Test connection**,
  periodically and after a refused token; until the app is approved the
  integration page says so (`chat_tenant_not_approved`). Linking a personal
  account needs only a healthy connection and never authorizes the tenant;
  a member outside the app's availability range is told so
  (`chat_app_unavailable_to_user`). The integration page shows the tenant,
  the workspace integration permission and the team binding permission
  separately, and refusals name the missing one (`permissionDomain`:
  `tenant`, `workspace` or `team`). Existing connections become
  automatically authorized on their next successful check. Groups the bot is
  not in, external groups and groups of another tenant are refused with
  their own messages.
- Lark and Feishu connections record the tenant at once: every connection
  check (saving, **Test connection** and the periodic check) reads the
  tenant's information with the app's tenant token, and the integration
  page shows the tenant's name and key. This needs the app permission
  获取企业信息 (`tenant:tenant:readonly`), granted with the app version.
  Without it the connection stays authorized, the page asks to grant it
  and publish a new version (`chat_tenant_info_permission_missing`), and
  only then is the key taken from the bot's internal groups or the first
  linked account, marked as first seen. A recorded key is never replaced:
  when the platform names another tenant, the connection shows a tenant
  mismatch with both keys (`chat_tenant_mismatch`) and stops until it is
  reconnected. Existing connections get the key and name on their next
  check.

### Fixed

- Check real-scale issue padding and logical tab/composer spacing in browser tests, and keep the workspace menu assertions aligned with its supported commands.
- Keep native phone settings selectors and key policy controls aligned with their reference layouts, and assert selected options and CSV downloads through their real controls.
- Preserve the measured phone settings rows, compact toolbars, Agent conversation actions and document metadata sheet after layout cleanup; keep off-screen tab measurements out of browser control assertions.
- Preserve fractional Projects tab text metrics on Linux, and check label editing and compact composer states against their saved-reference styling.
- Keep the phone Inbox header inside its page stacking context so it cannot intercept workspace drawer controls, and verify label-picker content independently of rounded hairlines.
- Align phone composer label rows with the search field at the measured viewport-relative width.
- Keep the saved-view browser scenarios on the shared fixture binding after concurrent test additions.
- Translate the phone integrations list's remaining-item count into Chinese.
- Keep the team issue header favorite button at its saved-reference position and wait for visible triage rows before checking their geometry.
- Wait for the inbox navigation snapshot's idle persistence before asserting its stored contents in browser tests, while keeping server-row and stale-ID checks strict.
- MCP issue saves retain inherited team workflows; issue counts use the team
  counter only for team and archive arguments, leaving all other filters on
  the general query path.

- Sidebar Try items use the shared navigation row styling through context-menu
  wrappers, keeping icons, labels and hover dismiss controls aligned in both themes.
- Close former-member mention gaps in state-only collaborative edits, posts
  and meetings. Decode Yjs server-side before accepting new mentions; imported
  chat replies keep former-member mention labels as plain text. Persist member
  suspension, removal and resumption domain events for the change log and User
  webhooks, audit member actions, and include status payloads in development
  realtime notices as well as authenticated ones.
- Starting a cycle today no longer generates an extra upcoming cycle, even
  after refreshing. Automatic maintenance replenishes the configured upcoming
  count at scheduled cycle boundaries; cycle settings still apply their count.
- Lark and Feishu DM agent replies survive API server loss: leased reply jobs
  let another server finish delivery, resume an existing worker reply, or mark
  an in-process reply interrupted with **Retry** / **重试**. Thread leases
  prevent two servers from answering the same conversation at once; retries
  and card delivery are deduplicated across takeovers.
- Worker agent runtime now handles loop-run replies and continuations, streams
  loop output, and applies the loop diagnostic, recovery and conversation budgets.
  Chat retries empty and text-approval replies; saved approval decisions without
  a harness state also execute on the worker. Web prompts use the worker's protocol.

- Issue PATCH accepts a stale `expectedVersion` when its fields have not
  changed since that version, including parent edits after a child auto-closes
  it. Same-field edits and versions without sufficient history still return
  409; responses return the new issue version.
- Concurrent metadata writes apply additive counter increments, membership
  changes and id-based appends as operations. Writers immediately discard a
  peer-deleted record instead of retaining it in their installed snapshot;
  team writer bases deep-copy nested slices and maps.
- Lark and Feishu group intake now requires an explicit request phrase,
  configured reaction, or shortcut by default; **Treat every mention as a
  request** is an opt-in binding setting (also off for existing bindings).
  Other mentions get private group help. Shortcut issue/request choices are
  exclusive per message and the form shows the result. Ask rejections reply
  in the request's thread with the optional reason, and reopening is noted.
  Group issue cards no longer duplicate status/assignment forms, and
  initiative cards include the specific update's text within existing
  sensitive-data and private-team restrictions.
- Issue assignee menus sort the remaining team members by display name in the active language
  (pinyin for Chinese), keeping the viewer and current assignee in the numbered section only.
- Invite dialogs use the existing light error color and retain focus and invalid field borders
  in light mode; surfaces, controls and placeholders continue to use their light theme tokens.
- Shared issue and metadata caches retire their revisions inside comment,
  relation, release and credential-usage writes, including writes that change
  no issue record. Two-server commit barriers also cover metadata migrations.

- Every other stored `PATCH` and `PUT` now also writes nothing when it only
  repeats the current values (no `version` or `updatedAt` change, event,
  realtime update, webhook or notification): releases, release pipelines and
  notes, templates, webhooks, OAuth applications and their manifests, API
  keys, integrations and GitLab connections, settings (workspace, account,
  team, cycle, SLA, notifications, coding agents), favorites, subscriptions and
  thread subscriptions, permissions, members, project and initiative updates,
  resources and relations, dashboards, posts, meetings, customer requests and
  upserts, notifications, link attachments, git automations, target branches,
  reviews and deploy previews among them. Favoriting a favorite, archiving an
  archived release or post and publishing a published release note keep the
  original date. `PATCH /api/issues/{id}` now honours `expectedVersion` when
  it is sent (409 on conflicting fields), as `/api/issue-records/{id}` does; a
  patch without it still applies its fields to the current issue. Uploads,
  connecting integrations, reinstalling applications and similar actions still
  always run; see [Updates that change nothing](docs/api.md#updates-that-change-nothing).
- With several API servers, metadata writes no longer silently undo each
  other. Comments, reactions and edits on one project or initiative update
  made on two servers are all kept (updates merge one by one, reactions key
  by key), as is an import job's progress written while another server edits
  the job. Nested settings merge key by key, so two servers turning on
  different notification switches of a team keep both. Team writers (rename,
  settings, deletion) and user settings apply only their change onto the
  stored team, label, cycle or settings record. When both servers changed the
  same value, every writer reruns on the stored record (with a short,
  jittered backoff) and answers 409 if it keeps conflicting, instead of
  keeping its own value; startup migrations and the progress backfill rerun
  until they succeed. Issue updates rerun the same way. A full workspace
  mutation computes project progress inside its transaction from the issues
  as committed, so an issue created on another server meanwhile is counted.
  Two servers filling the shared metadata cache at once no longer leave a
  partial copy, and Undo or Restore of an agent reply's changes runs once
  across servers.
- On MySQL, a long issue title (more than about 128 characters of CJK text,
  or a long English title) no longer fails the full-text search with Error
  191 "Too many words in a FTS phrase", which left triage suggestions,
  similar-issue lookups and search empty for that issue. A long query is
  searched as at most 16 required pieces of at most 64 characters each.
- 4xx responses, MCP tool errors and collaboration socket errors no longer
  carry raw database or object-storage text. Store errors keep their driver
  type through every wrap, object-storage (S3 and local file) errors are
  treated like database errors (a logged 500 with the request id), and a 4xx
  whose message still reads like driver or storage text answers "The request
  could not be completed (request id …)." with the original logged.
- Issue icons (191) and staged upload media types (255) and filenames (512)
  are limited in characters, as the tool schemas' `maxLength` and the
  database columns count them; a non-ASCII value at the limit used to be
  refused because the server counted bytes.
- People removed from or suspended in the workspace can no longer be made an
  assignee, delegate, subscriber, project lead or member, initiative owner,
  triage owner or template assignee: such a write is refused with 400 "This
  person is no longer a member of the workspace" (removed people used to get a
  403 that read like a permission problem, suspended people were accepted).
  References a record already has stay readable, and a write that keeps them
  goes through (keeping a removed project member used to fail with 403).
  Former members get no new notifications, triage rotations skip them, and
  pickers no longer offer suspended members. See
  [People who left the workspace](docs/api.md#people-who-left-the-workspace).
- A new mention of someone removed from or suspended in the workspace is
  refused with 400 "This person is no longer a member of the workspace and
  can't be mentioned" (it used to be saved silently and notify nobody); their
  existing mentions stay and edits that keep them save. The "@" menu leaves
  them out, and Markdown written through MCP keeps their name as text.
  Suspending, removing or resuming a member, in settings or through SCIM, now
  sends a realtime `workspace_member.*` event: other open sessions mark the
  person inactive (or active again) at once instead of offering them in
  pickers until a reload.
- With several API servers, an issue write no longer undoes a workspace
  change another server made at the same time. Creating, editing or deleting
  an issue (and commenting, attaching, or adding it to a release) writes
  the project, document, release, subscription and triage records it touches
  from the server's in-memory copy, which can be behind: a project renamed
  on another server got its old name back, the progress another server's new
  issue added to a project was lost, a release lost an issue added elsewhere,
  a removed subscription came back while a new one was deleted, and two
  round-robin triage assignments went to the same person. Each changed
  record is now locked and only the fields the write changed are applied to
  it as stored (lists of ids and entries are merged), project progress is
  computed from the stored history, and a write whose field another server
  changed too runs again on the fresh record. Writes on other servers also
  stop reading a stale copy of the workspace from the shared cache after an
  issue write changed it.
- A REST update that only repeats a resource's current values writes nothing:
  it returns 200 with the resource as it is, keeps its `version` and
  `updatedAt`, and records no history entry, event, realtime update, webhook
  or notification (documents, comments, projects, milestones, initiatives,
  labels, label groups, teams, workflow statuses, cycles, views, customers and
  document drafts; issues already did). A stale `expectedVersion` is still a
  409 when the values match. Archiving an archived document or project, or
  retiring a retired team, keeps its original date, and sending a workflow
  status's current type no longer fails when it is the only status of that
  type. See [Updates that change nothing](docs/api.md#updates-that-change-nothing).
- A server error (HTTP 500) no longer shows database or driver text: the body
  is `Internal server error` with the request id, and the error is logged with
  that id. MCP clients and agents get "The change could not be saved because
  of an internal server error (request id …)" instead of the database error.
  SCIM and OAuth errors carry the request id in their own formats.
- Over-long values are refused with a 400 that names the field and limit
  instead of failing with a 500 on MySQL and Postgres: issue titles over 2,048
  characters (REST, MCP `save_issue`, batch updates, templates, recurring
  issues, migrations; a CSV row is reported and skipped), SCIM `userName`,
  `externalId`, names and group `displayName`/`externalId` over 320 characters
  (SCIM `invalidValue`), MCP upload `title`/`subtitle` over 512 characters,
  account and member names over 320, emails over 254, avatar URLs over 2,048,
  team keys over 32, customer tier and owner over 191 and CSV source ids over
  160 bytes. Lengths count characters, not bytes. Long search queries are kept
  in search history cut to 300 characters. Webhook response bodies, agent run
  errors and chat job errors are cut on a character boundary and stored as
  valid UTF-8, so a receiver's binary or truncated response no longer fails
  the delivery on strict MySQL.
- On MySQL and PostgreSQL, generating or refreshing Triage Intelligence
  suggestions no longer fails with HTTP 500 (`Data too long for column
  'field'`) when a suggested label, assignee, project or team has an id of
  more than a few characters, and the suggestions are saved. Schema migration
  17 widens `issue_attribute_records.field` from `VARCHAR(32)` to
  `VARCHAR(191)` and keeps its rows; on MySQL it rebuilds that table and
  writes to it wait until the rebuild finishes, see
  [Wider issue attribute fields](docs/operations.md#wider-issue-attribute-fields-mysql-postgresql).
  Longer fields are stored as a prefix plus digest, so no id length can fail
  the write.
- On PostgreSQL, text containing a NUL character (U+0000) no longer fails
  with HTTP 500 (`invalid byte sequence for encoding "UTF8": 0x00`). NUL is
  removed from JSON request bodies (REST, MCP, SCIM), Flow tool arguments
  (MCP clients and the built-in agent), query strings and CSV/JSON import
  cells on every database, so issue titles, descriptions, comments, names,
  keys, documents and searches are saved without it. The store also drops it
  from any other text before PostgreSQL sees it, and keeps it out of the
  stored issue, comment and workspace JSON, where one `\u0000` used to make
  later queries that read descriptions or notifications fail.
- The MCP tool schemas advertise the server's text limits as `maxLength`
  (`save_issue` title 2,048, `save_template` name and title 2,048,
  `prepare_attachment_upload` title and subtitle 512 and `contentType` 255,
  `save_attachment` title and subtitle 255, `save_team` key 32), so MCP
  clients and the built-in agent see them, and an over-long value is refused
  by the schema check with an error naming the field before anything runs.
- On MySQL, editing a document larger than about 64 KiB no longer fails with
  HTTP 500 (`Data too long for column 'previous_values'`) and no longer
  discards the edit. This affected workspaces with an enabled webhook, an
  event-triggered loop or a connected Feishu/Lark app, through the web app,
  the API, `save_document` and application tokens. Schema migration 16
  widens `domain_events.previous_values` and `auth_identities.claims_json` to
  `LONGTEXT` and `code_host_accounts.scopes` to `MEDIUMTEXT`, and keeps their
  rows. On MySQL it rebuilds those tables, and writes to them wait until the
  rebuild finishes. Plan for this on a large `domain_events` table, see
  [Large event and identity values](docs/operations.md#large-event-and-identity-values-mysql).
  A document edit's stored event no longer repeats the body of every version
  of the document. Those bodies are stored once, with the document's other
  versions, and webhooks and `GET` event reads still return the full previous
  values. Each stored row now grows with the document instead of its history.
- `GET /api/health` answers anonymous probes with only `{"status":"ok"}`
  (`"degraded"` with `503` when Redis does not answer); the build version,
  commit and Redis mode moved to the startup log, and a coordinator outage is
  logged once when it starts and once when it ends.

- The floating agent chat, the entity agent panel and the loop chat follow a
  reply that runs on the server like the Agent page does: after a reload, in
  another tab or when the reply was asked from another surface they show its
  text so far, live approval and connector question cards and Stop, and an
  answer given in one surface shows in the others at once. An approval is
  answered only once, and Stop ends the reply on the server from every
  surface, including a reply waiting on a connector question (which is
  cancelled instead of waiting until it times out).
- Rolling updates and rollbacks no longer break pages whose scripts and
  styles were built by the other version. A refresh served by a new replica
  stayed on the loading screen when an old replica answered its files with
  404, and a page opened before the deploy could not open new routes. A
  replica now finds another release's hashed `/assets/` file in the earlier
  releases its image carries (`/app/web-previous`, Dockerfile
  `PREVIOUS_RELEASE_IMAGE`), on the other replicas (`FLOW_ASSET_PEERS`, Helm
  headless `<release>-peers` Service, on by default), in an optional shared
  directory every release publishes to (`FLOW_ASSET_SHARED_PATH`, Helm
  `assets.shared`), or at published origins (`FLOW_ASSET_FALLBACK_URL`, Helm
  `assets.fallbackUrls`). A file found nowhere is a 404 with
  `Cache-Control: no-store`, never the app's HTML. As a last resort the page
  reloads once when an entry file fails before the app starts. See
  [Rolling updates and web assets](docs/operations.md#rolling-updates-and-web-assets).
- Every route under an issue (`/api/issues/{id}/…` and
  `/api/issue-records/{id}/…`: comments, reactions, relations, attachments,
  links, permissions, subscriptions, history, sharing…) accepts the issue's
  identifier, and an identifier it had before it moved to another team, not
  only its internal id. Pull and merge requests (branch, title, description,
  magic words) and release references from commits link a moved issue by its
  old identifier too, and `ENG-1` no longer matches `ENG-12`. Subscribers who
  cannot see the team an issue moved to are not notified of the move.
- Collaboration: an issue permission change is in force when its request
  returns (a downgraded member's next edit is refused, an upgraded one's is
  applied) and on other servers as soon as the change reaches them.
  Re-parenting an issue (PATCH `parentId` or a sub-issue relation) re-checks
  the open descriptions it affects at once, since sub-issues inherit their
  parents' grants. Issue updates no longer leave other servers reading the
  previous record from the shared cache.
- Data loss with several API replicas: archiving an agent session (and other
  audit entries recorded after a change: connector and MCP tool calls, code
  host accounts, personal connections, coding environments, OAuth credentials,
  SCIM) rewrote every issue and comment record, deleting issues another
  replica created at the same moment. These writes now add only the audit
  entry. Writes that still replace whole collections keep issues, comments and
  activity that another replica committed meanwhile, and run again on fresh
  data when they changed the same record (`409` after three tries).
- Security: `X-Forwarded-Proto` and `X-Forwarded-Host` were believed from any
  client when Flow built links (OAuth and MCP metadata, SCIM locations, signed
  file URLs) and chose `Secure` for cookies, and rate limits trusted the
  client-supplied first `X-Forwarded-For` entry. Forwarding headers are now
  read only with `FLOW_TRUST_PROXY_HEADERS=true`, and only from the proxies in
  the new `FLOW_TRUSTED_PROXIES` (IPs or CIDR ranges; Helm `trustedProxies`)
  when it is set; the client address is the right-most entry that is not a
  trusted proxy. Without trust Flow uses TLS, the `Host` header and the peer
  address, and the scheme of `FLOW_APP_URL` for requests to its host, so links
  and cookies stay `https` behind a TLS ingress. See
  [HTTPS detection and trusted proxies](docs/configuration.md#https-detection-and-trusted-proxies).
- Security: documents attached to a private team's issue or project before
  inherited document scope existed stayed readable across the workspace until
  they were attached or detached again. A one-time startup migration records
  the inherited teams of every such document (including deleted ones), without
  changing grants, the document's own teams, its version or update time. See
  [Inherited document scope](docs/operations.md#inherited-document-scope).
- A panic in a request handler is now logged once (`http handler panic`, with
  the request id, method, path and stack; no headers, query strings or
  bodies) and answered with a JSON 500 in the standard error envelope that
  carries the request id and the security headers. A response that already
  started, such as an event stream, is aborted instead, and WebSocket
  connections are left to their handler.
- Flow Agent: asking it to create a team of 40 people no longer runs out of
  steps. `save_team` adds everyone it finds in one call and lists the rest
  (not in the workspace, or a name several members share, with candidates)
  instead of failing on the first. It does not send workspace invitations:
  for people who are not members its result carries `inviteHint`, which the
  reply passes on (Settings → Administration → Members → Invite, the emails
  to paste, the team to select, Send invites); `get_user` takes `queries` to
  look up many people at once;
  `list_users` honours `orderBy: createdAt`; and team requests get `save_team`
  without a `load_tools` step. A chat reply or agent thread run may now take
  `FLOW_AGENT_MAX_TOOL_TURNS` turns (default 40, was 8) and
  `FLOW_AGENT_MAX_TOOL_CALLS` tool calls (default 150); an identical call that
  already failed is not run again, and a reply that reaches its budget ends
  with what it did and what is left, with "Continue" to pick up, instead of
  "The agent used too many steps".
- Security: a member who may only view or comment on an issue (a viewer or
  commenter permission or share, a guest's read-only share, a workspace member
  outside a public team, or a read-only API key) could change its description
  through the realtime collaboration socket, which stored and relayed the
  edits although saving the description is refused to them. The socket now
  applies the same rule as saving the description (editor or owner access and
  a credential allowed to write issues): read-only members join read-only,
  still see others' edits and cursors live, and their edits are refused
  (`document.readonly`) without being stored or relayed. Raising or lowering a
  member's issue or document permission, role, team membership or a parent
  issue's permission reaches open editors on every server (`document.access`):
  the editor turns read-only, keeping unsaved changes downloadable, or
  editable again without a reload. Read-only API keys can no longer edit
  standalone documents on the socket either.
- Moving an issue to another team (`PATCH /api/issues/{id}` with `teamId`, the
  SDK, MCP `save_issue` with `team`, the team picker) returned success but left
  the issue in its team and published no event. The move now gives the issue
  the target team's key and a new number (`previousIdentifiers` keeps the old
  ones, which still open, read and update it), the target team's status of the
  same name and type, takes it out of its cycle, maps team labels by name, and
  records the move in the history and the `issue.updated` event, which also
  closes the description for members who cannot see the new team. Batch updates
  and accepted team suggestions move issues the same way. Moving needs access to
  the target team (guests cannot move issues), and update requests with fields
  the endpoint does not support are rejected with 400 instead of ignored.
- Several API servers starting at the same moment on one PostgreSQL or MySQL
  database no longer crash with errors such as `tuple concurrently updated`
  or a duplicate `schema_migrations` key. They take turns through a database
  lock (a PostgreSQL advisory lock, MySQL `GET_LOCK`, or a lock file next to a
  SQLite database), and a server that waited skips the work already done.
  `FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT` (default `10m`) bounds the wait. With
  several replicas, scheduled workflow runs and retries run once instead of on
  each replica, and storage maintenance runs on one replica.
- Agent chat: a reply still running when the page is reloaded, or when the
  chat is open in another tab, shows its progress (text so far, tool calls,
  and approval and connector question cards you can answer) instead of only
  "Thinking…" until it ends; answering in one tab updates the others. The
  in-progress reply is saved with the chat (message `status: "streaming"`), a
  reply cut off by a server restart is marked interrupted with Retry, and
  with Redis coordination an answer or Stop that reaches another API server is
  forwarded to the server running the reply.
- Issue descriptions open for collaborative editing follow access changes: a
  member removed from the issue's team, losing a private team or a shared
  issue, suspended or removed from the workspace, or whose session was signed
  out or revoked leaves the description at once on every API server (with
  Redis coordination), stops receiving its edits and can no longer send any.
  The editor turns read-only, says the member no longer has access, keeps
  unsaved changes downloadable and reloads the issue. Standalone documents
  now follow permission changes made on another server too.
- With several API servers, a request right after a change on another server
  no longer sees the old state: accepting an invitation to a workspace created
  a moment ago, using a new team, API key or member, or a just-granted role
  returned 403 or 404 until the server applied the peer's change (about 1.5
  seconds). Each change now bumps a per-workspace version in Redis, and a
  server that is behind (or does not have the workspace yet) reloads it from
  the database before authorizing the request.
- Coding session pushes (`coding_session.*` on `/api/realtime/events`) reach
  only the people who may open the session; before, every workspace member's
  stream carried each session's issue, phase and state, including another
  member's private chat session.
- The workspace event stream recovers from a connection that died silently
  (for example behind a proxy that kept it open after the API restarted): the
  server's keepalive is now a `keepalive` event the web app watches, and a
  stream quiet for 45 seconds reconnects from its cursor. After a resync, open
  coding session views and agent task threads reload too, instead of waiting
  for their next push.
- Resuming the workspace event stream replays the whole 256-event window; a
  client that missed more than 64 events used to get a resync (a full reload)
  instead.
- A reconnected event stream that went quiet before its first keepalive (the
  API went away again behind a proxy that kept the connection) is reconnected
  too; it used to stay dead until the page was reloaded.
- With several API replicas (Redis coordination): subscribers receive changes
  in `seq` order, so a change made on another replica no longer arrives after
  a newer one and leaves the older value on screen; a stream resumed on another
  replica resyncs instead of replaying a different order (skipping or repeating
  events); events published while a replica's Redis subscription was
  reconnecting no longer vanish (subscribers resync); and a burst of changes
  from another replica shares one workspace reload, so it reaches subscribers
  in tens of milliseconds instead of building a backlog of half a second.
- The sandbox runner reads agent output lines on LF only, so a JSON record
  holding U+2028 or U+2029 is no longer split and dropped.
- Coding sessions with the docker provider start on Docker Desktop and other
  daemons that use the containerd image store: the sandbox token is written
  through a short-lived helper container, because the archive API refuses
  copies into the read-only sandbox container ("container rootfs is marked
  read-only").
- Starting a coding session from an issue answers with the reason (for
  example `agentUnavailable` when the built-in Flow agent is off, or an
  invalid model) instead of an internal error.
- A new coding-session branch no longer tracks the base branch, and a bare
  `git push` by the agent publishes it under its own name; a summary line that
  ends with a colon no longer leaves the colon in the commit subject and pull
  request title.
- The first edit of a document that had a body but no version history (created
  with content through the API, MCP or a template, or written by an earlier
  release) no longer loses the body it replaced: the pre-edit state is kept as
  the oldest version, and the version history lists it.

### Removed

- `POST /api/cycles/{id}/complete` and starting a cycle while another cycle is
  current. Move a cycle's end date instead; scheduled maintenance completes it
  after its last day. Starting today is limited to the next upcoming cycle
  while the team has no current cycle.
- Unused entity activity shell and split-view agent slot, including their styles and tests.
- Coding sandboxes no longer set the older duplicate sandbox and snapshot
  variables. Setup and prepare scripts detect the sandbox with
  `FLOW_SANDBOX=1` and a snapshot build with `FLOW_SNAPSHOT=1`.

## [0.1.0] - 2026-10-17

The release series has been reset to v0.1.0 using the current main branch.
Earlier release tags and GitHub Releases have been removed. The development
history below is retained for reference.

### Added

- Agent image, video and file attachments, on-demand document reading and
  media transcription, rich clipboard paste, chat renaming and delete with Undo.
- Lean bootstrap responses, resumable realtime change cursors, parallel route
  loading, smoother issue lists, and static asset caching.
- Pulse initiative updates, unread indicators and comment attachments.
- Updated sidebar menus and settings pages, including Billing and Usage & limits.

### Fixed

- Missing static assets return 404 and stale chunks trigger a guarded reload.
- Plain-text Markdown snippets preserve bare angle brackets.
- API inventory and SDK generated contracts match the server routes and types.
- PostgreSQL issue-list migration compatibility and asynchronous test races.

### Security

- Restricted authentication redirects, sanitized pasted HTML, removed browser
  persistence of API key secrets, and hardened public outbound requests.
- Correct JSON escaping, Unicode conversion bounds and SDK placeholder parsing.

## Archived 0.3.0 - 2026-10-14

### Added

- Flow Agent on every surface: side panel, floating chat
  in documents (quote chips, "Use document as context", chat history), agent
  conversations in project and document comment threads, delegation, streamed
  questions and option pills; writes apply immediately and stay inside the
  issues a chat has seen.
- Agent runtime: run queue, run tokens and an internal runtime API, with a
  worker on the OpenAI Agents SDK (`agent-worker/`).
- Third-party application agents and OAuth apps: install and re-consent
  flow, team access ("All public teams and select other
  teams…"), realtime webhooks, and a public admin API for apps and their
  credentials.
- Webhooks and API platform pages; `@flow/sdk`, the TypeScript SDK for the
  Flow API.
- Selection toolbar on every rich text surface (create issue from selection,
  ask agent, comment) and an "Issue created" toast with quick actions.
- Mermaid diagrams: a code block shown as the diagram, with source, error
  console and fullscreen views.
- Reworked Releases, Documents, Customer requests, Triage Intelligence,
  Insights, project statuses and templates, label settings and initiative
  hierarchy.
- Members page with Invited, Application and Inactive / Left workspace
  sections; redesigned transactional and invitation emails.
- New workspaces start with Getting started tutorial issues; product-demo
  feature tour clips in `docs/media/`.
- Team membership changes are audited with where each membership came from.
- Typed public contract for webhook management and deliveries, documents,
  project milestones, project and initiative updates, deletes and single-record
  reads (`GET /api/teams/{id}`, users, workflow states, labels, cycles,
  projects, initiatives, customers); JSON Schemas of every webhook payload in
  the OpenAPI `webhooks` section; `createAsUser` / `displayIconUrl` on issue,
  comment, document and link attachment inputs.
- `GET /api/issue-records` pages backward (`last` / `before`) and answers with a
  connection (`totalCount`, `pageInfo`) when asked with first / last / after /
  before / orderBy / `connection=1`; list routes with two response shapes
  document both and accept `connection=1`.
- `@flow/sdk` 0.3.0: `fetchPrevious()` for issues, generated webhook event
  types, writes for documents, milestones and updates, single-record getters.

### Changed

- Member and admin roles: admin pages open as "Not found" for
  members, read-only settings explain themselves on hover, invite entries and
  ⌘K commands follow the workspace's who-can settings, and nobody can leave
  their last active team (409 "You should be a member of at least one active
  team").
- Private teams: admins see and manage them in settings, but their content
  needs membership.
- Projects and initiatives get stable unique URLs.
- Deletes: permanent customers and loops, Copy/Restore-only
  trash rows, application uninstall.

### Fixed

- A stale cached issue could be served after an update.
- The saved language no longer resets to the browser language after logout.
- Markdown tables written through the API/MCP become editor tables;
  `save_project` no longer reports success for a state or date it didn't
  apply.
- Plain-text parts of emails were empty.
- Team routes refused a team key (`/api/teams/ENG/...`) in permission checks.

### Security

- A browser-session request without a workspace could read the most recently
  written workspace; it is now scoped to the viewer's own workspace.
- Attach-from-URL can no longer reach internal addresses.

## Archived 0.2.0 - 2026-10-04

### Added

- Recurring issues: the recurring issue is the first
  instance, the next one is created the day after its due date passes (team
  time zone), sub-issues and icon are recreated, and Team settings has a full
  New recurring issue page with an indexed, paged-safe list.
- Loops (scheduled and issue-triggered agent workflows) with versions, run
  replies with skills and attachments, and an audit log API.
- Flow Agent: streaming conversations with OpenAI Responses, Anthropic
  Messages and Chat Completions providers, Flow tool execution, replies that
  survive page reloads, and a stop control.
- AI Triage Intelligence and a dedicated triage issue page.
- Paged issue storage for large workspaces, Redis hot cache, and server-side
  grouping and counts.
- Project detail: resources with menus and brand icons, rich project
  updates, milestones, progress graph in paged mode, document reminders.
- Members, teams, projects and views list pages, settings and sub-teams,
  SCIM, OAuth applications, MCP OAuth workflows, integrations (GitHub, Slack,
  Jira, Figma, Sentry) and webhooks.
- Simplified Chinese coverage for settings.

### Changed

- Workspace writes cost proportional to the change instead of the workspace
  metadata size; reads never wait on a write's database time.
- MySQL connections default to `interpolateParams=true` (add
  `interpolateParams=false` to the DSN to opt out).
- Pulse summaries are scheduled in one write per tick.
- Slow workspace writes are logged with stage timings
  (`FLOW_SLOW_MUTATION_MS`, default 500).

### Fixed

- Member removal, team deletion, project changes and release edits that took
  tens of seconds (or ran out of memory) on large MySQL workspaces.
- MCP OAuth tokens accumulating as API keys; `save_team` demoting owners;
  theme reverting during slow settings saves.

## Archived 0.1.0 - 2026-08-30

### Fixed

- Read authentication values from the submitted form so Chrome-autofilled credentials are handled reliably.
- Disable development authentication tokens by default.
- Remove workspace-specific data transformations and product defaults.
- Upgrade the Go runtime and XML signature dependency to patched releases.

### Added

- Releases, Asks, team archive, and audit log workspace surfaces.
- Professional open source project documentation and community health files.
- Apache License 2.0 licensing and attribution notice.
- Continuous integration for the React and Go applications.
- Automated dependency update configuration.
- Frontend unit coverage, browser end-to-end tests, Go race/coverage gates, CodeQL, dependency auditing, and secret scanning.
- Versioned database migrations and PostgreSQL, MySQL, Redis, and S3 integration tests.
- Non-root, read-only container runtime with application health checks.

## 2026-08-17

### Added

- Initial Flow application with issue, project, initiative, cycle, document,
  customer, notification, search, authentication, and workspace management modules.
- React and TypeScript web client with English and Simplified Chinese support.
- Go API with SQLite persistence, local attachments, domain events, and real-time updates.
