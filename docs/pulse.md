# Pulse

Pulse brings together updates from projects, initiatives, and teams so you can
follow progress across your workspace. It collects every project and initiative
update into one feed and can send you a daily or weekly summary in your Inbox.

## Turning Pulse on or off

Workspace admins manage Pulse in **Settings → Features → Pulse**.

- **Enable Pulse** turns the workspace-wide feed on or off. Turning it on sets
  the default workspace schedule to Daily when no schedule was chosen yet.
  Turning it off asks for confirmation and hides Pulse for everyone.
- **Default workspace schedule** (Daily, Weekly or Never) applies to every
  member who has not chosen their own schedule.
- **Your personal schedule** overrides the workspace default for you only.

Guests never see Pulse and never receive Pulse summaries.

## The feed

Open Pulse from the sidebar or press `G` then `F`. The feed has three built-in
tabs and any number of custom feeds:

| Tab | Key | Shows |
| --- | --- | --- |
| For me | `1` | Updates from initiatives and projects you are part of or subscribed to, updates you wrote, and updates that mention you. |
| Popular | `2` | Updates ranked by discussion and recency. |
| Recent | `3` | Every update you can see, newest first. |

Custom feeds (keys `4`–`9`, `0`) save a set of filters. Filter by author, team,
created date, update type, update health, initiative, project, project members,
project status, project status type or project labels, with "is" / "is not"
conditions that must all match or any match. Use **Find in feed…** to search
update text, project and initiative names, and authors.

Updates you have not seen yet are above the **Last seen** line. When new updates
arrive while Pulse is open, a **New updates available** pill appears at the top.
Archived projects and resources you cannot access never appear in the feed.

### Why am I seeing this?

On the For me tab, the card menu explains why an update is in your feed:

- You're mentioned in the update, or you wrote it.
- You own an initiative the project belongs to (owners are subscribed
  automatically), or you own the initiative.
- You're a member of the project, or of a project that belongs to the
  initiative or one of its sub-initiatives.
- You're subscribed to updates of the project, the initiative, or to project
  updates of one of the project's teams or initiatives. You follow team project
  updates of every team you belong to unless you unsubscribe.

A project belongs to an initiative whether it was added from the initiative or
from the project (the project's Initiatives property).

### Subscribing and unsubscribing

Use the subscription menu in the top-right corner of a project or initiative
page ("Pulse updates → Subscribe to project updates"), the team's Subscribe
menu ("A team project update is posted"), or the update card menu. On an
initiative update card, **Subscribe to {initiative}'s project updates** follows
every project update of the initiative and its sub-initiatives. An explicit
unsubscribe always wins: after you unsubscribe from a project you will not see
its updates in For me even though you are a member, until you subscribe again.

## What changed since the last update

When an update is posted, Flow records the project's status, priority, lead,
start and target dates and milestone progress (for initiatives: status, owner,
target date, projects and sub-initiatives). Completed milestones are marked as
done; milestones added since the previous update show only their current
progress. The card shows what changed since the previous
update, including **Progress since {date}** with milestone progress. Updates
posted before this was recorded show no change summary.

## Summary notifications

Pulse summaries arrive in your Inbox in the morning, at 06:00 in your local time
zone (the time zone of your browser, else your first team's time zone).
Daily summaries arrive every day, weekly summaries on Mondays. A summary covers
the For me updates posted since your previous summary, excluding your own, so
switching from daily to weekly (or changing time zone) never skips updates.
Every active member receives summaries, including invited members who have not
used Flow yet; guests never do.

- The Inbox item is titled **Daily Pulse** or **Weekly Pulse** and lists the
  projects and initiatives that posted, for example "Update from Mobile app" or
  "API, Mobile app and 3 other updates".
- Choose where summaries are delivered under **Settings → Notifications →
  Pulse summaries** (Inbox, desktop and email). Email follows your immediate or
  digest email setting.
- Right-click a summary and choose **Pulse frequency** to switch between Daily,
  Weekly and Never.

### Your Pulse

Opening a summary shows **Your Pulse**:

- **Summaries** (default) shows a one- or two-sentence summary of each update,
  grouped into project and initiative updates. When the workspace has an AI
  provider configured the summaries are written by AI with instructions not to
  add anything that is not in the update (update text is treated as content,
  never as instructions); otherwise — and always in HIPAA workspaces or when
  the workspace turned AI off — Flow shows the first sentences of each update.
  If the AI provider fails, Flow shows the first sentences and tries the AI
  again the next time the summary is opened (after a short wait). Use **Report invalid summary…** on a card to flag a
  wrong summary.
- **Updates** shows the full update cards with a table of contents.
- **Listen** reads the summary aloud (when your administrator enabled speech).
  Choose a playback speed from 0.75× to 2.25×.

## For administrators

- The AI summaries use the Flow Agent provider (`FLOW_AGENT_*`); speech uses
  `FLOW_TTS_*`. See [configuration](configuration.md#pulse-summaries-and-audio).
- The API is documented in `docs/openapi.json` under `/api/pulse/*`:
  `GET /api/pulse/feed`, `GET /api/pulse/unread`, `POST /api/pulse/seen`,
  `GET|PUT|DELETE /api/pulse/subscriptions/{type}/{id}`,
  `GET|PUT|DELETE /api/pulse/subscriptions/initiative/{id}/project-updates`
  (`{ "subscribed": boolean }`),
  `GET /api/pulse/summaries/{id}` (+ `/report`, `/audio`) and
  `GET /api/pulse/capabilities`.
- Realtime: project and initiative update events reach every member who can
  see the project or initiative, and a `notification.created` event
  (`{ "id", "recipientId" }`) reaches only the recipient whenever a
  notification (including a Pulse summary) is created.
- MCP clients can read updates with `get_status_updates`, filtered by project,
  initiative, user, created or updated date, and ordered by `createdAt` or
  `updatedAt`.
