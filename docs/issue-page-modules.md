# Issue Page Module Replication

The Issue page is replicated as independently measurable modules. A module is
not complete because its static screenshot looks plausible. It must match the
observed structure, interaction states, keyboard behavior, and persistence
contract.

## Workflow

Each module follows the same sequence:

1. Capture live Flow DOM, computed styles, assets, and relevant client code.
2. Record dimensions and an interaction-state matrix.
3. Isolate the local component and its data contract.
4. Implement one module without opportunistic changes to adjacent modules.
5. Compare screenshots at the same CSS viewport and domain zoom.
6. Verify pointer, keyboard, focus, loading, error, and persisted states.

## Module Map

| Order | Module | Component target | Primary states |
|---:|---|---|---|
| 1 | Issue Surface and Header | `IssueHeader`, `IssueView` | full page, list context, narrow width, scrolled |
| 2 | Title Editor | `IssueTitleEditor` | idle, hover, focus, multiline, saving, error |
| 3 | Description Editor | `IssueDescriptionEditor` | empty, rich content, selection, slash menu, autosave |
| 4 | Core Properties | one picker per property | closed, hover, open, search, selected, cleared |
| 5 | Labels and Project | `LabelPicker`, `ProjectPicker` | empty, populated, multiple, removed |
| 6 | Sub-issues | `SubIssueList`, `SubIssueComposer` | empty, creating, populated, collapsed |
| 7 | Relations | `IssueRelations`, `RelationPicker` | related, blocking, blocked, duplicate |
| 8 | Attachments | `IssueAttachments` | empty, uploading, success, failure, preview |
| 9 | Activity and Subscribers | `ActivityTimeline`, `SubscriberControl` | subscribed, unsubscribed, event variants |
| 10 | Comments | `CommentComposer`, `CommentThread` | empty, editing, submitting, reply, reaction |
| 11 | Cross-module behavior | shared Issue controller | pane/full page, navigation, responsive, errors |

## Module 1: Issue Surface and Header

Reference: Flow `CLE-88`, dark theme, captured `2026-08-12` at a
`1470 x 754` CSS viewport.

### Measured Structure

| Element | Observed value |
|---|---:|
| Main panel | `x=244`, `y=8`, `1218px` wide, `12px` radius |
| Header row | `44px` high |
| Header icon button | `28 x 28px`, pill radius |
| Context actions | favorite followed by Issue options, `6px` visual gap |
| Sequence | `current / total`, then down/up segmented navigation |
| Command strip top | `60.5px` viewport y (`52px` below panel top) |
| Command button | `28 x 28px`, `9999px` radius |
| Command gap | `6px` between standalone buttons |
| Work control | two adjacent `28px` segments, no gap |
| Issue options | `242 x 489px`, `32px` option rows, `4px` below trigger |
| Mark as submenu | `190 x 173px`, five relation categories |

### Verified Semantics

- Breadcrumb retains the source context (`My issues`) and current Issue.
- Favorite uses `role="switch"` and `aria-checked`.
- Flow exposes `Issue.favorite` as a related `Favorite` object, not as an
  Issue update boolean. The local switch state is implemented, while its
  persistence remains a separate domain contract rather than an invented
  `IssueUpdateInput.favorite` field.
- Issue options is a menu trigger.
- Sequence navigation orders buttons as next/down then previous/up.
- Next/previous disable at sequence boundaries.
- Command strip order is URL, Issue ID, branch, then the segmented work
  control.
- Issue options is a searchable `dialog > listbox > option` command surface,
  not a generic dropdown menu.
- `Mark as` opens a nested relation menu containing Parent, Sub-issue,
  Related, Blocked, and Duplicate actions.

### Acceptance

- [x] Header extracted from the monolithic detail component.
- [x] Sequence count and boundary-aware navigation implemented.
- [x] Command order and segmented work control implemented.
- [x] Main panel, Header, and command strip measured within one CSS pixel at
  the reference viewport.
- [x] Searchable Issue options surface and Mark as submenu implemented from
  observed Flow structure.
- [x] Header control icons use SVG paths extracted from the observed Flow
  production DOM instead of approximate Lucide glyphs.
- [ ] Favorite persistence uses a real `Favorite` entity/API.
- [ ] Hover, focus-visible, open-menu, and disabled screenshots captured as
  checked-in visual fixtures.
- [x] Narrow Surface/Header behavior matched at `768 x 754`: full-width panel,
  off-canvas Sidebar, hidden sequence navigation, and retained command strip.

## Module 2: Title Editor

Reference: the same Flow Issue and viewports as Module 1.

### Measured Structure

| Element | Desktop | `768 x 754` |
|---|---:|---:|
| Editor rect | `x=309.02`, `y=112.25`, `667.87 x 32px` | `x=14`, `y=89.5`, `740 x 32px` |
| Field wrapper | `margin: 32px 0 8px`, `padding: 6px 0` | same |
| Type | `24px`, weight `600` | same |
| Editor line-height | `38.4px` | same |
| Paragraph line-height | `32px` | same |

### Verified Behavior

- Title is an independent ProseMirror editor with a paragraph-only document.
- Enter does not insert a paragraph or newline.
- Escape blurs the editor and returns focus to `body`.
- Focus adds no background, border, radius, outline, or shadow.
- Updates retain the shared `600ms` autosave controller and persist through the
  Go API into SQLite.

### Acceptance

- [x] Extracted from the generic rich text component.
- [x] Desktop and narrow geometry match within one CSS pixel.
- [x] Typography, line boxes, wrapping, and focus appearance matched.
- [x] Enter and Escape keyboard behavior verified.
- [x] Autosave and persisted reload behavior verified; probe data restored.
- [ ] Save failure rollback/retry behavior matched to Flow.

## Module 3: Description Editor

Reference: Flow `CLE-88`, desktop `1470 x 754`, rich description populated
with H2, paragraph, bold, inline code, list, and empty paragraphs.

### Measured Structure

| Element | Observed value |
|---|---:|
| Editor outer rect | `x=295.02`, `y=158.25`, `695.87px` wide |
| Content rect | `x=309.02`, `667.87px` wide |
| Editor inset | `10px 14px 16px`; horizontal margin `-14px` |
| Body text | `15px / 24px`, weight `450` |
| H2 | `20px / 28px`, weight `600`, `16px` bottom margin |
| Empty paragraph separation | `16px` |
| Selection toolbar | `493 x 35px`, `8px` radius, `.5px` border |
| Toolbar control | normally `26 x 26px` |

### Verified Behavior

- Description is an independent Tiptap/ProseMirror editor, not the title or
  comment editor with a different class.
- Live Flow exposes `role=textbox`, `aria-multiline=true`,
  `aria-readonly=false`, `spellcheck=true`, and `translate=no`.
- Non-empty text selection exposes fourteen controls in this order: block
  type, Bold, Italic, Strikethrough, Underline, Link, Quote, Collapse, Inline
  code, Code block, List, Create issue from selection, Ask agent, Comment.
- The local toolbar matches that order and implements all document formatting
  operations supported by the current schema. Collaboration-only and
  issue-creation actions remain visibly disabled until their domain operations
  are introduced; they are not wired to invented behavior.
- Slash commands support filtering, `ArrowUp`, `ArrowDown`, `Enter`, and
  `Escape`. The slash token is removed in the same editor transaction before
  the chosen block command is applied.
- Markdown and ProseMirror JSON are emitted from the same update transaction
  and retain the existing `600ms` autosave controller.

### Acceptance

- [x] Extracted from generic `RichTextEditor` as `IssueDescriptionEditor`.
- [x] Desktop editor geometry matches the reference within one CSS pixel.
- [x] Heading, paragraph, list, inline-code, code-block, quote, and link styles
  implemented.
- [x] Selection toolbar order, dimensions, selected states, and Escape behavior
  implemented.
- [x] Slash command pointer and keyboard flow implemented and locally verified.
- [x] Autosave emits Markdown plus structured ProseMirror JSON; undo probe was
  restored after verification.
- [ ] Structured state is migrated from the compatibility `descriptionState`
  field to a separate Yjs-backed `DocumentContent.contentState` aggregate.
- [ ] Stable block IDs, author attribution, inline comments, and collaborative
  cursors are implemented.

## Module 4: Core Properties

Reference: the same Issue and desktop viewport.

### Measured Structure

| Element | Observed value |
|---|---:|
| Property trigger | `28px` high, pill radius, `6px 10px` horizontal inset |
| Status command surface | `207px` wide, `12px` radius, `.5px` border |
| Status search header | `42.5px` high |
| Search input | `36px` high, `13px` type |
| Option row | `206 x 32px`, `14px 18px` horizontal inset |
| Flow status surface with 6 states | `241.5px` high |

### Verified Behavior

- Status, Priority, and Assignee are separate public components built over one
  private command-surface primitive.
- Opening focuses search. Arrow keys move `aria-activedescendant`, Enter
  selects, Escape closes, and outside pointer closes.
- Status reflects real workspace state count and ordering rather than a fixed
  screenshot height. Priority retains Flow's `0..4` values. Assignee search
  includes display name and email and supports the unassigned value.

### Acceptance

- [x] Generic `PropertyMenu` removed from Status, Priority, and Assignee.
- [x] Trigger geometry and Status surface geometry matched within one pixel.
- [x] Search, pointer hover, checked, selected, keyboard, focus, empty-result,
  and outside-dismiss states implemented.
- [x] Existing Go/SQLite persistence contracts retained and verified by tests.
- [x] Narrow viewport renders core properties as horizontally stable chips
  beneath the title and removes the duplicated trailing aside.
- [x] Due date uses the observed nested natural-language command surface
  (`Custom`, `Tomorrow`, `In one week`) and an empty value does not render a
  persistent Properties section.
- [ ] Save-failure rollback and retry behavior matched.

## Modules 5-8: Labels, Project, Sub-issues, Relations, Attachments

### Measured Structure

| Surface | Observed value |
|---|---:|
| Label picker | `207px` wide, maximum observed `372px` high |
| Project picker | `228px` wide, observed `269.5px` high |
| Sub-issue composer | `673.87 x 105.5px`, `8px` radius, `.5px` border |
| Sub-issue title editor | `624.87 x 17.33px` live text line |
| Relation type submenu | `190 x 173px`, five categories |

### Acceptance

- [x] Labels use a searchable multi-select surface with checkbox state, color,
  pointer and keyboard navigation. Existing chips can be removed directly.
- [x] Project uses its own `228px` picker with No project, team projects, and a
  visible but disabled Create project entry pending Projects-domain wiring.
- [x] Sub-issue creation uses separate ProseMirror title/description editors,
  inherited status/priority/assignee/labels controls, Enter focus transfer,
  `Cmd/Ctrl+Enter` submit, Escape, loading, and the existing Go create API.
- [x] Parent/sub-issue links and inverse relation semantics persist in Go and
  are covered by API tests.
- [x] Relation results use a searchable command surface with pointer,
  ArrowUp/Down, Enter, Escape, outside close, pending and error states.
- [x] Relation removal persists and maintains inverse relation cleanup.
- [x] Attachments support upload, image preview/file card, size, delete,
  uploading, failure and retry states.
- [x] API enforces the `20 MB` attachment boundary and cleans partial files.
- [ ] Drag/drop and clipboard-paste attachment entry are measured and matched.

## Modules 9-10: Activity, Subscribers, Comments

### Acceptance

- [x] Activity events and comments share chronological rendering without
  duplicating comment content as a second activity card.
- [x] Subscribe and Unsubscribe use one persistent subscriber ID contract;
  archived issues disable the mutation rather than disabling all subscribed
  issues.
- [x] Subscriber multi-select persists through Issue updates.
- [x] Comment composer is ProseMirror-based and retains the draft after a
  failed submission with an explicit Retry action.
- [x] Comment create stores text and structured `bodyData`.
- [x] Comment edit, delete, reply, and reaction toggle APIs, domain events, and
  UI states are implemented and covered by Go tests.
- [x] Comment hover actions, edited timestamp, nested replies, reaction count,
  and viewer ownership actions are implemented.
- [ ] Mentions and comment attachments remain disabled until their referenced
  entity/upload contracts are added.

## Module 11: Cross-module Behavior

### Acceptance

- [x] Full Issue detail and list navigation share the same Issue entity and
  mutation controller.
- [x] Title, description, properties, subscribers, comments, relations, and
  attachments persist through the Go API and SQLite domain event transaction.
- [x] Description writes a separate `DocumentContent` relation containing
  Markdown projection, ProseMirror JSON, and a base64 Yjs update produced with
  `y-prosemirror`; legacy `descriptionState` remains a compatibility read.
- [x] Desktop Issue content has no horizontal overflow at the measured panel
  width; title/description geometry remains aligned to the reference.
- [x] Loading, empty, upload error, comment error, relation error, and retry
  states are explicit rather than silent failures.
- [x] Frontend lint/build, Go tests, and `git diff --check` pass.
- [ ] Favorite persistence requires the real separate Favorite entity.
- [x] Concurrent Yjs sync, Awareness presence, remote cursors, reconnect,
  durable incremental updates, guarded snapshots, and Redis multi-instance
  forwarding are implemented for Issue descriptions.
- [ ] Inline-comment anchoring remains.
# Issue editor implementation map

The Issue description editor is split by behavior rather than by visual container:

| Module | Responsibility | Implemented behavior |
| --- | --- | --- |
| `editor-content.ts` | Storage boundary | Markdown projection, ProseMirror JSON, Yjs update serialization, guarded server reconciliation |
| `slash-command-extension.ts` | ProseMirror state | Root paragraph trigger detection, exact command range, list/nested-block exclusion |
| `slash-command-menu.tsx` | Command surface | Caret anchoring, grouping, selected row scrolling, no-results state |
| `editor-commands.ts` | Search | Label and keyword filtering |
| `selection-toolbar.tsx` | Selection formatting | Block type, bold, italic, strike, underline, quote, code, list, link |
| `link-editor.tsx` | Link editing | Inline URL input, normalization, apply/remove, Enter/Escape |
| `use-issue-autosave.ts` | Persistence controller | 600 ms debounce, serialized writes, dirty retention, retry, blur/navigation/unmount flush |

## Slash interaction matrix

| Input | Result |
| --- | --- |
| `/` at the start of a root paragraph | Opens the anchored Basic blocks menu |
| `/hea` | Filters to Heading 2 and Heading 3 |
| `/unknown` | Keeps the command range and renders a no-results state |
| Arrow Up / Arrow Down | Wraps through matching commands without moving the document caret |
| Enter / Tab | Deletes the full slash query and applies the selected command |
| Escape | Closes the menu and keeps typed text |
| Slash inside a list item | Remains regular text; no root block menu opens |
| Click a command | Preserves editor focus and applies the same command path as Enter |

Flow persists the observed storage layers (`description`, ProseMirror
`descriptionData`, and Yjs `contentState`). The Issue editor connects them to a
custom Yjs provider over `/api/realtime/socket`; the existing SSE stream remains
the lower-frequency workspace/entity event channel.
