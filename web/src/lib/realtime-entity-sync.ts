import { ApiError } from '@/lib/api-client'
import { deriveResourceCounts } from '@/lib/resource-counts'
import { archiveProjectUpdateReminders, syncIssueProjectSummaries } from '@/lib/workspace-metadata-refresh'
import type { BootstrapData, Comment, DocumentContentDraft, Draft, FlowDocument, Initiative, InitiativeUpdate, Project, ProjectUpdate, RealtimeEvent, Team } from '@/types/flow'

/**
 * Realtime events about one document, project, initiative or team, applied to
 * the workspace snapshot without reloading the workspace metadata (tens of MiB
 * in a large workspace, fetched by every open client for every change).
 *
 * The server sends each viewer the entity as their own bootstrap shows it
 * (realtime_entity_payloads.go): a document they may open, a project or
 * initiative narrowed to the teams and links they may see. So a patch never
 * shows more than a reload would. What it cannot express locally falls back:
 *
 * - `scopeChanged` (a project or initiative moved between teams, or its links
 *   rewired) and relation events: one coalesced metadata reload (`reload`);
 * - document permission changes and every other event: the existing
 *   classification (realtime-dispatch), which reloads for access changes.
 *
 * The viewer's own drafts are patched too (the server sends them to their
 * author only); reviews, AI conversations, project templates and automations
 * reload through the coalescer instead of a full resync; dashboards, posts and
 * meetings are not in the snapshot and are ignored.
 *
 * Only an author's own document drafts and a document's comments without a
 * payload are fetched, from the per-resource endpoint; 403/404 drops the
 * resource. The per-resource endpoints build the viewer's whole metadata
 * projection per request, so nothing every client receives is answered with
 * one.
 */
export type EntityRealtimePlan =
  | { op: 'upsert-document'; document: FlowDocument; draftId?: string }
  | { op: 'remove-document'; id: string }
  | { op: 'document-comments'; documentId: string; comments?: Comment[] }
  | { op: 'upsert-draft'; draft: DocumentContentDraft }
  | { op: 'remove-draft'; id: string }
  | { op: 'fetch-drafts'; documentId: string }
  | { op: 'upsert-project'; project: Project; archiveReminders: boolean; updates?: ProjectUpdate[] }
  | { op: 'remove-project'; id: string }
  | { op: 'upsert-initiative'; initiative: Initiative; updates?: InitiativeUpdate[] }
  | { op: 'remove-initiative'; id: string }
  | { op: 'team-cosmetic'; team: Pick<Team, 'id'> & Partial<Pick<Team, 'name' | 'icon' | 'color'>> }
  | { op: 'upsert-user-draft'; draft: Draft }
  | { op: 'remove-user-draft'; id: string }
  | { op: 'clear-user-drafts' }
  | { op: 'reload' }
  | { op: 'ignore' }

type Payload = Record<string, unknown>

function record(value: unknown): Payload | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Payload : undefined
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value ? value : undefined
}

function entityOf(event: RealtimeEvent): Payload | undefined {
  const entity = record(event.payload?.entity)
  return entity && typeof entity.id === 'string' ? entity : undefined
}

const DOCUMENT_ACCESS_EVENT = /^document\.(?:permissions_updated|permission_updated|permission_deleted)$/
const RELATION_EVENT = /^(?:project|initiative)\.relation_/
// Records the workspace snapshot does not carry (their pages read their own endpoints).
const OFF_SNAPSHOT_EVENT = /^(?:dashboard|post|meeting)\./
// Reviews, the author's own AI conversations, project templates and automations: one coalesced
// metadata reload instead of the full resync they used to fall through to.
const COALESCED_RELOAD_EVENT = /^(?:review|ai|project_template|workflow_definition)\./
// Team edits that only rename or recolour it: every viewer who holds the team row may see the new values.
const COSMETIC_TEAM_KEYS = new Set(['name', 'icon', 'color', 'entity', 'deleted'])

function documentPlan(event: RealtimeEvent, payload: Payload): EntityRealtimePlan | undefined {
  const id = event.aggregateId
  if (DOCUMENT_ACCESS_EVENT.test(event.type)) return undefined
  if (event.type.startsWith('document.draft_')) {
    if (event.type === 'document.draft_deleted') return id ? { op: 'remove-draft', id } : undefined
    if (event.type === 'document.draft_created') return id ? { op: 'fetch-drafts', documentId: id } : undefined
    if (event.type === 'document.draft_updated') {
      const draft = entityOf(event)
      return draft && typeof draft.documentId === 'string' ? { op: 'upsert-draft', draft: draft as unknown as DocumentContentDraft } : undefined
    }
    // draft_published: the document changed for everyone; the author's draft is gone.
    const document = entityOf(event)
    if (document && typeof document.slugId === 'string') return { op: 'upsert-document', document: document as unknown as FlowDocument, draftId: text(payload.draftId) ?? id }
    return id ? { op: 'remove-draft', id } : undefined
  }
  if (event.type.startsWith('document.comment_')) {
    const documentId = text(payload.documentId) ?? id
    if (!documentId) return undefined
    return { op: 'document-comments', documentId, comments: Array.isArray(payload.comments) ? payload.comments as Comment[] : undefined }
  }
  if (payload.removed === true) {
    const removed = text(payload.documentId) ?? id
    return removed ? { op: 'remove-document', id: removed } : undefined
  }
  const document = entityOf(event)
  if (event.type !== 'document.deleted' && document && typeof document.slugId === 'string') return { op: 'upsert-document', document: document as unknown as FlowDocument }
  // document.deleted, or the bare signal of a document that is gone.
  return id ? { op: 'remove-document', id } : undefined
}

function projectPlan(event: RealtimeEvent, payload: Payload): EntityRealtimePlan | undefined {
  const id = event.aggregateId
  if (!id) return undefined
  if (event.type === 'project.deleted') return { op: 'remove-project', id }
  if (payload.scopeChanged === true || RELATION_EVENT.test(event.type)) return { op: 'reload' }
  const project = entityOf(event)
  if (!project || project.id !== id || typeof project.name !== 'string') return undefined
  const updates = Array.isArray(payload.updates) ? payload.updates as ProjectUpdate[] : undefined
  return { op: 'upsert-project', project: project as unknown as Project, archiveReminders: event.type === 'project.update_created', ...(updates ? { updates } : {}) }
}

function initiativePlan(event: RealtimeEvent, payload: Payload): EntityRealtimePlan | undefined {
  const id = event.aggregateId
  if (!id) return undefined
  if (event.type === 'initiative.deleted') return { op: 'remove-initiative', id }
  if (payload.scopeChanged === true || RELATION_EVENT.test(event.type)) return { op: 'reload' }
  const initiative = entityOf(event)
  if (!initiative || initiative.id !== id || typeof initiative.name !== 'string') return undefined
  const updates = Array.isArray(payload.updates) ? payload.updates as InitiativeUpdate[] : undefined
  return { op: 'upsert-initiative', initiative: initiative as unknown as Initiative, ...(updates ? { updates } : {}) }
}

function teamPlan(event: RealtimeEvent, payload: Payload): EntityRealtimePlan | undefined {
  if (event.type !== 'team.updated' || !event.aggregateId) return undefined
  // The team edit input is recorded with its Go field names (Name, Key, Color, Icon) and null for fields left alone.
  const keys = Object.entries(payload).filter(([, value]) => value !== null && value !== undefined && value !== '').map(([key]) => key.toLowerCase())
  if (!keys.some(key => key === 'name' || key === 'icon' || key === 'color') || keys.some(key => !COSMETIC_TEAM_KEYS.has(key))) return undefined
  const entity = entityOf(event)
  if (!entity || entity.id !== event.aggregateId) return undefined
  const team: Pick<Team, 'id'> & Partial<Pick<Team, 'name' | 'icon' | 'color'>> = { id: event.aggregateId }
  if (typeof entity.name === 'string') team.name = entity.name
  if (typeof entity.color === 'string') team.color = entity.color
  team.icon = typeof entity.icon === 'string' ? entity.icon : undefined
  return { op: 'team-cosmetic', team }
}

/** The viewer's own drafts (the server sends draft events to their author only). */
function userDraftPlan(event: RealtimeEvent): EntityRealtimePlan | undefined {
  if (event.type === 'drafts.deleted') return { op: 'clear-user-drafts' }
  if (event.type === 'draft.deleted') return event.aggregateId ? { op: 'remove-user-draft', id: event.aggregateId } : undefined
  const draft = entityOf(event)
  return draft && typeof draft.userId === 'string' ? { op: 'upsert-user-draft', draft: draft as unknown as Draft } : undefined
}

/** How to apply an entity event locally; undefined leaves it to the other realtime handlers. */
export function entityRealtimePlan(event: RealtimeEvent): EntityRealtimePlan | undefined {
  const payload = record(event.payload) ?? {}
  if (event.type.startsWith('draft.') || event.type.startsWith('drafts.')) return userDraftPlan(event)
  if (OFF_SNAPSHOT_EVENT.test(event.type)) return { op: 'ignore' }
  if (COALESCED_RELOAD_EVENT.test(event.type)) return { op: 'reload' }
  if (event.type.startsWith('document.')) return documentPlan(event, payload)
  if (event.type.startsWith('project.')) return projectPlan(event, payload)
  if (event.type.startsWith('initiative.')) return initiativePlan(event, payload)
  if (event.type.startsWith('team.')) return teamPlan(event, payload)
  return undefined
}

/**
 * `<kind>:<id>` for an event whose only effect is replacing the whole entity
 * (a plain upsert): a later one for the same entity supersedes it.
 */
export function supersedingEntityKey(event: RealtimeEvent): string | undefined {
  if (!event.aggregateId || !/^(?:document|project|initiative)\./.test(event.type)) return undefined
  const plan = entityRealtimePlan(event)
  if (plan?.op === 'upsert-document' && !plan.draftId) return `document:${plan.document.id}`
  if (plan?.op === 'upsert-project' && !plan.archiveReminders && !plan.updates) return `project:${plan.project.id}`
  if (plan?.op === 'upsert-initiative' && !plan.updates) return `initiative:${plan.initiative.id}`
  return undefined
}

/** An incoming copy older than the held one: a lower version, else an earlier updatedAt. */
function older(incoming: { version?: number; updatedAt?: string }, held: { version?: number; updatedAt?: string }): boolean {
  if (typeof incoming.version === 'number' && typeof held.version === 'number' && incoming.version !== held.version) return incoming.version < held.version
  const next = Date.parse(incoming.updatedAt ?? ''), previous = Date.parse(held.updatedAt ?? '')
  return Number.isFinite(next) && Number.isFinite(previous) && next < previous
}

const list = <T,>(value: T[] | null | undefined): T[] => Array.isArray(value) ? value : []

function normalizeDocument(document: FlowDocument): FlowDocument {
  return { ...document, projectIds: list(document.projectIds), teamIds: list(document.teamIds), subscriberIds: list(document.subscriberIds), revisions: list(document.revisions) }
}

function normalizeProject(project: Project): Project {
  return {
    ...project,
    memberIds: list(project.memberIds), labelIds: list(project.labelIds), teamIds: list(project.teamIds), dependencyIds: list(project.dependencyIds),
    initiatives: list(project.initiatives), customers: list(project.customers), resources: list(project.resources), milestones: list(project.milestones),
    comments: list(project.comments), descriptionRevisions: list(project.descriptionRevisions),
  }
}

function normalizeInitiative(initiative: Initiative): Initiative {
  return {
    ...initiative,
    contributingTeamIds: list(initiative.contributingTeamIds), labelIds: list(initiative.labelIds), parentInitiativeIds: list(initiative.parentInitiativeIds),
    projectIds: list(initiative.projectIds), resources: list(initiative.resources), comments: list(initiative.comments), descriptionHistory: list(initiative.descriptionHistory),
  }
}

function favorited(current: BootstrapData, resourceType: string, id: string): boolean {
  return (current.favorites ?? []).some(favorite => favorite.resourceType === resourceType && favorite.resourceId === id && !favorite.tab)
}

function removeDraft(current: BootstrapData, id: string): BootstrapData {
  const drafts = current.documentContentDrafts ?? []
  return drafts.some(draft => draft.id === id) ? { ...current, documentContentDrafts: drafts.filter(draft => draft.id !== id) } : current
}

export function upsertDocument(current: BootstrapData, incoming: FlowDocument, draftId?: string): BootstrapData {
  const base = draftId ? removeDraft(current, draftId) : current
  const documents = base.documents ?? []
  const index = documents.findIndex(document => document.id === incoming.id)
  const document = normalizeDocument(incoming)
  if (index < 0) return { ...base, documents: [{ ...document, favorite: favorited(base, 'document', document.id) }, ...documents] }
  const held = documents[index]
  if (older(document, held)) return base
  const next = documents.slice()
  next[index] = { ...document, favorite: held.favorite }
  return { ...base, documents: next }
}

export function removeDocument(current: BootstrapData, id: string): BootstrapData {
  const documents = current.documents ?? []
  const document = documents.find(item => item.id === id || item.slugId === id)
  if (!document) return current
  const comments = { ...current.comments }
  delete comments[document.id]
  return {
    ...current,
    documents: documents.filter(item => item.id !== document.id),
    documentContentDrafts: (current.documentContentDrafts ?? []).filter(draft => draft.documentId !== document.id),
    comments,
  }
}

/**
 * Replace a document's comments with the server's list. A comment the tab
 * holds that is newer than the event (written here after the server read the
 * list) stays, and so does a held comment with a higher version.
 */
export function mergeDocumentComments(current: BootstrapData, documentId: string, incoming: Comment[], eventCreatedAt?: string): BootstrapData {
  const document = (current.documents ?? []).find(item => item.id === documentId || item.slugId === documentId)
  if (!document) return current
  const merged = mergeNewest(current.comments?.[document.id], incoming, eventCreatedAt)
  return { ...current, comments: { ...current.comments, [document.id]: merged } }
}

/**
 * The server's list of a resource's children (comments, updates), keeping a
 * held child that is newer than the event (written in this tab after the
 * server read the list) and a held copy with a higher version.
 */
export function mergeNewest<T extends { id: string; createdAt: string; version?: number }>(held: T[] | undefined, incoming: T[], eventCreatedAt?: string): T[] {
  const mine = new Map((held ?? []).map(item => [item.id, item]))
  const listed = new Set(incoming.map(item => item.id))
  const since = Date.parse(eventCreatedAt ?? '')
  const merged = incoming.map(item => {
    const copy = mine.get(item.id)
    return copy && (copy.version ?? 0) > (item.version ?? 0) ? copy : item
  })
  for (const item of held ?? []) {
    if (!listed.has(item.id) && Number.isFinite(since) && Date.parse(item.createdAt) > since) merged.push(item)
  }
  return merged
}

export function upsertDraft(current: BootstrapData, draft: DocumentContentDraft): BootstrapData {
  if (draft.userId && draft.userId !== current.viewer.id) return current
  const drafts = current.documentContentDrafts ?? []
  const index = drafts.findIndex(item => item.id === draft.id)
  if (index < 0) return { ...current, documentContentDrafts: [...drafts, draft] }
  if (older(draft, drafts[index])) return current
  const next = drafts.slice()
  next[index] = draft
  return { ...current, documentContentDrafts: next }
}

export function replaceDocumentDrafts(current: BootstrapData, documentId: string, drafts: DocumentContentDraft[]): BootstrapData {
  const mine = drafts.filter(draft => !draft.userId || draft.userId === current.viewer.id)
  return { ...current, documentContentDrafts: [...(current.documentContentDrafts ?? []).filter(draft => draft.documentId !== documentId), ...mine] }
}

export function upsertProject(current: BootstrapData, incoming: Project, archiveReminders = false): BootstrapData {
  const projects = current.projects ?? []
  const index = projects.findIndex(project => project.id === incoming.id)
  const project = normalizeProject(incoming)
  let next: BootstrapData
  if (index < 0) {
    next = { ...current, projects: [{ ...project, issueCount: 0 }, ...projects] }
  } else {
    const held = projects[index]
    if (older(project, held)) return archiveReminders ? archiveProjectUpdateReminders(current, incoming.id) : current
    const updated = projects.slice()
    // The issue count is the viewer's own (from their issue queries): the server leaves it out.
    updated[index] = { ...project, issueCount: held.issueCount }
    next = { ...current, projects: updated }
  }
  next = deriveResourceCounts({ ...next, issues: syncIssueProjectSummaries(next.issues, project) })
  return archiveReminders ? archiveProjectUpdateReminders(next, incoming.id) : next
}

export function removeProject(current: BootstrapData, id: string): BootstrapData {
  if (!(current.projects ?? []).some(project => project.id === id)) return current
  return deriveResourceCounts({ ...current, projects: current.projects.filter(project => project.id !== id) })
}

export function upsertInitiative(current: BootstrapData, incoming: Initiative): BootstrapData {
  const initiatives = current.initiatives ?? []
  const index = initiatives.findIndex(initiative => initiative.id === incoming.id)
  const initiative = normalizeInitiative(incoming)
  if (index < 0) return { ...current, initiatives: [{ ...initiative, favorite: favorited(current, 'initiative', initiative.id) }, ...initiatives] }
  const held = initiatives[index]
  if (older(initiative, held)) return current
  const next = initiatives.slice()
  // Favorite and subscription are the viewer's own; the server leaves them out.
  next[index] = { ...initiative, favorite: held.favorite, subscribed: held.subscribed }
  return { ...current, initiatives: next }
}

export function removeInitiative(current: BootstrapData, id: string): BootstrapData {
  return (current.initiatives ?? []).some(initiative => initiative.id === id) ? { ...current, initiatives: current.initiatives.filter(initiative => initiative.id !== id) } : current
}

/** A team rename or recolour, applied to a team row the tab already holds (and to the copies its issues embed). */
export function applyTeamCosmetic(current: BootstrapData, team: Pick<Team, 'id'> & Partial<Pick<Team, 'name' | 'icon' | 'color'>>): BootstrapData {
  const held = current.teams.find(item => item.id === team.id)
  if (!held) return current
  const fields = { ...(team.name !== undefined ? { name: team.name } : {}), ...(team.color !== undefined ? { color: team.color } : {}), icon: team.icon }
  const teams = current.teams.map(item => item.id === team.id ? { ...item, ...fields } : item)
  const issues = current.issues.map(issue => issue.team?.id === team.id ? { ...issue, team: { ...issue.team, ...fields } } : issue)
  return { ...current, teams, issues }
}

/** Applies a plan that needs no request; fetch and reload plans return the snapshot unchanged. */
export function applyEntityPlan(current: BootstrapData, plan: EntityRealtimePlan, event?: Pick<RealtimeEvent, 'createdAt'>): BootstrapData {
  switch (plan.op) {
    case 'upsert-document': return upsertDocument(current, plan.document, plan.draftId)
    case 'remove-document': return removeDocument(current, plan.id)
    case 'document-comments': return plan.comments ? mergeDocumentComments(current, plan.documentId, plan.comments, event?.createdAt) : current
    case 'upsert-draft': return upsertDraft(current, plan.draft)
    case 'remove-draft': return removeDraft(current, plan.id)
    case 'upsert-project': {
      const next = upsertProject(current, plan.project, plan.archiveReminders)
      return plan.updates ? { ...next, projectUpdates: { ...next.projectUpdates, [plan.project.id]: mergeNewest(next.projectUpdates?.[plan.project.id], plan.updates, event?.createdAt) } } : next
    }
    case 'remove-project': return removeProject(current, plan.id)
    case 'upsert-initiative': {
      const next = upsertInitiative(current, plan.initiative)
      return plan.updates ? { ...next, initiativeUpdates: { ...next.initiativeUpdates, [plan.initiative.id]: mergeNewest(next.initiativeUpdates?.[plan.initiative.id], plan.updates, event?.createdAt) } } : next
    }
    case 'remove-initiative': return removeInitiative(current, plan.id)
    case 'team-cosmetic': return applyTeamCosmetic(current, plan.team)
    case 'upsert-user-draft': {
      if (plan.draft.userId !== current.viewer.id) return current
      const drafts = current.drafts ?? []
      const index = drafts.findIndex(draft => draft.id === plan.draft.id)
      if (index >= 0 && older(plan.draft, drafts[index])) return current
      return { ...current, drafts: index < 0 ? [plan.draft, ...drafts] : drafts.map((draft, at) => at === index ? plan.draft : draft) }
    }
    case 'remove-user-draft': return (current.drafts ?? []).some(draft => draft.id === plan.id) ? { ...current, drafts: current.drafts.filter(draft => draft.id !== plan.id) } : current
    case 'clear-user-drafts': return current.drafts?.length ? { ...current, drafts: [] } : current
    default: return current
  }
}

export function inaccessible(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 403 || error.status === 404)
}

export type EntityPlanContext = {
  /** Applies an update to the snapshot of the workspace and viewer the event came from (a no-op after a switch). */
  update: (apply: (current: BootstrapData) => BootstrapData) => void
  /** The snapshot as it is now (the event's workspace and viewer), for checks before a request. */
  snapshot: () => BootstrapData | null | undefined
  /** One coalesced metadata reload (realtime-bootstrap-coalescer). */
  reload: () => Promise<unknown>
  listDocumentDrafts: (documentId: string) => Promise<{ nodes?: DocumentContentDraft[] } | DocumentContentDraft[]>
  listDocumentComments: (documentId: string) => Promise<Comment[]>
  /** A document comment can mention an agent, which starts a task on the document. */
  onDocumentComment?: (documentId: string) => void
}

/**
 * Runs a plan: local plans apply at once; a fetch updates only the resource it
 * read and drops it on 403/404. Any other failure falls back to the coalesced
 * reload instead of a full resync.
 */
export async function runEntityPlan(plan: EntityRealtimePlan, event: RealtimeEvent, context: EntityPlanContext): Promise<void> {
  if (plan.op === 'ignore') return
  if (plan.op === 'reload') {
    await context.reload()
    return
  }
  if (plan.op === 'fetch-drafts') {
    try {
      const page = await context.listDocumentDrafts(plan.documentId)
      const drafts = Array.isArray(page) ? page : page.nodes ?? []
      context.update(current => replaceDocumentDrafts(current, plan.documentId, drafts))
    } catch (error) {
      if (!inaccessible(error)) throw error
      context.update(current => removeDocument(replaceDocumentDrafts(current, plan.documentId, []), plan.documentId))
    }
    return
  }
  if (plan.op === 'document-comments') {
    const held = context.snapshot()?.documents?.find(item => item.id === plan.documentId || item.slugId === plan.documentId)
    if (!held) return
    context.onDocumentComment?.(held.id)
    if (plan.comments) {
      context.update(current => mergeDocumentComments(current, held.id, plan.comments!, event.createdAt))
      return
    }
    try {
      const comments = await context.listDocumentComments(held.id)
      context.update(current => mergeDocumentComments(current, held.id, comments, event.createdAt))
    } catch (error) {
      if (!inaccessible(error)) throw error
      context.update(current => removeDocument(current, held.id))
    }
    return
  }
  context.update(current => applyEntityPlan(current, plan, event))
}
