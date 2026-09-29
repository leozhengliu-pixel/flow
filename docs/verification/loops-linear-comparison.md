# Loops — Linear parity (2026-09-29)

Observed in Linear workspace `flow-app12` (dark). Test loops were created there with the user's permission. Linear could
not execute runs in this trial workspace ("Your workspace's AI credits aren't set up yet"), so Linear run output is not
available; run behaviour below comes from the UI around runs.

## Loops page

- Header "Loops", tabs **My loops / All**, `Find loops…` search, filter and display buttons, `+ New loop` (top right).
- **No loops yet**: the page itself is the creation hub (below). With loops: a table grouped by team (group row with
  team icon + name + `+`), columns **Name ↓ · Trigger · Owner · Runs (30d) · Last executed**. Row: loop icon, name,
  one-line description (generated), trigger label (e.g. `Triage`), owner avatar + name, run count, relative time.
- `+ New loop` opens the same creation hub as a dialog.

## Creation hub ("Create a new loop")

- Title "Create a new loop", subtitle "Automate manual work for your team and keep your process moving", button
  **Start from scratch**.
- Composer "Ask Linear to build the loop for you" (attach, send = "Create loop from prompt").
- "or pick a template" — 2-column cards: icon, name, description, footer `trigger icon → action icon  action text`:

| Template | Trigger | Action line |
|---|---|---|
| Autofix bugs — Investigates new bug reports and starts a coding session when it finds a clear fix. | On triage | Start a coding session |
| Triage agent — Reviews incoming issues, adds context, and routes each one to the right owner. | On triage | Route the issue to an owner |
| Slack Q&A assistant — Answers product questions from the team in a selected Slack channel. | Hourly | Reply in the Slack thread |
| Weekly wrap — Summarizes the week's accomplishments for a person or team and shares the highlights. | Weekly | Share the highlights |
| Feature request report — Groups recent feature requests into a weekly report with themes and customer impact. | Weekly | Post a weekly report |
| Security alerts — Alerts a Slack channel and sets an SLA whenever a security issue is reported. | On issue change | Alert the Slack channel |

- Every entry point (scratch, prompt, template) first asks **"Where should the loop be created?"**: `Workspace` or a
  team. Triage templates disable Workspace with the hint "Triage loops must belong to a team".
- New loops are drafts (`/loops/new?draftId=…`) and show under **Drafts** in the sidebar until created.

## Editor (new / edit)

Breadcrumb `Loops › New loop`. Icon + large "Loop name" input, level picker (Workspace / team) at the right.

- **Trigger** card. Type menu: Schedule; Issue ▸; Project ▸; Initiative ▸; Release ▸; Team ▸.
  - Schedule row: `Starting [09/29/2026] every [1] [day|week|…] at [7AM]`; weekly adds `On Su Mo Tu We Th Fr Sa` chips.
  - Issue ▸ (with filter box): Created · Property updated · Status ▸ · Priority ▸ · Assignee ▸ · Agent ▸ · Project ▸ ·
    Team ▸ · Labels ▸ · New comment ▸ · New customer request. Status ▸ "Set to…": Any status, Triage, Backlog, Todo,
    In Progress, Done, Canceled, Duplicate.
  - Chosen event renders as a sentence: `Issue status is set ▾ to [Any status ▾] in [Select teams… ▾]`, template
    trigger `An issue is in triage`. `+ Add filter` adds conditions such as `Assignee is No assignee`.
- **Instructions** with "Compose with Agent" (new) / "Configure with Agent" (template/prompt draft); placeholder follows
  the trigger ("For example, summarize this week's progress, highlight blocked issues, and post a team update…" for
  schedules, "For example, review the issue's changes, check for blockers, and suggest next steps…" for issue events).
- **Connectors**: "No connectors added" + `+ Add connector`.
- **Permissions**: Team access ("Choose which team's data are available to this loop", `All public teams`); Allow
  changes outside triggering issue (event loops); Web search; Access code (Disabled / Read / Read & write, configured in
  Loops settings); Allow changes to externally synced issues and comments.
- Footer: `Cancel` · `Create loop`.

## Agent-assisted drafting

- **Template**: opens the editor pre-filled (name, trigger, full instructions) *and* the agent panel titled with the
  loop name: "I've opened a draft and written the instructions for you. A few details depend on how your workspace is
  set up, so I'll ask about those, and then you can publish it". It then asks elicitation questions with chip answers,
  e.g. "How much should the triage loop do on its own?" (Route and close clear duplicates / Route, but don't close /
  Suggest changes only), "Should the triage loop skip any issues that arrive in triage?" (Review all / Skip already
  assigned / Specify exceptions). Each answer → "Updated workflow definition draft" + loop card, editing instructions
  and trigger filters in place. After the last answer it **publishes and enables** the loop ("Created automation" card
  with `Ran 0 times (30d)` and team) and navigates to the loop page.
- **Prompt** ("Ask Linear to build…"): editor pre-filled from the request (name, schedule incl. weekday, detailed
  instructions with entity chips), panel titled from the request; reply ends "It remains a draft for your review; I
  didn't publish, enable, or run it".

## Loop page

Breadcrumb `Team › Loops › Loop ⋯`; right: "All team members can edit", copy link. Top-right `Edit`, `Run now`.
Icon, title, generated description, `Enabled` toggle, "Owned by <user>", "Last update …". **Run history** card ("Ran N
times over the last 30 days" ›), Trigger (read-only sentence + filter chips), Instructions (collapsed, `Expand`).

- `Run now` on an event loop opens "Search for issue to run loop on…" (recent issues; `↵ Run loop on`, `⌥↵ More actions`).
- `⋯`: Run loop on… · Disable · Duplicate · Move ▸ · Change owner ▸ · Favorite ⌥F · Copy ▸ · Show run history ·
  Show published versions · Delete.

## Run history page

`…/loop/<slug>/run/<id>`: left list (search runs, filter, display) with rows like `Manual run · ⚠ · 1s · Today`; right
pane: run time ("Today at 6:51 AM"), `Edit loop`, collapsed Instructions, result (or error card "Loop couldn't run …
Review AI usage"), 👍 👎.
