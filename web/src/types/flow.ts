export type UUID = string

export interface User { id: UUID; name: string; displayName: string; email: string; avatarUrl?: string; active: boolean; emailVerified: boolean }
export interface Workspace { id: UUID; name: string; urlKey: string; icon?: string; color?: string; region?: 'us'|'eu'|string; createdAt?: string }
export type WorkspaceRole = 'admin'|'member'|'guest'
export type TeamRole = 'owner'|'member'
export interface WorkspaceMembership { workspace: Workspace; role: 'Admin'|'Member'|'Guest'; joinedAt: string; issueCount: number }
export interface AccountBootstrap { viewer: User; workspaces: WorkspaceMembership[]; lastWorkspaceKey?: string }
export interface AuthSession { user: User; memberships: WorkspaceMembership[]; expiresAt: string }
export interface WorkspaceMember { user: User; role: WorkspaceRole; status: 'active'|'suspended'; joinedAt: string; lastSeenAt?: string }
export interface TeamMember { teamId: UUID; userId: UUID; role: TeamRole; joinedAt: string }
export interface Invitation { id: UUID; workspaceId: UUID; email: string; role: WorkspaceRole; teamIds: UUID[]; status: 'pending'|'accepted'|'revoked'; inviterId: UUID; expiresAt: string; createdAt: string; acceptedAt?: string; token?: string }
export interface InvitationPreview { id: UUID; email: string; role: WorkspaceRole; teamIds: UUID[]; expiresAt: string; workspace: Workspace }
export interface WorkspaceMutationInput { name?: string; urlKey?: string; icon?: string; color?: string; region?: string }
export interface Team { id: UUID; name: string; key: string; color: string; icon?: string; private?: boolean }
export interface Customer {
  id: UUID; name: string; logoUrl?: string; ownerId?: UUID; status: 'active'|'inactive'; tier?: string
  annualRevenue?: number; size?: number; domains: string[]; createdAt: string; updatedAt: string
}
export interface CustomerMutationInput { name?: string; logoUrl?: string; ownerId?: string; status?: Customer['status']; tier?: string; annualRevenue?: number; size?: number; domains?: string[] }
export type WorkflowStateType = 'backlog' | 'unstarted' | 'started' | 'completed' | 'canceled'
export interface WorkflowState { id: UUID; teamId?: UUID; name: string; description?: string; color: string; type: WorkflowStateType; position: number; default?: boolean; reserved?: boolean }
export interface IssueLabel { id: UUID; name: string; color: string; description?: string; issueCount?: number; scope?: string }
export interface ProjectSummary { id: UUID; name: string; icon?: string; color: string }
export interface DocumentContent { id: UUID; content: string; contentState: string; contentData: Record<string, unknown>; updatedAt: string }
export interface DocumentRevision { id: UUID; documentId: UUID; title: string; content: string; contentState?: string; contentData?: Record<string, unknown>; author: User; createdAt: string }
export interface FlowDocument { id: UUID; slugId: string; title: string; icon?: string; content: string; contentState?: string; contentData?: Record<string, unknown>; creator: User; projectIds: UUID[]; teamIds: UUID[]; subscriberIds: UUID[]; favorite: boolean; archivedAt?: string; createdAt: string; updatedAt: string; revisions: DocumentRevision[] }
export interface Issue {
  id: UUID; version: number; identifier: string; number: number; title: string; description: string; descriptionState?: string; documentContent?: DocumentContent
  priority: number; priorityLabel: string; sortOrder: number; estimate?: number; dueDate?: string
  createdAt: string; updatedAt: string; completedAt?: string; canceledAt?: string; archivedAt?: string
  team: Team; state: WorkflowState; assignee?: User; creator: User; labels: IssueLabel[]
  project?: ProjectSummary; cycleId?: UUID; parentId?: UUID; subscriberIds: UUID[]; reactions: Record<string, UUID[]>; subIssueIds: UUID[]
  relations: IssueRelation[]; attachments: Attachment[]
}
export type CycleStatus = 'upcoming'|'current'|'completed'
export interface Cycle {
  id: UUID; number: number; name: string; description: string; teamId: UUID; startsAt: string; endsAt: string
  status: CycleStatus; capacity: number; favorite: boolean; createdAt: string; updatedAt: string
}
export interface CycleSettings {
  enabled: boolean; durationWeeks: number; cooldownWeeks: number; startsOn: number; upcomingCount: number
  capacity: number; autoCreate: boolean; autoAddActive: boolean; autoAddDueDate: boolean
  autoAddStarted: boolean; autoAddCompleted: boolean; autoMigrate: boolean
}
export interface CycleMutationInput { name?: string; description?: string; startsAt?: string; endsAt?: string; capacity?: number; favorite?: boolean }
export type CycleSettingsMutationInput = Partial<CycleSettings>
export type IssueRelationType = 'related'|'blocks'|'blocked_by'|'duplicate'|'parent_of'|'sub_issue_of'
export interface IssueRelation { id: UUID; type: IssueRelationType; issueId: UUID; relatedIssueId: UUID }
export interface Attachment { id: UUID; issueId: UUID; title: string; url: string; contentType: string; size: number; createdAt: string; creator: User }
export interface Comment { id: UUID; version: number; body: string; bodyData?: Record<string, unknown>; parentId?: UUID; reactions: Record<string, UUID[]>; createdAt: string; editedAt?: string; user: User }
export interface ActivityEvent { id: UUID; type: string; createdAt: string; actor: User; metadata: Record<string, string> }
export interface Notification {
  id: UUID; recipientId: UUID; type: string; sourceType: string; sourceId: UUID; issueId?: UUID; projectId?: UUID
  commentId?: UUID; activityId?: UUID; actor: User; category: NotificationCategory; groupKey: string; occurrenceCount: number; latestActorIds: UUID[]
  readAt?: string; favoritedAt?: string; archivedAt?: string; deletedAt?: string; snoozedUntil?: string
  favorite: boolean; createdAt: string; updatedAt: string
}
export type NotificationCategory = 'assignments'|'statusChanges'|'comments'|'mentions'|'reactions'|'subscriptions'|'documents'|'updates'|'reminders'|'loops'|'integrations'|'billing'|'customerRequests'|'triage'
export interface NotificationChannelPreferences { enabled: boolean; categories: Record<NotificationCategory, boolean> }
export interface NotificationPreferences {
  userId: UUID; inbox: NotificationChannelPreferences; email: NotificationChannelPreferences; desktop: NotificationChannelPreferences
  emailFormat: 'immediate'|'digest'; delayLowPriority: boolean; immediateUrgent: boolean; soundEnabled: boolean
  desktopPermission?: NotificationPermission|string; updatedAt: string
}
export interface NotificationDelivery { id: UUID; notificationId: UUID; recipientId: UUID; channel: 'email'|'desktop'; status: 'pending'|'digest'|'pending-disabled'|'delivered'|'failed'; attempts: number; nextAttemptAt?: string; deliveredAt?: string; acknowledgedAt?: string; error?: string; createdAt: string; updatedAt: string }
export interface NotificationList { notifications: Notification[]; unreadCount: number }
export interface NotificationMutationInput {
  read?: boolean; favorite?: boolean; archived?: boolean; deleted?: boolean; snoozedUntil?: string | null
}
export interface Project {
  id: UUID; name: string; slugId: string; summary: string; description: string; icon?: string; color: string
  priority: number; priorityLabel: string; progress: number; health: 'onTrack'|'atRisk'|'offTrack'|'noUpdate'
  status: { id: UUID; name: string; color: string; type: string }; lead?: User; memberIds: UUID[]; labelIds: UUID[]; teamIds: UUID[]
  dependencyIds: UUID[]; initiatives: string[]; customers: string[]; resources: ProjectResource[]; milestones: ProjectMilestone[]; comments: Comment[]
  startDate?: string; targetDate?: string; issueCount: number; createdAt: string; updatedAt: string
}
export interface ProjectResource { id: UUID; projectId: UUID; type: 'link'|'document'; title: string; url: string; createdAt: string }
export interface ProjectMilestone { id: UUID; projectId: UUID; name: string; targetDate?: string; createdAt: string; updatedAt: string }
export type ProjectStatus = Project['status']
export interface ProjectUpdate {
  id: UUID; projectId: UUID; body: string; health: Project['health']; createdAt: string; editedAt?: string; user: User
  comments: Comment[]; reactions: Record<string, UUID[]>; dueAt?: string; missing?: boolean
}
export interface CustomerRequest { id: UUID; customerId: UUID; body: string; source: string; sourceUrl?: string; creator: User; issueId?: UUID; projectId?: UUID; attachments: Attachment[]; createdAt: string; updatedAt: string }
export interface Release { id: UUID; name: string; version: string; description: string; status: 'planned'|'inProgress'|'released'|'canceled'; targetDate?: string; projectIds: UUID[]; issueIds: UUID[]; subscriberIds: UUID[]; creator: User; archivedAt?: string; createdAt: string; updatedAt: string }
export interface AskApproval { id: UUID; askId: UUID; approver: User; decision: 'approved'|'rejected'; note?: string; decidedAt?: string }
export interface Ask { id: UUID; title: string; body: string; source: string; requester: User; teamId?: UUID; templateId?: UUID; status: 'pending'|'approved'|'rejected'; issueId?: UUID; approvals: AskApproval[]; createdAt: string; updatedAt: string }
export interface ProjectTemplate { id: UUID; name: string; description?: string; summary?: string; icon?: string; color?: string; statusId?: UUID; priority: number; teamIds: UUID[]; labelIds: UUID[]; creator: User; createdAt: string; updatedAt: string }
export interface SLARule { id: UUID; name: string; teamIds: UUID[]; filters: Record<string, unknown>; targetMinutes: number; pauseStatuses: string[]; businessHours: boolean; enabled: boolean; createdAt: string; updatedAt: string }
export interface IssueSLA { id: UUID; issueId: UUID; ruleId: UUID; startedAt: string; dueAt: string; pausedAt?: string; pausedMinutes: number; breachedAt?: string; completedAt?: string; remainingMinutes: number; status: 'active'|'paused'|'breached'|'completed'|'removed' }
export interface SLAEvent { id: UUID; issueId: UUID; slaId: UUID; type: string; createdAt: string }
export interface Draft { id: UUID; userId: UUID; type: string; resourceId?: UUID; title: string; body: string; contentData?: Record<string, unknown>; metadata?: Record<string, unknown>; createdAt: string; updatedAt: string }
export interface Favorite { id: UUID; userId: UUID; resourceType: string; resourceId: UUID; position: number; createdAt: string }
export interface Subscription { id: UUID; userId: UUID; resourceType: string; resourceId: UUID; createdAt: string }
export interface AuditLogEntry { id: UUID; actor: User; action: string; resourceType: string; resourceId: UUID; metadata?: Record<string, unknown>; createdAt: string }
export interface TrashEntry { id: UUID; resourceType: string; resourceId: UUID; title: string; deletedBy: User; deletedAt: string; expiresAt: string }
export interface ImportJob { id: UUID; userId: UUID; filename: string; format: 'csv'|'json'; status: 'mapping'|'running'|'completed'|'failed'; headers: string[]; rows?: Record<string,string>[]; mapping?: Record<string,string>; imported: number; errors: string[]; createdAt: string; updatedAt: string }
export interface ExportJob { id: UUID; userId: UUID; format: 'csv'|'json'; includePrivate: boolean; status: 'queued'|'completed'|'failed'; filename?: string; error?: string; createdAt: string; completedAt?: string }
export type InitiativeStatus = 'proposed'|'planned'|'active'|'completed'|'canceled'
export interface Initiative {
  id: UUID; name: string; slugId: string; summary: string; description: string; icon?: string; color: string
  status: InitiativeStatus; priority: number; priorityLabel: string; health: Project['health']; owner?: User
  labelIds: UUID[]; projectIds: UUID[]; resources: InitiativeResource[]; comments: Comment[]; targetDate?: string
  favorite: boolean; subscribed: boolean; createdAt: string; updatedAt: string
}
export interface InitiativeResource { id: UUID; initiativeId: UUID; type: 'link'|'document'; title: string; url: string; createdAt: string }
export interface InitiativeUpdate {
  id: UUID; initiativeId: UUID; body: string; health: Project['health']; createdAt: string; editedAt?: string; user: User
  comments: Comment[]; reactions: Record<string, UUID[]>
}
export interface InitiativeMutationInput {
  name?: string; summary?: string; description?: string; icon?: string; color?: string; status?: InitiativeStatus
  priority?: number; health?: Project['health']; ownerId?: string; labelIds?: string[]; projectIds?: string[]
  targetDate?: string; favorite?: boolean; subscribed?: boolean
}
export interface BootstrapData {
  workspace: Workspace; viewer: User; users: User[]; teams: Team[]; customers: Customer[]; states: WorkflowState[]; labels: IssueLabel[]
  issues: Issue[]; cycles: Cycle[]; cycleSettings: Record<UUID, CycleSettings>; teamSettings: Record<UUID, TeamSettings>; issueTemplates: IssueTemplate[]; projectTemplates: ProjectTemplate[]; documents: FlowDocument[]; customerRequests: CustomerRequest[]; releases: Release[]; asks: Ask[]; slaRules: SLARule[]; issueSlas: IssueSLA[]; slaEvents: SLAEvent[]; drafts: Draft[]; favorites: Favorite[]; subscriptions: Subscription[]; auditLog: AuditLogEntry[]; trash: TrashEntry[]; importJobs: ImportJob[]; exportJobs: ExportJob[]; projects: Project[]; projectStatuses: ProjectStatus[]; projectDisplayDefault?: Record<string, unknown>; projectUpdates: Record<UUID, ProjectUpdate[]>; initiatives: Initiative[]; initiativeUpdates: Record<UUID, InitiativeUpdate[]>; comments: Record<UUID, Comment[]>; activities: Record<UUID, ActivityEvent[]>; savedViews: SavedView[]; notifications: Notification[]; notificationPreferences: Record<UUID, NotificationPreferences>; notificationDeliveries: NotificationDelivery[]; settings?: Record<string, unknown>
  members: WorkspaceMember[]; teamMembers: TeamMember[]; invitations: Invitation[]; viewerRole: WorkspaceRole
}
export interface TeamSettings { teamId: UUID; description?: string; timezone: string; estimateType: 'notUsed'|'exponential'|'fibonacci'|'flow'; defaultStateId: UUID; defaultPriority: number; issueEmailEnabled: boolean; detailedHistory: boolean }
export interface TeamSettingsMutationInput extends Partial<Omit<TeamSettings,'teamId'>> { identifier?: string }
export interface IssueTemplate { id: UUID; teamId: UUID; name: string; description?: string; body?: string; stateId?: UUID; priority: number; assigneeId?: UUID; projectId?: UUID; labelIds: UUID[]; creator: User; createdAt: string; updatedAt: string }
export type IssueTemplateMutationInput = Partial<Pick<IssueTemplate,'name'|'description'|'body'|'stateId'|'priority'|'assigneeId'|'projectId'|'labelIds'>>
export interface SavedView { id: UUID; name: string; description: string; icon?: string; color?: string; resource?: 'issues'|'projects'; scope: 'personal'|'team'|'workspace'; teamId?: UUID; ownerId?: UUID; favorite?: boolean; subscribed?: boolean; view: 'active'|'backlog'|'all'; filters: unknown[]; display: Record<string, unknown>; createdAt: string; updatedAt: string }
export interface SavedViewMutationInput { name?: string; description?: string; icon?: string; color?: string; resource?: 'issues'|'projects'; scope?: 'personal'|'team'|'workspace'; teamId?: string; ownerId?: string; favorite?: boolean; subscribed?: boolean; view?: 'active'|'backlog'|'all'; filters?: unknown[]; display?: Record<string, unknown> }
export type FilterField = 'status'|'priority'|'assignee'|'label'|'project'
export interface FilterCondition { id: string; field: FilterField; operator: 'is'|'isNot'; value: string }

export interface IssueUpdateInput {
  expectedVersion?: number
  title?: string; description?: string; descriptionState?: string; descriptionData?: Record<string, unknown>; contentState?: string; stateId?: string; priority?: number; assigneeId?: string
  projectId?: string; cycleId?: string; dueDate?: string; labelIds?: string[]; subscriberIds?: string[]; archived?: boolean
  parentId?: string; sortOrder?: number
}

export type SearchResourceType = 'issue'|'project'|'initiative'|'document'|'member'|'customer'|'release'|'view'
export interface SearchResult {
  id: UUID; type: SearchResourceType; title: string; subtitle?: string; identifier?: string; parentId?: UUID; parentType?: 'project'|'initiative'
  email?: string; icon?: string; color?: string; score: number; updatedAt?: string
}
export interface SearchHistoryEntry { query: string; useCount: number; lastUsedAt: string }
export interface RecentResource { resourceType: SearchResourceType; resourceId: UUID; lastViewedAt: string }
export interface SearchResponse { results: SearchResult[]; history: SearchHistoryEntry[]; recent: RecentResource[] }
export interface Presence { clientId: string; user: User; issueId?: UUID; route?: string; lastSeenAt: string }
export interface RealtimeEvent { id: string; type: string; aggregateId?: UUID; actorId?: UUID; clientId?: string; payload?: { presence?: Presence[] }; createdAt: string }
