# Desktop Settings P1/P2

Inventory: `/tmp/desktop-ref/settings.md`; baseline `5613a190`. Rechecked at 1440 x 900 in bundled Chromium with `--force-device-scale-factor=2`, no context deviceScaleFactor; measured `[1440,900,2]`. Measurements and reviewed screenshots are under `/tmp/codex-desktop-p12-2/settings.json` and `shots/settings-*.png`. Repeated Language-row height/position consequences are grouped as one item.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| 1: Missing MCP connectors navigation | Fixed already | Administration link exists and opens its actual settings route. | `5613a190` |
| 1: Missing footer Help control | Fixed already | 24px help control opens the real menu; browser assertion. | `5613a190` |
| 1: Missing plan pill | Not applicable | Flow has no paid plans. No plan badge, billing page or checkout was restored. | `5613a190` |
| 2: Extra Language row/general-card height/interface cascade | Fixed already | Language remains usable in its separate section; no longer inserted into General or shifting Interface. | `5613a190` |
| 3: Missing account email | Fixed already | Actual API email displays; empty accounts truthfully show No email address. Unit/browser fixtures cover both. | `5613a190` |
| 4: Notification/account icon tiles | Fixed already | 32px icon slots, 65px rows and corrected label inset. | `5613a190` |
| 5: Extra Refresh action | Fixed | Absent from desktop Sessions; off-desktop Refresh still fetches the real session list. | `6f811c44` |
| 5: Missing session location/countries | Still open | UI renders supplied location/countryCodes, but `api/internal/store/auth.go` constructs account-session records without either field. This is not dismissed as a fixture-data difference; see backend decision below. | `5613a190` |
| 6: Time & region heading | Fixed already | 13/500/15.5, not 15px section display type. | `5613a190` |
| 6: Member onboarding heading | Fixed already | 13/500/15.5 and measured section rhythm. | `5613a190` |
| 6: Missing Announcements row | Fixed already | Existing configuration validates, saves and reopens persisted announcement content. | `5613a190` |
| 6: Delete workspace action | Fixed already | 32px pill treatment; destructive behavior retained. | `5613a190` |
| 8: Teams header columns | Fixed already | Corrected grid and toolbar/header spacing; live directory screenshot. | `5613a190` |
| 8: Missing column sorting | Fixed already | Every column is a sort button; both directions and actual resulting order verified. | `5613a190` |
| 9: Missing team subtitle | Fixed already | Actual workspace/team visibility sentence displays below the title. | `5613a190` |
| 9: Missing Team overview shortcut | Fixed already | 24px link opens the team's actual overview. | `5613a190` |
| 9: Missing row icons/short rows | Fixed already | 32px icon tiles in 65px overview rows. | `5613a190` |
| 9: Bold team-row titles | Fixed already | 13/500, not weight 700. | `5613a190` |
| 9: Missing right-side summary values | Fixed already | Real membership, enabled state, template and workflow counts. | `5613a190` |
| 9: Missing Project labels row | Fixed already | Functional row links to team project labels. No paid-plan qualifier added. | `5613a190` |
| 10: Issue-label column offsets | Fixed already | Corrected four right-hand tracks; screenshot and label-editor browser coverage. | `5613a190` |
| 10: Rules versus Used in | Fixed already | Used in heading and actual usage metadata, not a renamed inert control. | `5613a190` |
| 15: Integration search dimensions | Fixed already | 638 x 42 search field within the 640px section. | `5613a190` |
| 15: Missing Enabled block | Fixed already | Dedicated ENABLED section, separate from discovery; remains truthful when none are installed. | `5613a190` |
| 15: Missing enabled cards | Fixed already | 68px cards with actual connection state; isolated browser connection fixture verifies the populated case. | `5613a190` |
| 15: Discovery grid versus carousel | Fixed already | Featured 384px panel plus 45px category strip; previous/next, provider selection, search and detail navigation work. | `5613a190` |
| 16: Missing intro Docs link | Fixed already | Real documentation link in the intro. | `5613a190` |
| 16: Missing community help link | Fixed | Uses Flow's existing developer-community destination, not another service's community. Intro uses measured 22px line height, 4px top margin and 32px section separation; section headings remain at y206.5/381.5/578.5. | `6f811c44` |
| 16: Missing OAuth documentation | Fixed already | Real documentation link in the applications section. | `5613a190` |
| 16: Missing Webhooks documentation | Fixed already | Real documentation link in the Webhooks section; three Docs links asserted together. | `5613a190` |
| 18: Extra authentication-policy rows | Fixed | Admin bypass and two-factor controls moved to Authentication methods under Workspace management on desktop. Still functional, and phone order is unchanged. | `6f811c44` |

Counts: fixed already 26; fixed 3; not applicable 1; still open 1.

## Backend Decision

Session geography needs an explicit trusted data source and privacy policy. Existing domain/API fields already permit `location` and `countryCodes`, and the UI has rendering coverage, but the session store never populates them. Completing this requires: choose server-side IP geolocation or deployment-controlled edge metadata; define trusted proxy boundaries so client-supplied headers cannot spoof session location; decide precision, retention and historical country aggregation; persist/enrich metadata on session creation/refresh; and test authenticated session listing, local/private IPs and untrusted headers across the supported stores. No new geolocation service, tracking retention policy or fabricated geography was introduced in a visual closeout.

## Verification

Full `npm run lint` including typecheck and repository checks: exit 0. Seven targeted Vitest files: 82 tests, exit 0. Changed `desktop-settings-parity` spec: six tests, exit 0. It creates isolated workspaces; current-session action, policy placement, community destination, three Docs links and section positions are asserted in a real browser. No Go, REST schema, migrations or SDK changes.

Phone gate: 548 existing element checks; the only 16 failures are missing billing elements at both widths, matching the already-removed paid-plan surface. Those were not restored. No other settings screen failed. The changed API surface was rerun separately: `mobile-gate settings / api: 48 element checks, 0 failures`. Desktop conditions leave the session Refresh action and authentication-row order unchanged on phones; the existing off-desktop Refresh workflow test still passes.

## Screenshot Review

Opened every `settings-*.png`: preferences shows the unshifted General/Interface sections; profile shows actual empty-email state and editable account fields; notifications shows 32px icon slots; account-security shows the real current session without Refresh and without invented geography; workspace shows all three onboarding rows; teams shows sort controls; team overview shows icons, links and real summaries; issue labels shows Used in and the corrected columns; integrations shows an honest empty Enabled block and functioning discovery panel; API shows community/docs links and complete sections; workspace security shows standard authentication controls without the two extra policies in the first group. No final screenshot is a loading/error page. The populated Enabled case is separately covered by the isolated browser connection fixture, not claimed as an installed service in the scratch workspace.

Reviewer: `/W/settings/account/security` -> Sessions has no Refresh. `/W/settings/security` -> Workspace access retains its normal authentication rows; scroll to Workspace management -> Authentication methods for admin bypass/two-factor controls. `/W/settings/api` -> developer community and all three Docs links; the intro and section spacing remain aligned. `/W/settings/integrations` -> inspect Enabled, use carousel arrows/provider tabs, search, open a detail. `/W/settings/teams` -> click each header twice, then a team -> Team overview/project labels. All scratch routes use workspace `desktop-p12-1793532276117`.
