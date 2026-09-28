# Agent features — Linear parity notes (2026-09-28)

Flow runs against an OpenAI-compatible gateway (`FLOW_AGENT_PROTOCOL=openai-chat-completions`,
model `gpt-6-luna`); Linear was observed in workspace `flow-app12` (dark theme). Nothing was posted or
saved in Linear — drafts created by Linear's agent were discarded.

## Surfaces compared

| Surface | Linear | Flow before | Fix |
|---|---|---|---|
| Agent page chat | Grounded answers, identifiers, ~10 s | Misread priority `0` as Urgent, cited `issue_1`, ~50 s | `list_issues` projections always carry `identifier`, add `priorityLabel`, fill `url`; grounding rules in the base prompt |
| New project › Create with Agent | Fills the form live, "Updated project draft" step + project card, one sentence, relative dates resolved ("two weeks" → Oct 12) | Raw JSON dumped in chat, dates left empty, unrequested milestones, numeric priority silently dropped, placeholder said "Linear" | Hide the JSON block, add the draft card, `Today:` in the prompt, priority labels, numeric priority mapping, milestones only on request, placeholder "Draft your project with Flow…" |
| Floating Agent panel (bottom-right) | Attaches the current page entity as a context chip; answers "summarize this project" directly; date header, bubble, "added to context", "Worked for N seconds", 👍 👎 copy; placeholder "@ to mention any issue, project, or document" | No page context ("Which project should I summarize?"), "You / Flow Agent" labels, no work group or feedback row | Page context from the route, thread styling aligned with the Agent page |
| Project update › Write with Agent | Auto-sends "Help me write an update for this project: [project]", titles the chat "Write … project update", writes the draft straight into the composer, shows a "Created draft" card that turns "Outdated / Restore" after discarding | Pastes a long prompt into the composer and waits; reply needs "Insert into update" | Auto-send with project context; `update` fenced block written into the composer; draft card |
| Chat titles | Generated ("Summarize Compare Test Project Status") | First line of the message | Parallel title request on the first turn (`agentAutoTitle`) |
| Issue › Work on issue | Menu: Copy as prompt (⌘⌥P), Configure coding tools…; no visible search | Same items plus a visible search row | Search hidden until typing |
| Entity panels (document / project sidebars) | Entity is part of the context | Only issue ids were sent | Send `documentIds` / `projectIds` |
| AI filter | "AI filter" first item in the filter menu | Present; 20 s hard timeout was too short for slower models | Timeout follows `FLOW_AGENT_TIMEOUT` (max 60 s) |

## Not comparable here

- Linear's New Issue dialog has no agent entry point.
- Initiatives/documents: the Linear workspace has none, and creating them would modify it.
- Agent actions that write (loops, delegation, approvals) need `FLOW_AGENT_WRITE_TOOLS=true`.
- Response latency is dominated by the model/gateway (Flow 25–50 s vs Linear ~10 s).

## Linear agent capability probe (2026-09-28)

Linear declines to list its tools, so capabilities were inferred from read-only prompts and the step rows it renders.

**Tool rows observed** (label · detail): Looked at users · team; Looked at assigned issues; Looked at issues · Team › View;
Looked at issue; Looked at issue activity; Searched issues · several quoted queries; Searched documentation; Searched the web
(answers cite links); Looked at project updates · project; Looked at project; Looked at project activity; Reviewing inbox;
Couldn't read Slack channel · No connected channel; Created draft.

**Step titles** (model-written phase summaries, 12px/500 rows, first one followed by a narration quote): Gathering project
updates, Evaluating drafting and delegation, Looking into search retrieval, Clarifying attachment needs, Inventorying saved
views / labels and cycles / templates / initiatives and documents, Checking customers and integrations, Considering tool
capabilities. While running, the group label is the current title with "…" ("Reviewing inbox…").

**Answer chrome**: inline entity chips (⁠FLO-3 Import your data), a list of referenced issue cards under the answer,
follow-up suggestion chips ("Compare FLO-2 and FLO-3", "Review migration steps"), 👍 👎 copy.

**Write capabilities it claims**: create/assign/re-status/re-prioritise issues and comment; update projects, milestones and
project updates; create or edit issue and project drafts; delegate an issue to an AI agent with instructions; schedule via
cycles, due dates, reminders and recurring issues.

Flow now has `report_progress` (step titles) and the health glyph on draft cards; missing tools and answer chrome are tracked
in the follow-up work.
