# Workspace Menu Review

Review branch: `review/workspace-menu-fix`. This work is for review and has not been merged to main.

The starting worktree was `e4a727f6`. Its workspace menu component and workspace/sidebar styles are identical to the reported `8e61b07f` revision. Historical browser previews used complete archived source trees for `c238811b` and `e4a727f6`, the worktree's installed dependencies, and an isolated API/database. These previews reproduce the defects below. They are before evidence, not passing verification.

## Findings and Causes

| Defect | Cause commit / history | Fix | Crop evidence under `/tmp/codex-workspace-menu/shots/` |
| --- | --- | --- | --- |
| The desktop chevron is black despite its computed gray color, and does not follow the name. | `011c1baa9` replaced the stroked icon with an SVG path without a fill. The gray override originates in `93fc453cb`. Both defects already exist at `c238811b`; `3436a646` and `9a5240f9` corrected name typography but did not repair the icon. | Explicit `currentColor` fill and inherited foreground. Desktop name and chevron use the measured `lch(90.155 1.2 272)`. | `c238811b-desktop-closed-cjk.png`, `e4a727f6-desktop-closed-cjk.png`, `after-desktop-closed-cjk.png` |
| Trigger hover/open is too bright. | The `--theme-control` override is from `5eecb2c38c`, before `c238811b`. | Desktop hover/open uses the measured `lch(8.445 1.3 272)`; light mode uses the existing theme hover token. Phone open styling remains intact. | `after-desktop-hover-menu.png`; all `states-1440-*.png` sheets |
| The panel has fitted width/offsets and fractional row heights; square hovers touch the edges. | Square rows came from `011c1baa9`. `77875bfa` forced 219.5px, .5px offsets, an inset border shadow, 31.36px rows and 11.76px separators. All are present at `c238811b`. | Remove fitted rules. Natural content width with a 224px minimum, a real .5px border, 32px shared rows, 12px separators and inset 8px rounded hovers. A shared minimum-width variable also prevents the light theme's generic 160px minimum from shrinking this menu. | `c238811b-desktop-hover-menu.png`, `e4a727f6-desktop-hover-menu.png`, `after-desktop-hover-menu.png` |
| The Switch workspace row has an outlined chevron and can lose its active fill when keyboard focus enters the submenu. | The generic submenu arrow and highlight-only dark styling date to `93fc453cb`. | A filled triangle and explicit open-row fill scoped to this workspace trigger. | `after-desktop-switch-cjk.png`; keyboard browser assertions |
| A Chinese initial pair wraps and clips in a 20px avatar. | The 11px trigger initial style was introduced by `5a7a1502` on desktop and `98e729062` on phones. Both fail at `c238811b`. OAuth's 20px/11px avatar also exposed the same shared limitation. | Shared grapheme-aware initials; no wrapping; wide pairs scale to fit. `中文团队`, `中a`, and `a中` fit and stay centered. Settings uses the same initials renderer. The logo image path remains separate. | `c238811b-desktop-closed-cjk.png`, `c238811b-phone-drawer-cjk.png`, `after-desktop-closed-cjk.png`, `after-phone-drawer-cjk.png`, `cjk-settings-*.png`, `cjk-invite-*.png`, `cjk-oauth-*.png` |
| Selecting a workspace can return to the previous workspace's home page. | `a5188ef5a` changed switching to use the bare workspace route, exposing the old home redirect's workspace guard being too late. Reproduced at both historical revisions. | Check that the loaded snapshot belongs to the route before resolving its home page. | `c238811b-desktop-switch-result.png`, `e4a727f6-desktop-switch-result.png`, `after-desktop-switch-result.png`; exact URLs in `/tmp/codex-workspace-menu/audit.json` |
| Large workspace lists can exceed the screen. | The original submenu (`93fc453cb`) has no available-height limit or scrolling. | Constrain it to the floating menu's available height and scroll its contents. | `many-workspaces-desktop.png`, `many-workspaces-375.png`, `many-workspaces-390.png` |
| Long names can shrink a header action, and the phone Search button is 28px in light mode. | Original flex items could shrink; `03c83ca94a` adds an unconditional light-theme 28px override. | Header actions keep their natural fixed sizes; remove the duplicate light width so the phone's 32px rule applies. Truncation remains in the name and the chevron remains visible. | All `states-*.png` sheets; browser assertions compare action positions across names. |
| The workspace shortcut does not open the phone workspace list. | The desktop-only delayed selector remained when `c0d421e9` introduced the phone branch. `9c2f2028` and `355d431b` fix phone presentation, not this shortcut path. | Set the phone switch view directly. Desktop focus/open happens after the menu mounts, on the next animation frame. | Browser test covers `O then W`, arrows and Escape at desktop, 375px and 390px. |
| The advertised Log out shortcut can miss a modified physical Q key. | `78f6d1b9b` checks the character only. | Accept `KeyQ` as well as the Q character, and ignore unrelated modified/repeated sequence keys. | Browser test checks the real logout request; unit test covers the modified `Œ` character. |

The menu hover fill was not a color defect: read-only inspection of the live reference's hovered Settings item measured `lch(20.82 1.3 272)` and an inset 8px rounded child. The repair retains that measured fill and corrects its geometry. The reference capture's scaled rows are 31.36px; live settled rows measure 32px. Unsupported download and plan rows removed by earlier work were not restored.

Uploaded logos are rendered: an actual safe SVG was uploaded through the workspace logo API and its decoded image dimensions were checked in the trigger/drawer, workspace list and Settings. See `uploaded-switch-1440.png` and `uploaded-switch-390.png`. The CJK avatar was also checked in the invite dialog and OAuth header/list. The reference account's three available workspaces had Latin names, so no Chinese reference entity was available; the owner's fit-and-center rule was used without editing reference data.

## State Verification

Real bundled Chromium, isolated Vite at port 5317, isolated API at port 18327, auth disabled, and scratch database/storage under `/tmp/codex-workspace-menu/`.

The state runner checked 148 states across 12 viewport/theme/language combinations. Every combination checks shortest text `A`, a long mixed name, two Chinese initials, resting/hover/focus-visible triggers, an open menu with hovered Settings, keyboard-selected Invite, the selected workspace in its submenu, and the Settings logo resting/hover/focus-visible/busy-disabled. Desktop combinations also check the menu in the collapsed sidebar. Trigger and navigation rows have no disabled product state; no disabled styling was fabricated. The shared initials are decorative inside the workspace controls, rather than separate chips.

| Viewport | Dark English | Dark Chinese | Light English | Light Chinese |
| --- | --- | --- | --- | --- |
| 1440x900 | [13 states](/tmp/codex-workspace-menu/shots/states-1440-dark-en-US.png) | [13 states](/tmp/codex-workspace-menu/shots/states-1440-dark-zh-CN.png) | [13 states](/tmp/codex-workspace-menu/shots/states-1440-light-en-US.png) | [13 states](/tmp/codex-workspace-menu/shots/states-1440-light-zh-CN.png) |
| 375x667 | [12 states](/tmp/codex-workspace-menu/shots/states-375-dark-en-US.png) | [12 states](/tmp/codex-workspace-menu/shots/states-375-dark-zh-CN.png) | [12 states](/tmp/codex-workspace-menu/shots/states-375-light-en-US.png) | [12 states](/tmp/codex-workspace-menu/shots/states-375-light-zh-CN.png) |
| 390x844 | [12 states](/tmp/codex-workspace-menu/shots/states-390-dark-en-US.png) | [12 states](/tmp/codex-workspace-menu/shots/states-390-dark-zh-CN.png) | [12 states](/tmp/codex-workspace-menu/shots/states-390-light-en-US.png) | [12 states](/tmp/codex-workspace-menu/shots/states-390-light-zh-CN.png) |

Additional browser checks cover the left padding, avatar, name, chevron and right padding as click targets; name truncation without action movement; real workspace changes on desktop and both phone widths; Settings and Invite routes; logo upload/removal; CJK initials in the invite and OAuth surfaces; a two-workspace list and a stress list containing 65 additional fixture entries; Log out and its actual API request; and all advertised shortcuts. The extra list entries exercise scrolling, while actual workspace selections use real workspaces created by `workspace-fixture`.

Measured desktop repair: panel `(12, 48, 224, 153)`, first row `(18.5, 54.5, 211, 32)`, 6.5px left/right insets, 8px row radius, and a .5px border. Both themes and languages retain the 224px minimum.

## Review Clicks

1. Open `/:workspace/my-issues/assigned` at 1440x900. Hover the top-left workspace name, Tab to it, open it by Enter or ArrowDown, and move through Settings, Invite, Switch workspace and Log out. Check the chevron, inset hover, triangle and Escape return focus.
2. In `/:workspace/settings/workspace`, set the name on a review workspace to `中文团队`, `中a`, `a中`, `A`, then a long name. Return to the top-left trigger after each change. Search and New issue must remain fixed. Hover/focus the Logo control, upload an image, and verify it in the trigger and Switch workspace list.
3. Choose Switch workspace by hover and click; select another real workspace. Its name, route and content must change. Repeat with `O then W` and arrows. A long account list must scroll inside the viewport. Inspect the supplied many-workspace crops or run the browser spec for the stress fixture.
4. Click the sidebar resize separator to collapse it, reveal the sidebar using the header button, and open the workspace menu. The floating menu must remain on screen. `G then S` opens workspace Settings; Log out and Alt+Shift+Q issue the logout request.
5. At 375px and 390px, open the header sidebar button, then the workspace trigger. Check the phone menu, filter, workspace list and selection. Repeat in light/dark and English/Chinese.
6. Open `/:workspace/settings/members` and click Invite. The dialog header must show both CJK initials. Open a valid `/oauth/authorize?...` consent request and inspect its corner workspace control and list. The browser spec registers its own consent client and exercises both sizes.

## Checks

All heavy jobs use `~/.flow-local/heavy.sh`; only targeted suites were run. Exact command logs and browser measurement JSON are under `/tmp/codex-workspace-menu/`. The phone gate expectation and locator files were not changed.

- Full `npm run lint`, including its `npm run typecheck` and every foundation/token/motion check: exit 0.
- Targeted Vitest for workspace menu/initials, sidebar, Settings, invite dialog, OAuth and route/bootstrap navigation: 8 files, 100 tests, exit 0.
- Browser regression pass: 15 tests covering both changed specs and the existing phone shell menu/navigation checks, exit 0. The settled invite crops and all four arrow directions also receive a focused repeat.
- Required phone gate `shell/drawer`: 58 checks, zero failures, exit 0.
- Required phone gate `shell/workspace-menu`: 24 checks, zero failures, exit 0.
- Historical and state browser scripts: exit 0. Historical broken states are explicitly labeled before evidence.

The historical preview's source copies were removed from the worktree. Scratch processes and installed dependencies are cleaned up after verification. Crops and logs remain under `/tmp/codex-workspace-menu/` for review.
