import { render, waitFor } from '@testing-library/react'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CollaborativeEditor } from './collaborative-editor'
import { makeBootstrap, teammate } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'editor-test' }))

describe('document editor', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    vi.stubGlobal('WebSocket', class { static OPEN = 1; readyState = 0; send() {} close() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('gives the shared editor the document upload function so pasted media never keeps a blob: URL', async () => {
    const upload = vi.fn().mockResolvedValue('/uploads/shot.png')
    const data = makeBootstrap()
    const { container } = render(<CollaborativeEditor data={data as never} document={{ id: 'doc-1', title: 'Doc' } as never} value="" presence={[]} onPresence={vi.fn()} onChange={vi.fn()} onPersist={vi.fn().mockResolvedValue(undefined)} onUploadFile={upload}/>)
    await waitFor(() => expect(container.querySelector('.ProseMirror')).not.toBeNull())
    const editor = (container.querySelector('.ProseMirror') as unknown as { editor: Editor }).editor
    expect(editor.storage.image.upload).toBe(upload)
  })

  it('keeps one editor instance when other workspace records arrive', async () => {
    const data = makeBootstrap()
    const document = { id: 'doc-1', title: 'Doc' } as FlowDocument
    const editorRef = vi.fn<(editor: Editor | null) => void>()
    const props = { document, value: 'A draft', presence: [], onPresence: vi.fn(), onChange: vi.fn(), onPersist: vi.fn(), editorRef }
    const { container, rerender } = render(<CollaborativeEditor {...props} data={data}/>)
    await waitFor(() => expect(container.querySelector('.ProseMirror')).toBeInTheDocument())
    const content = container.querySelector('.ProseMirror')
    const liveEditors = () => editorRef.mock.calls.flatMap(([editor]) => editor ? [editor] : [])
    expect(liveEditors()).toHaveLength(1)
    const [editor] = liveEditors()

    rerender(<CollaborativeEditor {...props} data={{ ...data, users: [...data.users, { ...teammate, id: 'new-member' }] }}/>)
    rerender(<CollaborativeEditor {...props} data={{ ...data, projects: [] }}/>)
    expect(liveEditors()).toEqual([editor])
    expect(editor.isDestroyed).toBe(false)
    expect(container.querySelector('.ProseMirror')).toBe(content)
  })
})
