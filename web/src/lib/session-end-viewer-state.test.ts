import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => {
  const calls: Array<{ kind: string; resolve: (value: unknown) => void }> = []
  const record = (kind: string) => new Promise(resolve => { calls.push({ kind, resolve }) })
  return { calls, record }
})
vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  createDraft: () => api.record('create'),
  updateDraft: () => api.record('update'),
  deleteDraft: () => api.record('delete'),
  addFavorite: () => api.record('addFavorite'),
  removeFavorite: () => api.record('removeFavorite'),
  setPulseSubscription: async (_type: string, _id: string, subscribed: boolean) => ({ subscribed }),
  setInitiativeProjectUpdatesSubscription: async (_id: string, subscribed: boolean) => ({ subscribed }),
}))

import { clearSessionCaches, flushBeforeSessionEnd } from '@/lib/session-cache-registry'
import { ApiError } from '@/lib/api-client'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { getDocumentContent, resetDocumentContentEditorState, setDocumentContent } from '@/components/documents/document-content-editor-state'
import { logoutAccount } from '@/lib/api'
import { liveActorUser, setLiveApplicationIdentities } from './actor-attribution'
import { DraftAutosave, draftAutosaveFor, resetDraftAutosaves } from './draft-autosave'
import { overlayPendingFavoriteIntents, resetFavoriteIntents, toggleFavorite } from './favorites'
import { getInlineFilter, setInlineFilterTerm } from './inline-filter'
import { queueIssueUpdate, queueIssueWrite, resetIssueMutationQueue } from './issue-mutation-queue'
import { changeInitiativeProjectUpdatesSubscription, changePulseSubscription, resetInitiativeProjectUpdatesChoices, resetPulseSessionChoices, sessionPulseChoice } from './pulse-subscriptions'
import { overlayUserSettings, resetUserSettingsOverrides, trackUserSettingsWrite } from './user-settings-overrides'

beforeEach(() => { api.calls.length = 0; vi.useFakeTimers() })
afterEach(() => {
  vi.useRealTimers()
  resetUserSettingsOverrides(); resetPulseSessionChoices(); resetInitiativeProjectUpdatesChoices(); resetFavoriteIntents()
  resetIssueMutationQueue(); resetDraftAutosaves(); resetDocumentContentEditorState(); setLiveApplicationIdentities(null)
})

describe('viewer state is dropped when the session ends', () => {
  it('user-settings overrides', () => {
    trackUserSettingsWrite('acme', { theme: 'dark' } as never)
    expect(overlayUserSettings('acme', { theme: 'light' } as never)).toMatchObject({ theme: 'dark' })
    clearSessionCaches()
    expect(overlayUserSettings('acme', { theme: 'light' } as never)).toMatchObject({ theme: 'light' })
  })

  it('live application identities', () => {
    setLiveApplicationIdentities([{ id: 'app-user', app: true, displayName: 'Renamed' }])
    const user = { id: 'app-user', app: true, displayName: 'Old' }
    expect(liveActorUser(user).displayName).toBe('Renamed')
    clearSessionCaches()
    expect(liveActorUser(user).displayName).toBe('Old')
  })

  it('pulse subscription choices', async () => {
    await changePulseSubscription('project', 'p1', true)
    await changeInitiativeProjectUpdatesSubscription('i1', true)
    expect(sessionPulseChoice('project', 'p1')).toBe(true)
    clearSessionCaches()
    expect(sessionPulseChoice('project', 'p1')).toBeUndefined()
    const { renderHook } = await import('@testing-library/react')
    const { useInitiativeProjectUpdatesSubscription } = await import('./pulse-subscriptions')
    expect(renderHook(() => useInitiativeProjectUpdatesSubscription('i1')).result.current.subscribed).toBe(false)
  })

  it('inline filter terms', () => {
    setInlineFilterTerm('issues', 'secret term')
    clearSessionCaches()
    expect(getInlineFilter('issues').lastSearchTerm).toBe('')
  })

  it('the document content mirror', () => {
    setDocumentContent('doc-1', 'private body', 'state')
    clearSessionCaches()
    expect(getDocumentContent('doc-1')).toBeUndefined()
  })

  it('pending favorite intents, and a request waiting behind another is never sent', async () => {
    const data = makeBootstrap()
    const base = { workspaceKey: data.workspace.urlKey, userId: viewer.id, resourceType: 'issue', resourceId: 'i1', currentlyFavorited: false }
    const first = toggleFavorite(base)
    void first.catch(() => undefined)
    const second = toggleFavorite({ ...base, resourceId: 'i1', currentlyFavorited: false, nextFavorite: false })
    void second.catch(() => undefined)
    expect(overlayPendingFavoriteIntents(data).favorites).not.toBe(data.favorites)
    clearSessionCaches()
    expect(overlayPendingFavoriteIntents(data)).toBe(data)
    api.calls[0]?.resolve(undefined)
    await vi.advanceTimersByTimeAsync(0)
    expect(api.calls.map(call => call.kind)).toEqual(['addFavorite'])
  })

  it('issue writes still waiting are rejected, not sent for the next viewer', async () => {
    let release!: () => void
    const sent = vi.fn(async () => ({ id: 'i1', version: 3 }))
    const first = queueIssueWrite('i1', () => new Promise(resolve => { release = () => resolve({ id: 'i1', version: 2 }) }))
    const waiting = queueIssueUpdate('i1', '/api/issues/i1', { title: 'typed by the first viewer' } as never, sent as never)
    const rejected = waiting.then(() => 'sent', error => error)
    clearSessionCaches()
    release()
    await first
    const outcome = await rejected
    expect(sent).not.toHaveBeenCalled()
    expect(outcome).toBeInstanceOf(ApiError)
  })

  it('the first viewer\'s in-flight write does not seed the next viewer\'s versions', async () => {
    let release!: () => void
    const first = queueIssueWrite('i1', () => new Promise(resolve => { release = () => resolve({ id: 'i1', version: 5 }) }))
    clearSessionCaches()
    release()
    await first
    const send = vi.fn(async (input: { expectedVersion?: number }) => ({ id: 'i1', version: (input.expectedVersion ?? 0) + 1 }))
    await queueIssueUpdate('i1', '/api/issues/i1', { title: 'x', expectedVersion: 4 } as never, send as never)
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ expectedVersion: 4 }))
  })

  it('draft saves waiting for the autosave delay are not sent after logout', async () => {
    const autosave = draftAutosaveFor('acme|comment|issue|i1|top-level')
    autosave.schedule({ type: 'comment', resourceId: 'i1', title: '', body: 'half typed', metadata: {} } as never)
    clearSessionCaches()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(api.calls).toEqual([])
    autosave.flush()
    expect(api.calls).toEqual([])
    expect(draftAutosaveFor('acme|comment|issue|i1|top-level')).not.toBe(autosave)
  })

  it('a workspace-scoped clear keeps unsaved work of the same viewer', async () => {
    const autosave = new DraftAutosave()
    autosave.schedule({ type: 'comment', resourceId: 'i1', title: '', body: 'kept', metadata: {} } as never)
    setInlineFilterTerm('issues', 'kept')
    clearSessionCaches(viewer.id, 'acme')
    expect(getInlineFilter('issues').lastSearchTerm).toBe('kept')
    await vi.advanceTimersByTimeAsync(400)
    expect(api.calls.map(call => call.kind)).toEqual(['create'])
  })

  describe('flush before logout', () => {
    const draft = (body: string) => ({ type: 'comment', resourceId: 'i1', title: '', body, metadata: {} }) as never

    it('saves a draft typed right before logout as the current viewer', async () => {
      const autosave = draftAutosaveFor('acme|comment|issue|i1|top-level')
      autosave.schedule(draft('typed just now'))
      let done = false
      const flushed = flushBeforeSessionEnd().then(() => { done = true })
      await vi.advanceTimersByTimeAsync(0)
      expect(api.calls.map(call => call.kind)).toEqual(['create'])
      expect(done).toBe(false)
      api.calls[0].resolve({ id: 'd1', userId: 'u', type: 'comment', title: '', body: '', createdAt: '', updatedAt: '' })
      await flushed
      expect(done).toBe(true)
      clearSessionCaches()
      await vi.advanceTimersByTimeAsync(5_000)
      expect(api.calls.map(call => call.kind)).toEqual(['create'])
    })

    it('waits for queued issue writes before the logout request is made', async () => {
      const order: string[] = []
      let release!: () => void
      const first = queueIssueWrite('i1', () => new Promise(resolve => { release = () => { order.push('write'); resolve({ id: 'i1', version: 2 }) } }))
      vi.stubGlobal('fetch', vi.fn(async () => { order.push('logout'); return new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } }) }))
      const loggedOut = logoutAccount().catch(() => undefined)
      await vi.advanceTimersByTimeAsync(100)
      expect(order).toEqual([])
      release()
      await first
      await vi.advanceTimersByTimeAsync(0)
      await loggedOut
      expect(order[0]).toBe('write')
      vi.unstubAllGlobals()
    })

    it('gives up after the bound and rejects what is still pending instead of sending it', async () => {
      const sent = vi.fn(async () => ({ id: 'i1', version: 3 }))
      void queueIssueWrite('i1', () => new Promise(() => undefined)).catch(() => undefined)
      const waiting = queueIssueUpdate('i1', '/api/issues/i1', { title: 'late' } as never, sent as never).then(() => 'sent', error => error)
      const flushed = flushBeforeSessionEnd(1500)
      await vi.advanceTimersByTimeAsync(1500)
      await flushed
      clearSessionCaches()
      expect(await waiting).toBeInstanceOf(ApiError)
      expect(sent).not.toHaveBeenCalled()
    })
  })
})
