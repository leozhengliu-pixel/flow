import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useCommentsEditState } from './use-comments-edit'

vi.mock('sonner', () => ({ toast: { message: vi.fn(), error: vi.fn() } }))

describe('useCommentsEditState', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('tracks editing, dirty, and ready state', () => {
    const { result } = renderHook(() => useCommentsEditState())
    act(() => result.current.startEditing('c1', 'Hello'))
    expect(result.current.isEditing('c1')).toBe(true)
    expect(result.current.isDirty('c1')).toBe(false)
    act(() => result.current.setDraft('c1', 'Hello world'))
    expect(result.current.isDirty('c1')).toBe(true)
    act(() => result.current.onEditorReadyStateChange('c1', true))
    expect(result.current.isEditorReady('c1')).toBe(true)
    act(() => result.current.markClean('c1'))
    expect(result.current.isEditing('c1')).toBe(false)
  })

  it('toasts on no-op validation', async () => {
    const { toast } = await import('sonner')
    const { result } = renderHook(() => useCommentsEditState())
    expect(result.current.validateNoOpSuggestionOrToast(' same ', 'same')).toBe(false)
    expect(toast.message).toHaveBeenCalled()
    expect(result.current.validateNoOpSuggestionOrToast('changed', 'same')).toBe(true)
  })
})
