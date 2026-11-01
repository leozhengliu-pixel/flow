# Desktop Inbox P1/P2

Baseline: main `f38272ce`. Verified at 1440 x 900 in bundled Chromium with `--force-device-scale-factor=2`, no context scale option, and asserted `[1440,900,2]`. The table covers the two distinct P1 roots, 22 P2 summary entries and the additional unread-toggle/list-header entries marked P2 in `/tmp/desktop-ref/inbox.md`.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| P1 list/preview width and cascaded controls | Fixed already | List is 300px including its separator; preview and toolbar no longer have the 100px cascade. | `52eff32b` |
| P1 workspace welcome content | Still open | Title, body and resources render correctly; the unavailable-issue failure is fixed. The video is still an unconditional unavailable placeholder, not a player. Needs a Flow-owned video asset and a persisted, validated workspace media setting with an allowed embed/direct-media delivery policy. | `df899238`, `64db1c70`; current audit |
| P2 list-header padding | Fixed already | Declared title/header insets restored without a compensating group offset. | `4ec96ba0` |
| P2 repeated welcome actor | Fixed already | Same one-welcome fixture paints Welcome once, not an actor prefix plus title. | `52eff32b` |
| P2 preview unread count | Fixed already | One unread notification shown with one unread fixture. | `52eff32b` |
| P2 unread-toggle relabeling | Fixed already | Active unread-only state exposes Show read; Show all restores read rows. | `52eff32b` |
| P2 unread-empty copy/action | Fixed already | No unreads and a 32px ghost Show all notifications action, without the extra explanation. | `28e60d74` |
| P2 header-action ink | Fixed already | Bright desktop header tokens used; phone ink unchanged. | `52eff32b` |
| P2 Team filter | Fixed already | Working filter visible with a team in the same-data fixture. | `52eff32b` |
| P2 Initiative filter | Fixed already | Creating an initiative makes the real Initiative filter available; no fabricated entity option. | `52eff32b` |
| P2 extra Review status filter | Not applicable | With the same welcome-only notification data, Review status is absent. It appears only with review notifications. | `52eff32b`; `adec75b5` browser proof |
| P2 welcome preview title/extra controls | Fixed already | Welcome host has the plain header; issue favorite/options/subscription actions are not rendered there. | `df899238` |
| P2 extra context-menu Favorite/Copy | Fixed already | Compact desktop row menu has read/unread, snooze and delete; favorite/copy remain accessible on supported issue surfaces. | `658488a2` |
| P2 Delete notification truncation | Fixed already | Full label fits the measured 190px row menu. | `658488a2` |
| P2 snooze three-hour preset | Fixed already | Working preset persists approximately now plus three hours. | `658488a2` |
| P2 snooze dates | Fixed already | Five right-aligned actual date/time labels. | `658488a2` |
| P2 snooze icons | Fixed already | Leading icon on every preset/custom action. | `658488a2` |
| P2 editable snooze time | Fixed already | Typed in 3 hours persists through the real notification API and survives reload. | `658488a2` |
| P2 snooze width | Fixed already | Measured 318px menu and 291px input. | `eaa42777` |
| P2 Pulse illustration | Fixed | Uses the measured 91 x 86 vector and its 18 paths, with existing semantic paint tokens and no added stroke. Phone artwork retained. | `adec75b5` |
| P2 Pulse paragraph width | Fixed already | 340px column; correct conjunction retained. | `28e60d74` |
| P2 Pulse Documentation ink | Fixed already | Bright desktop text on the subtle button surface. | `28e60d74` |
| P2 Pulse add-view ink | Fixed already | Bright desktop token; actual view creation remains wired. | `52eff32b` |
| P2 Pulse subscription ink | Fixed already | Bright desktop icon token. | `52eff32b` |
| P2 Reviews leading icon/title inset | Fixed | 16px leading icon plus 6px gap places title at x284.5. | `adec75b5` |
| P2 Reviews onboarding actions | Fixed | Get started opens the real connection settings; opt-out persists codeReviewsEnabled=false then returns to Agent. Connected workspaces keep list tools; phone controls and both code hosts remain available. | `adec75b5` |

## Verification

Full lint/typecheck: exit 0. Targeted Reviews/Pulse component tests: 33 passed (exit 0). Nine browser cases passed across the new P1/P2 spec and existing inbox parity spec (exit 0). The stale context scale option was removed from that spec. Cases use fresh workspace fixtures and real welcome, snooze, comment-free preview and preference endpoints.

Phone gates: `mobile-gate inbox / pulse: 44 element checks, 0 failures`; `mobile-gate inbox / reviews-list: 26 element checks, 0 failures` (both exit 0). The browser test also checks that the new desktop Reviews controls remain absent at 375px and 390px.

All screenshots under `/tmp/codex-desktop-p12/shots/` were opened. `inbox-pulse.png` shows the corrected thin-edge stack and full-width text. `inbox-reviews.png` shows the leading icon, onboarding badge and opt-out, with both connection actions. `inbox-welcome.png` confirms the split frame/title/resources but also shows the known unavailable-video failure; it is limitation evidence, not a complete welcome verification pass. The earlier over-stroked illustration capture was rejected and replaced.

## Review

`/W/inbox`: open Add filter with an initiative present, then open the welcome item; check its plain header and remaining media limitation. Right-click a row, hover Snooze and enter a time. `/W/pulse/following`: inspect the illustration. `/W/reviews` without a code host: click Get started; return, then click I don't do code review. Connected workspaces keep their list and both supported hosts.

Product decision: provide an owned introductory video and choose workspace configuration/delivery policy. No external product's introductory media was embedded.
