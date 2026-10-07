# Members module — Linear parity spec (2026-09-28)

Measured against linear.app (dark theme, viewport 1470×708 CSS px, main panel x244 w1218).
Flow measured at the same viewport. All sizes are CSS px from `offsetWidth/offsetHeight`
(Linear popovers animate from `scale(0.98)`, so bounding rects in a hidden tab read ~2% small).

Colour reference (Linear dark → Flow token that already resolves to it in dark):
`#e3e4e6` primary text, `#959597` secondary text, `#565759` tertiary/count text, `#1b1c1d` subtle
button bg, `#121213` panel bg, `#161617` group header bg, `#26a644` online dot, `#5e6ad2` accent.
Always use existing tokens (see `web/src/styles/tokens.css`) so light theme follows automatically.

## 1. Workspace Members page — `/:workspace/members`

### Header (row 1, 44px, bottom hairline)
| Element | Linear | Flow today |
|---|---|---|
| Title | "Members" only, 13px/500 `#e3e4e6` at x263 | "Members 1" + an **Applications** tab — remove count and tab |
| Invite members | button 127×28 radius 9999, **no bg**, plus icon 14, text 12px/500 `#959597`; hover bg | has bg `#1c1c1d` |

Applications are **not** a separate tab: they are listed inline in the same table (Status "Application",
username under the name, Last seen shows a date).

### Toolbar (row 2, y53 h44)
- Search input "Find members…" 300×28 at x253 (Flow x263), bg = panel bg, 16px search icon, radius 999.
- Right: **Add filter** (x1392) and **Display options** (x1426), both 28×28 radius 9999 bg `#1b1c1d`,
  icon colour `#e3e4e6`. Flow only has the filter button (and at x1426).

Filter menu (from Add filter): 175×89, hidden search (type-to-filter, shortcut F), 32px items:
"Advanced filter", "Status ▸". Status submenu 175×109, opens to the left, checkbox items
"Admin" / "Guest" / "Member" each with a right-aligned "N member(s)" count (muted).

Display options popover: 301×136. Row "Ordering": direction toggle button 24×24 + dropdown 62×24
(radius 8, bg subtle) showing the field ("Name"; options Name / Status / Joined).
"Display properties" section: toggle chips **Status**, **Joined**, **Teams** (24px tall, radius 9999,
active bg ~`#2e2f31`, 12px/500 white; inactive muted). Toggling hides that column.

### Column header row (y96, h32)
- Sortable headers are pill buttons 24px tall, radius 9999, padding 0 6px, text 12px/450 `#959597`
  (Flow: 11px `#565759`, 25px tall). Sorted column shows a 12px arrow icon after the label.
- Buttons: Name x259 (text x265), Status x1086 (text x1092), Joined x1185 (text x1191).
  Non-sortable plain labels: Teams x1285, Last seen x1356. Flow Status column is at x1069 → move to x1086.
- Aria: "Order by Name, sorted ascending" etc.

### Rows (link to `/profiles/{username}`, 50px, radius 8, hover bg only, no context menu)
- Avatar 24px at x265, initials 11px/400 (Flow 9px).
- Name 13px/500 `#e3e4e6` (Flow 12px/450 white); username below 12px/500 `#959597` (Flow 11px `#565759`).
- Status: Admin/Owner → badge 48×19-ish, bg `#5f69d2 @ 0.20`, radius 3, padding 0 6px,
  text 12px/450 `#adbbff`. Apps → plain "Application" 12px/450 `#959597`. Flow shows plain "Owner" in `#565759`.
- Joined: "Sep 27" 12px/450 `#959597`, `title="Joined Sep 27, 6:25:59 PM"`.
- Teams: chip **button** 28px tall radius 9999 (hover bg) with team icon + team key, key 12px/450 `#e3e4e6`
  (Flow key `#959597`, 24px tall).
- Last seen: online → 8px dot `#26a644` with 1px ring in panel bg + "Online" 12px/500 `#959597`, 4px gap
  (Flow: 6px dot `#3db56b`, text `#565759`). Offline → date 12px/450 `#959597` with `title="Last seen Sep 27, 2026"`.

### Invite dialog (opened by "Invite members", and by Settings › Members "Invite")
- Modal 562×307, horizontally centred, top ≈154, bg = elevated panel (`lch(9.2 0.85 272)` ≈ `#101011`),
  radius 12, border 0.5px `lch(22.2)` ≈ `#303032`, large soft shadow.
- Header: workspace avatar 18×18 (radius 8 — rounded square) at x+16,y+18 then
  "Invite to your workspace" 15px/23px/450 white at x+42. Close button 28×28 round, 14px X, aria "Close modal dialog".
- "Email" label 13px/500 `#e3e4e6` at y+72; textarea 527×64 (x+16, y+95) radius 8, border 0.5px `≈#48494b`,
  bg same as modal, padding 6px 12px, 13px text, placeholder `email@gmail.com, email2@gmail.com…`,
  autofocused (accent focus border).
- "Role" label at y+182; select button 527×30 bg `#1b1c1d` radius 8 padding 1px 28px 1px 10px:
  "Member" 13px/400 + " - Full access with limited permissions" 13px/450 `#959597`, 10×5 chevron at right.
- Role listbox 333 wide, 32px options, check icon on the selected one, positioned so the selected option
  overlays the trigger: "Guest - Limited access to teams", "Member - Full access with limited permissions",
  "Admin - Full administrative access".
- "Send invites" 101×32 radius 9999 accent bg, 13px/500, bottom-right (y+261); dimmed/disabled until
  the textarea holds at least one valid email.

## 2. Member profile — `/:workspace/profiles/:username[/created]`

### Header
- "Open user" button 148×24 (auto width) radius 6 at x259 y18: avatar 16px + name 13px/500 `#e3e4e6`
  + chevron. Opens a **user switcher** popover (175 wide, hidden search, shortcut "O then U"), items
  31px: avatar 16 + name 13px/450; apps show an "Agent" badge (11px/500 `#9c9d9f`, 0.5px border, radius 6).
  Selecting navigates to that profile. Flow: plain "DU Dev User" text.

### Tabs + toolbar (y60)
- Assigned / Created pill tabs (matches already).
- Toolbar buttons 28×28 at x1324 / 1358 / 1392 / 1426 (34px pitch; Flow 36px pitch at 1318/1354/1390):
  "Add filter", "Display options", "Open insights" (Flow aria "Open view insights"), "Open details".
- Empty state: illustration + "No matching issues" 15px/23px/600 `#959597`.

### Details panel ("Open details", toggle ⌘I; tooltip "Close details ⌘ I" when open)
Flow currently opens the issue-view insights panel ("Issues / Labels / Priority / Projects") — wrong.
Linear shows a **profile aside** (≈440 wide, right side, under the toolbar row, list shrinks):
- Header card 428×69, bg `#1a1a1b`, border 0.5px `#29292c`, radius 10: avatar 44px (initials 18px),
  name 18px/500 white, second line "username ⋅ Online ●" 13px/450 `#98999c` with 8px `#26a644` dot;
  "Open menu" button 24×24 round at the right → menu 175 wide, one 32px item "Edit profile"
  (self only; goes to settings › account profile).
- Details card 428×171, 8px below, same styling: rows Email / Local time / Joined / Teams, 38px pitch,
  label 12px/450 `#98999c` at x+13, value 13px/500 `#e4e5e7` at x+117. Joined is relative ("5 hours ago")
  with full date tooltip. Teams: chip link (22px tall, radius 4, team icon + name 13px/450) to the team.
- Details open state persists.

## 3. Settings › Members — `/:workspace/settings/members`

| Element | Linear | Flow today |
|---|---|---|
| Title | "Members" 24px/32px/500 white at y73 | ok |
| Search | 300×32 at x301 **y118**, bg `#1b1c1d`, border 0.5px `#3f4042`, radius 8, placeholder "Search by name or email" | y137, bg `#161617`, border `#232426` |
| Filter | "All ⌄" button 54×32 at x607 y118, bg `#1b1c1d`, radius 8, 13px/400 `#e3e4e6`, aria "Member filter" | 56×30 bg `#232325` at y138 |
| Export CSV / Invite | 96×32 / 59×32 at y118 (x1242 / x1347) | y137 |
| Column headers | y171 row h32; pill buttons 24 tall, 12px/450 **`#959597`**; Name x298, Email x725, Status x1009, Teams x1155 (plain), Joined x1231, Last seen x1313 | text white; Email x654, Status x920, Teams x1064, Joined x1168, Last seen x1262 |
| Group header | x253 w1201 h32 bg `#161617` radius 8; "Active" 12px/500 `#959597` at x307; count 12px/500 `#565759` | bg indigo `#1c1e37`, count 11px |
| Row | 50px link, radius 8, hover bg | — |
| Avatar / name / username | avatar 24 at x305 (11px initials); name 13px/500 `#e3e4e6`; username 12px/500 `#959597` | username 11px/450 |
| Email | 12px/450 `#959597` at x731 | x654 |
| Status | Admin badge (as §1) at x1009; apps "Application" | grey "Owner" badge 11px |
| Teams | "1 team" 12px/450 `#959597` at x1155 | ok-ish |
| Joined / Last seen | 12px/450 at x1237 / dot 8px `#26a644` + "Online" 12px/500 at x1319 | 6px dot |

Groups: "Active", "Application" (apps), and the other filter states when present (Pending invites,
Suspended, Left workspace).

Filter menu ("All"): popover 142 wide at the trigger, 32px items with a check on the selected one:
All, Admins, Members, Guests, Applications, Pending invites, Suspended, Left workspace.

Selection:
- Each member row has a 14×14 checkbox at x266 (radius 3, 1px border `#737476`), hidden (opacity 0)
  until the row is hovered or anything is selected; aria "Select {username}". Checked: accent bg + border.
- Selected row: accent-tinted bg; a "…" button 28×28 appears at the right end of the row (also on hover).
  Row menu 175 wide, 32px items (for self: "Add to teams…", disabled when already in every team;
  for others additionally role change / suspend / remove as the backend supports).
- Bulk bar: bottom-centre pill 218×44, bg elevated panel, radius 9999, shadow; "1 selected" 12px/400 white,
  "⌘ Actions" button 84×28 radius 9999 bg `#1b1c1d` 12px/500 (opens command menu for the selection,
  aria "Open command menu"), clear X 28×28 aria "Clear selected". Esc clears.

## Verification notes (after the fix pass)

- Flow's generated theme maps `--theme-text-primary` to pure white; Linear's row/body text is
  `lch(90.8–91.2)`, so member names, team keys, profile values and dialog labels use `--text`.
- Surfaces that Linear pins to exact lch values (profile cards `lch(9.232)`, display popover
  `lch(12.72)`, role listbox `lch(16.433)` with `lch(25.69)` highlight, selected settings row
  `lch(15.966 18.242 286.4)`) got dedicated tokens because the generated theme overrides the
  shared surface tokens.
- Row hover in Linear is a `::before` inset 8px (x253 w1201); Flow rows use an 8px margin.
- Sort arrows point down for ascending (Linear "Name ↓" = A→Z).
- Online dots are 8px including a 1px ring in the surrounding surface colour (6px green fill).
- Status submenu hides the checkbox until hover/checked and omits zero counts.
- Settings bulk bar sits 16px above the panel bottom (`bottom: 52px`).
- Linear's row actions for *other* members (role change / suspend / remove) could not be observed
  (single-member workspace); Flow implements them only where the API already supports them.
