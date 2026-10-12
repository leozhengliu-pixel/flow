import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, request } from './api-client'
import { replyApplicationTask, type ApplicationTask } from './application-agents'

vi.mock('./api-client', async original => ({ ...await original<typeof import('./api-client')>(), request: vi.fn() }))
const task: ApplicationTask = { id: 'task', issueId: 'issue', appUserId: 'app', creatorId: 'person', teamId: 'team', prompt: '', trigger: 'mention', status: 'active', version: 5, updatedAt: '' }
beforeEach(() => vi.resetAllMocks())

describe('replying during application streaming', () => {
  it('retries a version conflict caused only by new stream fragments', async () => {
    vi.mocked(request).mockRejectedValueOnce(new ApiError('Changed', 409)).mockResolvedValueOnce({ session: { ...task, version: 7 }, activities: [{ type: 'thought' }, { type: 'output' }] }).mockResolvedValueOnce({ ...task, version: 8, status: 'pending' })
    await expect(replyApplicationTask('workspace', task, 'prompt', 'Follow up')).resolves.toMatchObject({ version: 8 })
    expect(request).toHaveBeenNthCalledWith(2, '/api/agent-tasks/task?after=00000000000000000005', expect.anything())
    expect(JSON.parse(vi.mocked(request).mock.calls[2][1]!.body as string)).toMatchObject({ expectedVersion: 7, body: 'Follow up' })
  })
  it('does not resend across another person\'s input', async () => {
    vi.mocked(request).mockRejectedValueOnce(new ApiError('Changed', 409)).mockResolvedValueOnce({ session: { ...task, version: 6 }, activities: [{ type: 'prompt' }] })
    await expect(replyApplicationTask('workspace', task, 'prompt', 'Follow up')).rejects.toMatchObject({ status: 409 })
    expect(request).toHaveBeenCalledTimes(2)
  })
  it('never automatically retries a business approval', async () => {
    vi.mocked(request).mockRejectedValueOnce(new ApiError('Changed', 409))
    await expect(replyApplicationTask('workspace', task, 'prompt', '', true)).rejects.toMatchObject({ status: 409 })
    expect(request).toHaveBeenCalledTimes(1)
  })
})
