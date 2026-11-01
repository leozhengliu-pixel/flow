# Desktop Agent P1/P2

Baseline: main `591c1de7`. Rechecked at 1440 x 900 in bundled Chromium launched with `--force-device-scale-factor=2`; no context scale option. The browser assertions verify `[1440,900,2]`. Cascading positions are counted once. This table expands the grouped findings in `/tmp/desktop-ref/agent.md`; its summary count is not an independent inventory.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| P1 home example label and three cards | Fixed already | Empty-history workspace renders the label and three working prompt cards. | `4310d0c1` |
| P1 hub category filters | Fixed already | Featured, Engineering, Triage, Reporting, Security and Support filter the real catalogue. | `4310d0c1` |
| P1 editor name typography | Fixed already | 32/500/40, flexible remaining width; no sampled title width. | `4310d0c1`, `07b8b79c` |
| P1 run view tabs | Fixed already | Overview and Run history; pane starts at y96. | `4310d0c1` |
| P1 run action | Fixed already | Run now uses the real endpoint; disabled loops expose a disabled action. | `4310d0c1` |
| P2 home title chevron | Fixed already | Desktop uses primary header ink. | `b15a7f72` |
| P2 attachment icon | Fixed already | Desktop composer uses its bright tool token. | `b15a7f72` |
| P2 empty send state | Fixed already | Enabled accent control; empty submission does not create a message. | `4310d0c1` |
| P2 Skills menu surface and width | Fixed | Fresh read-only reference measurement after animation: declared 174px minimum content width, 0.5px border, 12px radius, 6px list padding and 32px action. The earlier 170.5px box was captured while scaled. | `d8c57bbd` |
| P2 Skills header/search | Fixed | Fresh reference has an off-screen searchable input, not a painted search field or Select skill label. Flow retains keyboard filtering without the extra visible row. | `d8c57bbd` |
| P2 conversation Skills comparison | Fixed already | Same Agent-page composer uses Skills; the original note compared a different coding-session surface and explicitly did not establish an Agent-page defect. | `4310d0c1` |
| P2 table header hit targets | Fixed already | 24px buttons with 6px horizontal padding. | `4310d0c1` |
| P2 table group disclosure | Fixed already | 20px disclosure in a 36px band. | `4310d0c1` |
| P2 table owner target and behavior | Fixed | 140px column, 28px owner picker; selection persists through the existing loop update endpoint without opening the row. | `d8c57bbd` |
| P2 hub heading | Fixed already | 15/600/23 heading. | `4310d0c1` |
| P2 hub subtitle | Fixed already | 13/450 subtitle. | `4310d0c1` |
| P2 empty-list hub instead of modal | Fixed already | Empty My loops renders the hub inline; a populated list keeps its explicit New loop dialog. | `4310d0c1` |
| P2 template card anatomy | Fixed already | 12px padding, 10px radius, no extra border; height follows real text. | `4310d0c1`, `07b8b79c` |
| P2 Start fresh surface | Fixed already | Uses the measured subtle control surface; width follows its translated caption. | `07b8b79c` |
| P2 stacked location picker | Fixed already | Empty-list hub is inline, so opening its location picker produces one dialog, not two stacked dialogs. | `4310d0c1` |
| P2 extra editor location control | Fixed | Hidden only on desktop; chosen location remains in the breadcrumb and phone behavior is retained. | `d8c57bbd` |
| P2 schedule select typography/fill | Fixed already | 12/450 trigger, measured schedule surface and border tokens. | `07b8b79c` |
| P2 schedule date/interval/unit/time | Fixed already | 104px date minimum, 26px interval/unit/time, weight 400 and declared schedule tokens. | `07b8b79c` |
| P2 extra weekday row | Not applicable | Reproduced the same daily config with no weekdays: no weekday row. Weekly or explicitly restricted schedules retain their real choices. | `07b8b79c` |
| P2 instruction section height/frame | Fixed | Removed the extra desktop card padding and framing; retained the editor's declared 180px minimum rather than fitting a total section height. | `d8c57bbd` |
| P2 output-contract editor section | Not applicable | Flow Loops' output-contract review is a retained product feature. | `2be92340` |
| P2 reasoning level | Still open | No persisted loop reasoning-effort field or provider capability contract exists. Requires schema/API/version-snapshot support, runner mapping and provider capability validation before adding a functional selector. | `d8c57bbd` audit |
| P2 Web access label/status copy | Fixed | Desktop label is Web access; removed the additional configuration-status line from this editor. Existing setting and phone strings are unchanged. | `d8c57bbd` |
| P2 extra trusted-sources note | Fixed | Removed from desktop editor only; the actual workspace setting is retained. | `d8c57bbd` |
| P2 run breadcrumb trailing crumb | Fixed already | Loop is the current breadcrumb; no extra Run history crumb. | `4310d0c1` |
| P2 run breadcrumb overflow | Fixed | Reuses the working loop actions menu: edit, duplicate, move, ownership, access, favorite, copy, history, versions and delete. | `d8c57bbd` |
| P2 run subscribers | Still open | Loops lack a run-subscriber model and subscription delivery event. Requires permission-aware subscriber persistence, run-completion/failure notification policy, API and delivery tests. No decorative bell added. | `d8c57bbd` audit |
| P2 run title | Fixed already | 13/500/16 title. | `07b8b79c` |
| P2 run Instructions disclosure/card | Fixed | Title is the 24px disclosure with chevron; 12px card padding. No separate desktop Expand pill. | `d8c57bbd` |
| P2 extra run outputs card | Not applicable | Retained Flow Loops output-contract evidence for the run's actual version. | `2be92340` |

## Verification

Targeted component run: 93 tests passed in Agent page/empty-state and loop list/editor/run files (exit 0). Full lint, including typecheck, passed (exit 0). Bundled Chromium: the new three-case P1/P2 spec and two existing loop cases passed (exit 0). Every new case creates a workspace through `workspace-fixture`.

Phone gate: `mobile-gate agent / agent-home: 44 element checks, 0 failures` (exit 0). The existing conversation locator could not reproduce its shared-data fixture: `locator.click: Timeout 30000ms exceeded`, waiting for `main details > summary`. It is not reported as a pass. New layout declarations are desktop-scoped; the phone run disclosure is unchanged. No phone values or fixture expectations were changed.

Screenshots under `/tmp/codex-desktop-p12/shots/` were opened: `agent-skills.png` shows the stable compact skill menu and three example cards; `agent-loop-editor.png` shows the large name, compact daily schedule, unframed instructions and a saved reply-only contract; `agent-loop-run.png` shows tabs, overflow trigger and expanded Instructions without a separate pill. Intermediate animation/loading captures were rejected and replaced. The saved output contract is deterministic test data, not a real model claim.

## Review

`/W/agent`: click Skills, then Create skill. `/W/loops/all`: click an owner, select another active member. `/W/loops/new?draftId=L`: inspect name and daily trigger, then scroll to Web access. `/W/loop/L/run/R`: click Instructions and the breadcrumb overflow menu.

Product decisions: reasoning-effort support and run-subscriber delivery need the backend designs above. They remain open.
