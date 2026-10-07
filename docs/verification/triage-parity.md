# Triage & Triage Intelligence — Linear parity (2026-09-29)

Observed in Linear workspace `flow-app12` (team Flow-app, Triage + Triage Intelligence enabled with the user's permission;
changes kept). Flow mirrored with DEV-2…DEV-5 (= FLO-1…FLO-4 onboarding issues) and four triage issues.

## Same four inputs, side by side

| Triage issue | Linear (agentic AI, ~1–2 min) | Flow today (token heuristic, instant) |
|---|---|---|
| Sync Slack and GitHub … stay in sync | **Related to** FLO-2 Connect your tools | related DEV-2 *Get familiar with Flow* (wrong), assignee = creator |
| App crashes when importing a large CSV file | **Related to** FLO-3 Import your data | no related issue; assignee = creator |
| Add keyboard shortcut to toggle dark mode | **No suggestions found** · Run again | assignee = creator |
| Project overview spacing differs from the Compare Test reference | **Suggestions**: assignee Skyler Anderson, project Compare Test | project Compare Test, assignee = creator (template reasons) |

**Flow with the model (gpt-6-luna, 2026-09-29, ~20 s per issue):** Slack/GitHub → related DEV-3 *Connect your tools* ✓;
dark-mode shortcut → no suggestions ✓; Compare Test spacing → project Compare Test + assignee (owner of the project's
other issue) ✓; CSV crash → the model reply was cut off by the 4096-token output cap (reasoning counts against it) and
fell back to the heuristic; triage calls now get 16k tokens.

Linear is conservative: no label suggestions when no similar issue uses the label, no assignee just because someone
created the issue, related issues only on real topical overlap; otherwise "No suggestions found".

## Linear issue page (issue in Triage)

- Status property reads **Triage** (orange triage glyph). Header right: **Accept**, **Decline**, a duplicate icon button,
  a snooze icon button. No inline triage bar.
- **Triage Intelligence card** directly under the title, above the description: bordered, radius ~8, padding ~12;
  header row "✳ Triage Intelligence" (13px/500) with a "…" menu on hover: *Show thinking…*, *Run again*,
  *Dismiss all suggestions*.
  - Relation rows: label column ("Related to" / "Duplicate of", 13px secondary) + issue chip (status icon, identifier
    secondary, title) with an **Apply** button on hover.
  - Property row: label "Suggestions" + dashed-border pill chips (assignee: avatar + name; project: project icon + name;
    label: colour dot + name; team: team icon + key).
  - Empty: "No suggestions found" + "Run again" button.
- Chip hover card (≈330 wide): entity header (project: icon, name, summary, status, priority; user: avatar + name),
  divider, "**Why this project/assignee was suggested**", 1–3 model-written bullets that cite the concrete evidence
  (e.g. "This project is the explicit Compare Test fixture referenced by the issue"), then a full-width
  "✓ Accept project suggestion" / "Accept user suggestion" button and a 👎 dismiss button.

## Triage view & actions (docs)

Accept `1` (optional comment, moves to the team's default status), Decline `2` (Canceled, optional comment),
Duplicate `3` or `MM` (Canceled + duplicate relation), Snooze `H`. Flow currently maps 2 = duplicate, 3 = decline.

## Settings

Linear: AI & Agents › Triage Intelligence — enable toggle; Behavior rows *Assignee / Project / Label / Team / Duplicate
issue / Related issue is suggested* → Show / Hide / Auto-apply; Workspace guidance textarea. Team triage settings page
shows "Triage Intelligence is not enabled in this workspace · View settings" when off.
