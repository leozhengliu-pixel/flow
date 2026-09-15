import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createIssue,
  createProject,
  deleteIssue,
  deleteProject,
  fetchBootstrap,
  fetchInboxNotifications,
  listIssueRecords,
  listIssues,
  listProjectRecords,
  realtimeClientId,
  replaceTeamDefaultFavorites,
  searchWorkspace,
  semanticSearch,
  updateInboxNotification,
  updateIssue,
  updatePresence,
  updateProject,
} from './api'

function response(payload: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: vi.fn().mockResolvedValue(payload) } as unknown as Response
}

describe('API client contract', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    window.history.replaceState({}, '', '/workspace/issues/all')
  })

  it('strips server-owned default favorite fields even from full runtime objects', async () => {
    fetchMock.mockResolvedValue(response({ items: [] }))
    const legacy = { resourceType: 'project', resourceId: 'project-1', id: 'local:project-1', workspaceKey: 'wrong', teamId: 'wrong', position: 99, createdAt: '', updatedAt: '' }
    await replaceTeamDefaultFavorites('team/1', [legacy, { resourceType: 'view', resourceId: 'view-1' }])
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/teams/team%2F1/default-favorites')
    expect(JSON.parse(String(init.body))).toEqual({ items: [{ resourceType: 'project', resourceId: 'project-1' }, { resourceType: 'view', resourceId: 'view-1' }] })
    expect(legacy.createdAt).toBe('')
    fetchMock.mockResolvedValue(response({ error: 'resource is outside this workspace' }, 400))
    await expect(replaceTeamDefaultFavorites('team-1', [legacy])).rejects.toMatchObject({ message: 'resource is outside this workspace', status: 400 })
  })

  it('attaches workspace, realtime client, credentials, and explicit JSON requests', async () => {
    fetchMock.mockResolvedValue(response({ workspace: { id: 'workspace-1' } }))
    await fetchBootstrap()
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/bootstrap')
    expect(new Headers(init.headers).get('X-Workspace-Key')).toBe('workspace')
    expect(new Headers(init.headers).get('X-Client-ID')).toBe(realtimeClientId())
    expect(init.credentials).toBe('same-origin')

    fetchMock.mockResolvedValue(response({ id: 'issue-1' }))
    await createIssue({ title: 'Issue', description: '', teamId: 'team-1', labelIds: ['label-1'] })
    const issueRequest = fetchMock.mock.calls[1][1] as RequestInit
    expect(issueRequest.method).toBe('POST')
    expect(JSON.parse(String(issueRequest.body))).toMatchObject({ title: 'Issue', teamId: 'team-1', labelIds: ['label-1'] })
    expect(new Headers(issueRequest.headers).get('Content-Type')).toBe('application/json')
  })

  it('covers common issue, project, inbox, presence, and search endpoints', async () => {
    fetchMock.mockResolvedValue(response({ id: 'result' }))
    await updateIssue('issue/1', { priority: 4 })
    await deleteIssue('issue/1')
    await createProject({ name: 'Project', dependencyRelations: [
      { projectId: 'project/blocker', type: 'blocked_by' },
      { projectId: 'project/blocked', type: 'blocks' },
    ] })
    await updateProject('project/1', { summary: 'Summary' })
    await deleteProject('project/1')
    await fetchInboxNotifications('?archived=true')
    await updateInboxNotification('notification/1', { read: true })
    await updatePresence('client-1', 'issue-1', '/workspace/issue/TST-1')
    await searchWorkspace('customer request', ['issue'], 10)

    expect(fetchMock.mock.calls.map(call => call[0])).toEqual([
      '/api/issues/issue/1', '/api/issues/issue/1', '/api/projects', '/api/projects/project/1', '/api/projects/project/1',
      '/api/notifications?archived=true', '/api/notifications/notification/1', '/api/realtime/presence',
      '/api/search?q=customer+request&limit=10&types=issue',
    ])
    expect(JSON.parse(String((fetchMock.mock.calls[2][1] as RequestInit).body))).toMatchObject({
      dependencyRelations: [
        { projectId: 'project/blocker', type: 'blocked_by' },
        { projectId: 'project/blocked', type: 'blocks' },
      ],
    })
  })

  it('returns undefined for 204 and exposes structured API errors', async () => {
    fetchMock.mockResolvedValueOnce({ ok: true, status: 204, json: vi.fn() })
    await expect(deleteIssue('issue-1')).resolves.toBeUndefined()
    fetchMock.mockResolvedValueOnce(response({ error: 'Version conflict', code: 'conflict', current: { version: 4 } }, 409))
    await expect(updateIssue('issue-1', { title: 'Next', expectedVersion: 3 })).rejects.toMatchObject({
      name: 'ApiError', message: 'Version conflict', status: 409, code: 'conflict', current: { version: 4 },
    })
  })

  it('serializes release-scoped issue record queries', async () => {
    fetchMock.mockResolvedValue(response({ items: [], nextCursor: 'page-2', hasMore: true, total: -1 }))
    await listIssueRecords({ releaseId: 'release-1', archived: 'false', limit: 100, cursor: 'page-2' })
    const parsed = new URL(String(fetchMock.mock.calls[0][0]), 'http://flow.local')
    expect(parsed.pathname).toBe('/api/issue-records')
    expect(parsed.searchParams.get('releaseId')).toBe('release-1')
    expect(parsed.searchParams.get('archived')).toBe('false')
    expect(parsed.searchParams.get('limit')).toBe('100')
    expect(parsed.searchParams.get('cursor')).toBe('page-2')
    expect(parsed.searchParams.get('projection')).toBe('list')
  })

  it('requests the project-list bootstrap and paged project directory explicitly', async () => {
    fetchMock.mockResolvedValueOnce(response({ workspace: {}, apiKeys: [] }))
    await fetchBootstrap('workspace', 'project-list')
    const bootstrapHeaders = new Headers((fetchMock.mock.calls[0][1] as RequestInit).headers)
    expect(bootstrapHeaders.get('X-Workspace-Key')).toBe('workspace')
    expect(bootstrapHeaders.get('X-Flow-Projection')).toBe('project-list')

    fetchMock.mockResolvedValueOnce(response({ items: [], hasMore: false, total: 0 }))
    const controller = new AbortController()
    const filter = [{ field: 'status', operator: 'is', values: ['started'] }]
    await listProjectRecords({ teamId: 'team-1', archived: 'all', filter, limit: 100, cursor: 'next' }, controller.signal)
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit]
    const parsed = new URL(url, 'http://flow.local')
    expect(parsed.pathname).toBe('/api/projects')
    expect(parsed.searchParams.get('teamId')).toBe('team-1')
    expect(parsed.searchParams.get('limit')).toBe('100')
    expect(parsed.searchParams.get('cursor')).toBe('next')
    expect(JSON.parse(parsed.searchParams.get('filter')!)).toEqual(filter)
    expect(init.signal).toBe(controller.signal)
  })

  it('serializes cursor issue queries and structured filters', async () => {
    fetchMock.mockResolvedValue(response({ items: [], nextCursor: '', hasMore: false, total: 0 }))
    await listIssues({ teamId: ['team-1', 'team-2'], archived: 'all', limit: 25, cursor: 'next/page', sort: 'updatedAt', direction: 'desc', filter: { and: [{ field: 'priority', operator: 'is', values: ['1'] }] } })
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit]
    const parsed = new URL(url, 'http://flow.local')
    expect(parsed.pathname).toBe('/api/issues')
    expect(parsed.searchParams.get('teamId')).toBe('team-1,team-2')
    expect(parsed.searchParams.get('cursor')).toBe('next/page')
    expect(JSON.parse(parsed.searchParams.get('filter') ?? '{}')).toEqual({ and: [{ field: 'priority', operator: 'is', values: ['1'] }] })
  })

  it('does not infer a workspace header from authentication routes', async () => {
    window.history.replaceState({}, '', '/login')
    fetchMock.mockResolvedValue(response({ workspace: {} }))
    await fetchBootstrap()
    const headers = new Headers((fetchMock.mock.calls[0][1] as RequestInit).headers)
    expect(headers.has('X-Workspace-Key')).toBe(false)
  })

  it('forwards cancellation and identical server filter options to both search modes', async () => {
    fetchMock.mockResolvedValue(response({ results: [] }))
    const controller = new AbortController()
    const filter = { or: [{ field: 'assigneeId', operator: 'is', values: ['none'] }, { field: 'updatedAt', operator: 'after', values: ['2026-09-01'] }] }
    for (const search of [searchWorkspace, semanticSearch]) {
      await search('needle', ['issue'], { filter, includeArchived: true, sort: 'updatedAt', updatedBefore: '2026-09-10' }, controller.signal)
    }
    for (const [url, init] of fetchMock.mock.calls as [string, RequestInit][]) {
      const params = new URL(url, 'http://flow.local').searchParams
      expect(JSON.parse(params.get('filter')!)).toEqual(filter)
      expect(params.get('includeArchived')).toBe('true')
      expect(params.get('sort')).toBe('updatedAt')
      expect(params.get('updatedBefore')).toBe('2026-09-10')
      expect(init.signal).toBe(controller.signal)
    }
  })
})
