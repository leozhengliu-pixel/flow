import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { viewer } from '@/test/fixtures'
import { IssueDescriptionEditor } from './issue-description-editor'
import { serializeDescription } from './editor/editor-content'
import { descriptionRecoveryKey, readDescriptionRecovery } from './editor/description-recovery'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'editor-test' }))

class Socket {
  static OPEN = 1
  static instances: Socket[] = []
  readyState = 0
  sent: unknown[] = []
  onopen?: () => void
  onmessage?: (event: { data: unknown }) => void
  onclose?: () => void
  constructor() { Socket.instances.push(this) }
  send(value: unknown) { this.sent.push(value) }
  close() { if (this.readyState === 3) return; this.readyState = 3; this.onclose?.() }
  sync(documentId = 'document-1', contentState?: string) {
    this.readyState = 1
    this.onopen?.()
    this.onmessage?.({ data: JSON.stringify({ type: 'document.sync', documentId, contentState, updates: [] }) })
  }
}

const collab = { workspaceKey: 'test', issueId: 'issue-1', documentId: 'document-1', documentVersion: 3, viewer }

describe('real Tiptap description collaboration', () => {
  beforeEach(() => {
    sessionStorage.clear()
    Socket.instances = []
    vi.stubGlobal('WebSocket', Socket)
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

  it('waits for a full description before creating Y.Doc and preserves Markdown formatting', async () => {
    const persist = vi.fn().mockResolvedValue(undefined)
    let editor: Editor | null = null
    const ref = (value: Editor | null) => { editor = value }
    const view = render(<IssueDescriptionEditor loading value="" editorRef={ref} collaboration={{ ...collab, onPersist: persist }}/>)
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(Socket.instances).toHaveLength(0)
    view.rerender(<IssueDescriptionEditor value={'## MCP body\n\n**Important** content'} editorRef={ref} collaboration={{ ...collab, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('MCP body')
    expect(screen.getByText('Important').tagName).toBe('STRONG')
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    act(() => Socket.instances[0].sync())
    fireEvent.blur(screen.getByRole('textbox'))
    expect(persist).not.toHaveBeenCalled()
    expect(Socket.instances[0].sent.filter(value => value instanceof Uint8Array && value[0] === 1)).toHaveLength(0)
  })

  it('ignores remote transactions and never persists an unedited blur', async () => {
    const persist = vi.fn().mockResolvedValue(undefined)
    const change = vi.fn()
    let editor: Editor | null = null
    const view = render(<IssueDescriptionEditor value="Remote body" editorRef={value => { editor = value }} collaboration={{ ...collab, onPersist: persist }} onChange={change}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    const snapshot = serializeDescription(editor!)
    act(() => Socket.instances[0].sync('document-1', snapshot.contentState))
    view.rerender(<IssueDescriptionEditor value="Remote body" collaboration={{ ...collab, documentVersion: 4, contentState: snapshot.contentState, onPersist: persist }} onChange={change}/>)
    fireEvent.blur(screen.getByRole('textbox'))
    expect(persist).not.toHaveBeenCalled()
    expect(change).not.toHaveBeenCalled()
  })

  it('does not publish synthetic trailing paragraphs when opening and selecting a Markdown list', async () => {
    const persist = vi.fn().mockResolvedValue(undefined)
    let editor: Editor | null = null
    render(<IssueDescriptionEditor value={'# Shared content\n\n- first item\n- second item'} editorRef={value => { editor = value }} collaboration={{ ...collab, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    act(() => Socket.instances[0].sync())
    act(() => { editor!.commands.setTextSelection(2) })
    fireEvent.focus(screen.getByRole('textbox'))
    fireEvent.blur(screen.getByRole('textbox'))
    expect(Socket.instances[0].sent.filter(value => value instanceof Uint8Array && value[0] === 1)).toHaveLength(0)
    expect(editor!.getJSON().content?.at(-1)?.type).toBe('bulletList')
    expect(persist).not.toHaveBeenCalled()
  })

  it('preserves unsaved edits on conflict, does not blindly retry, and loads replacement generations', async () => {
    const persist = vi.fn().mockRejectedValue(Object.assign(new Error('Version conflict'), { status: 409 }))
    let editor: Editor | null = null
    const view = render(<IssueDescriptionEditor value="Original" editorRef={value => { editor = value }} collaboration={{ ...collab, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    act(() => Socket.instances[0].sync())
    act(() => { editor!.commands.insertContent('Local addition ') })
    view.rerender(<IssueDescriptionEditor value="Concurrent server change" editorRef={value => { editor = value }} collaboration={{ ...collab, documentVersion: 4, onPersist: persist }}/>)
    fireEvent.blur(screen.getByRole('textbox'))
    await waitFor(() => expect(persist).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('another session'))
    expect(persist.mock.calls[0][2]).toBe(3)
    expect(readDescriptionRecovery(descriptionRecoveryKey('test', 'issue-1', viewer.id))?.snapshot.markdown).toContain('Local addition')
    vi.useFakeTimers()
    await act(async () => { vi.advanceTimersByTime(5_000) })
    expect(persist).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
    view.rerender(<IssueDescriptionEditor value="New MCP replacement" collaboration={{ ...collab, documentId: 'document-2', documentVersion: 4, onPersist: persist }}/>)
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveTextContent('New MCP replacement'))
    expect(screen.getByRole('textbox')).not.toHaveTextContent('Original')
    expect(screen.getByRole('button', { name: 'Download local changes' })).toBeInTheDocument()
    expect(readDescriptionRecovery(descriptionRecoveryKey('test', 'issue-1', viewer.id))?.snapshot.markdown).toContain('Local addition')
  })

  it('keeps offline edits locally and saves only after reconnecting and merging server state', async () => {
    const persist = vi.fn().mockResolvedValue(undefined)
    let editor: Editor | null = null
    render(<IssueDescriptionEditor value="Original" editorRef={value => { editor = value }} collaboration={{ ...collab, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    act(() => { editor!.commands.insertContent('Offline addition ') })
    fireEvent.blur(screen.getByRole('textbox'))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Retry when connected'))
    expect(persist).not.toHaveBeenCalled()
    act(() => Socket.instances[0].sync())
    await waitFor(() => expect(persist).toHaveBeenCalledTimes(1))
    expect(persist.mock.calls[0][0].markdown).toContain('Offline addition')
    expect(persist.mock.calls[0][0].markdown).toContain('Original')
    expect(readDescriptionRecovery(descriptionRecoveryKey('test', 'issue-1', viewer.id))).toBeUndefined()
  })

  it('isolates Y.Doc sessions and recovery drafts across accounts and ignores ownerless legacy drafts', async () => {
    const persist = vi.fn().mockResolvedValue(undefined)
    let editor: Editor | null = null
    const view = render(<IssueDescriptionEditor value="Shared server body" editorRef={value => { editor = value }} collaboration={{ ...collab, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    const firstEditor = editor
    act(() => { editor!.commands.insertContent('Private unsaved change ') })
    const ownerKey = descriptionRecoveryKey('test', 'issue-1', viewer.id)
    expect(readDescriptionRecovery(ownerKey)?.snapshot.markdown).toContain('Private unsaved change')
    sessionStorage.setItem('flow:description-recovery:test:issue-1', sessionStorage.getItem(ownerKey)!)

    const otherViewer = { ...viewer, id: 'other-account', displayName: 'Other account' }
    view.rerender(<IssueDescriptionEditor value="Shared server body" editorRef={value => { editor = value }} collaboration={{ ...collab, viewer: otherViewer, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBe(firstEditor))
    await waitFor(() => expect(Socket.instances).toHaveLength(2))
    expect(Socket.instances[0].readyState).toBe(3)
    expect(screen.getByRole('textbox')).toHaveTextContent('Shared server body')
    expect(screen.getByRole('textbox')).not.toHaveTextContent('Private unsaved change')
    expect(screen.queryByRole('button', { name: 'Download local changes' })).not.toBeInTheDocument()
    expect(readDescriptionRecovery(descriptionRecoveryKey('test', 'issue-1', otherViewer.id))).toBeUndefined()
    expect(readDescriptionRecovery(ownerKey)?.snapshot.markdown).toContain('Private unsaved change')
    expect(persist).not.toHaveBeenCalled()
  })

  it('persists local Yjs undo operations with the next acknowledged document version', async () => {
    const persist = vi.fn().mockResolvedValue(undefined)
    let editor: Editor | null = null
    render(<IssueDescriptionEditor value="Original" editorRef={value => { editor = value }} collaboration={{ ...collab, onPersist: persist }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    await waitFor(() => expect(Socket.instances).toHaveLength(1))
    act(() => Socket.instances[0].sync())
    act(() => { editor!.commands.insertContent('Addition ') })
    fireEvent.blur(screen.getByRole('textbox'))
    await waitFor(() => expect(persist).toHaveBeenCalledTimes(1))
    act(() => { editor!.commands.undo() })
    fireEvent.blur(screen.getByRole('textbox'))
    await waitFor(() => expect(persist).toHaveBeenCalledTimes(2))
    expect(persist.mock.calls[1][0].markdown).toBe('Original')
    expect(persist.mock.calls[1][2]).toBe(4)
  })

  it('merges two real editors without echo-saving remote transactions or overwriting a conflicting snapshot', async () => {
    const firstPersist = vi.fn().mockResolvedValue(undefined)
    const secondPersist = vi.fn().mockRejectedValue(Object.assign(new Error('Another snapshot won'), { status: 409 }))
    const editors: Array<Editor | null> = [null, null]
    render(<>
      <IssueDescriptionEditor value="Shared body" editorRef={editor => { editors[0] = editor }} collaboration={{ ...collab, onPersist: firstPersist }}/>
      <IssueDescriptionEditor value="Shared body" editorRef={editor => { editors[1] = editor }} collaboration={{ ...collab, onPersist: secondPersist }}/>
    </>)
    await waitFor(() => expect(editors.every(Boolean)).toBe(true))
    await waitFor(() => expect(Socket.instances).toHaveLength(2))
    act(() => { Socket.instances[0].sync(); Socket.instances[1].sync() })
    act(() => {
      editors[0]!.commands.insertContent('First edit ')
      editors[1]!.commands.insertContent('Second edit ')
    })
    const frames = Socket.instances.flatMap(socket => socket.sent.filter((value): value is Uint8Array => value instanceof Uint8Array && value[0] === 1))
    act(() => {
      for (const frame of frames) for (const socket of Socket.instances) socket.onmessage?.({ data: frame.buffer })
    })
    expect(editors[0]!.getJSON()).toEqual(editors[1]!.getJSON())
    expect(editors[0]!.getText()).toContain('First edit')
    expect(editors[0]!.getText()).toContain('Second edit')
    for (const textbox of screen.getAllByRole('textbox')) fireEvent.blur(textbox)
    await waitFor(() => expect(firstPersist).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(secondPersist).toHaveBeenCalledTimes(1))
    expect(firstPersist.mock.calls[0][0].document).toEqual(secondPersist.mock.calls[0][0].document)
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('another session'))
    expect(secondPersist.mock.calls[0][2]).toBe(3)
  })
})
