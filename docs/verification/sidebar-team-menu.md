# Sidebar Team Menu Verification

Date: 2026-09-17. Issue: HAI-75717.

## Behavior

The previous Radix trigger opened on pointerdown, while the menu overlapped
the trigger (`side=right`, `sideOffset=-24`). Radix menu items synthesize a click
on pointerup if they did not receive the matching pointerdown. That combination
permits an opening gesture to select a newly mounted item. The fix preserves
desktop positioning but mounts the menu after the completed trigger click.
This diagnosis is based on the installed Radix code and the reported behavior;
the exact original production pointer sequence was not captured.

Real Chromium pointer tests verified zero preference writes for opening with
mouse down/up, movement by +/-2px, Enter, Space, and mobile touch. Explicit
selection sends one favorite PUT. Escape restores trigger focus. Favorite
failure restores the original item and reports the error. These checks used
the production menu component with isolated HTTP fixtures, not production data.

Favorite state comes from shared resource preferences. Subscriptions use a
pending guard, rollback and visible failure feedback. Leave requires a
confirmation, checks the actual membership, managed membership, last owner,
active team count and child membership, and refreshes workspace data afterward.
Backend membership protections remain authoritative. Parent-team departure is
blocked until children are left, matching the current backend contract; this
change does not add cascading membership deletion.

Filtering reveals the input once text is entered, searches translated labels,
removes empty groups and reports empty results. Subscription submenus open on
hover. Keyboard Space toggles their checkboxes without invoking root filtering.

## Rendered Measurements

Reference measurements were read from the logged-in reference application's
rendered DOM through Chrome DevTools. The downloaded action bundle also confirms
hidden initial search, grouped subscription options, leave confirmation and
membership restrictions. Local measurements used Chromium at 1440x900 and
390x844, in English and Chinese, with dark and light themes.

| Property | Reference | Flow |
| --- | --- | --- |
| Main menu | 247x273px | 247x273px |
| Item height | 32px | 32px |
| Item horizontal padding | 14px left, 18px right | 14px left, 18px right |
| Hover background inset/radius | 6px / 8px | 6px / 8px |
| Container radius/border | 12px / 0.5px | 12px / 0.5px |
| Dark background | lch(12.72 0.85 272) | same |
| Dark foreground | lch(91.178 1.425 272) | same |
| Light background | lch(100 0 282) | same |
| Light foreground | lch(20 1 282) | same |
| Light border | lch(91.9 0 282) | same |
| Subscription submenu | 318x195px | 318x195px |
| Subscription option font | 13px / 400 | 13px / 400 |

Both themes' three-layer shadows match the rendered reference values. Settings
and archive SVG paths are shared with the team overview. Favorite, link,
subscription and Slack use existing shared glyphs. Leave has no leading glyph.

Mobile placement was corrected after detecting x=-17 overflow: the primary menu
now uses bottom/end placement with collision padding. Verified bounds at the
bottom edge of a 390x844 viewport: main menu x=8,y=461,w=247,h=273; subscription
menu x=8,y=620,w=317,h=195 before the final 1px reference correction. Neither
overflowed. Mobile opening caused no writes.

## Limits

After the Mac was unlocked, both themes' reference subscription submenus were
measured live. This found an additional 1px width difference and a stronger
submenu shadow; both were corrected. The submenu has five dark shadow layers
and three light layers, distinct from the primary menu. Local submenu hover,
touch, keyboard, bounds and save behavior were verified. Exact animation duration,
production deployment and production acceptance are not claimed by these checks.
