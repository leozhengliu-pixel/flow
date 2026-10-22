import { act, render, waitFor } from '@testing-library/react'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { viewer } from '@/test/fixtures'
import { IssueDescriptionEditor } from './issue-description-editor'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'collab-sync-test' }))

/** An in-memory relay: every Yjs update frame one editor sends is held until `deliver()` hands it to the other editors. */
class Socket {
  static OPEN = 1
  static instances: Socket[] = []
  static queue: Array<{ from: Socket; data: ArrayBuffer }> = []
  readyState = 0
  onopen?: () => void
  onmessage?: (event: { data: unknown }) => void
  onclose?: () => void
  constructor() { Socket.instances.push(this) }
  send(value: unknown) { if (value instanceof Uint8Array && value[0] === 1) Socket.queue.push({ from: this, data: value.slice().buffer }) }
  close() { if (this.readyState === 3) return; this.readyState = 3; this.onclose?.() }
  sync(documentId: string) {
    this.readyState = 1
    this.onopen?.()
    this.onmessage?.({ data: JSON.stringify({ type: 'document.sync', documentId, updates: [] }) })
  }
  static deliver() {
    for (const frame of Socket.queue.splice(0)) for (const socket of Socket.instances) if (socket !== frame.from) socket.onmessage?.({ data: frame.data })
  }
}

const collab = { workspaceKey: 'test', issueId: 'issue-1', documentId: 'document-1', documentVersion: 1, viewer, onPersist: async () => {} }

async function mountPair(value: string) {
  const editors: Array<Editor | null> = [null, null]
  render(<>
    <IssueDescriptionEditor value={value} editorRef={editor => { editors[0] = editor }} collaboration={collab}/>
    <IssueDescriptionEditor value={value} editorRef={editor => { editors[1] = editor }} collaboration={collab}/>
  </>)
  await waitFor(() => expect(editors.every(Boolean)).toBe(true))
  await waitFor(() => expect(Socket.instances).toHaveLength(2))
  act(() => { for (const socket of Socket.instances) socket.sync(collab.documentId) })
  return editors as [Editor, Editor]
}

/** What ProseMirror's paste handler dispatches for plain clipboard text: the text replaces the selection, tagged as a paste. */
function pasteText(editor: Editor, text: string) {
  const { state } = editor.view
  editor.view.dispatch(state.tr.insertText(text).setMeta('paste', true).setMeta('uiEvent', 'paste'))
}

function typeText(editor: Editor, text: string) {
  const view = editor.view
  for (const character of text) {
    const { from, to } = view.state.selection
    const handled = view.someProp('handleTextInput', handler => handler(view, from, to, character, () => view.state.tr.insertText(character, from, to)))
    if (!handled) view.dispatch(view.state.tr.insertText(character, from, to))
  }
}

const linkHrefs = (editor: Editor) => {
  const hrefs: string[] = []
  editor.state.doc.descendants(node => { for (const mark of node.marks) if (mark.type.name === 'link') hrefs.push(mark.attrs.href as string) })
  return hrefs
}

describe('collaborative description: rewrites appended to a local text edit', () => {
  let errors: unknown[][]
  beforeEach(() => {
    sessionStorage.clear()
    Socket.instances = []
    Socket.queue = []
    vi.stubGlobal('WebSocket', Socket)
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    errors = []
    vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => { errors.push(args) })
  })
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

  it('pastes a URL into a new empty paragraph: the link paste rule runs, nothing is lost, and both editors converge', async () => {
    const [first, second] = await mountPair('Intro text')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    act(() => { pasteText(first, 'https://example.com/page') })
    expect(errors).toEqual([])
    expect(first.getJSON().content?.[1]).toMatchObject({ type: 'paragraph', content: [{ type: 'text', text: 'https://example.com/page', marks: [{ type: 'link', attrs: { href: 'https://example.com/page' } }] }] })
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
    expect(linkHrefs(second)).toEqual(['https://example.com/page'])
  })

  it('pastes Markdown emphasis after an inline mention chip and keeps the bold paste rule', async () => {
    const [first, second] = await mountPair('Owner')
    act(() => {
      first.commands.focus('end')
      first.commands.insertContent({ type: 'mention', attrs: { id: 'usr_1', label: 'Dev User', mentionType: 'user' } })
    })
    act(() => { pasteText(first, ' **ship it**') })
    expect(errors).toEqual([])
    const paragraph = first.getJSON().content?.[0]
    expect(paragraph?.content?.map(node => node.type)).toEqual(['text', 'mention', 'text', 'text'])
    expect(paragraph?.content?.at(-1)).toMatchObject({ text: 'ship it', marks: [{ type: 'bold' }] })
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
  })

  it('types a URL, a line break and a space: autolink links the URL without a position error', async () => {
    const [first, second] = await mountPair('Start')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    act(() => { typeText(first, 'https://flow.test/a') })
    act(() => { first.commands.setHardBreak() })
    act(() => { typeText(first, ' ') })
    expect(errors).toEqual([])
    expect(linkHrefs(first)).toEqual(['https://flow.test/a'])
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
  })

  it('undoes the paste and its link together, and two editors editing concurrently converge', async () => {
    const [first, second] = await mountPair('Shared body')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    act(() => { pasteText(first, 'https://example.com/undo') })
    act(() => { second.commands.focus('start'); typeText(second, 'Second: ') })
    act(() => { Socket.deliver() })
    expect(first.getJSON()).toEqual(second.getJSON())
    expect(first.getText()).toContain('Second: Shared body')
    expect(linkHrefs(second)).toEqual(['https://example.com/undo'])
    act(() => { first.commands.undo() })
    expect(first.getText()).not.toContain('https://example.com/undo')
    expect(linkHrefs(first)).toEqual([])
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
    expect(second.getText()).toContain('Second: Shared body')
    act(() => { first.commands.redo(); Socket.deliver() })
    expect(linkHrefs(first)).toEqual(['https://example.com/undo'])
    expect(second.getJSON()).toEqual(first.getJSON())
    expect(errors).toEqual([])
  })
})
