import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { CommentActions } from './comment-actions'
import { useCommentActions } from '@/hooks/use-comment-actions'
import { COMMENT_ACTION_RESOLVED, COMMENT_ACTIONS_CHANGED, type CommentAction } from '@/lib/comment-actions'

const api = vi.hoisted(() => ({ listCommentActions: vi.fn(), resolveCommentAction: vi.fn() }))
vi.mock('@/lib/comment-actions', async original => ({ ...await original<typeof import('@/lib/comment-actions')>(), ...api }))

const proposal: CommentAction = { id: 'a', issueId: 'i', commentId: 'c', targetUserId: 'u', status: 'pending', action: 'priority', arguments: { priority: 1 }, createdAt: '2026-11-04T00:00:00Z' }
const wrapper = ({ children }: { children: React.ReactNode }) => <I18nProvider>{children}</I18nProvider>

describe('comment suggested actions', () => {
  beforeEach(() => { vi.resetAllMocks(); api.listCommentActions.mockResolvedValue([proposal]) })

  it('accepts optimistically, prevents duplicate calls and keeps the accepted state', async () => {
    let finish!: (value: unknown) => void
    api.resolveCommentAction.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const model = renderHook(() => useCommentActions('i', 'c', 'u'))
    await waitFor(() => expect(model.result.current.actions).toHaveLength(1))
    let pending!: Promise<void>
    act(() => { pending = model.result.current.resolve(proposal, 'accepted') })
    expect(model.result.current.actions[0].status).toBe('accepted')
    await act(() => model.result.current.resolve(proposal, 'accepted'))
    expect(api.resolveCommentAction).toHaveBeenCalledTimes(1)
    await act(async () => { finish({ suggestion: { ...proposal, status: 'accepted' }, issue: makeBootstrap().issues[0] }); await pending })
    expect(model.result.current.busy.size).toBe(0)
    expect(model.result.current.actions[0].status).toBe('accepted')
  })

  it('dismisses immediately and restores a failed resolution', async () => {
    api.resolveCommentAction.mockRejectedValue(new Error('conflict'))
    const model = renderHook(() => useCommentActions('i', 'c', 'u'))
    await waitFor(() => expect(model.result.current.actions).toHaveLength(1))
    await act(() => model.result.current.resolve(proposal, 'dismissed'))
    expect(model.result.current.actions[0].status).toBe('pending')
    expect(model.result.current.error).toBe('a')
  })

  it('refreshes only for its issue and renders accessible controls', async () => {
    const model = renderHook(() => useCommentActions('i', 'c', 'u'))
    await waitFor(() => expect(model.result.current.actions).toHaveLength(1))
    act(() => window.dispatchEvent(new CustomEvent(COMMENT_ACTIONS_CHANGED, { detail: 'other' })))
    expect(api.listCommentActions).toHaveBeenCalledTimes(1)
    act(() => window.dispatchEvent(new CustomEvent(COMMENT_ACTIONS_CHANGED, { detail: 'i' })))
    await waitFor(() => expect(api.listCommentActions).toHaveBeenCalledTimes(2))
    const resolve = vi.fn()
    render(<CommentActions commentId="c" model={{ ...model.result.current, resolve }} />, { wrapper })
    expect(screen.getByText('Set priority to Urgent')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss suggestion' }))
    expect(resolve).toHaveBeenCalledWith(proposal, 'dismissed')
  })

  it('hides dismissed proposals and prevents disabled acceptance', () => {
    const model = { actions: [{ ...proposal, status: 'dismissed' as const }], busy: new Set<string>(), error: undefined, resolve: vi.fn() }
    const view = render(<CommentActions commentId="c" model={model} />, { wrapper })
    expect(screen.queryByLabelText('Suggested actions')).not.toBeInTheDocument()
    view.rerender(<CommentActions commentId="c" model={{ ...model, actions: [proposal], busy: new Set(['a']) }} />)
    expect(screen.getByRole('button', { name: 'Accept' })).toBeDisabled()
  })

  it('attributes delayed acceptance to its original workspace and viewer', async () => {
    window.history.replaceState(null, '', '/source/issue/I-1')
    let finish!: (value: unknown) => void
    api.resolveCommentAction.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const events: CustomEvent[] = []
    const listener = (event: Event) => events.push(event as CustomEvent)
    window.addEventListener(COMMENT_ACTION_RESOLVED, listener)
    const historyChanged = vi.fn()
    window.addEventListener('flow-issue-history-changed', historyChanged)
    try {
      const model = renderHook(() => useCommentActions('i', 'c', 'original-viewer'))
      await waitFor(() => expect(model.result.current.actions).toHaveLength(1))
      let pending!: Promise<void>
      act(() => { pending = model.result.current.resolve(proposal, 'accepted') })
      window.history.replaceState(null, '', '/destination/issue/I-1')
      await act(async () => { finish({ suggestion: { ...proposal, status: 'accepted' }, issue: makeBootstrap().issues[0] }); await pending })
      expect(events).toHaveLength(1)
      expect(events[0].detail.workspaceKey).toBe('source')
      expect(events[0].detail.viewerId).toBe('original-viewer')
      expect(historyChanged).not.toHaveBeenCalled()
    } finally {
      window.removeEventListener(COMMENT_ACTION_RESOLVED, listener)
      window.removeEventListener('flow-issue-history-changed', historyChanged)
      window.history.replaceState(null, '', '/')
    }
  })

  it('drops proposals immediately when the viewer changes or access is lost', async () => {
    const model = renderHook(({ viewer }) => useCommentActions('i', 'c', viewer), { initialProps: { viewer: 'original' } })
    await waitFor(() => expect(model.result.current.actions).toHaveLength(1))
    api.listCommentActions.mockRejectedValue(new Error('forbidden'))
    model.rerender({ viewer: 'other' })
    expect(model.result.current.actions).toHaveLength(0)
    await waitFor(() => expect(api.listCommentActions).toHaveBeenCalledTimes(2))
    api.listCommentActions.mockResolvedValue([proposal])
    act(() => window.dispatchEvent(new CustomEvent(COMMENT_ACTIONS_CHANGED, { detail: 'i' })))
    await waitFor(() => expect(model.result.current.actions).toHaveLength(1))
    api.listCommentActions.mockRejectedValue(new Error('forbidden'))
    act(() => window.dispatchEvent(new CustomEvent(COMMENT_ACTIONS_CHANGED)))
    await waitFor(() => expect(model.result.current.actions).toHaveLength(0))
  })
})
