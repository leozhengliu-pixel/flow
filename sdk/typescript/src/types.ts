import type { Webhook } from './generated/models.js'

/** Comparators every field supports. */
export interface BaseComparator<T> {
  eq?: T
  neq?: T
  in?: T[]
  nin?: T[]
  /** `true` matches missing values, `false` present ones. */
  null?: boolean
}

export interface StringComparator extends BaseComparator<string> {
  eqIgnoreCase?: string
  neqIgnoreCase?: string
  startsWith?: string
  startsWithIgnoreCase?: string
  notStartsWith?: string
  endsWith?: string
  notEndsWith?: string
  contains?: string
  notContains?: string
  containsIgnoreCase?: string
  notContainsIgnoreCase?: string
}

export interface NumberComparator extends BaseComparator<number> {
  lt?: number
  lte?: number
  gt?: number
  gte?: number
}

/** Dates are RFC 3339 timestamps, `YYYY-MM-DD` or ISO 8601 durations relative to now (`-P2W`). */
export interface DateComparator extends BaseComparator<string> {
  lt?: string
  lte?: string
  gt?: string
  gte?: string
}

/**
 * The `filter` object of the connection endpoints. Conditions in one object
 * must all match; `and` / `or` take arrays. Each endpoint's filterable fields
 * are listed in the OpenAPI document; relations are nested objects.
 *
 * ```ts
 * { status: { type: { eq: 'started' } }, updatedAt: { gt: '-P2W' }, or: [{ lead: { email: { eq: 'ada@example.com' } } }] }
 * ```
 */
export interface FlowFilter {
  and?: FlowFilter[]
  or?: FlowFilter[]
  [field: string]: unknown
}

/** The view filter tree of the issue query API (`and`, `or`, `not`, or a `field` / `operator` / `values` condition). */
export interface IssueFilter {
  and?: IssueFilter[]
  or?: IssueFilter[]
  not?: IssueFilter
  field?: string
  operator?: string
  value?: unknown
  values?: unknown[]
}

export type IssueOrderBy = 'createdAt' | 'updatedAt' | 'priority' | 'title' | 'sortOrder'

export interface IssuesArgs {
  /** Page size, 1-100 (default 50). */
  first?: number
  /** Continue after this cursor (`pageInfo.endCursor`). Issues page forward only. */
  after?: string
  /** View filter tree, or its JSON text. */
  filter?: IssueFilter | string
  /** Default `createdAt`. */
  orderBy?: IssueOrderBy
  /** Default `desc` for `createdAt` and `updatedAt`, the server default otherwise. */
  orderDirection?: 'asc' | 'desc'
  /** Include archived issues. */
  includeArchived?: boolean
  /** Text search over title and description. */
  query?: string
  /** Team id, key or name; several comma separated or as an array. */
  teamId?: string | string[]
  projectId?: string | string[]
  /** Workflow state id, type or name. */
  stateId?: string | string[]
}

/**
 * Attribution for OAuth `actor=app` tokens: records show "<createAsUser> via
 * <application>". Rejected for user tokens.
 */
export interface AppAttribution {
  /** Name of the person in your system on whose behalf the application acts (at most 100 characters). */
  createAsUser?: string
  /** Avatar of that person; requires `createAsUser`. */
  displayIconUrl?: string
}

export interface WebhookCreateInput {
  /** Name shown in the webhook list; `label` is accepted as an alias by the server. */
  name?: string
  label?: string
  /** Public HTTPS endpoint. */
  url: string
  /** Payload type names (`Issue`, `Comment`) or Flow ids (`issues`, `comments`). */
  resourceTypes: string[]
  /** Restrict to one team, several teams, or (default) every team you can access. */
  teamId?: string
  teamIds?: string[]
  allPublicTeams?: boolean
  enabled?: boolean
}

export interface WebhookUpdateInput {
  name?: string
  url?: string
  resourceTypes?: string[]
  teamIds?: string[]
  enabled?: boolean
  /** `2` switches a legacy webhook to the current envelope. */
  payloadVersion?: number
}

/** The secret is returned only when it is created or rotated. */
export type WebhookWithSecret = Webhook & { secret: string }

export interface WebhookDeliveriesArgs {
  /** `Pending`, `Retrying`, `Delivered`, `Failed` or `Canceled` (case as the server reports it). */
  status?: string
  first?: number
  after?: string
}

export interface WebhookDelivery {
  id: string
  status?: string
  [field: string]: unknown
}

export type LinkAttachmentResult<T> = { attachment: T; created: boolean }
export type CustomerUpsertResult<T> = { customer: T; created: boolean }

/** Bytes accepted by `uploadFile`. Strings are sent as UTF-8. */
export type UploadBody = Blob | ArrayBuffer | ArrayBufferView | string

export interface UploadFileOptions {
  /** Defaults to the `name` of a `File`, else `upload`. */
  filename?: string
  /** Defaults to the `type` of a `Blob`, else `application/octet-stream`. */
  contentType?: string
}
