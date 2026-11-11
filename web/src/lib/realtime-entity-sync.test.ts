import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-client'
import { classifyRealtimeEvent } from '@/lib/realtime-dispatch'
import { RealtimeEventQueue } from '@/lib/realtime-event-queue'
import { makeBootstrap, makeIssue, project as baseProject, viewer } from '@/test/fixtures'
import type { BootstrapData, Comment, DocumentContentDraft, FlowDocument, Initiative, Project, ProjectUpdate, RealtimeEvent } from '@/types/flow'

import { applyEntityPlan, entityRealtimePlan, runEntityPlan, type EntityPlanContext, type EntityRealtimePlan } from './realtime-entity-sync'

const event = (type: string, aggregateId: string, payload: Record<string, unknown> = {}, extra: Partial<RealtimeEvent> = {}): RealtimeEvent =>
  ({ id: `evt-${type}`, type, aggregateId, payload: payload as RealtimeEvent['payload'], createdAt: '2026-11-11T10:00:00.000Z', ...extra })

const doc = (overrides: Partial<FlowDocument> = {}): FlowDocument => ({
  id: 'doc-1', slugId: 'doc-one', title: 'Spec', content: 'v1', creator: viewer, projectIds: [], teamIds: ['team-1'], subscriberIds: [],
  favorite: false, createdAt: '2026-11-01T00:00:00.000Z', updatedAt: '2026-11-01T00:00:00.000Z', revisions: [], version: 1, ...overrides,
})

const comment = (id: string, overrides: Partial<Comment> = {}): Comment =>
  ({ id, version: 1, body: id, reactions: {}, createdAt: '2026-11-11T09:00:00.000Z', user: viewer, ...overrides })

const initiative = (overrides: Partial<Initiative> = {}): Initiative => ({
  id: 'init-1', slugId: 'init-one', name: 'Grow', summary: '', description: '', color: '#000', status: 'active', priority: 0, priorityLabel: 'None',
  health: 'onTrack', creator: viewer, contributingTeamIds: [], labelIds: [], projectIds: [], resources: [], comments: [], favorite: true, subscribed: true,
  notificationRules: { descriptionChanges: false, newUpdate: false, allProjectUpdates: false }, updateSchedule: { cadence: 'never', weekday: 1, timeRange: '' } as Initiative['updateSchedule'],
  descriptionHistory: [], createdAt: '2026-11-01T00:00:00.000Z', updatedAt: '2026-11-01T00:00:00.000Z', ...overrides,
})

const project = (overrides: Partial<Project> = {}): Project => ({ ...baseProject, comments: [], updatedAt: '2026-11-01T00:00:00.000Z', ...overrides })

function snapshot(overrides: Partial<BootstrapData> = {}): BootstrapData {
  return makeBootstrap({ documents: [doc({ favorite: true })], documentContentDrafts: [], comments: {}, favorites: [], projects: [project({ issueCount: 7 })], initiatives: [initiative()], notifications: [], ...overrides })
}

function apply(current: BootstrapData, realtime: RealtimeEvent): BootstrapData {
  const plan = entityRealtimePlan(realtime)
  if (!plan) throw new Error(`no plan for ${realtime.type}`)
  return applyEntityPlan(current, plan, realtime)
}

describe('entity realtime classification', () => {
  it('applies document edits, comments and project updates locally: never a metadata reload or a resync', () => {
    const editEvents = [
      event('document.updated', 'doc-1', { entity: doc({ version: 2 }) }),
      event('document.created', 'doc-2', { entity: doc({ id: 'doc-2', slugId: 'doc-two' }) }),
      event('document.revision_restored', 'doc-1', { entity: doc({ version: 3 }) }),
      event('document.comment_created', 'doc-1', { documentId: 'doc-1', comments: [comment('c1')] }),
      event('project.commented', 'project-1', { entity: project() }),
      event('project.comment_updated', 'project-1', { entity: project() }),
      event('project.update_created', 'project-1', { entity: project() }),
      event('project.update_commented', 'project-1', { entity: project() }),
      event('project.milestone_created', 'project-1', { entity: project() }),
      event('initiative.commented', 'init-1', { entity: initiative() }),
      event('initiative.update_created', 'init-1', { entity: initiative() }),
    ]
    for (const realtime of editEvents) {
      for (const paged of [true, false]) expect(classifyRealtimeEvent(realtime, paged).kind, `${realtime.type} paged=${paged}`).toBe('entity')
    }
  })

  it('reloads (coalesced) for access changes: document permissions, membership, team privacy, scope moves, relations', () => {
    expect(classifyRealtimeEvent(event('document.permissions_updated', 'doc-1'), true)).toEqual({ kind: 'metadata', visibility: false })
    expect(classifyRealtimeEvent(event('team_member.added', 'team-1'), true)).toEqual({ kind: 'metadata', visibility: true })
    expect(classifyRealtimeEvent(event('team.updated', 'team-1', { private: true, entity: { id: 'team-1', name: 'Test team' } }), true)).toEqual({ kind: 'metadata', visibility: true })
    expect(entityRealtimePlan(event('project.updated', 'project-1', { scopeChanged: true, entity: project() }))).toEqual({ op: 'reload' })
    expect(entityRealtimePlan(event('project.updated', 'project-1', { scopeChanged: true }))).toEqual({ op: 'reload' })
    expect(entityRealtimePlan(event('initiative.updated', 'init-1', { scopeChanged: true }))).toEqual({ op: 'reload' })
    expect(entityRealtimePlan(event('project.relation_created', 'project-1', { entity: project() }))).toEqual({ op: 'reload' })
    expect(classifyRealtimeEvent(event('workspace.resync_required', ''), true)).toEqual({ kind: 'resync' })
  })

  it('renames a held team locally, but leaves key, access and settings changes to the reload', () => {
    expect(entityRealtimePlan(event('team.updated', 'team-1', { name: 'Platform', entity: { id: 'team-1', name: 'Platform', key: 'TST', color: '#111' } })))
      .toEqual({ op: 'team-cosmetic', team: { id: 'team-1', name: 'Platform', color: '#111', icon: undefined } })
    expect(entityRealtimePlan(event('team.updated', 'team-1', { key: 'NEW', name: 'x', entity: { id: 'team-1', name: 'x' } }))).toBeUndefined()
    expect(entityRealtimePlan(event('team.updated', 'team-1', { description: 'x', entity: { id: 'team-1', name: 'x' } }))).toBeUndefined()
    expect(entityRealtimePlan(event('team.updated', 'team-1', { name: 'x' }))).toBeUndefined()
  })

  it('patches the viewer\'s own drafts, ignores records the snapshot lacks, coalesces reviews instead of a resync', () => {
    const draft = { id: 'dr-1', userId: viewer.id, type: 'issue', title: 'Draft', body: 'b', createdAt: '', updatedAt: '2026-11-11T00:00:00.000Z' }
    const current = snapshot({ drafts: [] })
    const created = apply(current, event('draft.created', 'dr-1', { entity: draft }))
    expect(created.drafts).toEqual([draft])
    expect(apply(created, event('draft.updated', 'dr-1', { entity: { ...draft, userId: 'someone-else', body: 'x' } })).drafts[0].body).toBe('b')
    expect(apply(created, event('draft.deleted', 'dr-1')).drafts).toEqual([])
    expect(apply(created, event('drafts.deleted', 'all')).drafts).toEqual([])
    for (const type of ['dashboard.updated', 'post.created', 'meeting.deleted']) expect(classifyRealtimeEvent(event(type, 'x'), true), type).toEqual({ kind: 'entity', plan: { op: 'ignore' } })
    for (const type of ['review.synced', 'ai.conversation_updated', 'project_template.created', 'workflow_definition.updated']) {
      for (const paged of [true, false]) expect(classifyRealtimeEvent(event(type, 'x'), paged), type).toEqual({ kind: 'entity', plan: { op: 'reload' } })
    }
  })

  it('leaves events without a usable entity to the existing handlers', () => {
    expect(entityRealtimePlan(event('project.milestone_deleted', 'project-1'))).toBeUndefined()
    expect(entityRealtimePlan(event('project.updated', 'project-1', { entity: { id: 'other', name: 'x' } }))).toBeUndefined()
    expect(entityRealtimePlan(event('label.created', 'label-9'))).toBeUndefined()
  })
})

describe('entity patches', () => {
  it('upserts a document, keeping the viewer\'s favorite, and ignores an older copy (out of order)', () => {
    const current = snapshot()
    const edited = apply(current, event('document.updated', 'doc-1', { entity: doc({ content: 'v3', version: 3, favorite: false }) }))
    expect(edited.documents[0]).toMatchObject({ content: 'v3', version: 3, favorite: true })
    const stale = apply(edited, event('document.updated', 'doc-1', { entity: doc({ content: 'v2', version: 2 }) }))
    expect(stale).toBe(edited)
  })

  it('adds a document the tab never loaded, with its favorite from the viewer\'s favorites', () => {
    const current = snapshot({ favorites: [{ id: 'f', userId: viewer.id, resourceType: 'document', resourceId: 'doc-2', position: 0 }] as BootstrapData['favorites'] })
    const next = apply(current, event('document.created', 'doc-2', { entity: doc({ id: 'doc-2', slugId: 'doc-two', projectIds: null as unknown as string[] }) }))
    expect(next.documents.map(item => item.id)).toEqual(['doc-2', 'doc-1'])
    expect(next.documents[0]).toMatchObject({ favorite: true, projectIds: [] })
  })

  it('drops a deleted document, the bare signal of a gone one, and one moved out of reach', () => {
    const current = snapshot({ comments: { 'doc-1': [comment('c1')] } })
    for (const realtime of [event('document.deleted', 'doc-1', { deleted: true }), event('document.updated', 'doc-1'), event('document.updated', 'doc-1', { documentId: 'doc-1', removed: true })]) {
      const next = apply(current, realtime)
      expect(next.documents, realtime.type).toEqual([])
      expect(next.comments['doc-1']).toBeUndefined()
    }
    // An event about a document the tab does not hold changes nothing.
    const untouched = snapshot()
    expect(apply(untouched, event('document.deleted', 'doc-9'))).toBe(untouched)
  })

  it('publishes a draft: the document changes and the author\'s draft goes', () => {
    const draft: DocumentContentDraft = { id: 'draft-1', documentId: 'doc-1', userId: viewer.id, content: 'draft', version: 1, createdAt: '', updatedAt: '' }
    const current = snapshot({ documentContentDrafts: [draft] })
    const next = apply(current, event('document.draft_published', 'draft-1', { documentId: 'doc-1', draftId: 'draft-1', entity: doc({ content: 'draft', version: 2 }) }))
    expect(next.documents[0].content).toBe('draft')
    expect(next.documentContentDrafts).toEqual([])
    const updated = apply(current, event('document.draft_updated', 'draft-1', { entity: { ...draft, content: 'more', version: 2 } }))
    expect(updated.documentContentDrafts[0].content).toBe('more')
    expect(apply(current, event('document.draft_deleted', 'draft-1')).documentContentDrafts).toEqual([])
  })

  it('merges a document\'s comments from the payload, keeping newer local writes', () => {
    const local = comment('mine', { createdAt: '2026-11-11T10:00:05.000Z' })
    const edited = comment('c1', { version: 3, body: 'edited here' })
    const current = snapshot({ comments: { 'doc-1': [edited, local] } })
    const next = apply(current, event('document.comment_created', 'doc-1', { documentId: 'doc-1', comments: [comment('c1', { version: 2 }), comment('c2')] }))
    expect(next.comments['doc-1'].map(item => [item.id, item.body])).toEqual([['c1', 'edited here'], ['c2', 'c2'], ['mine', 'mine']])
  })

  it('upserts a project from its projection, deriving the issue count, embedded summaries and reminders', () => {
    const issue = makeIssue({ project: { id: 'project-1', name: 'Project one', icon: 'Project', color: '#5e6ad2' } as never })
    const reminder = { id: 'n1', projectId: 'project-1', type: 'projectUpdateReminder', recipientId: viewer.id } as unknown as BootstrapData['notifications'][number]
    const current = snapshot({ issues: [issue], notifications: [reminder] })
    const next = apply(current, event('project.update_created', 'project-1', { entity: project({ name: 'Renamed', health: 'atRisk', comments: [comment('pc1')], issueCount: 0, updatedAt: '2026-11-11T10:00:00.000Z' }) }))
    // The issue count is derived from the viewer's own issues (the server sends none).
    expect(next.projects[0]).toMatchObject({ name: 'Renamed', health: 'atRisk', issueCount: 1 })
    expect(next.projects[0].comments.map(item => item.id)).toEqual(['pc1'])
    expect(next.issues[0].project?.name).toBe('Renamed')
    expect(next.notifications[0].archivedAt).toBeTruthy()
    // An older projection (delivered late) does not roll the project back.
    const late = apply(next, event('project.commented', 'project-1', { entity: project({ name: 'Old', updatedAt: '2026-11-05T00:00:00.000Z' }) }))
    expect(late.projects[0].name).toBe('Renamed')
  })

  it('replaces a project\'s updates from an update event, keeping an update this tab posted after the event', () => {
    const mine = { id: 'u-mine', projectId: 'project-1', body: 'mine', createdAt: '2026-11-11T10:00:09.000Z' } as unknown as ProjectUpdate
    const current = snapshot({ projectUpdates: { 'project-1': [mine, { id: 'u-old', createdAt: '2026-11-10T00:00:00.000Z' } as unknown as ProjectUpdate] } })
    const next = apply(current, event('project.update_deleted', 'project-1', { entity: project(), updates: [{ id: 'u-new', projectId: 'project-1', body: 'new', createdAt: '2026-11-11T09:59:00.000Z' }] }))
    expect(next.projectUpdates['project-1'].map(update => update.id)).toEqual(['u-new', 'u-mine'])
  })

  it('removes a deleted project or initiative, and keeps the viewer\'s initiative flags', () => {
    expect(apply(snapshot(), event('project.deleted', 'project-1', { deleted: true })).projects).toEqual([])
    expect(apply(snapshot(), event('initiative.deleted', 'init-1', { deleted: true })).initiatives).toEqual([])
    const next = apply(snapshot(), event('initiative.commented', 'init-1', { entity: initiative({ comments: [comment('ic')], favorite: false, subscribed: false, updatedAt: '2026-11-11T00:00:00.000Z' }) }))
    expect(next.initiatives[0]).toMatchObject({ favorite: true, subscribed: true })
    expect(next.initiatives[0].comments).toHaveLength(1)
  })

  it('renames a held team and its issues\' copies, and ignores a team the viewer does not hold', () => {
    const current = snapshot()
    const plan = entityRealtimePlan(event('team.updated', 'team-1', { name: 'Platform', entity: { id: 'team-1', name: 'Platform', key: 'TST', color: '#5e6ad2' } }))!
    const next = applyEntityPlan(current, plan)
    expect(next.teams[0].name).toBe('Platform')
    expect(next.issues[0].team.name).toBe('Platform')
    const hidden = applyEntityPlan(current, { op: 'team-cosmetic', team: { id: 'private-team', name: 'Secret' } })
    expect(hidden).toBe(current)
  })
})

describe('entity plan runner', () => {
  function context(state: { data: BootstrapData | null }, overrides: Partial<EntityPlanContext> = {}): EntityPlanContext {
    return {
      update: apply => { if (state.data) state.data = apply(state.data) },
      snapshot: () => state.data,
      reload: vi.fn(async () => undefined),
      listDocumentDrafts: vi.fn(async () => ({ nodes: [] })),
      listDocumentComments: vi.fn(async () => []),
      ...overrides,
    }
  }

  it('reloads once through the coalescer for a scope change', async () => {
    const state = { data: snapshot() }
    const ctx = context(state)
    await runEntityPlan({ op: 'reload' }, event('project.updated', 'project-1', { scopeChanged: true }), ctx)
    expect(ctx.reload).toHaveBeenCalledTimes(1)
  })

  it('fetches only the author\'s drafts of one document; 403/404 drops the document', async () => {
    const draft: DocumentContentDraft = { id: 'draft-1', documentId: 'doc-1', userId: viewer.id, content: 'x', version: 1, createdAt: '', updatedAt: '' }
    const state = { data: snapshot() }
    await runEntityPlan({ op: 'fetch-drafts', documentId: 'doc-1' }, event('document.draft_created', 'doc-1'), context(state, { listDocumentDrafts: async () => ({ nodes: [draft] }) }))
    expect(state.data.documentContentDrafts).toEqual([draft])
    await runEntityPlan({ op: 'fetch-drafts', documentId: 'doc-1' }, event('document.draft_created', 'doc-1'), context(state, { listDocumentDrafts: async () => { throw new ApiError('gone', 404) } }))
    expect(state.data.documents).toEqual([])
    expect(state.data.documentContentDrafts).toEqual([])
  })

  it('refetches comments only for a held document without a payload, and drops it on 403', async () => {
    const state = { data: snapshot() }
    const onDocumentComment = vi.fn()
    const list = vi.fn(async () => [comment('c9')])
    await runEntityPlan({ op: 'document-comments', documentId: 'doc-1' }, event('document.comment_created', 'doc-1'), context(state, { listDocumentComments: list, onDocumentComment }))
    expect(state.data.comments['doc-1'].map(item => item.id)).toEqual(['c9'])
    expect(onDocumentComment).toHaveBeenCalledWith('doc-1')
    await runEntityPlan({ op: 'document-comments', documentId: 'doc-404' }, event('document.comment_created', 'doc-404'), context(state, { listDocumentComments: list }))
    expect(list).toHaveBeenCalledTimes(1)
    await runEntityPlan({ op: 'document-comments', documentId: 'doc-1' }, event('document.comment_created', 'doc-1'), context(state, { listDocumentComments: async () => { throw new ApiError('forbidden', 403) } }))
    expect(state.data.documents).toEqual([])
  })

  it('applies nothing after a workspace or user switch mid-flight', async () => {
    const state: { data: BootstrapData | null } = { data: snapshot() }
    let release!: (value: { nodes: DocumentContentDraft[] }) => void
    const pending = new Promise<{ nodes: DocumentContentDraft[] }>(resolve => { release = resolve })
    const switched = { workspace: 'workspace' }
    const ctx = context(state, {
      listDocumentDrafts: () => pending,
      // The App's guard: only the event's workspace and viewer are updated.
      update: apply => { if (state.data && switched.workspace === 'workspace') state.data = apply(state.data) },
    })
    const running = runEntityPlan({ op: 'fetch-drafts', documentId: 'doc-1' }, event('document.draft_created', 'doc-1'), ctx)
    switched.workspace = 'other'
    const before = state.data
    release({ nodes: [{ id: 'd', documentId: 'doc-1', userId: viewer.id, content: '', version: 1, createdAt: '', updatedAt: '' }] })
    await running
    expect(state.data).toBe(before)
  })

  it('rethrows other failures so the queue falls back', async () => {
    const state = { data: snapshot() }
    await expect(runEntityPlan({ op: 'fetch-drafts', documentId: 'doc-1' } as EntityRealtimePlan, event('document.draft_created', 'doc-1'), context(state, { listDocumentDrafts: async () => { throw new ApiError('busy', 503) } }))).rejects.toThrow('busy')
  })
})

describe('realtime queue', () => {
  it('keeps only the newest pending copy of an entity, never across a different event for it', () => {
    const queue = new RealtimeEventQueue()
    queue.push(event('document.updated', 'doc-1', { entity: doc({ version: 2 }) }, { id: 'a' }), 100)
    queue.push(event('project.commented', 'project-1', { entity: project() }, { id: 'b' }), 100)
    queue.push(event('document.updated', 'doc-1', { entity: doc({ version: 3 }) }, { id: 'c' }), 100)
    expect([queue.shift()?.id, queue.shift()?.id, queue.shift()]).toEqual(['b', 'c', undefined])
    queue.push(event('document.updated', 'doc-1', { entity: doc({ version: 2 }) }, { id: 'd' }), 100)
    queue.push(event('document.comment_created', 'doc-1', { documentId: 'doc-1', comments: [] }, { id: 'e' }), 100)
    queue.push(event('document.updated', 'doc-1', { entity: doc({ version: 3 }) }, { id: 'f' }), 100)
    expect([queue.shift()?.id, queue.shift()?.id, queue.shift()?.id]).toEqual(['d', 'e', 'f'])
  })

  it('does not overflow into a resync on an autosave burst of a large document', () => {
    const queue = new RealtimeEventQueue()
    for (let version = 1; version <= 300; version++) queue.push(event('document.updated', 'doc-1', { entity: doc({ version }) }, { id: `v${version}` }), 64_000)
    expect(queue.length).toBe(1)
    expect(queue.shift()?.id).toBe('v300')
  })
})
