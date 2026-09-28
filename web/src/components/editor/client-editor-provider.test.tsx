import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { ClientEditorProvider, useClientEditor } from './client-editor-provider'

function wrap(issueId?: string) {
  return ({ children }: { children: ReactNode }) => (
    <ClientEditorProvider issueId={issueId}>{children}</ClientEditorProvider>
  )
}

describe('ClientEditorProvider', () => {
  it('exposes hash helpers and size checks', () => {
    const { result } = renderHook(() => useClientEditor(), { wrapper: wrap('issue-1') })
    expect(result.current.targetCommentHash('abc')).toBe('#comment-abc')
    expect(result.current.isAnyCommentInHash('#comment-abc')).toBe(true)
    expect(result.current.isAnyCommentInHash('#activity-1')).toBe(false)
    expect(result.current.fetchCommentByHash('#comment-xyz')?.id).toBe('xyz')
    expect(result.current.fetchProjectUpdateByHash('#project-update-1')?.id).toBe('1')
    expect(result.current.getAnchoredCommentRedirectPath('/FLOW-1', 'c1')).toBe('/FLOW-1#comment-c1')
    const ok = result.current.checkUploadSize(new File(['x'], 'ok.png', { type: 'image/png' }))
    expect(ok.ok).toBe(true)
    const big = new File([new Uint8Array(21 << 20)], 'big.bin')
    expect(result.current.checkUploadSize(big).ok).toBe(false)
  })
})
