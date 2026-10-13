import type { IssueFilter } from './generated/models.js'

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

export type IssueOrderBy = 'createdAt' | 'updatedAt' | 'priority' | 'title' | 'sortOrder'

export interface IssuesArgs {
  /** Page size forward, 1-100 (default 50). */
  first?: number
  /** Continue after this cursor (`pageInfo.endCursor`). */
  after?: string
  /** Page size backward, 1-100. Without `before` this is the final page. */
  last?: number
  /** Continue backward from this cursor (`pageInfo.startCursor`). */
  before?: string
  /** View filter tree (`IssueFilter`), or its JSON text. */
  filter?: IssueFilter | string
  /** Default `createdAt`. */
  orderBy?: IssueOrderBy
  /** Default `desc` for `createdAt` and `updatedAt`, `asc` otherwise. */
  orderDirection?: 'asc' | 'desc'
  /** Include archived issues. */
  includeArchived?: boolean
  /** Fill `totalCount` with the number of matching issues. */
  includeTotal?: boolean
  /** Text search over title and identifier. */
  query?: string
  /** Team id, key or name; several comma separated or as an array. */
  teamId?: string | string[]
  projectId?: string | string[]
  /** Workflow state id. */
  stateId?: string | string[]
}

/**
 * Attribution for OAuth `actor=app` tokens: records show "<createAsUser> via
 * <application>". Rejected for user tokens. The generated create inputs carry
 * these fields themselves; this type is for your own wrappers.
 */
export interface AppAttribution {
  /** Name of the person in your system on whose behalf the application acts (at most 100 characters). */
  createAsUser?: string
  /** Avatar of that person; requires `createAsUser`. */
  displayIconUrl?: string
}

export interface WebhookDeliveriesArgs {
  /** `failed` also lists deliveries that are retrying. */
  status?: 'pending' | 'delivering' | 'retrying' | 'succeeded' | 'failed' | 'canceled'
  first?: number
  after?: string
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
