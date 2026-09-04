export type UUID = string;

export interface User {
  id: UUID;
  name: string;
  displayName: string;
  email: string;
  avatarUrl?: string;
  active: boolean;
  emailVerified: boolean;
}
export interface Workspace {
  id: UUID;
  name: string;
  urlKey: string;
  icon?: string;
  logoUrl?: string;
  color?: string;
  region?: "us" | "eu" | string;
  createdAt?: string;
}
export type WorkspaceRole = "owner" | "admin" | "member" | "guest";
export type TeamRole = "owner" | "member";
export interface WorkspaceMembership {
  workspace: Workspace;
  role: "Admin" | "Member" | "Guest";
  joinedAt: string;
  issueCount: number;
}
export interface AccountBootstrap {
  viewer: User;
  workspaces: WorkspaceMembership[];
  lastWorkspaceKey?: string;
  workspaceRegionSelectorEnabled: boolean;
  workspaceDefaultRegion: string;
}
export interface AuthSession {
  user: User;
  memberships: WorkspaceMembership[];
  expiresAt: string;
}
export interface WorkspaceMember {
  user: User;
  role: WorkspaceRole;
  status: "active" | "suspended";
  joinedAt: string;
  lastSeenAt?: string;
}
export interface TeamMember {
  teamId: UUID;
  userId: UUID;
  role: TeamRole;
  joinedAt: string;
}
export interface Invitation {
  id: UUID;
  workspaceId: UUID;
  email: string;
  role: WorkspaceRole;
  teamIds: UUID[];
  status: "pending" | "accepted" | "revoked";
  inviterId: UUID;
  expiresAt: string;
  createdAt: string;
  acceptedAt?: string;
  token?: string;
}
export interface InvitationPreview {
  id: UUID;
  email: string;
  role: WorkspaceRole;
  teamIds: UUID[];
  expiresAt: string;
  workspace: Workspace;
}
export interface WorkspaceMutationInput {
  name?: string;
  urlKey?: string;
  icon?: string;
  logoUrl?: string;
  color?: string;
  region?: string;
}
export interface Team {
  id: UUID;
  name: string;
  key: string;
  color: string;
  icon?: string;
  private?: boolean;
  retiredAt?: string;
}
export interface Customer {
  id: UUID;
  name: string;
  logoUrl?: string;
  ownerId?: UUID;
  status: string;
  tier?: string;
  annualRevenue?: number;
  size?: number;
  domains: string[];
  createdAt: string;
  updatedAt: string;
}
export interface CustomerMutationInput {
  name?: string;
  logoUrl?: string;
  ownerId?: string;
  status?: Customer["status"];
  tier?: string;
  annualRevenue?: number;
  size?: number;
  domains?: string[];
}
export type WorkflowStateType =
  "backlog" | "unstarted" | "started" | "completed" | "canceled";
export interface WorkflowState {
  id: UUID;
  teamId?: UUID;
  name: string;
  description?: string;
  color: string;
  type: WorkflowStateType;
  position: number;
  default?: boolean;
  reserved?: boolean;
}
export type LabelResourceType = "issue" | "project" | "initiative";
export interface IssueLabel {
  id: UUID;
  name: string;
  color: string;
  description?: string;
  issueCount?: number;
  scope?: string;
  resourceType?: LabelResourceType;
  groupId?: UUID;
  creatorId?: UUID;
  createdAt?: string;
  lastAppliedAt?: string;
  archivedAt?: string;
}
export interface LabelGroup {
  id: UUID;
  name: string;
  color: string;
  description?: string;
  scope?: string;
  resourceType: "issue" | "project";
  createdAt: string;
  archivedAt?: string;
}
export interface ProjectSummary {
  id: UUID;
  name: string;
  icon?: string;
  color: string;
}
export interface DocumentContent {
  id: UUID;
  version: number;
  content: string;
  contentState: string;
  contentData: Record<string, unknown>;
  updatedAt: string;
}
export interface DocumentRevision {
  id: UUID;
  documentId: UUID;
  title: string;
  content: string;
  contentState?: string;
  contentData?: Record<string, unknown>;
  author: User;
  createdAt: string;
}
export interface DocumentPermission {
  id: UUID;
  documentId: UUID;
  subjectType: "user" | "team" | "workspace";
  subjectId: UUID | string;
  role: "owner" | "editor" | "commenter" | "viewer";
  createdAt: string;
  updatedAt: string;
}
export interface FlowDocument {
  id: UUID;
  slugId: string;
  title: string;
  icon?: string;
  color?: string;
  content: string;
  contentState?: string;
  contentData?: Record<string, unknown>;
  creator: User;
  projectIds: UUID[];
  teamIds: UUID[];
  issueId?: UUID;
  subscriberIds: UUID[];
  favorite: boolean;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
  revisions: DocumentRevision[];
  permissions?: DocumentPermission[];
}
export interface DocumentTemplate {
  id: UUID;
  teamId: UUID;
  name: string;
  description?: string;
  title?: string;
  icon?: string;
  content?: string;
  contentState?: string;
  contentData?: Record<string, unknown>;
  creator: User;
  createdAt: string;
  updatedAt: string;
}
export interface DocumentContentDraft {
  id: UUID;
  documentId: UUID;
  userId: UUID;
  content: string;
  contentState?: string;
  contentData?: Record<string, unknown>;
  version: number;
  createdAt: string;
  updatedAt: string;
}
export interface Issue {
  id: UUID;
  version: number;
  identifier: string;
  number: number;
  title: string;
  description: string;
  descriptionState?: string;
  documentContent?: DocumentContent;
  priority: number;
  priorityLabel: string;
  sortOrder: number;
  estimate?: number;
  dueDate?: string;
  slaBreachesAt?: string;
  slaType?: "all" | "onlyBusinessDays";
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  startedAt?: string;
  triagedAt?: string;
  statusChangedAt?: string;
  autoClosedAt?: string;
  canceledAt?: string;
  archivedAt?: string;
  shareToken?: string;
  sharedAt?: string;
  team: Team;
  state: WorkflowState;
  assignee?: User;
  delegate?: User;
  creator: User;
  labels: IssueLabel[];
  project?: ProjectSummary;
  projectMilestoneId?: UUID;
  cycleId?: UUID;
  addedToCycle?: "planned" | "during" | "after";
  agentSessionId?: UUID;
  suggestedLabelIds?: UUID[];
  externalSource?: string;
  autoClosed?: boolean;
  templateId?: UUID;
  parentId?: UUID;
  recurrence?: "daily" | "weekly" | "monthly";
  nextOccurrenceAt?: string;
  subscriberIds: UUID[];
  reactions: Record<string, UUID[]>;
  subIssueIds: UUID[];
  relations: IssueRelation[];
  attachments: Attachment[];
}
export type CycleStatus = "upcoming" | "current" | "completed";
export interface Cycle {
  id: UUID;
  number: number;
  name: string;
  description: string;
  teamId: UUID;
  startsAt: string;
  endsAt: string;
  status: CycleStatus;
  capacity: number;
  capacityByMember?: Record<UUID, Record<string, number>>;
  favorite: boolean;
  resources: CycleResource[];
  insight?: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}
export interface CycleResource {
  id: UUID;
  type: "link" | "document";
  title: string;
  url: string;
  documentId?: UUID;
  createdAt: string;
}
export interface CycleSettings {
  enabled: boolean;
  durationWeeks: number;
  cooldownWeeks: number;
  startsOn: number;
  upcomingCount: number;
  capacity: number;
  autoCreate: boolean;
  autoAddActive: boolean;
  autoAddDueDate: boolean;
  autoAddStarted: boolean;
  autoAddCompleted: boolean;
  autoMigrate: boolean;
  favoriteView: boolean;
}
export interface CycleMutationInput {
  name?: string;
  description?: string;
  startsAt?: string;
  endsAt?: string;
  capacity?: number;
  capacityByMember?: Record<UUID, Record<string, number>>;
  favorite?: boolean;
  status?: CycleStatus;
  insight?: Record<string, string>;
}
export type CycleSettingsMutationInput = Partial<CycleSettings>;
export type IssueRelationType =
  | "related"
  | "blocks"
  | "blocked_by"
  | "duplicate"
  | "parent_of"
  | "sub_issue_of";
export interface IssueRelation {
  id: UUID;
  type: IssueRelationType;
  issueId: UUID;
  relatedIssueId: UUID;
}
export interface Attachment {
  id: UUID;
  issueId: UUID;
  title: string;
  url: string;
  contentType: string;
  size: number;
  createdAt: string;
  creator: User;
  provider?: string;
  providerId?: string;
  providerUrl?: string;
  linkbackUrl?: string;
  syncStatus?: string;
}
export interface Comment {
  id: UUID;
  version: number;
  body: string;
  bodyData?: Record<string, unknown>;
  parentId?: UUID;
  reactions: Record<string, UUID[]>;
  createdAt: string;
  editedAt?: string;
  user: User;
}
export interface ActivityEvent {
  id: UUID;
  type: string;
  createdAt: string;
  actor: User;
  metadata: Record<string, string>;
}
export interface Notification {
  id: UUID;
  recipientId: UUID;
  type: string;
  sourceType: string;
  sourceId: UUID;
  issueId?: UUID;
  projectId?: UUID;
  reviewId?: UUID;
  commentId?: UUID;
  activityId?: UUID;
  actor: User;
  category: NotificationCategory;
  groupKey: string;
  occurrenceCount: number;
  latestActorIds: UUID[];
  readAt?: string;
  favoritedAt?: string;
  archivedAt?: string;
  deletedAt?: string;
  snoozedUntil?: string;
  favorite: boolean;
  createdAt: string;
  updatedAt: string;
}
export type NotificationCategory =
  | "assignments"
  | "statusChanges"
  | "comments"
  | "mentions"
  | "reactions"
  | "subscriptions"
  | "documents"
  | "updates"
  | "reminders"
  | "loops"
  | "integrations"
  | "billing"
  | "customerRequests"
  | "triage";
export interface NotificationChannelPreferences {
  enabled: boolean;
  categories: Record<NotificationCategory, boolean>;
}
export interface NotificationPreferences {
  userId: UUID;
  inbox: NotificationChannelPreferences;
  email: NotificationChannelPreferences;
  desktop: NotificationChannelPreferences;
  emailFormat: "immediate" | "digest";
  delayLowPriority: boolean;
  immediateUrgent: boolean;
  soundEnabled: boolean;
  desktopPermission?: NotificationPermission | string;
  updatedAt: string;
}
export interface NotificationDelivery {
  id: UUID;
  notificationId: UUID;
  recipientId: UUID;
  channel: "email" | "desktop" | "push";
  status: "pending" | "digest" | "pending-disabled" | "delivered" | "failed";
  attempts: number;
  nextAttemptAt?: string;
  deliveredAt?: string;
  acknowledgedAt?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}
export interface PushSubscription {
  id: UUID;
  userId: UUID;
  endpoint: string;
  p256dh: string;
  auth: string;
  userAgent?: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface TriageResponsibility {
  id: UUID;
  teamId: UUID;
  name: string;
  mode: "individual" | "roundRobin";
  userIds: UUID[];
  cursor: number;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface TriageRoutingRule {
  id: UUID;
  teamId: UUID;
  name: string;
  position: number;
  enabled: boolean;
  conditions: Record<string, string>;
  responsibilityId: UUID;
  priority?: number;
  labelIds: UUID[];
  createdAt: string;
  updatedAt: string;
}
export interface TriageAssignment {
  id: UUID;
  issueId: UUID;
  ruleId?: UUID;
  responsibilityId: UUID;
  assigneeId?: UUID;
  createdAt: string;
}
export interface WorkflowAction {
  type:
    | "notify"
    | "assignIssue"
    | "setIssuePriority"
    | "addIssueLabel"
    | "createIssue";
  config: Record<string, string>;
}
export interface WorkflowDefinition {
  id: UUID;
  name: string;
  description?: string;
  teamId?: UUID;
  trigger: "manual" | "schedule" | "issueCreated";
  schedule?: string;
  conditions: Record<string, string>;
  actions: WorkflowAction[];
  enabled: boolean;
  maxAttempts: number;
  nextRunAt?: string;
  lastRunAt?: string;
  lastRunStatus?: string;
  consecutiveErrors: number;
  creatorId: UUID;
  createdAt: string;
  updatedAt: string;
}
export interface WorkflowRun {
  id: UUID;
  workflowId: UUID;
  trigger: string;
  resourceType?: string;
  resourceId?: UUID;
  status: "running" | "succeeded" | "failed";
  attempt: number;
  error?: string;
  output: Record<string, string>;
  scheduledAt: string;
  startedAt: string;
  completedAt?: string;
  nextRetryAt?: string;
}
export interface EmailIntakeAlias {
  address: string;
  expiresAt: string;
}
export interface EmailIntakeAddress {
  id: UUID;
  teamId: UUID;
  localPart: string;
  domain: string;
  address: string;
  verificationToken?: string;
  verificationState: "pending" | "verified";
  verifiedAt?: string;
  aliases: EmailIntakeAlias[];
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface EmailIntakeMessage {
  id: UUID;
  addressId: UUID;
  messageId: string;
  from: string;
  subject: string;
  issueId?: UUID;
  status: string;
  error?: string;
  receivedAt: string;
  processedAt?: string;
}
export interface NotificationList {
  notifications: Notification[];
  unreadCount: number;
}
export interface NotificationMutationInput {
  read?: boolean;
  favorite?: boolean;
  archived?: boolean;
  deleted?: boolean;
  snoozedUntil?: string | null;
}
export interface Project {
  id: UUID;
  name: string;
  slugId: string;
  summary: string;
  description: string;
  icon?: string;
  color: string;
  priority: number;
  position?: number;
  priorityLabel: string;
  progress: number;
  health: "onTrack" | "atRisk" | "offTrack" | "noUpdate";
  status: { id: UUID; name: string; color: string; type: string };
  lead?: User;
  memberIds: UUID[];
  labelIds: UUID[];
  teamIds: UUID[];
  dependencyIds: UUID[];
  initiatives: string[];
  customers: string[];
  resources: ProjectResource[];
  milestones: ProjectMilestone[];
  comments: Comment[];
  descriptionRevisions: ProjectDescriptionRevision[];
  updateCadence: "none" | "weekly" | "biweekly" | "monthly";
  startDate?: string;
  startDateResolution?: "halfYear" | "month" | "quarter" | "year";
  targetDate?: string;
  targetDateResolution?: "halfYear" | "month" | "quarter" | "year";
  slackChannelId?: string;
  slackChannelName?: string;
  archivedAt?: string;
  issueCount: number;
  issueCountHistory?: ProjectProgressHistoryPoint[];
  scopeHistory?: ProjectProgressHistoryPoint[];
  completedScopeHistory?: ProjectProgressHistoryPoint[];
  inProgressScopeHistory?: ProjectProgressHistoryPoint[];
  progressHistory?: ProjectProgressHistoryPoint[];
  createdAt: string;
  updatedAt: string;
}
export interface ProjectProgressHistoryPoint {
  date: string;
  value: number;
  backlogEstimate?: number;
  unstartedEstimate?: number;
  startedEstimate?: number;
  completedEstimate?: number;
  scopeEstimate?: number;
  scopeCount?: number;
  completedIssueCount?: number;
  startedIssueCount?: number;
  completedEstimateToday?: number;
  completedIssueCountToday?: number;
  addedEstimateToday?: number;
  addedIssueCountToday?: number;
}
export interface ProjectRelation {
  id: UUID;
  projectId: UUID;
  relatedProjectId: UUID;
  type: "related" | "blocks" | "blocked_by" | "dependency";
  projectMilestoneId?: UUID;
  relatedProjectMilestoneId?: UUID;
  createdAt: string;
  updatedAt: string;
}
export type ProjectDependencyRelationType = "blocked_by" | "blocks";
export interface ProjectDependencyRelationInput {
  projectId: UUID;
  type: ProjectDependencyRelationType;
}
export interface ProjectDescriptionRevision {
  id: UUID;
  projectId: UUID;
  description: string;
  author: User;
  createdAt: string;
}
export interface ProjectResource {
  id: UUID;
  projectId: UUID;
  type: "link" | "document";
  title: string;
  url: string;
  pinnedTeamIds: UUID[];
  createdAt: string;
}
export interface ProjectMilestone {
  id: UUID;
  projectId: UUID;
  name: string;
  description: string;
  targetDate?: string;
  createdAt: string;
  updatedAt: string;
}
export type ProjectStatus = Project["status"] & {
  description?: string;
  position?: number;
};
export interface ProjectUpdate {
  id: UUID;
  projectId: UUID;
  body: string;
  bodyData?: Record<string, unknown>;
  health: Project["health"];
  createdAt: string;
  editedAt?: string;
  user: User;
  comments: Comment[];
  reactions: Record<string, UUID[]>;
  attachments: Attachment[];
  dueAt?: string;
  missing?: boolean;
}
export interface CustomerRequest {
  id: UUID;
  customerId: UUID;
  body: string;
  source: string;
  sourceUrl?: string;
  creator: User;
  issueId?: UUID;
  projectId?: UUID;
  attachments: Attachment[];
  priority?: number;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface ReleaseResource {
  id: UUID;
  type: "link" | "document";
  title: string;
  url?: string;
  documentId?: UUID;
  createdAt: string;
}
export interface Release {
  id: UUID;
  slugId: string;
  name: string;
  version: string;
  description: string;
  status: "planned" | "inProgress" | "released" | "canceled";
  pipelineId?: UUID;
  stage?: string;
  commitSha?: string;
  releaseNotes?: string;
  position: number;
  startDate?: string;
  targetDate?: string;
  projectIds: UUID[];
  issueIds: UUID[];
  subscriberIds: UUID[];
  resources: ReleaseResource[];
  creator: User;
  startedAt?: string;
  releasedAt?: string;
  stageFrozenAt?: string;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface AskApproval {
  id: UUID;
  askId: UUID;
  approver: User;
  decision: "approved" | "rejected";
  note?: string;
  decidedAt?: string;
}
export interface Ask {
  id: UUID;
  title: string;
  body: string;
  source: string;
  requester: User;
  teamId?: UUID;
  templateId?: UUID;
  status: "pending" | "approved" | "rejected";
  issueId?: UUID;
  approvals: AskApproval[];
  createdAt: string;
  updatedAt: string;
}
export interface Loop {
  id: UUID;
  name: string;
  icon?: string;
  color?: string;
  level: "workspace" | "team";
  triggerType: "schedule" | "issue" | "project" | "initiative" | "cycle";
  triggerConfig?: Record<string, unknown>;
  instructions: string;
  connectorIds: UUID[];
  teamAccess: "allPublic" | "selected";
  allowChangesOutsideTrigger: boolean;
  allowExternalSync: boolean;
  enabled: boolean;
  creator: User;
  lastRunAt?: string;
  nextRunAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface TemplateMilestone {
  id: UUID;
  name: string;
  description?: string;
}
export interface ProjectTemplate {
  id: UUID;
  name: string;
  projectName?: string;
  templateDescription?: string;
  description?: string;
  summary?: string;
  icon?: string;
  color?: string;
  statusId?: UUID;
  priority: number;
  teamIds: UUID[];
  labelIds: UUID[];
  leadId?: UUID;
  memberIds?: UUID[];
  dependencyIds?: UUID[];
  initiativeIds?: UUID[];
  issueIds?: UUID[];
  milestones?: TemplateMilestone[];
  visibility?: "workspace" | "teams";
  visibilityTeamId?: UUID;
  creator: User;
  createdAt: string;
  updatedAt: string;
}
export type TemplateFormFieldType =
  | "text"
  | "longText"
  | "dropdown"
  | "checkboxes"
  | "date"
  | "upload"
  | "instructions"
  | "title"
  | "labelGroup"
  | "priority"
  | "dueDate";
export interface TemplateFormField {
  id: UUID;
  label: string;
  description?: string;
  type: TemplateFormFieldType;
  required: boolean;
  options?: string[];
}
export interface TemplateSubIssue {
  id: UUID;
  title: string;
  description?: string;
  teamId?: UUID;
  priority: number;
  assigneeId?: UUID;
  labelIds: UUID[];
}
export interface UserSettings {
  userId: UUID;
  language: string;
  homeView: string;
  displayNames: string;
  firstDay: string;
  emoticons: boolean;
  sendComments: string;
  fontSize: string;
  pointerCursor: boolean;
  underlineLinks: boolean;
  disableAnimatedImages?: boolean;
  interfaceTheme: string;
  lightTheme: string;
  darkTheme: string;
  desktopLinks: boolean;
  autoAssign: boolean;
  assignStarted: boolean;
  reviewAutoAssign: boolean;
  branchFormat: string;
  personalSettingsVersion?: number;
  codeReviewsEnabled?: boolean;
  autoConvertDrafts?: boolean;
  mergeStrategy?: string;
  codeTheme?: string;
  codeFont?: string;
  reviewCommentsFilter?: string;
  reviewRequests?: boolean;
  githubTeamReviewRequests?: boolean;
  checksMergeQueue?: boolean;
  requireSignedCommits?: boolean;
  gitAttachmentFormat?: string;
  gitBranchMoveStarted?: boolean;
  codingToolMoveStarted?: boolean;
  changelogUpdates?: boolean;
  changelogNewsletter?: boolean;
  marketingUpdates?: boolean;
  inviteAcceptedUpdates?: boolean;
  privacyUpdates?: boolean;
  dpaUpdates?: boolean;
  agentEnabled: boolean;
  agentInstructions: string;
  pulseSchedule?: "daily" | "weekly" | "never";
  jobTitle?: string;
  username?: string;
  updatedAt: string;
}
export interface FeatureOption {
  id: UUID;
  name: string;
  color?: string;
}
export interface FeatureSettings {
  aiUsageFeedback: boolean;
  initiativeUpdateSchedule: string;
  customerDefaultTeamId?: UUID;
  customerRevenueFormat: string;
  customerRevenueCurrency: string;
  customerManualEdits: boolean;
  customerStatuses: FeatureOption[];
  customerTiers: FeatureOption[];
  customerExcludedDomains: string[];
  customerGenericDomains: string[];
  pulseWorkspaceSchedule: string;
  asksEmailAddresses: string[];
}
export interface WorkspaceSettings {
  fiscalMonth: string;
  welcomeMessage?: string;
  defaultHomeView?: string;
  guestsAllowed: boolean;
  requireTwoFactor: boolean;
  sessionDurationDays: number;
  allowedDomains: string[];
  invitePermission: string;
  teamCreatePermission: string;
  labelPermission: string;
  templatePermission: string;
  apiKeyPermission: string;
  featureFlags: Record<string, boolean>;
  featureSettings: FeatureSettings;
  billingEmail?: string;
  plan: string;
  updatedAt: string;
  inviteLinksEnabled?: boolean;
  googleAuthEnabled?: boolean;
  emailAuthEnabled?: boolean;
  disableAdminBypass?: boolean;
  initiativePermission?: string;
  loopPermission?: string;
  agentGuidancePermission?: string;
  preventGuestAgents?: boolean;
  aiUsageSharing?: boolean;
  agentWebSearch?: boolean;
  externalLoopTriggers?: boolean;
  mcpConnectorsEnabled?: boolean;
  reviewThirdPartyApplications?: boolean;
  allowedMcpConnectors?: "all" | "approved";
  restrictFileUploads?: boolean;
  reduceSupportPersonalInfo?: boolean;
  hipaaCompliance?: boolean;
  aiCreditBalanceCents?: number;
  aiCreditAutoReload?: boolean;
  aiCreditReloadThresholdCents?: number;
  aiCreditReloadAmountCents?: number;
  aiWorkspaceSpendLimitCents?: number;
  scimEnabled?: boolean;
  scimRoleClaim?: string;
  scimRoleMapping?: Record<string, string>;
  scimRoleGroups?: Record<string, string>;
  scimTeamGroupMapping?: Record<string, string>;
  scimDefaultRole?: string;
}
export interface ReleasePipeline {
  id: UUID;
  slugId: string;
  name: string;
  teamIds: UUID[];
  type: "scheduled" | "continuous";
  production: boolean;
  stages: string[];
  stageStatuses: Record<string, Release["status"]>;
  position: number;
  pathFilters: string[];
  releaseNotesTemplate?: string;
  autoGenerateReleaseNotes: boolean;
  moveOpenIssuesToNextRelease?: boolean;
  accessKeyPrefix?: string;
  accessKeyCreatedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface ReleasePipelineAccessKey {
  pipelineId: UUID;
  prefix: string;
  secret: string;
  createdAt: string;
}
export interface CustomEmoji {
  id: UUID;
  name: string;
  imageUrl: string;
  creator: User;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface APIKey {
  id: UUID;
  name: string;
  prefix: string;
  creatorId: UUID;
  // `null` is the persisted full-access representation; an explicit empty
  // array is a deny-all policy and must remain distinguishable in the UI.
  scopes: string[] | null;
  teamIds: UUID[];
  teamRestriction?: "all" | "selected";
  createdAt: string;
  lastUsedAt?: string;
  revokedAt?: string;
  expiresAt?: string;
  oauthClientId?: string;
  authorizationId?: string;
}
export interface Passkey {
  id: UUID;
  userId: UUID;
  name: string;
  createdAt: string;
  lastUsedAt?: string;
}
export interface CommitSigningKey {
  name: string;
  fingerprint: string;
  type: "ssh" | "pgp";
  addedAt: string;
}
export interface OAuthApplication {
  id: UUID;
  name: string;
  description?: string;
  clientId: string;
  clientSecret?: string;
  redirectUris: string[];
  scopes: string[];
  creatorId: UUID;
  createdAt: string;
  updatedAt: string;
}
export interface OAuthAuthorization {
  id: UUID;
  clientId: string;
  clientName: string;
  userId: UUID;
  scopes: string[];
  createdAt: string;
  lastUsedAt?: string;
  revokedAt?: string;
}
export interface IntegrationConnection {
  id: UUID;
  provider: string;
  name: string;
  status: string;
  config?: Record<string, string>;
  scopes: string[];
  channels: string[];
  linkbackEnabled: boolean;
  deliveryAttempts: number;
  connectedBy: UUID;
  createdAt: string;
  updatedAt: string;
  lastWebhookAt?: string;
  lastTestAt?: string;
  lastTestStatus?: "ready" | "error";
  lastDeliveryAt?: string;
  lastError?: string;
  oauthStartedAt?: string;
  oauthCompletedAt?: string;
}
export interface IdentityProvider {
  id: UUID;
  workspaceId: UUID;
  type: "oidc" | "saml";
  name: string;
  issuer: string;
  clientId?: string;
  clientSecretEnv?: string;
  scopes: string[];
  domains: string[];
  enabled: boolean;
  enforced: boolean;
  discoveryStatus: string;
  lastVerifiedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface AuthIdentity {
  id: UUID;
  userId: UUID;
  provider: string;
  issuer: string;
  subject: string;
  username?: string;
  createdAt: string;
  lastLoginAt: string;
}
export interface IntegrationDelivery {
  id: UUID;
  connectionId: UUID;
  eventType: string;
  resourceId: string;
  channel?: string;
  status: string;
  attempts: number;
  nextAttemptAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  previousValues?: unknown;
}
export interface GitAutomationState {
  id: UUID;
  teamId: UUID;
  repository: string;
  event: string;
  workflowStateId: UUID;
  syncComments: boolean;
  syncLabels: boolean;
  syncAssignees: boolean;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface TargetBranch {
  id: UUID;
  teamId: UUID;
  repository: string;
  branch: string;
  default: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface ReviewCheck {
  id: UUID;
  name: string;
  status: "pending" | "running" | "passed" | "failed" | "skipped";
  url?: string;
}
export interface ReviewFile {
  path: string;
  status: "added" | "modified" | "deleted" | "renamed";
  additions: number;
  deletions: number;
  patch: string;
}
export interface ReviewEvent {
  id: UUID;
  type: string;
  body?: string;
  actor: User;
  createdAt: string;
}
export interface CodeReview {
  id: UUID;
  slugId: string;
  provider: "github" | "gitlab";
  externalId: string;
  number: number;
  title: string;
  description: string;
  status: "open" | "inReview" | "approved" | "merged" | "closed";
  repositoryOwner: string;
  repositoryName: string;
  url: string;
  author: User;
  reviewerIds: UUID[];
  teamReviewers: string[];
  issueIds: UUID[];
  baseBranch: string;
  headBranch: string;
  branchState: "upToDate" | "behind" | "conflicted";
  additions: number;
  deletions: number;
  commitCount: number;
  checks: ReviewCheck[];
  files: ReviewFile[];
  events: ReviewEvent[];
  favorite: boolean;
  draft: boolean;
  quickToReview: boolean;
  createdAt: string;
  updatedAt: string;
  mergedAt?: string;
  closedAt?: string;
}
export interface Webhook {
  id: UUID;
  name: string;
  url: string;
  resourceTypes: string[];
  teamIds: UUID[];
  enabled: boolean;
  creatorId: UUID;
  createdAt: string;
  updatedAt: string;
}
export interface AccountSessionInfo {
  id: string;
  current: boolean;
  name?: string;
  operatingSystem?: string;
  browserType?: string;
  location?: string;
  ip?: string;
  countryCodes?: string[];
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
}
export interface WorkspaceUsageEvent {
  id: UUID;
  feature: "coding-sessions" | "loops";
  userId?: UUID;
  amountCents: number;
  createdAt: string;
}
export interface WorkspaceUsage {
  plan: string;
  members: number;
  issues: number;
  storageBytes: number;
  limits: { members: number; issues: number; storageBytes: number };
  aiCredits: {
    balanceCents: number;
    autoReloadEnabled: boolean;
    autoReloadThresholdCents: number;
    autoReloadAmountCents: number;
    workspaceSpendLimitCents: number;
  };
  events: WorkspaceUsageEvent[];
}
export interface SLARule {
  id: UUID;
  name: string;
  teamIds: UUID[];
  filters: Record<string, unknown>;
  targetMinutes: number;
  pauseStatuses: string[];
  businessHours: boolean;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface IssueSLA {
  id: UUID;
  issueId: UUID;
  ruleId: UUID;
  startedAt: string;
  dueAt: string;
  pausedAt?: string;
  pausedMinutes: number;
  breachedAt?: string;
  completedAt?: string;
  remainingMinutes: number;
  status: "active" | "paused" | "breached" | "completed" | "removed";
}
export interface SLAEvent {
  id: UUID;
  issueId: UUID;
  slaId: UUID;
  type: string;
  createdAt: string;
}
export interface Draft {
  id: UUID;
  userId: UUID;
  type: string;
  resourceId?: UUID;
  title: string;
  body: string;
  contentData?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}
export interface Favorite {
  id: UUID;
  userId: UUID;
  resourceType: string;
  resourceId: UUID;
  folderId?: UUID;
  position: number;
  createdAt: string;
}
export interface FavoriteFolder {
  id: UUID;
  userId: UUID;
  name: string;
  position: number;
  createdAt: string;
  updatedAt: string;
}
export interface Subscription {
  id: UUID;
  userId: UUID;
  resourceType: string;
  resourceId: UUID;
  events?: string[];
  createdAt: string;
}
export interface AuditLogEntry {
  id: UUID;
  actor: User;
  action: string;
  resourceType: string;
  resourceId: UUID;
  metadata?: Record<string, unknown>;
  createdAt: string;
}
export interface TrashEntry {
  id: UUID;
  resourceType: string;
  resourceId: UUID;
  title: string;
  teamIds?: UUID[];
  deletedBy: User;
  deletedAt: string;
  expiresAt: string;
}
export interface ImportJob {
  id: UUID;
  userId: UUID;
  filename: string;
  format: "csv" | "json";
  status: "mapping" | "running" | "completed" | "failed" | "cancelled";
  headers: string[];
  rows?: Record<string, string>[];
  mapping?: Record<string, string>;
  imported: number;
  errors: string[];
  createdAt: string;
  updatedAt: string;
  progress?: number;
  error?: string;
  retryCount?: number;
  teamId?: UUID;
}
export interface MigrationEntityMapping {
  id: string;
  entityType: string;
  sourceId: string;
  sourceName?: string;
  targetId?: string;
  targetName?: string;
  action: "map" | "create" | "invite" | "skip" | "metadata" | "review";
  status: string;
  error?: string;
  metadata?: Record<string, unknown>;
}
export interface MigrationJob {
  id: string;
  userId: string;
  filename: string;
  source: string;
  target: "flow" | "linear";
  targetTeamId?: string;
  status: "mapping" | "running" | "completed" | "failed" | "rolled_back";
  phase: string;
  progress: number;
  counts: Record<string, number>;
  mappings: MigrationEntityMapping[];
  errors: string[];
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
}
export interface ExportJob {
  id: UUID;
  userId: UUID;
  format: "csv" | "json";
  includePrivate: boolean;
  status: "queued" | "completed" | "failed";
  filename?: string;
  error?: string;
  createdAt: string;
  completedAt?: string;
}
export type InitiativeStatus =
  "proposed" | "planned" | "active" | "completed" | "canceled";
export interface Initiative {
  id: UUID;
  name: string;
  slugId: string;
  summary: string;
  description: string;
  icon?: string;
  color: string;
  status: InitiativeStatus;
  priority: number;
  position?: number;
  priorityLabel: string;
  health: Project["health"];
  owner?: User;
  creator: User;
  leadTeamId?: UUID;
  contributingTeamIds: UUID[];
  labelIds: UUID[];
  parentInitiativeIds?: UUID[];
  projectIds: UUID[];
  resources: InitiativeResource[];
  comments: Comment[];
  targetDate?: string;
  targetDateResolution?: "halfYear" | "month" | "quarter" | "year";
  favorite: boolean;
  subscribed: boolean;
  notificationRules: InitiativeNotificationRules;
  updateSchedule: InitiativeUpdateSchedule;
  descriptionHistory: InitiativeDescriptionRevision[];
  createdAt: string;
  updatedAt: string;
}
export interface InitiativeNotificationRules {
  descriptionChanges: boolean;
  newUpdate: boolean;
  allProjectUpdates: boolean;
}
export interface InitiativeUpdateSchedule {
  cadence: "none" | "weekly" | "biweekly" | "monthly" | "custom" | "never";
  weekday: number;
  timeRange: string;
}
export interface InitiativeDescriptionRevision {
  id: UUID;
  description: string;
  editedAt: string;
  editor: User;
}
export interface InitiativeResource {
  id: UUID;
  initiativeId: UUID;
  type: "link" | "document";
  title: string;
  url: string;
  documentId?: UUID;
  createdAt: string;
}
export interface InitiativeRelation {
  id: UUID;
  initiativeId: UUID;
  relatedInitiativeId: UUID;
  type: "related" | "parent" | "blocks" | "blocked_by";
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}
export interface CustomerStatus {
  id: UUID;
  name: string;
  color: string;
  position: number;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface CustomerTier {
  id: UUID;
  name: string;
  color: string;
  position: number;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface ReleaseNote {
  id: UUID;
  releaseId: UUID;
  title: string;
  body: string;
  bodyData?: Record<string, unknown>;
  creator: User;
  publishedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface ReleaseHistory {
  id: UUID;
  releaseId: UUID;
  actor: User;
  action: string;
  metadata: Record<string, unknown>;
  createdAt: string;
}
export interface TeamResourceSection {
  id: UUID;
  teamId: UUID;
  name: string;
  position: number;
  createdAt: string;
  updatedAt: string;
}
export interface TeamPinnedResource {
  id: UUID;
  teamId: UUID;
  sectionId?: UUID;
  resourceType: string;
  resourceId: string;
  url?: string;
  title: string;
  position: number;
  createdAt: string;
  updatedAt: string;
}
export interface AgentActivity {
  id: UUID;
  sessionId: UUID;
  issueId?: UUID;
  type: string;
  status: string;
  body?: string;
  metadata?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}
export interface AIConversation {
  id: UUID;
  userId: UUID;
  title: string;
  status: string;
  context?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}
export interface AIPromptProgress {
  id: UUID;
  conversationId: UUID;
  phase: string;
  progress: number;
  message?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}
export interface UsageAlert {
  id: UUID;
  type: string;
  threshold: number;
  current: number;
  status: string;
  metadata?: Record<string, unknown>;
  createdAt: string;
  resolvedAt?: string;
  archivedAt?: string;
}
export interface PaidSubscription {
  id: UUID;
  plan: string;
  status: string;
  seats: number;
  currentPeriodStart: string;
  currentPeriodEnd: string;
  cancelAtPeriodEnd: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface InitiativeUpdate {
  id: UUID;
  initiativeId: UUID;
  body: string;
  bodyData?: Record<string, unknown>;
  health: Project["health"];
  createdAt: string;
  editedAt?: string;
  user: User;
  comments: Comment[];
  reactions: Record<string, UUID[]>;
  attachments: Attachment[];
}
export interface InitiativeMutationInput {
  name?: string;
  summary?: string;
  description?: string;
  icon?: string;
  color?: string;
  status?: InitiativeStatus;
  priority?: number;
  position?: number;
  health?: Project["health"];
  ownerId?: string;
  leadTeamId?: string;
  contributingTeamIds?: string[];
  labelIds?: string[];
  projectIds?: string[];
  targetDate?: string;
  targetDateResolution?: "halfYear" | "month" | "quarter" | "year" | "";
  favorite?: boolean;
  subscribed?: boolean;
  notificationRules?: InitiativeNotificationRules;
  updateSchedule?: InitiativeUpdateSchedule;
}
export interface BootstrapData {
  workspace: Workspace;
  viewer: User;
  users: User[];
  teams: Team[];
  customers: Customer[];
  states: WorkflowState[];
  labels: IssueLabel[];
  issues: Issue[];
  cycles: Cycle[];
  cycleSettings: Record<UUID, CycleSettings>;
  teamSettings: Record<UUID, TeamSettings>;
  issueTemplates: IssueTemplate[];
  projectTemplates: ProjectTemplate[];
  documentTemplates: DocumentTemplate[];
  documents: FlowDocument[];
  documentContentDrafts: DocumentContentDraft[];
  customerRequests: CustomerRequest[];
  customerStatuses: CustomerStatus[];
  customerTiers: CustomerTier[];
  releases: Release[];
  releasePipelines: ReleasePipeline[];
  releaseNotes: ReleaseNote[];
  releaseHistory: ReleaseHistory[];
  customEmojis: CustomEmoji[];
  asks: Ask[];
  loops: Loop[];
  slaRules: SLARule[];
  issueSlas: IssueSLA[];
  slaEvents: SLAEvent[];
  drafts: Draft[];
  favorites: Favorite[];
  favoriteFolders: FavoriteFolder[];
  subscriptions: Subscription[];
  auditLog: AuditLogEntry[];
  trash: TrashEntry[];
  importJobs: ImportJob[];
  exportJobs: ExportJob[];
  migrationJobs: MigrationJob[];
  projects: Project[];
  projectRelations: ProjectRelation[];
  projectStatuses: ProjectStatus[];
  projectDisplayDefault?: Record<string, unknown>;
  projectUpdates: Record<UUID, ProjectUpdate[]>;
  initiatives: Initiative[];
  initiativeRelations: InitiativeRelation[];
  initiativeUpdates: Record<UUID, InitiativeUpdate[]>;
  comments: Record<UUID, Comment[]>;
  activities: Record<UUID, ActivityEvent[]>;
  savedViews: SavedView[];
  notifications: Notification[];
  notificationPreferences: Record<UUID, NotificationPreferences>;
  notificationDeliveries: NotificationDelivery[];
  pushSubscriptions: PushSubscription[];
  triageResponsibilities: TriageResponsibility[];
  triageRoutingRules: TriageRoutingRule[];
  triageAssignments: TriageAssignment[];
  workflowDefinitions: WorkflowDefinition[];
  workflowRuns: WorkflowRun[];
  emailIntakeAddresses: EmailIntakeAddress[];
  emailIntakeMessages: EmailIntakeMessage[];
  teamResourceSections: TeamResourceSection[];
  teamPinnedResources: TeamPinnedResource[];
  agentActivities: AgentActivity[];
  aiConversations: AIConversation[];
  aiPromptProgress: AIPromptProgress[];
  usageAlerts: UsageAlert[];
  paidSubscriptions: PaidSubscription[];
  settings?: Record<string, unknown>;
  labelGroups: LabelGroup[];
  userSettings: Record<UUID, UserSettings>;
  workspaceSettings: WorkspaceSettings;
  apiKeys: APIKey[];
  oauthApplications: OAuthApplication[];
  oauthAuthorizations: OAuthAuthorization[];
  webhooks: Webhook[];
  integrationConnections: IntegrationConnection[];
  identityProviders: IdentityProvider[];
  integrationDeliveries: IntegrationDelivery[];
  gitAutomationStates: GitAutomationState[];
  targetBranches: TargetBranch[];
  reviews: CodeReview[];
  agentSessions: AgentSession[];
  agentSkills: PersonalAgentSkill[];
  members: WorkspaceMember[];
  teamMembers: TeamMember[];
  invitations: Invitation[];
  viewerRole: WorkspaceRole;
}
export interface TeamAutomationRule {
  id: UUID;
  name: string;
  trigger: string;
  action: string;
  enabled: boolean;
}
export interface TeamAgentSkill {
  id: UUID;
  name: string;
  instructions: string;
  enabled: boolean;
}
export interface TeamSettings {
  teamId: UUID;
  description?: string;
  timezone: string;
  estimateType: "notUsed" | "exponential" | "fibonacci" | "flow";
  defaultStateId: UUID;
  defaultPriority: number;
  issueEmailEnabled: boolean;
  detailedHistory: boolean;
  access: "public" | "private";
  membershipRestriction: "open" | "members" | "owners";
  settingsPermission: "allMembers" | "teamMembers" | "owners";
  labelPermission: "allMembers" | "teamMembers" | "owners";
  templatePermission: "allMembers" | "teamMembers" | "owners";
  agentSkillPermission: "allMembers" | "teamMembers" | "owners";
  loopPermission: "allMembers" | "teamMembers" | "owners";
  memberPermission: "allMembers" | "teamMembers" | "owners";
  slackChannelId?: string;
  slackChannelName?: string;
  slackNotifications: Record<string, boolean>;
  prAutomations: Record<string, string>;
  autoCloseParents: boolean;
  autoCloseSubIssues: boolean;
  autoCloseStale: boolean;
  staleMonths: number;
  staleStatusId?: UUID;
  autoArchiveMonths: number;
  progressOrder: "first" | "last" | "noAction";
  releaseAutomations: TeamAutomationRule[];
  triageEnabled: boolean;
  triageRequirePriority: boolean;
  triageAction: string;
  triageRules: TeamAutomationRule[];
  agentSkills: TeamAgentSkill[];
  projectUpdatePrompt: string;
  resolvedThreadSummaries: boolean;
  showInitiatives: boolean;
  parentTeamId?: UUID;
}
export interface TeamSettingsMutationInput extends Partial<
  Omit<TeamSettings, "teamId">
> {
  identifier?: string;
}
export interface IssueTemplate {
  id: UUID;
  teamId?: UUID;
  scope?: "workspace" | "team";
  visibilityTeamId?: UUID;
  icon?: string;
  color?: string;
  templateType?: "standard" | "customForm";
  formFields?: TemplateFormField[];
  subIssues?: TemplateSubIssue[];
  name: string;
  title?: string;
  description?: string;
  body?: string;
  stateId?: UUID;
  priority: number;
  assigneeId?: UUID;
  projectId?: UUID;
  labelIds: UUID[];
  creator: User;
  createdAt: string;
  updatedAt: string;
}
export type IssueTemplateMutationInput = Partial<
  Pick<
    IssueTemplate,
    | "teamId"
    | "visibilityTeamId"
    | "icon"
    | "color"
    | "templateType"
    | "formFields"
    | "subIssues"
    | "name"
    | "title"
    | "description"
    | "body"
    | "stateId"
    | "priority"
    | "assigneeId"
    | "projectId"
    | "labelIds"
  >
>;
export type SavedViewResource =
  "issues" | "projects" | "initiativeProjects" | "pulse";

export type DashboardWidgetType =
  | "insight"
  | "issue_count"
  | "status_breakdown"
  | "assignee_workload"
  | "cycle_progress"
  | "project_progress"
  | "sla_health"
  | "throughput";
export interface DashboardWidget {
  id: string;
  type: DashboardWidgetType;
  title: string;
  description?: string;
  position: number;
  width: 1 | 2;
  config: DashboardInsightConfig & Record<string, unknown>;
}
export interface DashboardInsightConfig {
  display?: "chart" | "table" | "metric";
  measure?:
    "issue_count" | "estimate" | "cycle_time" | "lead_time" | "sla_breaches";
  aggregation?: "count" | "sum" | "average" | "minimum" | "maximum";
  slice?:
    | "none"
    | "status"
    | "team"
    | "assignee"
    | "label"
    | "project"
    | "cycle"
    | "priority"
    | "created_at"
    | "completed_at";
  segment?: "none" | "status" | "team" | "assignee" | "project" | "priority";
  dateAggregation?: "day" | "week" | "month" | "quarter" | "year";
  teamIds?: string[];
  stateIds?: string[];
  assigneeIds?: string[];
  labelIds?: string[];
  sinceDays?: number;
}
export interface Dashboard {
  id: string;
  name: string;
  description?: string;
  ownerId: string;
  visibility: "workspace" | "team" | "private";
  teamIds: string[];
  filters?: {
    teamIds?: string[];
    stateIds?: string[];
    assigneeIds?: string[];
    labelIds?: string[];
  };
  hideFilters?: boolean;
  widgets: DashboardWidget[];
  subscriberIds: string[];
  shareToken?: string;
  sharedAt?: string;
  createdAt: string;
  updatedAt: string;
}
export interface DashboardWidgetResult {
  widget: DashboardWidget;
  value: unknown;
}
export interface WorkspacePost {
  id: string;
  title?: string;
  body: string;
  creatorId: string;
  teamIds: string[];
  projectId?: string;
  initiativeId?: string;
  subscriberIds: string[];
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
}
export interface FeedItem {
  id: string;
  type: string;
  actorId: string;
  resourceType: string;
  resourceId: string;
  teamIds: string[];
  title: string;
  body?: string;
  metadata?: Record<string, string>;
  createdAt: string;
}
export interface Meeting {
  id: string;
  title: string;
  description?: string;
  organizerId: string;
  attendeeIds: string[];
  teamIds: string[];
  projectIds: string[];
  issueIds: string[];
  startsAt: string;
  durationMinutes: number;
  url?: string;
  notes?: string;
  transcript?: string;
  subscriberIds: string[];
  createdAt: string;
  updatedAt: string;
}
export interface CursorPage<T> {
  items: T[];
  nextCursor: string;
  hasMore: boolean;
  total: number;
}
export interface SemanticSearchFacet {
  key: string;
  value: string;
  label: string;
  count: number;
}
export interface SemanticSearchResult extends SearchResult {
  semanticScore: number;
  matchedTerms: string[];
}
export interface SemanticSearchResponse {
  results: SemanticSearchResult[];
  facets: Record<string, SemanticSearchFacet[]>;
  nextCursor: string;
  hasMore: boolean;
  total: number;
}
export interface FilterSuggestion {
  field: string;
  value: string;
  label: string;
  count: number;
}
export interface SavedView {
  id: UUID;
  slugId?: string;
  name: string;
  description: string;
  icon?: string;
  color?: string;
  resource?: SavedViewResource;
  scope: "personal" | "team" | "workspace";
  projectId?: UUID;
  teamId?: UUID;
  ownerId?: UUID;
  favorite?: boolean;
  subscribed?: boolean;
  shareToken?: string;
  sharedAt?: string;
  view: "active" | "backlog" | "all";
  filters: unknown[];
  display: Record<string, unknown>;
  insights?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}
export interface SavedViewMutationInput {
  name?: string;
  description?: string;
  icon?: string;
  color?: string;
  resource?: SavedViewResource;
  scope?: "personal" | "team" | "workspace";
  projectId?: string;
  teamId?: string;
  ownerId?: string;
  favorite?: boolean;
  subscribed?: boolean;
  view?: "active" | "backlog" | "all";
  filters?: unknown[];
  display?: Record<string, unknown>;
  insights?: Record<string, unknown>;
}
export type FilterField =
  "status" | "priority" | "assignee" | "label" | "project";
export interface FilterCondition {
  id: string;
  field: FilterField;
  operator: "is" | "isNot";
  value: string;
}

export interface IssueUpdateInput {
  expectedVersion?: number;
  title?: string;
  description?: string;
  descriptionState?: string;
  descriptionData?: Record<string, unknown>;
  contentState?: string;
  stateId?: string;
  priority?: number;
  estimate?: number;
  assigneeId?: string;
  documentUpdateIds?: string[];
  expectedDocumentVersion?: number;
  projectId?: string;
  projectMilestoneId?: string;
  cycleId?: string;
  dueDate?: string;
  labelIds?: string[];
  subscriberIds?: string[];
  archived?: boolean;
  recurrence?: "" | "daily" | "weekly" | "monthly";
  nextOccurrenceAt?: string;
  parentId?: string;
  sortOrder?: number;
}

export type SearchResourceType =
  | "issue"
  | "project"
  | "initiative"
  | "document"
  | "member"
  | "customer"
  | "release"
  | "view";
export interface SearchResult {
  id: UUID;
  type: SearchResourceType;
  title: string;
  subtitle?: string;
  identifier?: string;
  parentId?: UUID;
  parentType?: "project" | "initiative";
  email?: string;
  icon?: string;
  color?: string;
  score: number;
  updatedAt?: string;
}
export interface SearchHistoryEntry {
  query: string;
  useCount: number;
  lastUsedAt: string;
}
export interface AgentChatMessage {
  role: "user" | "assistant";
  content: string;
}
export interface AgentMessage extends AgentChatMessage {
  id: UUID;
  parts?: AgentMessagePart[];
  durationMs?: number;
  createdAt: string;
}
export interface AgentMessagePart {
  id: UUID;
  type:
    | "text"
    | "reasoning"
    | "toolCall"
    | "error"
    | "event"
    | "widget"
    | "elicitation"
    | "context";
  text?: string;
  status?: "pending" | "running" | "completed" | "error";
  toolCall?: AgentToolCall;
}
export interface AgentToolCall {
  id: UUID;
  name: string;
  arguments?: Record<string, unknown>;
  result?: unknown;
  status: "pending" | "running" | "completed" | "error";
  error?: string;
}
export interface AgentSession {
  id: UUID;
  slugId: string;
  userId: UUID;
  title: string;
  favorite: boolean;
  location: "page" | "toolbar";
  issueIds: UUID[];
  skillIds: UUID[];
  messages: AgentMessage[];
  createdAt: string;
  updatedAt: string;
}
export interface PersonalAgentSkill {
  id: UUID;
  userId: UUID;
  name: string;
  instructions: string;
  createdAt: string;
  updatedAt: string;
}
export interface AgentStatus {
  enabled: boolean;
  model: string;
  protocol?:
    "openai-responses" | "anthropic-messages" | "openai-chat-completions";
  toolsEnabled?: boolean;
  writeTools?: boolean;
}
export interface AgentChatResponse {
  message: string;
  model: string;
}
export interface RecentResource {
  resourceType: SearchResourceType;
  resourceId: UUID;
  lastViewedAt: string;
}
export interface SearchResponse {
  results: SearchResult[];
  history: SearchHistoryEntry[];
  recent: RecentResource[];
}
export interface Presence {
  clientId: string;
  user: User;
  issueId?: UUID;
  documentId?: UUID;
  route?: string;
  lastSeenAt: string;
}
export interface RealtimeEvent {
  id: string;
  type: string;
  aggregateId?: UUID;
  actorId?: UUID;
  clientId?: string;
  payload?: {
    presence?: Presence[];
    issue?: Issue;
    changes?: IssueUpdateInput;
  };
  createdAt: string;
}
