import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-client'
import { DraftAutosave, resetDraftAutosaves, wasDiscardedDraft, type DraftSaveInput } from './draft-autosave'

type Call = { kind: 'create' | 'update' | 'delete'; id?: string; input?: Record<string, unknown>; resolve: (value: unknown) => void; reject: (reason: unknown) => void }
const api = vi.hoisted(() => {
  const calls: Call[] = []
  const record = (kind: Call['kind'], id?: string, input?: Record<string, unknown>) => new Promise((resolve, reject) => { calls.push({ kind, id, input, resolve, reject }) })
  return { calls, record }
})
vi.mock('@/lib/api', () => ({
  createDraft: (input: Record<string, unknown>) => api.record('create', undefined, input),
  updateDraft: (id: string, input: Record<string, unknown>) => api.record('update', id, input),
  deleteDraft: (id: string) => api.record('delete', id),
}))

const input = (body: string): DraftSaveInput => ({ type: 'project_update', resourceId: 'project-1', title: 'Project', body, metadata: { resourceType: 'project', health: 'onTrack' } })
const saved = (id: string) => ({ id, userId: 'viewer', type: 'project_update', title: 'Project', body: '', createdAt: '', updatedAt: '' })
const flush = () => vi.advanceTimersByTimeAsync(0)
const kinds = () => api.calls.map(call => call.kind)

beforeEach(() => { api.calls.length = 0; resetDraftAutosaves(); vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

describe('draft autosave', () => {
  it('sends one create while it is pending and then only the latest content', async () => {
    const autosave = new DraftAutosave()
    autosave.schedule(input('a'))
    await vi.advanceTimersByTimeAsync(350)
    for (const body of ['ab', 'abc', 'abcd']) { autosave.schedule(input(body)); await vi.advanceTimersByTimeAsync(350) }
    expect(kinds()).toEqual(['create'])
    api.calls[0].resolve(saved('draft_1'))
    await flush()
    expect(kinds()).toEqual(['create', 'update'])
    expect(api.calls[1]).toMatchObject({ id: 'draft_1', input: { body: 'abcd', clientKey: api.calls[0].input?.clientKey } })
  })

  it('re-creates only on the API’s own not found, never on a proxy 404 or a refused update', async () => {
    const autosave = new DraftAutosave()
    autosave.seed({ id: 'draft_old', input: input('kept') })
    autosave.schedule(input('kept, edited'))
    await vi.advanceTimersByTimeAsync(350)
    api.calls[0].reject(new ApiError('Request failed: 404', 404))
    await vi.advanceTimersByTimeAsync(1_000)
    expect(kinds()).toEqual(['update', 'update'])
    api.calls[1].reject(new ApiError('comment draft requires a valid parent resource', 400, 'comment draft requires a valid parent resource'))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(kinds()).toEqual(['update', 'update'])
    autosave.schedule(input('kept, edited again'))
    await vi.advanceTimersByTimeAsync(350)
    api.calls[2].reject(new ApiError('not found', 404, 'not found'))
    await flush()
    expect(kinds()).toEqual(['update', 'update', 'update', 'create'])
    expect(api.calls[3].input).toMatchObject({ body: 'kept, edited again' })
  })

  it('resolves a create with an unknown outcome before deleting the sent draft', async () => {
    const autosave = new DraftAutosave()
    autosave.schedule(input('posted'))
    await vi.advanceTimersByTimeAsync(350)
    const sent = autosave.detach()
    const done = sent.discard()
    api.calls[0].reject(new TypeError('Failed to fetch'))
    await flush()
    // The lost create is asked again with the same key, and whatever it names is deleted.
    expect(api.calls[1]).toMatchObject({ kind: 'create', input: { clientKey: api.calls[0].input?.clientKey } })
    api.calls[1].resolve(saved('draft_9'))
    await flush()
    expect(api.calls[2]).toMatchObject({ kind: 'delete', id: 'draft_9' })
    api.calls[2].resolve(undefined)
    await done
    expect(wasDiscardedDraft('draft_9')).toBe(true)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.calls).toHaveLength(3)
  })

  it('does not save content the server already has', async () => {
    const autosave = new DraftAutosave()
    autosave.seed({ id: 'draft_old', input: input('same') })
    autosave.schedule({ ...input('same'), metadata: { health: 'onTrack', resourceType: 'project', bodyData: undefined } })
    await vi.advanceTimersByTimeAsync(5_000)
    expect(api.calls).toHaveLength(0)
  })
})
