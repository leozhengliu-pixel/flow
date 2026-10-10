import { render, waitFor } from '@testing-library/react'
import type { Editor } from '@tiptap/react'
import { describe, expect, it, vi } from 'vitest'
import { CollaborativeEditor } from './collaborative-editor'
import { makeBootstrap } from '@/test/fixtures'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'editor-test' }))

describe('document editor uploads', () => {
  it('gives the shared editor the document upload function so pasted media never keeps a blob: URL', async () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    vi.stubGlobal('WebSocket', class { static OPEN = 1; readyState = 0; send() {} close() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    const upload = vi.fn().mockResolvedValue('/uploads/shot.png')
    const data = makeBootstrap()
    const { container } = render(<CollaborativeEditor data={data as never} document={{ id: 'doc-1', title: 'Doc' } as never} value="" presence={[]} onPresence={vi.fn()} onChange={vi.fn()} onPersist={vi.fn().mockResolvedValue(undefined)} onUploadFile={upload}/>)
    await waitFor(() => expect(container.querySelector('.ProseMirror')).not.toBeNull())
    const editor = (container.querySelector('.ProseMirror') as unknown as { editor: Editor }).editor
    expect(editor.storage.image.upload).toBe(upload)
  })
})
