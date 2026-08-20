# Linear Settings DOM audit: Features

Measured on 2026-08-20 in the user's authenticated Chrome session against
`https://linear.app/leozhengliu/settings/*`. Flow was verified against the
isolated local API at `http://127.0.0.1:5180/cleantrack/settings/*`. All Linear
values below came from live DOM/computed styles, not screenshots.

## Coverage and states

| Page | Linear and Flow states inspected |
| --- | --- |
| AI & Agents | Usage feedback, Agent, Coding sessions, Loops, Code Intelligence, Triage Intelligence, installed-agent guidance, summaries; toggles on/off/busy |
| Initiatives | Enabled/disabled, update schedule closed/open, Slack disconnected/connected, label count |
| Documents | Empty/list, New/Edit dialog, autofocus, invalid disabled Create, content editing, Delete, Cancel, Escape |
| Customer requests | Enabled, default-team portal, status/tier empty/list, create/edit/delete menus and dialogs, currency/format, manual edits, excluded/generic domain editors |
| Releases | Search, active/archived filters, empty/result table, New/Edit pipeline, teams/type/production/stages, archive/restore |
| Pulse | Enabled, daily/weekly/never schedule, open/focus/keyboard/Escape states |
| Asks | Slack and email empty/configured states, connect/disconnect, validated email dialog, remove |
| Emojis | Search, active/archive, native image chooser, size/type guard, upload dialog, archive/restore, empty states |
| Integrations | Search, six category tabs, enabled/disconnected cards, connect/disconnect busy state, empty result, horizontally scrollable mobile categories |

Archived state is applicable to release pipelines and emojis and was implemented
as a persistent API state, not a client-only filter. Buttons only appear when an
implemented mutation exists; unsupported informational states are static text.

## Linear DOM measurements

Desktop viewport: `1470 x 693`, DPR `2`.

| Element | Linear measured value |
| --- | --- |
| Settings sidebar | `x=0`, `y=0`, `244px` wide, full viewport height |
| Main surface | `x=244`, `y=8`, `1218px` wide, `649-677px` high, `12px` radius, `0.5px` border |
| Standard content column | `640px`, left edge `x=533` |
| Standard row | `640 x 65px`, `16px` horizontal padding, `12px` gap |
| Group corners | `10px` on the first/last outer corners |
| UI font | Inter Variable / SF Pro Display / system, `13px`, normal row line-height `19.5px` |
| Toggle | `30 x 20px`, `72px` radius, background/opacity `150ms ease-out` |
| Select | `30px` high, `1px 28px 1px 10px` padding, `8px` radius |
| Pill button | `32px` high, `12px` horizontal padding, pill radius, `13px/500` |
| SVG icons | normally `16-18px`, currentColor stroke, `1.5-2px` stroke width |
| Integration card | approximately `200.7 x 154px`, `8px` radius |
| Portal listbox | `z-index:500`, `12px` radius, `0.5px` border, `32px` rows, `104.5px` high for three options |
| Portal shadow | `0 6px 18px`, `0 3px 9px`, and `0 1px 1px` layers |

## Linear to Flow comparison

| Measurement | Linear | Flow measured | Result |
| --- | --- | --- | --- |
| Desktop main | `244,8 / 1218px` | `244,8 / 1218px` | exact |
| Sidebar width | `244px` | `244px` | exact |
| Content width | `640px` | `640px` outer, `638px` row content after border | equivalent box model |
| Row | `65px`, `16px`, `12px` gap | `65px`, `8px 16px`, `12px` gap | exact visible geometry |
| Main radius/border | `12px / 0.5px` | `12px / 0.5px` | exact |
| Toggle | `30 x 20`, `72px` | `30 x 20`, `72px` | exact |
| Toggle transition | `150ms ease-out` | `150ms ease-out` | exact |
| Select | `30px`, `8px` | `30px`, `8px` | exact |
| Portal (3 options) | `104.5px`, `z=500` | `106px`, `z=500` | +1.5px from Flow's 1px border |
| Portal radius | `12px` | `12px` | exact |
| Portal dark surface | theme-derived | `lch(12.72 0.85 272)` | correct dark token |
| Portal light surface | theme-derived | `lch(100 0 282)` | correct light token |
| Dialog | `12px` surface | `480px` wide, `12px`, `z=1000` | matched surface; responsive max-width added |
| Mobile main | responsive surface | `x=6`, `378 x 832` at `390 x 844` | no document/main overflow |

Flow uses Lucide's real SVG components for every available icon. Hover/focus use
the shared active-surface and focus-ring tokens. Menu entry resolves to the
shared `190ms cubic-bezier(.16,1,.3,1)` floating-surface animation; control
color/background transitions remain `150ms`.

## Data and API capability audit

| Linear behavior | Flow before | Completed capability |
| --- | --- | --- |
| Structured feature preferences | Feature flags only | Added `FeatureSettings` to workspace settings persistence |
| Customer status/tier/domain settings | Not expressible | Added structured options, currency/format, source/manual-edit and domain arrays |
| Release pipeline lifecycle | No pipeline entity | Added `ReleasePipeline`, create/update/archive/restore APIs and bootstrap data |
| Custom emoji lifecycle | No emoji entity | Added `CustomEmoji`, image data, create/update/archive/restore APIs and bootstrap data |
| Document templates | Existing CRUD | Reused real API and added empty/list/editor UI |
| Slack/other integrations | Existing connection API | Reused real connect/disconnect mutations |
| Ask email addresses | Not persisted | Added to structured feature settings with validation and removal |
| Local authenticated QA | Required production auth | Added opt-in `FLOW_AUTH_DISABLED` and `FLOW_HTTP_ADDR`; production defaults unchanged |

Backend handlers validate names, pipeline stages, image payloads and entity
existence. All new state survives bootstrap reload. The focused API tests cover
workspace feature persistence, release create/archive, and emoji create/archive.

## Portal, I18n, and entity names

Radix menus and dialogs were separately inspected after they were portaled out
of the page subtree:

- Dark/Chinese menu: `94 x 106px`, `z-index:500`, dark root token, options
  `每天 / 每周 / 从未`.
- Light/English menu: light root token, options `Daily / Weekly / Never`.
- Border `1px` in Flow, `12px` radius, three-layer shadow; menu items remain
  keyboard navigable and `Escape` closes the portal and returns focus context.
- Dialogs use `z-index:1000`, so they stay above menus, settings content and the
  mobile sidebar. The measured document dialog was `480 x 349px`; Template name
  received autofocus and Create was disabled while empty.
- UI labels and portal options use `useI18n()` at the portal-owning component.
  Dynamic team, template, pipeline, customer status/tier, email, user, emoji and
  integration brand names carry `data-i18n-ignore` and remain literal.

## Verification matrix

| Locale | Theme | Desktop `1470x693` | Mobile `390x844` |
| --- | --- | --- | --- |
| English | Light | all nine pages, dialogs, menus, empty/active/archive states passed | all nine pages passed; no page/main horizontal overflow |
| English | Dark | token switching and portal contrast verified through the same root theme provider | responsive rules are theme-independent; no overlap observed |
| Simplified Chinese | Light | UI translations and light portal verified; entity names unchanged | translated strings wrap inside rows and controls |
| Simplified Chinese | Dark | all nine pages scanned; dialogs, menu options, disabled/focus states passed | `390px` surface and off-canvas sidebar verified |

The mobile Integrations category strip intentionally scrolls horizontally; its
last categories start outside the first viewport but remain reachable inside
the strip. No control leaks into document-level horizontal overflow.

## Interaction and network acceptance

- Pulse select: click/Enter opens, Arrow keys are owned by the Radix menu, and
  Escape closes it.
- Documents: New opens a modal, name autofocus works, Create is disabled when
  invalid, and Escape closes without mutation.
- Customer requests: default-team portal showed `No default team / Cleantrack`;
  new-status dialog autofocused Name and kept Save disabled while empty.
- Release, Ask, emoji and integration actions call real APIs and expose busy or
  disabled state during mutation.
- After reloading `5180`, CDP counted a 3.5-second idle window:
  `PATCH /api/account/settings = 0`, `GET /api/bootstrap = 1`. The former
  account-settings/bootstrap feedback loop is no longer present.

## Verification commands

- `cd api && go test ./...`
- `cd web && npm run build`

Both passed. Vite reports only the existing large-chunk advisory; it is not a
Features settings regression.
