# Feature tour clips

Short product demos of Flow (English UI, dark theme, invented "Acme" demo
workspace). Every clip exists as a GIF (for READMEs and docs) and as an
H.264 MP4 (sharper, smaller, with the same content).

The clips are generated, not hand-recorded: see
[`scripts/feature-tour`](../../scripts/feature-tour/README.md) to re-record them
after a UI change.

## Core workflow

| Clip | What it shows |
| --- | --- |
| ![Issues board](issues-board.gif) | `issues-board`: the issue list, switching to a board from Display options, and dragging a card to a new status. |
| ![Issue detail](issue-detail.gif) | `issue-detail`: opening an issue, changing a property, sub-issues and the activity feed. |
| ![Create an issue](issue-create.gif) | `issue-create`: the create dialog with title, description, priority, assignee, project and label. |
| ![Filters and views](filters-views.gif) | `filters-views`: combining filters and saving them as a view. |
| ![Inbox](inbox.gif) | `inbox`: reading a notification in context and snoozing another. |

## Planning

| Clip | What it shows |
| --- | --- |
| ![Projects](projects-overview.gif) | `projects-overview`: the projects list, then a project's properties, milestones and updates. |
| ![Initiatives](initiatives.gif) | `initiatives`: initiatives grouping projects toward a goal. |
| ![Cycles](cycles.gif) | `cycles`: the current cycle with scope, progress and workload. |
| ![Project update](project-update.gif) | `project-update`: posting a project update with a health status. |

## Documents and collaboration

| Clip | What it shows |
| --- | --- |
| ![Document editing](doc-editing.gif) | `doc-editing`: slash commands for a heading and a list, then bold and italic from the selection toolbar. |
| ![Selection toolbar](doc-selection-toolbar.gif) | `doc-selection-toolbar`: the toolbar that appears on a selection, ending in "Create issue from selection". |
| ![Inline comments](doc-comment.gif) | `doc-comment`: an inline comment thread anchored to a selection, with a reply and a reaction. |
| ![Issue comments](issue-comments.gif) | `issue-comments`: a threaded discussion on an issue with reactions and a reply. |

## Agent

| Clip | What it shows |
| --- | --- |
| ![Agent chat](agent-chat.gif) | `agent-chat`: asking the Agent page a question; it uses tools and answers with a table and live issue rows. |
| ![Floating chat](agent-floating-chat.gif) | `agent-floating-chat`: "Ask agent" on a document selection opens the floating chat with that text as context. |
| ![Mention Flow](agent-mention.gif) | `agent-mention`: mentioning @Flow in an issue comment; the working row and the reply in the thread. |
| ![Option pills](agent-options.gif) | `agent-options`: the agent asks a clarifying question with option pills, then creates the issue. |

Agent clips use the local model proxy configured in `.claude/launch.json`
(`deepseek-v4.1-flash`), so the wording of replies differs from take to take.
