import type { BootstrapData, Issue, SyncChanges } from '@/types/flow'

export const LOCAL_STORE_HEADER = 'X-Flow-Local-Store'
export const LOCAL_STORE_DISABLED = 'disabled'
export const LOCAL_STORE_CAPABILITY = 'local-store-shadow-v1'
export const LOCAL_STORE_ISSUE_CAPABILITY = 'local-store-issues-v1'
export const LOCAL_STORE_PULSE_CURSOR = 'pulseDeliveryCursors'

let serverDisabled = false

export function localStoreEnabled() {
  return !serverDisabled
}

/** Record which build wrote the store for diagnostics. */
export function localStoreBuildId() {
  const entry = typeof document === 'undefined' ? undefined : document.querySelector<HTMLScriptElement>('script[type="module"][src]')?.src
  const url = entry || import.meta.url
  try { return new URL(url).pathname } catch { return url }
}

export function markLocalStoreDisabled() {
  serverDisabled = true
}

export interface LocalStoreAuthority {
  viewerId: string
  workspaceId: string
  authzEpoch: string
  head: string
}

export function decimal(value: unknown): value is string {
  return typeof value === 'string' && /^(0|[1-9][0-9]*)$/.test(value)
}

export function compareLocalStoreSeq(a: string, b: string) {
  return a.length - b.length || (a === b ? 0 : a < b ? -1 : 1)
}

export function localStoreAuthority(value: unknown): LocalStoreAuthority | undefined {
  if (!value || typeof value !== 'object') return undefined
  const stamp = value as Partial<LocalStoreAuthority>
  if (typeof stamp.viewerId !== 'string' || !stamp.viewerId || typeof stamp.workspaceId !== 'string' || !stamp.workspaceId ||
      typeof stamp.authzEpoch !== 'string' || !stamp.authzEpoch || !decimal(stamp.head)) return undefined
  return stamp as LocalStoreAuthority
}

export function readLocalStoreStamp(headers: Headers) {
  try { return localStoreAuthority(JSON.parse(headers.get(LOCAL_STORE_HEADER) ?? 'null')) }
  catch { return undefined }
}

// Keep this projection common to the bootstrap and journal adapters. Fields
// that need another loader, including bodies and grants, never reach disk.
const collections = {
  issue: 'issues', team: 'teams', user: 'users', workflowState: 'states', label: 'labels', labelGroup: 'labelGroups',
  project: 'projects', initiative: 'initiatives', cycle: 'cycles', document: 'documents', customer: 'customers',
  savedView: 'savedViews', favorite: 'favorites', notification: 'notifications',
} as const satisfies Record<string, keyof BootstrapData>

export type LocalKind = keyof typeof collections
export const localKinds = Object.keys(collections) as LocalKind[]
export const lazyLocalKinds = ['comment', 'activity', 'attachment'] as const

const fields = {
  issue: 'identifier number title priority priorityLabel sortOrder estimate dueDate createdAt updatedAt archivedAt parentId version contentVersion',
  team: 'key name icon color private parentTeamId',
  user: 'name displayName avatarUrl active status',
  workflowState: 'teamId name color type position',
  label: 'teamId name color groupId parentId',
  labelGroup: 'teamId name color',
  project: 'name slugId icon color teamIds status startDate targetDate updatedAt archivedAt',
  initiative: 'name slugId icon color status leadTeamId startDate targetDate updatedAt archivedAt',
  cycle: 'teamId name number startsAt endsAt startDate endDate',
  document: 'title slugId icon color teamIds projectIds updatedAt contentVersion hasContent',
  customer: 'name logoUrl statusId tierId',
  savedView: 'name description icon color teamId ownerId type',
  favorite: 'type resourceId folderId sortOrder position',
  notification: 'type issueId createdAt readAt archivedAt',
} satisfies Record<LocalKind, string>

const fieldLists = Object.fromEntries(localKinds.map(kind => [kind, fields[kind].split(' ')])) as Record<LocalKind, string[]>
const wireKinds = new Map<string, LocalKind>(localKinds.flatMap(kind => [[kind, kind], [collections[kind], kind]]))
wireKinds.set('state', 'workflowState')

export interface LocalRow extends Record<string, unknown> {
  id: string
  // A legacy bootstrap has no canonical record version. A journal upsert has one.
  _v: string | null
  _seq: string
}

function project(kind: LocalKind, id: string, value: Record<string, unknown>, seq: string, version: string | null): LocalRow {
  const row: LocalRow = { id, _v: version, _seq: seq }
  for (const field of fieldLists[kind]) if (Object.hasOwn(value, field)) row[field] = value[field]
  if (kind === 'issue') {
    for (const [field, index] of [['team', 'teamId'], ['state', 'stateId'], ['assignee', 'assigneeId'], ['project', 'projectId']] as const) {
      const ref = value[field] as { id?: unknown } | null | undefined
      if (typeof ref?.id === 'string') row[index] = ref.id
    }
  }
  return row
}

export function projectLocalCollection(data: BootstrapData, kind: LocalKind, head: string) {
  const collection = data[collections[kind]]
  const rows = new Map<string, LocalRow>()
  if (!Array.isArray(collection)) return rows
  for (const item of collection) {
    const value = item as unknown as Record<string, unknown>
    if (typeof value.id !== 'string' || !value.id || value.deletedAt || kind === 'issue' && value.archivedAt) continue
    rows.set(value.id, project(kind, value.id, value, head, null))
  }
  return rows
}

const issueListFields = (
  'previousIdentifiers icon slaBreachesAt slaType completedAt startedAt triagedAt suggestionsGeneratedAt statusChangedAt ' +
  'autoClosedAt canceledAt team state assignee delegate creator botActor labels project projectMilestoneId cycleId addedToCycle ' +
  'agentSessionId agentSessionState suggestedLabelIds suggestedAssigneeIds suggestedProjectIds suggestedTeamIds suggestedDuplicateIds ' +
  'suggestedRelatedIds externalSource autoClosed templateId recurrence nextOccurrenceAt subIssueCount recurrenceSeriesId ' +
  'recurrenceOccurrence snoozedUntil subscriberIds reactionCounts subIssueIds relations hasContent'
).split(' ')

export function projectLocalIssueRecord(id: string, value: Record<string, unknown>, seq: string, version: string): LocalRow {
  if (!id || value.id !== id || value.isSummary !== true || !decimal(version) || typeof value.title !== 'string' || !value.team || !value.state) {
    throw new LocalStoreReset('Invalid issue list record')
  }
  const row = project('issue', id, value, seq, version)
  for (const field of issueListFields) if (Object.hasOwn(value, field)) row[field] = value[field]
  return row
}

const emptySummaryList = Object.freeze([]) as unknown as never[]
const emptySummaryReactions = Object.freeze({})
const issueIndexFields = new Set(['_v', '_seq', 'teamId', 'stateId', 'assigneeId', 'projectId'])

export function issueFromLocalRow(row: LocalRow) {
  // Allocate common list fields together. Adding them after construction can
  // grow the property storage of every issue in a large collection.
  const summary: Record<string, unknown> = {
    isSummary: true, id: row.id, identifier: row.identifier, number: row.number, title: row.title,
    version: row.version, team: row.team, state: row.state, creator: row.creator,
    priority: row.priority, priorityLabel: row.priorityLabel, sortOrder: row.sortOrder,
    createdAt: row.createdAt, updatedAt: row.updatedAt, statusChangedAt: row.statusChangedAt,
    hasContent: row.hasContent ?? false,
    description: '', attachments: emptySummaryList, reactions: emptySummaryReactions,
    relations: row.relations ?? emptySummaryList, labels: row.labels ?? emptySummaryList,
    subscriberIds: row.subscriberIds ?? emptySummaryList, subIssueIds: row.subIssueIds ?? emptySummaryList,
  }
  for (const field of Object.keys(row)) {
    if (!Object.hasOwn(summary, field) && !issueIndexFields.has(field)) summary[field] = row[field]
  }
  return summary as unknown as Issue
}

export function createLocalIssueView() {
  // Structured cloning duplicates the directory references in every issue.
  // Share equal references from this authorized read, including any redactions.
  const references = new Map<string, object>()
  const stringValues = new Map<string, string>()
  const intern = <T,>(value: T): T => {
    if (!value || typeof value !== 'object') return value
    if (Array.isArray(value) && !value.length) return emptySummaryList as T
    const key = JSON.stringify(value)
    const known = references.get(key)
    if (known) return known as T
    references.set(key, value)
    return value
  }
  const internString = (value: string) => {
    // A Map keeps the original string value when structured cloning supplied
    // separate copies, including timestamps shared by batch updates.
    const existing = stringValues.get(value)
    if (existing !== undefined) return existing
    stringValues.set(value, value)
    return value
  }
  return (row: LocalRow) => {
    const issue = issueFromLocalRow(row)
    issue.createdAt = internString(issue.createdAt)
    issue.updatedAt = internString(issue.updatedAt)
    if (issue.priorityLabel) issue.priorityLabel = internString(issue.priorityLabel)
    issue.team = intern(issue.team)
    issue.state = intern(issue.state)
    issue.creator = intern(issue.creator)
    if (issue.assignee) issue.assignee = intern(issue.assignee)
    if (issue.delegate) issue.delegate = intern(issue.delegate)
    if (issue.project) issue.project = intern(issue.project)
    issue.labels = intern(issue.labels)
    if (!issue.relations.length) issue.relations = emptySummaryList
    if (!issue.subscriberIds.length) issue.subscriberIds = emptySummaryList
    if (!issue.subIssueIds.length) issue.subIssueIds = emptySummaryList
    return issue
  }
}

export type LocalChange = { kind: LocalKind; id: string; row?: LocalRow }

export function applyLocalIssueView(issues: Issue[], changes: readonly LocalChange[]) {
  const pending = new Map(changes.map(change => [change.id, change.row ? issueFromLocalRow(change.row) : undefined]))
  const next: Issue[] = []
  for (const issue of issues) {
    if (!pending.has(issue.id)) { next.push(issue); continue }
    const updated = pending.get(issue.id)
    if (updated) next.push(updated)
    pending.delete(issue.id)
  }
  for (const issue of pending.values()) if (issue) next.push(issue)
  return next
}

export class LocalStoreReset extends Error {}
export class LocalStoreRefreshRequired extends LocalStoreReset {}

/** HTTP next is the scanned cursor. Invisible transactions need no record writes. */
export function planLocalChanges(page: SyncChanges, after: string, epoch: string, issueLists = false): LocalChange[] {
  if (page.authzEpoch !== epoch || page.resyncRequired) throw new LocalStoreRefreshRequired('Journal baseline changed')
  if (!decimal(page.next) || !decimal(page.head) ||
      compareLocalStoreSeq(page.next, after) < 0 || compareLocalStoreSeq(page.next, page.head) > 0 ||
      (page.hasMore ? page.next === after : page.next !== page.head)) throw new LocalStoreReset('Invalid journal cursor')
  const writes: LocalChange[] = []
  let seq = after
  for (const transaction of page.transactions) {
    if (!decimal(transaction.seq) || compareLocalStoreSeq(transaction.seq, seq) <= 0 || compareLocalStoreSeq(transaction.seq, page.next) > 0 ||
        !transaction.txId) throw new LocalStoreReset('Invalid journal transaction')
    if (transaction.invalidations.some(kind => !issueLists || kind !== LOCAL_STORE_PULSE_CURSOR)) {
      throw new LocalStoreRefreshRequired('Journal invalidated a collection')
    }
    seq = transaction.seq
    for (const { key, version, value, completeness } of transaction.upserts) {
      const kind = wireKinds.get(key.kind)
      if (!kind || !decimal(version) || !key.id || value?.id !== key.id || !['summary', 'detail'].includes(completeness)) throw new LocalStoreReset('Journal record is unsupported')
      const row = kind === 'issue' && value.archivedAt ? undefined :
        issueLists && kind === 'issue' ? projectLocalIssueRecord(key.id, value, seq, version) : project(kind, key.id, value, seq, version)
      writes.push({ kind, id: key.id, row })
    }
    for (const { key, version } of transaction.removals) {
      const kind = wireKinds.get(key.kind)
      if (!kind || !decimal(version) || !key.id) throw new LocalStoreReset('Journal removal is unsupported')
      writes.push({ kind, id: key.id })
    }
  }
  return writes
}

export function localRowBytes(row: LocalRow) {
  return JSON.stringify(row).length * 2
}

// An order-independent checksum lets the issue store replace one row's
// contribution without retaining or rewriting the rest of the collection.
export function localRowFingerprint(row: LocalRow) {
  const text = JSON.stringify(row)
  let hash = 0x811c9dc5
  for (let i = 0; i < text.length; i++) hash = Math.imul(hash ^ text.charCodeAt(i), 0x01000193)
  return { hash, bytes: text.length * 2 }
}

export async function localChecksum(rows: Iterable<LocalRow>) {
  const ordered = [...rows].sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
  const bytes = new TextEncoder().encode(JSON.stringify(ordered))
  return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)).join(',')
}
