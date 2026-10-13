import type { IssueWebhookEvent } from './generated/models.js'

/**
 * Typed payloads of Flow webhook deliveries (see docs/webhooks.md). They are
 * generated from the `webhooks` section of the OpenAPI document, which is
 * built from the same Go types the server's deliveries are checked against.
 * Entity `data` is the REST model as it was after the change, with only `id`
 * guaranteed (payloads of older webhooks and nested objects carry a subset),
 * so the entities are the `*Partial` models.
 */
export type {
  AgentSessionWebhookEvent,
  AppUserNotificationWebhookEvent,
  AttachmentWebhookEvent,
  CommentWebhookEvent,
  CustomerNeedWebhookEvent,
  CustomerWebhookEvent,
  CycleWebhookEvent,
  DocumentWebhookEvent,
  InitiativeUpdateWebhookEvent,
  InitiativeWebhookEvent,
  IssueLabelWebhookEvent,
  IssueRelationWebhookEvent,
  IssueSLAWebhookEvent,
  IssueWebhookEvent,
  LegacyWebhookEvent,
  OAuthAppWebhookEvent,
  PermissionChangeWebhookEvent,
  ProjectMilestoneWebhookEvent,
  ProjectUpdateWebhookEvent,
  ProjectWebhookEvent,
  ReactionWebhookEvent,
  ReleaseNoteWebhookEvent,
  ReleaseWebhookEvent,
  UserWebhookEvent,
  WebhookActor,
  WebhookTestEvent,
} from './generated/models.js'
export { webhookEventTypes } from './generated/webhooks.js'
export type { WebhookEvent, WebhookEventOf, WebhookEventType } from './generated/webhooks.js'

/** The fields every delivery carries. */
export type WebhookEnvelope = Omit<IssueWebhookEvent, 'type' | 'action' | 'data' | 'updatedFrom'>

/** `create`, `update` or `remove`: the actions of data change events. */
export type DataChangeAction = IssueWebhookEvent['action']

/** A delivery whose type this SDK version does not know yet; Flow adds types without notice. */
export interface UnknownWebhookEvent extends WebhookEnvelope {
  type: string
  action: string
  [field: string]: unknown
}
