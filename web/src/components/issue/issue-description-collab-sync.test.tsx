import { act, render, waitFor } from '@testing-library/react'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { viewer } from '@/test/fixtures'
import { IssueDescriptionEditor } from './issue-description-editor'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }), useOptionalTranslate: () => (value: string) => value }))
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

  it('seeds math in both schemas, merges concurrent source edits and undoes only the local edit', async () => {
    const [first, second] = await mountPair('$$\nx+y\n$$')
    expect(first.state.doc.firstChild?.type.name).toBe('mathBlock')
    expect(second.getJSON()).toEqual(first.getJSON())
    act(() => {
      first.chain().setTextSelection(2).insertContent('^2').run()
      second.chain().setTextSelection(4).insertContent('^3').run()
      Socket.deliver()
    })
    expect(first.getMarkdown()).toContain('$$\nx^2+y^3\n$$')
    expect(second.getJSON()).toEqual(first.getJSON())
    act(() => { first.commands.undo(); Socket.deliver() })
    expect(first.getMarkdown()).toContain('$$\nx+y^3\n$$')
    expect(second.getJSON()).toEqual(first.getJSON())
    expect(errors).toEqual([])
  })

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

type TableJson = { type?: string; text?: string; content?: TableJson[] }
const tableRows = (editor: Editor) => {
  const table = (editor.getJSON().content as TableJson[]).find(node => node.type === 'table')
  const text = (node: TableJson): string => node.text ?? (node.content ?? []).map(text).join('')
  return table?.content?.map(row => (row.content ?? []).map(text)) ?? []
}
const firstTablePos = (editor: Editor) => {
  let found = -1
  editor.state.doc.descendants((node, pos) => { if (found < 0 && node.type.name === 'table') found = pos; return found < 0 })
  return found
}

describe('collaborative description: table commands', () => {
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

  it('typing "|--" in a new empty paragraph inserts the default table in both editors without tripping the appended-transaction sync', async () => {
    const [first, second] = await mountPair('Intro text')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    act(() => { typeText(first, '|--') })
    expect(errors).toEqual([])
    expect(tableRows(first)).toEqual([['', ''], ['', ''], ['', '']])
    const inserted = first.getJSON().content?.[1] as TableJson
    const rowTypes = inserted.content?.map(row => row.content?.map(cell => cell.type))
    expect(rowTypes).toEqual([['tableHeader', 'tableHeader'], ['tableCell', 'tableCell'], ['tableCell', 'tableCell']])
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
    // Typing into the first cell right after the rule keeps both editors in step.
    act(() => { typeText(first, 'Name') })
    act(() => { Socket.deliver() })
    expect(tableRows(second)[0][0]).toBe('Name')
    expect(errors).toEqual([])
  })

  it('a pasted spreadsheet (tab separated text) becomes a table that the other editor receives', async () => {
    const [first, second] = await mountPair('Intro text')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    const event = new Event('paste', { bubbles: true, cancelable: true })
    const values: Record<string, string> = { 'text/plain': 'a\tb\n1\t2', 'text/html': '' }
    Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => values[type] ?? '' } })
    act(() => { first.view.dom.dispatchEvent(event) })
    expect(errors).toEqual([])
    expect(tableRows(first)).toEqual([['a', 'b'], ['1', '2']])
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
  })

  it('sorting, moving and duplicating from the table menus converge in the other editor and undo in one step', async () => {
    const { sortColumn, moveLine, duplicateLine } = await import('@/components/editor/table/table-lines')
    const [first, second] = await mountPair('Intro text')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    act(() => {
      first.commands.insertContent('<table><tr><th><p>Name</p></th><th><p>Qty</p></th></tr><tr><td><p>banana</p></td><td><p>10</p></td></tr><tr><td><p>Apple</p></td><td><p>9</p></td></tr><tr><td><p>cherry</p></td><td><p>100</p></td></tr></table>')
    })
    act(() => { Socket.deliver() })
    expect(tableRows(second)).toEqual(tableRows(first))
    const before = tableRows(first)
    act(() => { sortColumn(first, { tablePos: firstTablePos(first), axis: 'column', index: 1 }, 'asc') })
    expect(tableRows(first).map(row => row[1])).toEqual(['Qty', '9', '10', '100'])
    act(() => { Socket.deliver() })
    expect(tableRows(second)).toEqual(tableRows(first))
    act(() => { moveLine(second, { tablePos: firstTablePos(second), axis: 'column', index: 0 }, 'after') })
    act(() => { Socket.deliver() })
    expect(tableRows(first)[0]).toEqual(['Qty', 'Name'])
    expect(tableRows(second)).toEqual(tableRows(first))
    // Yjs groups edits made within its capture window into one undo step; let the earlier edits close their group.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 650)) })
    act(() => { duplicateLine(first,{ tablePos: firstTablePos(first), axis: 'row', index: 1 }) })
    act(() => { Socket.deliver() })
    expect(tableRows(second)).toHaveLength(5)
    expect(tableRows(second)).toEqual(tableRows(first))
    act(() => { first.commands.undo() })
    act(() => { Socket.deliver() })
    expect(tableRows(first)).toHaveLength(4)
    expect(tableRows(second)).toEqual(tableRows(first))
    expect(before).toHaveLength(4)
    expect(errors).toEqual([])
  })

  const columnWidths = (editor: Editor) => {
    const table = (editor.getJSON().content as Array<{ type?: string; content?: Array<{ content?: Array<{ attrs?: { colwidth?: number[] | null } }> }> }>).find(node => node.type === 'table')
    return table?.content?.map(row => row.content?.map(cell => cell.attrs?.colwidth?.[0] ?? null)) ?? []
  }

  it('default column widths: a new table, an inserted column and a duplicated column reach the other editor, and concurrent inserts converge', async () => {
    const { duplicateLine } = await import('@/components/editor/table/table-lines')
    const [first, second] = await mountPair('Intro text')
    act(() => { first.commands.focus('end'); first.commands.splitBlock() })
    act(() => { typeText(first, '|--') })
    expect(columnWidths(first)).toEqual([[390, 390], [390, 390], [390, 390]])
    act(() => { Socket.deliver() })
    expect(columnWidths(second)).toEqual(columnWidths(first))
    act(() => { first.commands.addColumnAfter() })
    expect(columnWidths(first)).toEqual([[260, 260, 260], [260, 260, 260], [260, 260, 260]])
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
    act(() => { duplicateLine(second, { tablePos: firstTablePos(second), axis: 'column', index: 0 }) })
    act(() => { Socket.deliver() })
    expect(columnWidths(first)[0]).toEqual([260, 260, 260, 260])
    // Both editors insert a column at the same time: the widths both computed are identical, so nothing diverges.
    act(() => { first.commands.addColumnAfter(); second.commands.addColumnAfter(); Socket.deliver() })
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
    expect(columnWidths(first).every(row => row?.every(width => typeof width === 'number'))).toBe(true)
    expect(errors).toEqual([])
  })

  it('bold italic text keeps the same nesting in both editors', async () => {
    const [first, second] = await mountPair('Intro text')
    act(() => { first.commands.focus('end'); first.commands.insertContent('<p><strong><em>both</em></strong></p>') })
    act(() => { Socket.deliver() })
    expect(second.getJSON()).toEqual(first.getJSON())
    expect(first.getHTML()).toContain('<em><strong>both</strong></em>')
    expect(second.getHTML()).toContain('<em><strong>both</strong></em>')
  })
})
