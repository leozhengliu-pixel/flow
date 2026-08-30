# Linear Agent and AI UI parity evidence

Measured 2026-08-30 in the signed-in Linear workspace at a 1470 x 702 CSS-pixel
viewport with device pixel ratio 2.

## Verified Linear entry points

| Capability/API model | Linear UI entry | Verified visible behavior |
| --- | --- | --- |
| Agent session / AI conversation | `/leozhengliu/agent` | New-chat header, centered composer, skills menu, attachment action, disabled submit, persistent chat-history launcher |
| Agent activity | Agent conversation in the full-page or toolbar chat | User/AI documents, timestamp, work result, chat actions; it is not a separate administration page |
| AI prompt progress | Active Agent conversation | Transient execution state inside the conversation, not a standalone settings surface |
| Agent skills | `/leozhengliu/settings/account/agents` | Guidance editor, skills list/empty row, MCP connector section |
| Agent skill create | `/leozhengliu/settings/skill/new` | Breadcrumb, `Skill name`, `Skill instructions`, Cancel and Create |
| AI workspace controls | `/leozhengliu/settings/ai` | Usage summary, usage feedback, Agent feature cards, installed agents and summary controls |
| Usage/billing projection | `/leozhengliu/settings/usage` | Credits, reload, spend limits, period analytics and usage history |
| Usage limits | `/leozhengliu/settings/usage/spend-limits` | Workspace/user/Loop limits, reset cadence and overrides |

## Corrections made from evidence

- Removed the invented example-card section from Flow's Agent empty state.
- Retained Flow's own faint logo graphic instead of copying Linear branding.
- Changed personal guidance to the measured inline editor without an invented
  counter/save footer; persistence occurs on blur.
- Replaced the oversized skills empty state with Linear's compact bordered row
  and right-aligned plus action.
- Added explicit Chinese translations for the measured Agent settings copy.

## Evidence files

- `linear-agent-page.png` and `linear-agent-page.dom.json`
- `linear-agent-personalization.png` and `.dom.json`
- `linear-agent-skill-create.png` and `.dom.json`
- `linear-ai-settings.png` and `.dom.json`
- Flow counterparts use the `flow-` prefix.

No Linear data was created, edited, shared, subscribed, or deleted during this
audit. Opening menus and empty create states had no persistence side effects.
