import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createIssue,
  createProject,
  deleteIssue,
  deleteProject,
  fetchBootstrap,
  fetchInboxNotifications,
  realtimeClientId,
  searchWorkspace,
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

  it('does not infer a workspace header from authentication routes', async () => {
    window.history.replaceState({}, '', '/login')
    fetchMock.mockResolvedValue(response({ workspace: {} }))
    await fetchBootstrap()
    const headers = new Headers((fetchMock.mock.calls[0][1] as RequestInit).headers)
    expect(headers.has('X-Workspace-Key')).toBe(false)
  })
})
