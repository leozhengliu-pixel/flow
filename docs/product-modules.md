# Product Modules

## 1. Product Boundary

This project treats Flow as a complete work-management platform. Inbox is
one child module. The central product model is:

```text
Workspace
  -> Teams
    -> Issues
    -> Projects
      -> Milestones
  -> Initiatives
  -> Views
  -> Members and customers
```

Cross-cutting systems such as search, notifications, activity, permissions,
automation, and AI operate on these core objects.

## 2. Module Map

### M01. Application Shell and Navigation

Responsibilities:

- Authentication entry and workspace selection
- Persistent sidebar and team navigation tree
- Header, breadcrumbs, tabs, and split detail panels
- Favorites, recent locations, and navigation history
- Responsive layout and desktop-style keyboard navigation
- Loading, empty, error, offline, and permission-denied states

Primary surfaces:

- Main application shell
- Workspace menu
- Sidebar customization
- Help and account menus

Depends on: M02, M03, M17

### M02. Design System

Responsibilities:

- Color, typography, spacing, radius, border, and shadow tokens
- Light and dark themes
- Iconography and avatar system
- Buttons, inputs, menus, dialogs, tabs, tooltips, and toasts
- Table rows, board cards, property chips, and skeleton states
- Motion timing, focus treatment, hover states, and selection states

This module is required for pixel-level consistency. Page-specific styling
must not bypass shared tokens without a documented exception.

Depends on: none

### M03. Command and Interaction System

Responsibilities:

- Global command menu
- Workspace search launcher
- Keyboard shortcut registry
- Context menus and nested property pickers
- Multi-select and bulk actions
- Quick-create overlays
- Undo and optimistic-action feedback

Depends on: M02

### M04. Workspace and Team Foundation

Responsibilities:

- Workspace creation, selection, and metadata
- Team creation, joining, and team identifiers
- Workspace and team membership
- Team home, resources, members, and documents tabs
- Team-specific workflows and navigation
- Multi-team data boundaries

Core entities: `Workspace`, `Team`, `Membership`, `User`

Depends on: M01, M17

### M05. Issue Engine

Responsibilities:

- Quick and expanded issue creation
- Editable title and rich-text description
- Status, priority, assignee, labels, dates, and project assignment
- Parent and sub-issues
- Related, duplicate, blocking, and blocked-by relationships
- Attachments, subscribers, favorites, and issue identifiers
- Full-page detail and side-panel detail
- Copy URL, issue ID, and branch name actions

Core entities: `Issue`, `IssueStatus`, `IssueRelation`, `Label`, `Attachment`

Depends on: M02, M03, M04, M06, M07, M17

### M06. Rich Text and Documents

Responsibilities:

- Block-based rich-text editor
- Headings, lists, quotes, code blocks, links, and mentions
- Inline issue, project, user, and document references
- Image and file attachments
- Team and project documents
- Document organization and permissions
- Autosave, draft recovery, and collaborative editing readiness

Core entities: `Document`, `DocumentContent`, `Attachment`

Depends on: M02, M04, M17

### M07. Comments and Activity

Responsibilities:

- Issue and project comments
- Threaded replies where supported
- Reactions and mentions
- Subscribers and notification fan-out
- Immutable activity records for property changes
- Combined comment and history timeline

Core entities: `Comment`, `Reaction`, `ActivityEvent`, `Subscription`

Depends on: M04, M06, M12, M17

### M08. Issue Views and Board

Responsibilities:

- Issue list with virtualized rows
- Board grouped by status or another supported property
- Grouping, sorting, filtering, and collapsible groups
- Configurable visible fields
- Inline property editing
- Bulk selection and bulk updates
- Workspace and team issue scopes

Depends on: M05, M09, M10

### M09. Query and Filter Engine

Responsibilities:

- Structured filter expression model
- AND/OR filter groups
- Filters for status, assignee, creator, label, team, project, and dates
- Sort and group definitions
- URL serialization and shareable query state
- Server-side query execution and pagination

This is shared infrastructure. My Issues, Views, Projects, and Inbox should
not implement independent filtering systems.

Depends on: M17

### M10. Saved Views

Responsibilities:

- Issue and project saved views
- Private, team, and workspace ownership
- Persisted filters, sorting, grouping, and display settings
- Favorites, duplication, sharing, and permissions
- Custom sidebar placement

Core entity: `SavedView`

Depends on: M03, M09, M17

### M11. My Issues

Responsibilities:

- Assigned, created, subscribed, and activity tabs
- Personal grouping and display preferences
- User-scoped filters built on the shared query engine
- Personal workload and due-date visibility

Depends on: M05, M08, M09

### M12. Inbox and Notifications

Responsibilities:

- Assignment, mention, comment, update, and system notifications
- Unread, read, archived, and snoozed states
- Notification filtering and display settings
- List and side-detail synchronization
- Notification preferences and summary frequency
- Inbox counters and real-time delivery

Core entities: `Notification`, `NotificationPreference`

Depends on: M05, M07, M09, M17

### M13. Global Search

Responsibilities:

- Search issues, projects, initiatives, documents, and people
- Type-aware results and keyboard navigation
- Recent items and suggestions
- Direct navigation from the command menu
- Permission-aware indexing

Depends on: M03, M04, M05, M06, M14, M15

### M14. Projects

Responsibilities:

- Workspace and team project lists
- Project table, groups, filters, and saved project views
- Project overview, activity, and issue tabs
- Status, priority, lead, members, dates, and teams
- Milestones, resources, dependencies, and customers
- Progress metrics by status, assignee, and label
- Project updates, health state, comments, and update history
- Project templates, labels, and configurable statuses

Core entities: `Project`, `ProjectStatus`, `Milestone`, `ProjectUpdate`

Depends on: M05, M06, M07, M09, M10, M17

### M15. Initiatives and Roadmap

Responsibilities:

- Active, planned, and all initiative lists
- Initiative creation and detail
- Project-to-initiative association
- Cross-project progress and health aggregation
- Timeline and roadmap visualization
- Strategic owner, status, target date, and updates

Core entity: `Initiative`

Depends on: M14, M17

### M16. Cycles and Team Planning

Responsibilities:

- Team cycle configuration and recurrence
- Current, upcoming, and completed cycles
- Cycle issue assignment
- Capacity, scope, and completion statistics
- Rollover and auto-close behavior

Core entity: `Cycle`

Depends on: M04, M05, M08, M17

### M17. Identity, Permissions, and Realtime

Responsibilities:

- Authentication and session management
- Users, roles, guests, and workspace memberships
- Resource-level authorization
- Optimistic mutations and conflict handling
- Realtime updates and presence-ready transport
- Audit records and soft deletion

This module supplies platform behavior used by almost every business module.

Depends on: none

### M18. Pulse

Responsibilities:

- Personalized product-work feed
- For me, popular, and recent views
- Project updates and relevant activity summaries
- Reactions and comments
- Scheduled Inbox summaries

Depends on: M07, M12, M14, M17

### M19. Customers and Requests

Responsibilities:

- Customer directory and customer detail
- Customer request capture
- Link requests to issues and projects
- Request status and source tracking
- Customer-level feedback aggregation

Core entities: `Customer`, `CustomerRequest`

Depends on: M05, M14, M17

### M20. AI Agent

Responsibilities:

- Agent chat workspace and history
- Skills and file attachments
- Issue summarization, creation, decomposition, and editing
- Work-on-issue workflow
- User personalization and tool permissions
- Visible execution state and approval boundaries

Core entities: `AgentThread`, `AgentMessage`, `AgentRun`, `AgentSkill`

Depends on: M05, M06, M13, M17, M21

### M21. Integrations and Automation

Responsibilities:

- GitHub and GitLab issue, branch, commit, and pull request links
- Slack and notification-channel integration
- Connected accounts and OAuth lifecycle
- Webhooks, API keys, and third-party applications
- Workflow rules such as auto-assignment and status transitions
- Import and export

Core entities: `Integration`, `Webhook`, `ApiKey`, `AutomationRule`

Depends on: M04, M05, M12, M17

### M22. Settings and Administration

Responsibilities:

- Personal profile, preferences, notifications, security, and connections
- Issue labels, templates, and SLAs
- Project labels, templates, statuses, and update settings
- Feature configuration for AI, initiatives, documents, customers, releases,
  Pulse, and integrations
- Workspace, teams, members, security, API, and applications
- Billing, usage limits, and import/export

Depends on: all configurable domain modules

## 3. Shared UI Primitives

The following primitives should be implemented before duplicating page UI:

- `AppShell`
- `SidebarTree`
- `PageHeader`
- `SplitDetailPane`
- `CommandMenu`
- `ContextMenu`
- `PropertyPicker`
- `FilterBuilder`
- `DisplayOptions`
- `VirtualList`
- `DataTable`
- `IssueRow`
- `BoardColumn`
- `BoardCard`
- `RichTextEditor`
- `ActivityTimeline`
- `CommentComposer`
- `UserAvatar`
- `StatusIcon`
- `PriorityIcon`
- `EmptyState`
- `SkeletonState`

## 4. Out of Scope for the First Release

- Billing and payment processing
- Enterprise SSO and SCIM
- Full external integration marketplace
- Native desktop and mobile applications
- Advanced AI autonomous execution
- Multiplayer rich-text editing
- Full analytics and enterprise audit exports

These remain in the module map so the initial architecture does not prevent
their later implementation.

