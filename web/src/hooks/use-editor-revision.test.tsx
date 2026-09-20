import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useEditorRevision } from './use-editor-revision'

describe('useEditorRevision', () => {
  it('starts at 1 and bumps on content change', () => {
    const { result } = renderHook(() => useEditorRevision())
    expect(result.current.revision).toBe(1)
    act(() => result.current.onContentChange())
    expect(result.current.revision).toBe(2)
    act(() => result.current.setRevision(10))
    expect(result.current.revision).toBe(10)
  })
})
