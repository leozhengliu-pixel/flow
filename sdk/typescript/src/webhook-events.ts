import type {
  Attachment,
  Comment,
  Customer,
  CustomerRequest,
  Cycle,
  Document,
  Initiative,
  Issue,
  IssueLabel,
  IssueRelation,
  Project,
  ProjectMilestone,
  Release,
  User,
} from './generated/models.js'

/**
 * Typed payloads of Flow webhook deliveries (see docs/webhooks.md). Entity
 * `data` is the REST model as it was after the change, so every field except
 * `id` is optional: payloads of older webhooks and nested objects carry only a
 * subset.
 */

export type DeepPartial<T> = T extends (infer U)[] ? DeepPartial<U>[] : T extends object ? { [K in keyof T]?: DeepPartial<T[K]> } : T

/** The entity of a data change event: its `id` plus whatever else the payload carries. */
export type WebhookEntity<T> = { id: string } & DeepPartial<T> & { [field: string]: unknown }

/** Who made the change; `null` on the envelope for system changes. */
export interface WebhookActor {
  id: string
  type: 'user' | 'application'
  name?: string
  email?: string
  url?: string
}

export interface WebhookEnvelope {
  /** When the change happened (ISO 8601). */
  createdAt: string
  /** The workspace id. */
  organizationId: string
  /** The webhook this delivery belongs to. */
  webhookId: string
  /** Unix time in milliseconds when the delivery was sent. Signed: use it for the replay check. */
  webhookTimestamp: number
  /** The entity's page in Flow, when it has one. */
  url?: string
  /** Flow's id of the change; identical across webhooks receiving the same change. Use it to deduplicate. */
  eventId?: string
  /** Flow's detailed event, for example `issue.updated` or `comment.resolved`. */
  eventType?: string
  actor?: WebhookActor | null
}

export type DataChangeAction = 'create' | 'update' | 'remove'

export interface DataChangeEvent<TType extends string, TData> extends WebhookEnvelope {
  type: TType
  action: DataChangeAction
  /** The entity after the change; for `remove`, before it was removed. */
  data: TData
  /** For `update`, the previous values of the changed properties. */
  updatedFrom?: Record<string, unknown>
}

export type IssueWebhookEvent = DataChangeEvent<'Issue', WebhookEntity<Issue>>
export type AttachmentWebhookEvent = DataChangeEvent<'Attachment', WebhookEntity<Attachment>>
export type CommentWebhookEvent = DataChangeEvent<'Comment', WebhookEntity<Comment> & { issueId?: string }>
export type IssueLabelWebhookEvent = DataChangeEvent<'IssueLabel', WebhookEntity<IssueLabel>>
export type ReactionWebhookEvent = DataChangeEvent<
  'Reaction',
  { id: string; emoji: string; userId: string; issueId?: string; commentId?: string; projectUpdateId?: string; initiativeUpdateId?: string }
>
export type IssueRelationWebhookEvent = DataChangeEvent<'IssueRelation', WebhookEntity<IssueRelation>>
export type ProjectWebhookEvent = DataChangeEvent<'Project', WebhookEntity<Project>>
export type ProjectUpdateWebhookEvent = DataChangeEvent<'ProjectUpdate', { id: string; [field: string]: unknown }>
export type ProjectMilestoneWebhookEvent = DataChangeEvent<'ProjectMilestone', WebhookEntity<ProjectMilestone>>
export type DocumentWebhookEvent = DataChangeEvent<'Document', WebhookEntity<Document>>
export type InitiativeWebhookEvent = DataChangeEvent<'Initiative', WebhookEntity<Initiative>>
export type InitiativeUpdateWebhookEvent = DataChangeEvent<'InitiativeUpdate', { id: string; [field: string]: unknown }>
export type CycleWebhookEvent = DataChangeEvent<'Cycle', WebhookEntity<Cycle>>
export type CustomerWebhookEvent = DataChangeEvent<'Customer', WebhookEntity<Customer>>
/** A customer request (Flow's name for a customer need). */
export type CustomerNeedWebhookEvent = DataChangeEvent<'CustomerNeed', WebhookEntity<CustomerRequest>>
export type UserWebhookEvent = DataChangeEvent<'User', WebhookEntity<User>>
export type ReleaseWebhookEvent = DataChangeEvent<'Release', WebhookEntity<Release>>
export type ReleaseNoteWebhookEvent = DataChangeEvent<'ReleaseNote', { id: string; [field: string]: unknown }>

/** An SLA was set on an issue, is close to breaching, or breached. */
export interface IssueSLAWebhookEvent extends WebhookEnvelope {
  type: 'IssueSLA'
  action: 'set' | 'highRisk' | 'breached'
  issueData: { id: string; identifier?: string; title?: string; [field: string]: unknown }
  data: { id: string; issueId: string; ruleId?: string; dueAt?: string; status?: string; [field: string]: unknown }
}

/** Sent by "Send test event" and `POST /api/webhooks/{id}/test`. */
export interface WebhookTestEvent extends WebhookEnvelope {
  type: 'WebhookTest'
  action: 'test'
  data: { webhookId: string; message: string }
}

/** Application webhooks: the application was uninstalled or its last authorization revoked. */
export interface OAuthAppWebhookEvent extends WebhookEnvelope {
  type: 'OAuthApp'
  action: 'revoked'
  oauthClientId: string
}

/** Application webhooks: the teams the application can access changed. */
export interface PermissionChangeWebhookEvent extends WebhookEnvelope {
  type: 'PermissionChange'
  action: 'teamAccessChanged'
  oauthClientId: string
  appUserId: string
  addedTeamIds: string[]
  removedTeamIds: string[]
}

export type AppUserNotificationAction =
  | 'issueMention'
  | 'issueCommentMention'
  | 'issueEmojiReaction'
  | 'issueCommentReaction'
  | 'issueAssignedToYou'
  | 'issueUnassignedFromYou'
  | 'issueNewComment'
  | 'issueStatusChanged'

/** Application webhooks: the inbox of the application's user. */
export interface AppUserNotificationWebhookEvent extends WebhookEnvelope {
  type: 'AppUserNotification'
  action: AppUserNotificationAction
  oauthClientId: string
  appUserId: string
  notification: {
    id: string
    type: string
    issueId?: string
    commentId?: string
    actorId?: string
    issue?: { id: string; identifier?: string; title?: string; teamId?: string; url?: string }
    comment?: { id: string; body?: string; userId?: string }
    [field: string]: unknown
  }
}

/** Application webhooks: an agent was delegated an issue or mentioned (`created`), or a person replied (`prompted`). Answer within 5 seconds. */
export interface AgentSessionWebhookEvent extends WebhookEnvelope {
  type: 'AgentSessionEvent'
  action: 'created' | 'prompted'
  oauthClientId: string
  appUserId: string
  agentSession: {
    id: string
    status: string
    issueId?: string
    commentId?: string
    issue?: { id: string; identifier?: string; title?: string }
    comment?: { id: string; body?: string; userId?: string }
    creator?: { id: string; name?: string; email?: string }
    [field: string]: unknown
  }
  issue?: { id: string; identifier?: string }
  previousComments?: Array<{ id: string; body?: string; userId?: string; userName?: string; createdAt?: string }>
  guidance?: Array<{ body: string; origin: string }>
  /** The person's prompt, on `prompted` events only. */
  agentActivity?: { type: string; body?: string }
  /** A ready-to-use prompt: issue, triggering thread and guidance rules, XML-escaped. */
  promptContext?: string
}

export type WebhookEvent =
  | IssueWebhookEvent
  | AttachmentWebhookEvent
  | CommentWebhookEvent
  | IssueLabelWebhookEvent
  | ReactionWebhookEvent
  | IssueRelationWebhookEvent
  | ProjectWebhookEvent
  | ProjectUpdateWebhookEvent
  | ProjectMilestoneWebhookEvent
  | DocumentWebhookEvent
  | InitiativeWebhookEvent
  | InitiativeUpdateWebhookEvent
  | CycleWebhookEvent
  | CustomerWebhookEvent
  | CustomerNeedWebhookEvent
  | UserWebhookEvent
  | ReleaseWebhookEvent
  | ReleaseNoteWebhookEvent
  | IssueSLAWebhookEvent
  | WebhookTestEvent
  | OAuthAppWebhookEvent
  | PermissionChangeWebhookEvent
  | AppUserNotificationWebhookEvent
  | AgentSessionWebhookEvent

/** The payload `type` values, as sent in `Flow-Event`. */
export type WebhookEventType = WebhookEvent['type']

/** The event of one payload type, for example `WebhookEventOf<'Issue'>`. */
export type WebhookEventOf<T extends WebhookEventType> = Extract<WebhookEvent, { type: T }>

/** A delivery whose type this SDK version does not know yet; Flow adds types without notice. */
export interface UnknownWebhookEvent extends WebhookEnvelope {
  type: string
  action: string
  [field: string]: unknown
}
