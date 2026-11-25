import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { rememberUploadLimit, resetRememberedUploadLimit } from '@/lib/upload-limit'
import { ClientEditorProvider } from './client-editor-provider'
import { useClientEditor } from './client-editor-context'

function wrap(issueId?: string) {
  return ({ children }: { children: ReactNode }) => (
    <ClientEditorProvider issueId={issueId}>{children}</ClientEditorProvider>
  )
}

/** A file whose size is reported without allocating its bytes. */
function sizedFile(size: number) {
  const file = new File(['x'], 'big.bin')
  Object.defineProperty(file, 'size', { value: size })
  return file
}

describe('ClientEditorProvider', () => {
  afterEach(() => resetRememberedUploadLimit())

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
    // The server's default limit is 100 MB.
    expect(result.current.maxUploadBytes).toBe(100 << 20)
    expect(result.current.checkUploadSize(sizedFile(100 << 20)).ok).toBe(true)
    expect(result.current.checkUploadSize(sizedFile((100 << 20) + 1))).toEqual({ ok: false, message: 'File exceeds 100 MB limit' })
  })

  it('follows the upload limit of the loaded bootstrap', () => {
    rememberUploadLimit({ uploadMaxBytes: 250 << 20 })
    const { result } = renderHook(() => useClientEditor(), { wrapper: wrap('issue-1') })
    expect(result.current.checkUploadSize(sizedFile(200 << 20)).ok).toBe(true)
    expect(result.current.checkUploadSize(sizedFile((250 << 20) + 1))).toEqual({ ok: false, message: 'File exceeds 250 MB limit' })
  })
})
