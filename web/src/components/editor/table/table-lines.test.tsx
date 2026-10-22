import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { Fragment, Slice } from '@tiptap/pm/model'
import { CellSelection } from '@tiptap/pm/tables'
import { EditorContent, useEditor } from '@tiptap/react'
import type { Editor } from '@tiptap/core'
import { useEffect } from 'react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { MarkdownPaste } from '@/components/editor/markdown-paste'
import { flowTableKit } from '.'
import {
  compareCellText, addHeaderRow, copyLineText, deleteLine, duplicateLine, formatLine, insertLine, lineAvailability, lineClipboard, moveLine, moveSelectedLine, selectLine,
  sortColumn, type TableLine,
} from './table-lines'
import { parseTabularText, tableFromRows, withHeaderRow } from './table-paste'

let current: Editor | null = null

function Harness({ content }: { content: string }) {
  const editor = useEditor({ extensions: [StarterKit, Markdown, MarkdownPaste, ...flowTableKit], content })
  useEffect(() => { current = editor }, [editor])
  return editor ? <EditorContent editor={editor}/> : null
}

const SAMPLE = '<table><tr><th><p>Name</p></th><th><p>Qty</p></th></tr><tr><td><p>banana</p></td><td><p>10</p></td></tr><tr><td><p>Apple</p></td><td><p>9</p></td></tr><tr><td><p>cherry</p></td><td><p>100</p></td></tr></table><p></p>'

async function setup(content = SAMPLE) {
  render(<I18nProvider><Harness content={content}/></I18nProvider>)
  await waitFor(() => expect(current).not.toBeNull())
  await waitFor(() => expect(document.querySelector('.ProseMirror')).not.toBeNull())
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)) })
  return current!
}

beforeAll(() => {
  // prosemirror-tables resolves the cell under the pointer on mousemove (user.hover).
  Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => null })
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
})
afterEach(() => { current?.destroy(); current = null })

type Json = { type?: string; text?: string; marks?: Array<{ type: string }>; content?: Json[] }
const tableJson = (editor: Editor) => (editor.getJSON().content as Json[]).find(node => node.type === 'table')!
const textOf = (node: Json): string => node.text ?? (node.content ?? []).map(textOf).join('')
const grid = (editor: Editor) => tableJson(editor).content!.map(row => row.content!.map(textOf))
const cellTypes = (editor: Editor) => tableJson(editor).content!.map(row => row.content!.map(cell => cell.type === 'tableHeader' ? 'H' : 'D').join(''))
const tablePos = (editor: Editor) => { let found = 0; editor.state.doc.descendants((node, pos) => { if (node.type.name === 'table') { found = pos; return false } return true }); return found }
const line = (editor: Editor, axis: 'row' | 'column', index: number): TableLine => ({ tablePos: tablePos(editor), axis, index })
const tableLocation = (editor: Editor) => { const pos = tablePos(editor); return { pos, node: editor.state.doc.nodeAt(pos)! } }

/** Runs `run` and reports how many transactions the editor dispatched (a menu command must be exactly one: one undo step). */
function countTransactions(editor: Editor, run: () => unknown) {
  let count = 0
  const onTransaction = ({ transaction }: { transaction: { docChanged: boolean } }) => { if (transaction.docChanged) count++ }
  editor.on('transaction', onTransaction)
  let result: unknown
  act(() => { result = run() })
  editor.off('transaction', onTransaction)
  return { count, result }
}

function paste(editor: Editor, clipboard: { text?: string; html?: string }) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  const values: Record<string, string> = { 'text/plain': clipboard.text ?? '', 'text/html': clipboard.html ?? '' }
  Object.defineProperty(event, 'clipboardData', {
    value: { types: Object.keys(values).filter(type => values[type]), files: [], items: [], getData: (type: string) => values[type] ?? '' },
  })
  act(() => { editor.view.dom.dispatchEvent(event) })
  return event
}

/** What ProseMirror does for typed text: input rules run from handleTextInput. */
function typeText(editor: Editor, text: string) {
  const view = editor.view
  for (const character of text) {
    const { from, to } = view.state.selection
    const handled = view.someProp('handleTextInput', handler => handler(view, from, to, character, () => view.state.tr.insertText(character, from, to)))
    if (!handled) act(() => { view.dispatch(view.state.tr.insertText(character, from, to)) })
  }
}

const tableCount = (editor: Editor) => (editor.getJSON().content as Json[]).filter(node => node.type === 'table').length

describe('insert', () => {
  it('inserts a row above / below and a column before / after in one transaction each, with ordinary cells', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => insertLine(editor, line(editor, 'row', 2), 'before')).count).toBe(1)
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'banana', '', 'Apple', 'cherry'])
    act(() => { insertLine(editor, line(editor, 'row', 4), 'after') })
    expect(grid(editor)).toHaveLength(6)
    expect(cellTypes(editor).slice(1)).toEqual(['DD', 'DD', 'DD', 'DD', 'DD'])
    expect(countTransactions(editor, () => insertLine(editor, line(editor, 'column', 1), 'after')).count).toBe(1)
    expect(grid(editor)[0]).toEqual(['Name', 'Qty', ''])
    act(() => { insertLine(editor, line(editor, 'column', 0), 'before') })
    expect(grid(editor)[0]).toEqual(['', 'Name', 'Qty', ''])
  })

  it('does not insert above the header row, but inserts below it as ordinary cells', async () => {
    const editor = await setup()
    expect(insertLine(editor, line(editor, 'row', 0), 'before')).toBe(false)
    act(() => { insertLine(editor, line(editor, 'row', 0), 'after') })
    expect(cellTypes(editor)).toEqual(['HH', 'DD', 'DD', 'DD', 'DD'])
  })
})

describe('move', () => {
  it('moves a row down and up (the header row stays pinned) and keeps the moved row selected', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => moveLine(editor, line(editor, 'row', 1), 'after')).count).toBe(1)
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'Apple', 'banana', 'cherry'])
    expect(editor.state.selection).toBeInstanceOf(CellSelection)
    expect((editor.state.selection as CellSelection).$anchorCell.index(-1)).toBe(2)
    act(() => { moveLine(editor, line(editor, 'row', 2), 'before') })
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'banana', 'Apple', 'cherry'])
    expect(moveLine(editor, line(editor, 'row', 1), 'before')).toBe(false)
    expect(moveLine(editor, line(editor, 'row', 0), 'after')).toBe(false)
    expect(moveLine(editor, line(editor, 'row', 3), 'after')).toBe(false)
    expect(cellTypes(editor)[0]).toBe('HH')
  })

  it('moves a column left and right with its cells (header cells stay header cells)', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => moveLine(editor, line(editor, 'column', 0), 'after')).count).toBe(1)
    expect(grid(editor)).toEqual([['Qty', 'Name'], ['10', 'banana'], ['9', 'Apple'], ['100', 'cherry']])
    expect(cellTypes(editor)[0]).toBe('HH')
    act(() => { moveLine(editor, line(editor, 'column', 1), 'before') })
    expect(grid(editor)[0]).toEqual(['Name', 'Qty'])
    expect(moveLine(editor, line(editor, 'column', 0), 'before')).toBe(false)
    expect(moveLine(editor, line(editor, 'column', 1), 'after')).toBe(false)
  })

  it('undoes a move in one step', async () => {
    const editor = await setup()
    const before = grid(editor)
    act(() => { moveLine(editor, line(editor, 'row', 1), 'after') })
    act(() => { editor.commands.undo() })
    expect(grid(editor)).toEqual(before)
  })

  it('moves the selected row / column with the keyboard shortcut', async () => {
    const editor = await setup()
    act(() => { selectLine(editor, line(editor, 'row', 2)) })
    act(() => { moveSelectedLine(editor, 'row', 'before') })
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'Apple', 'banana', 'cherry'])
    act(() => { selectLine(editor, line(editor, 'column', 0)) })
    act(() => { moveSelectedLine(editor, 'column', 'after') })
    expect(grid(editor)[0]).toEqual(['Qty', 'Name'])
  })
})

describe('duplicate', () => {
  it('copies a row right after itself and selects the copy', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => duplicateLine(editor, line(editor, 'row', 1))).count).toBe(1)
    expect(grid(editor).map(row => row.join(','))).toEqual(['Name,Qty', 'banana,10', 'banana,10', 'Apple,9', 'cherry,100'])
    expect((editor.state.selection as CellSelection).$anchorCell.index(-1)).toBe(2)
  })

  it('copies a column right after itself (the header row keeps header cells)', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => duplicateLine(editor, line(editor, 'column', 0))).count).toBe(1)
    expect(grid(editor).map(row => row.join(','))).toEqual(['Name,Name,Qty', 'banana,banana,10', 'Apple,Apple,9', 'cherry,cherry,100'])
    expect(cellTypes(editor)[0]).toBe('HHH')
  })

  it('does not duplicate the header row (its menu has no Duplicate)', async () => {
    const editor = await setup()
    expect(duplicateLine(editor, line(editor, 'row', 0))).toBe(false)
    expect(cellTypes(editor)).toEqual(['HH', 'DD', 'DD', 'DD'])
  })
})

describe('sort', () => {
  it('sorts numbers numerically and keeps the header row on top, ascending and descending', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => sortColumn(editor, line(editor, 'column', 1), 'asc')).count).toBe(1)
    expect(grid(editor).map(row => row[1])).toEqual(['Qty', '9', '10', '100'])
    act(() => { sortColumn(editor, line(editor, 'column', 1), 'desc') })
    expect(grid(editor).map(row => row[1])).toEqual(['Qty', '100', '10', '9'])
    expect(cellTypes(editor)[0]).toBe('HH')
  })

  it('sorts text case-insensitively and stably', async () => {
    const editor = await setup('<table><tr><th><p>N</p></th><th><p>V</p></th></tr><tr><td><p>zed</p></td><td><p>1</p></td></tr><tr><td><p>Banana</p></td><td><p>2</p></td></tr><tr><td><p>apple</p></td><td><p>3</p></td></tr><tr><td><p>banana</p></td><td><p>4</p></td></tr></table><p></p>')
    act(() => { sortColumn(editor, line(editor, 'column', 0), 'asc') })
    expect(grid(editor).map(row => row.join(':'))).toEqual(['N:V', 'apple:3', 'Banana:2', 'banana:4', 'zed:1'])
    act(() => { sortColumn(editor, line(editor, 'column', 0), 'desc') })
    expect(grid(editor).map(row => row.join(':'))).toEqual(['N:V', 'zed:1', 'Banana:2', 'banana:4', 'apple:3'])
  })

  it('sorts every row when there is no header row, and does nothing when already sorted', async () => {
    const editor = await setup('<table><tr><td><p>b</p></td></tr><tr><td><p>a</p></td></tr></table><p></p>')
    act(() => { sortColumn(editor, line(editor, 'column', 0), 'asc') })
    expect(grid(editor)).toEqual([['a'], ['b']])
    expect(sortColumn(editor, line(editor, 'column', 0), 'asc')).toBe(false)
  })

  it('compares numbers, formatted numbers, mixed text and empty cells', () => {
    expect(compareCellText('9', '10')).toBeLessThan(0)
    expect(compareCellText('1,000', '999')).toBeGreaterThan(0)
    expect(compareCellText('-2', '1.5')).toBeLessThan(0)
    expect(compareCellText('item 2', 'item 10')).toBeLessThan(0)
    expect(compareCellText('', 'a')).toBeLessThan(0)
    expect(compareCellText('B', 'a')).toBeGreaterThan(0)
  })

  it('undoes a sort in one step', async () => {
    const editor = await setup()
    const before = grid(editor)
    act(() => { sortColumn(editor, line(editor, 'column', 1), 'asc') })
    act(() => { editor.commands.undo() })
    expect(grid(editor)).toEqual(before)
  })
})

describe('format', () => {
  const marked = (editor: Editor, row: number, mark: string) => tableJson(editor).content![row].content!.map(cell => (cell.content![0].content ?? []).every(node => node.marks?.some(item => item.type === mark)))

  it('applies a mark to every cell of the row, and removes it again when every cell has it', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => formatLine(editor, line(editor, 'row', 1), 'bold')).count).toBe(1)
    expect(marked(editor, 1, 'bold')).toEqual([true, true])
    expect(marked(editor, 2, 'bold')).toEqual([false, false])
    act(() => { formatLine(editor, line(editor, 'row', 1), 'bold') })
    expect(marked(editor, 1, 'bold')).toEqual([false, false])
  })

  it('removes the mark when only some cells have it (as the reference does)', async () => {
    const editor = await setup()
    act(() => { formatLine(editor, line(editor, 'column', 0), 'italic') })
    expect(marked(editor, 2, 'italic')).toEqual([true, false])
    act(() => { formatLine(editor, line(editor, 'row', 2), 'italic') })
    expect(marked(editor, 2, 'italic')).toEqual([false, false])
  })

  it('formats a column with underline and strike, and keeps the line selected', async () => {
    const editor = await setup()
    act(() => { formatLine(editor, line(editor, 'column', 1), 'underline') })
    for (const row of [0, 1, 2, 3]) expect(marked(editor, row, 'underline')[1]).toBe(true)
    act(() => { formatLine(editor, line(editor, 'column', 1), 'strike') })
    expect(marked(editor, 3, 'strike')[1]).toBe(true)
    expect(editor.state.selection).toBeInstanceOf(CellSelection)
  })
})

describe('copy text, select, delete', () => {
  it('copies a row as cell texts separated by blank lines plus an html table without header cells', async () => {
    const editor = await setup()
    const clip = lineClipboard(editor, line(editor, 'row', 1))!
    expect(clip.text).toBe('banana\n\n10')
    expect(clip.html).toContain('<table')
    expect(clip.html).toContain('data-headerless')
    expect(clip.html).not.toContain('<th')
  })

  it('copies a column including its header cell', async () => {
    const editor = await setup()
    const clip = lineClipboard(editor, line(editor, 'column', 1))!
    expect(clip.text).toBe('Qty\n\n10\n\n9\n\n100')
    expect(clip.html).toContain('<th')
    expect(clip.html).not.toContain('data-headerless')
    expect(clip.html.match(/<tr/g)).toHaveLength(4)
  })

  it('writes a blank line only after text, like the reference: empty cells add nothing at the start or in the middle, an empty last cell leaves the trailing blank line', async () => {
    const editor = await setup('<table><tr><th><p>H</p></th><th><p></p></th></tr><tr><td><p>x</p></td><td><p></p></td></tr><tr><td><p></p></td><td><p></p></td></tr><tr><td><p>z</p></td><td><p>w</p></td></tr></table><p></p>')
    expect(lineClipboard(editor, line(editor, 'column', 0))!.text).toBe('H\n\nx\n\nz')
    expect(lineClipboard(editor, line(editor, 'column', 1))!.text).toBe('w')
    expect(lineClipboard(editor, line(editor, 'row', 2))!.text).toBe('')
    expect(lineClipboard(editor, line(editor, 'row', 3))!.text).toBe('z\n\nw')
    // Reference measurements: a row [empty, "two"] is "two"; a column [header, text, empty] is "Name\n\ntext\n\n"; [text, empty, text] has one blank line.
    const mixed = await setup('<table><tr><th><p>Name</p></th><th><p>Val</p></th></tr><tr><td><p>text</p></td><td><p>one</p></td></tr><tr><td><p></p></td><td><p>two</p></td></tr></table><p></p>')
    expect(lineClipboard(mixed, line(mixed, 'row', 2))!.text).toBe('two')
    expect(lineClipboard(mixed, line(mixed, 'column', 0))!.text).toBe('Name\n\ntext\n\n')
  })

  it('copies the header row with header cells and no header-less marker', async () => {
    const editor = await setup()
    const clip = lineClipboard(editor, line(editor, 'row', 0))!
    expect(clip.text).toBe('Name\n\nQty')
    expect(clip.html).toContain('<th')
    expect(clip.html).not.toContain('data-headerless')
    expect(clip.html.match(/<tr/g)).toHaveLength(1)
  })

  it('puts the plain text and the html table on the clipboard in one write', async () => {
    const editor = await setup()
    const written: Array<Record<string, string>> = []
    class FakeItem {
      data: Record<string, Blob>
      constructor(data: Record<string, Blob>) { this.data = data }
    }
    vi.stubGlobal('ClipboardItem', FakeItem)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { write: async (items: FakeItem[]) => { for (const item of items) written.push(Object.fromEntries(await Promise.all(Object.entries(item.data).map(async ([type, blob]) => [type, await blob.text()])))) } },
    })
    try {
      await copyLineText(editor, line(editor, 'row', 1))
    } finally { vi.unstubAllGlobals() }
    expect(written).toHaveLength(1)
    expect(Object.keys(written[0])).toEqual(['text/plain', 'text/html'])
    expect(written[0]['text/plain']).toBe('banana\n\n10')
    expect(written[0]['text/html']).toMatch(/^<table data-headerless=""><tbody><tr><td><p>banana<\/p><\/td><td><p>10<\/p><\/td><\/tr><\/tbody><\/table>$/)
  })

  it('selects a whole row / column', async () => {
    const editor = await setup()
    act(() => { selectLine(editor, line(editor, 'row', 2)) })
    expect((editor.state.selection as CellSelection).isRowSelection()).toBe(true)
    act(() => { selectLine(editor, line(editor, 'column', 1)) })
    expect((editor.state.selection as CellSelection).isColSelection()).toBe(true)
  })

  it('never deletes the header row or the last body row, but a column and then the table', async () => {
    const editor = await setup('<table><tr><th><p>H</p></th></tr><tr><td><p>solo</p></td></tr></table><p></p>')
    expect(deleteLine(editor, line(editor, 'row', 0))).toBe(false)
    expect(deleteLine(editor, line(editor, 'row', 1))).toBe(false)
    expect(grid(editor)).toEqual([['H'], ['solo']])
    expect(lineAvailability(tableLocation(editor), 'row', 1).delete).toBe(false)
    expect(lineAvailability(tableLocation(editor), 'column', 0).delete).toBe(true)
    act(() => { deleteLine(editor, line(editor, 'column', 0)) })
    expect(tableCount(editor)).toBe(0)
  })

  it('offers the header row only Insert below, and the other lines everything their position allows', async () => {
    const editor = await setup()
    const table = tableLocation(editor)
    expect(lineAvailability(table, 'row', 0)).toMatchObject({ insertBefore: false, insertAfter: true, moveBefore: false, moveAfter: false, duplicate: false, delete: false })
    expect(lineAvailability(table, 'row', 1)).toMatchObject({ insertBefore: true, moveBefore: false, moveAfter: true, duplicate: true, delete: true, separateDuplicate: false })
    expect(lineAvailability(table, 'row', 3)).toMatchObject({ moveBefore: true, moveAfter: false, separateDuplicate: true })
    expect(lineAvailability(table, 'column', 0)).toMatchObject({ insertBefore: true, moveBefore: false, moveAfter: true, sort: true, delete: true })
    expect(lineAvailability(table, 'column', 1)).toMatchObject({ moveBefore: true, moveAfter: false, separateDuplicate: true })
  })

  it('deletes a row / column, and the table when it was the last one', async () => {
    const editor = await setup()
    expect(countTransactions(editor, () => deleteLine(editor, line(editor, 'row', 2))).count).toBe(1)
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'banana', 'cherry'])
    act(() => { deleteLine(editor, line(editor, 'column', 0)) })
    expect(grid(editor)).toEqual([['Qty'], ['10'], ['100']])
    act(() => { deleteLine(editor, line(editor, 'column', 0)) })
    expect(tableCount(editor)).toBe(0)
  })
})

/** The clipboard html of the reference app minus its cosmetic wrappers (see REFERENCE_HTML below for a full capture). */
const mark = (type: string, attrs?: Record<string, string>) => ({ type, attrs })
const textNode = (text: string, marks: Array<ReturnType<typeof mark>> = []) => ({ type: 'text', text, ...(marks.length ? { marks } : {}) })
const cellNode = (kind: 'tableCell' | 'tableHeader', content: Array<ReturnType<typeof textNode>>, colwidth?: number) =>
  ({ type: kind, ...(colwidth ? { attrs: { colwidth: [colwidth] } } : {}), content: [{ type: 'paragraph', ...(content.length ? { content } : {}) }] })
const FORMATTED = [
  textNode('bold', [mark('bold')]), textNode(' '), textNode('ital', [mark('italic')]), textNode(' '), textNode('code', [mark('code')]), textNode(' '),
  textNode('https://example.com', [mark('link', { href: 'https://example.com' })]), textNode(' '),
]
const LINK_HTML = '<a href="https://example.com" target="_blank" rel="noopener noreferrer nofollow">https://example.com</a>'

describe('clipboard payloads match the reference app', () => {
  async function load(rows: object[][]) {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.setContent({ type: 'doc', content: [{ type: 'table', content: rows.map(cells => ({ type: 'tableRow', content: cells })) }, { type: 'paragraph' }] }, { emitUpdate: false }) })
    return editor
  }
  const formatted = () => [
    [cellNode('tableHeader', [textNode('Name')], 290), cellNode('tableHeader', [textNode('Val')], 390)],
    [cellNode('tableCell', FORMATTED, 290), cellNode('tableCell', [textNode('one')], 390)],
    [cellNode('tableCell', [], 290), cellNode('tableCell', [textNode('two')], 390)],
  ]

  it('a body row: header-less marker, data-colwidth per cell, marks and links as the reference writes them', async () => {
    const editor = await load(formatted())
    const clip = lineClipboard(editor, line(editor, 'row', 1))!
    expect(clip.html).toBe(`<table data-headerless=""><tbody><tr><td data-colwidth="290"><p><strong>bold</strong> <em>ital</em> <code>code</code> ${LINK_HTML} </p></td><td data-colwidth="390"><p>one</p></td></tr></tbody></table>`)
    expect(clip.text).toBe('bold ital code https://example.com \n\none')
  })

  it('a header-row copy: header cells, no marker, widths kept', async () => {
    const editor = await load(formatted())
    const clip = lineClipboard(editor, line(editor, 'row', 0))!
    expect(clip.html).toBe('<table><tbody><tr><th data-colwidth="290"><p>Name</p></th><th data-colwidth="390"><p>Val</p></th></tr></tbody></table>')
    expect(clip.text).toBe('Name\n\nVal')
  })

  it('a column with a header and an empty cell: one row per cell, an empty paragraph, a trailing blank line in the text', async () => {
    const editor = await load(formatted())
    const clip = lineClipboard(editor, line(editor, 'column', 0))!
    expect(clip.html).toBe(`<table><tbody><tr><th data-colwidth="290"><p>Name</p></th></tr><tr><td data-colwidth="290"><p><strong>bold</strong> <em>ital</em> <code>code</code> ${LINK_HTML} </p></td></tr><tr><td data-colwidth="290"><p></p></td></tr></tbody></table>`)
    expect(clip.text).toBe('Name\n\nbold ital code https://example.com \n\n')
  })

  it('a row whose first cell is empty has no leading blank line; a cell without a width has no data-colwidth', async () => {
    const editor = await load([[cellNode('tableCell', []), cellNode('tableCell', [textNode('two')])]])
    const clip = lineClipboard(editor, line(editor, 'row', 0))!
    expect(clip.text).toBe('two')
    expect(clip.html).toBe('<table data-headerless=""><tbody><tr><td><p></p></td><td><p>two</p></td></tr></tbody></table>')
  })

  it('a column of a header-less table keeps the marker and every row', async () => {
    const editor = await load([[cellNode('tableCell', [textNode('a')], 390)], [cellNode('tableCell', [textNode('b')], 390)], [cellNode('tableCell', [textNode('c')], 390)]])
    const clip = lineClipboard(editor, line(editor, 'column', 0))!
    expect(clip.html).toBe('<table data-headerless=""><tbody><tr><td data-colwidth="390"><p>a</p></td></tr><tr><td data-colwidth="390"><p>b</p></td></tr><tr><td data-colwidth="390"><p>c</p></td></tr></tbody></table>')
    expect(clip.text).toBe('a\n\nb\n\nc')
  })

  it('underline and strikethrough are u and s elements', async () => {
    const editor = await load([[cellNode('tableCell', [textNode('under', [mark('underline')]), textNode(' '), textNode('strike', [mark('strike')])])]])
    expect(lineClipboard(editor, line(editor, 'row', 0))!.html).toBe('<table data-headerless=""><tbody><tr><td><p><u>under</u> <s>strike</s></p></td></tr></tbody></table>')
  })

  it('round-trips through paste: a copied body row comes back header-less with its widths and marks, a copied header row keeps header cells', async () => {
    const editor = await load(formatted())
    const body = lineClipboard(editor, line(editor, 'row', 1))!
    const header = lineClipboard(editor, line(editor, 'row', 0))!
    const target = await setup('<p></p>')
    act(() => { target.commands.focus('end') })
    paste(target, { html: body.html, text: body.text })
    expect(cellTypes(target)).toEqual(['DD'])
    const row = tableJson(target).content![0].content! as Array<{ attrs?: { colwidth?: number[] }; content?: Json[] }>
    expect(row.map(cell => cell.attrs?.colwidth)).toEqual([[290], [390]])
    const marks = (row[0].content![0].content as Json[]).map(node => node.marks?.map(entry => entry.type).join('+') ?? '')
    expect(marks.filter(Boolean)).toEqual(['bold', 'italic', 'code', 'link'])
    const next = await setup('<p></p>')
    act(() => { next.commands.focus('end') })
    paste(next, { html: header.html, text: header.text })
    expect(cellTypes(next)).toEqual(['HH'])
    expect((tableJson(next).content![0].content! as Array<{ attrs?: { colwidth?: number[] } }>).map(cell => cell.attrs?.colwidth)).toEqual([[290], [390]])
  })

  // The reference app's own clipboard (a body row, abbreviated): a leading doc-id span, text-node paragraphs and authorship spans.
  const REFERENCE_HTML = '<span data-doc-id="03fbeff7-8d8e-4647-8828-f41f9e01d47f"></span><table data-headerless=""><tbody><tr><td data-colwidth="290"><p class="text-node"><strong><span class="attr" data-user-id="2b168382">bold</span></strong><span class="attr" data-user-id="2b168382"> </span><em><span class="attr" data-user-id="2b168382">ital</span></em><span class="attr" data-user-id="2b168382"> </span><code spellcheck="false" class="inline"><span class="attr" data-user-id="2b168382">code</span></code><span class="attr" data-user-id="2b168382"> </span><a href="https://example.com" target="_blank" rel="noopener noreferrer nofollow"><span class="attr" data-user-id="2b168382">https://example.com</span></a></p></td><td data-colwidth="390"><p class="text-node"><span class="attr" data-user-id="2b168382">one</span></p></td></tr></tbody></table>'

  it('pasting the reference app clipboard gives the same table: header-less, widths, marks, no stray paragraph', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    paste(editor, { html: REFERENCE_HTML, text: 'bold ital code https://example.com\n\none' })
    expect(cellTypes(editor)).toEqual(['DD'])
    const row = tableJson(editor).content![0].content! as Array<{ attrs?: { colwidth?: number[] } }>
    expect(row.map(cell => cell.attrs?.colwidth)).toEqual([[290], [390]])
    expect(textOf(tableJson(editor))).toBe('bold ital code https://example.comone')
    expect(editor.getHTML()).toContain('data-colwidth="290"')
    const top = (editor.getJSON().content as Json[]).map(node => node.type)
    expect(top.filter(type => type === 'table')).toHaveLength(1)
    expect(top.filter(type => type === 'paragraph').length).toBeLessThanOrEqual(2)
  })

  it('pasting reference html without the header-less marker promotes the first row, with widths (as the reference does)', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    paste(editor, { html: '<table><tbody><tr><td data-colwidth="200"><p>x</p></td><td data-colwidth="300"><p>y</p></td></tr></tbody></table>', text: 'x\ty' })
    expect(cellTypes(editor)).toEqual(['HH'])
    expect((tableJson(editor).content![0].content! as Array<{ attrs?: { colwidth?: number[] } }>).map(cell => cell.attrs?.colwidth)).toEqual([[200], [300]])
  })

  it('still reads the older colwidth attribute', async () => {
    const editor = await setup('<table><tr><th colwidth="250"><p>A</p></th></tr></table><p></p>')
    expect((tableJson(editor).content![0].content! as Array<{ attrs?: { colwidth?: number[] } }>)[0].attrs?.colwidth).toEqual([250])
  })
})

describe('header-less tables', () => {
  const ROWS = [['c', '3'], ['a', '1'], ['b', '2']]
  const loadHeaderless = async (rows: number) => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    const body = ROWS.slice(0, rows).map(cells => `<tr>${cells.map(text => `<td><p>${text}</p></td>`).join('')}</tr>`).join('')
    paste(editor, { html: `<table data-headerless=""><tbody>${body}</tbody></table>` })
    return editor
  }
  const items = async () => (await screen.findAllByRole('menuitem')).map(item => item.textContent?.replace(/[⌘⌥⇧↑↓←→▶]|Ctrl|Alt|Shift/g, '').trim())

  it('offers the first row Add header, Insert above and Move down, and the rest as any row (measured on the reference)', async () => {
    const editor = await loadHeaderless(3)
    expect(cellTypes(editor)).toEqual(['DD', 'DD', 'DD'])
    const table = tableLocation(editor)
    expect(lineAvailability(table, 'row', 0)).toMatchObject({ addHeader: true, insertBefore: true, insertAfter: true, moveBefore: false, moveAfter: true, duplicate: true, delete: true })
    expect(lineAvailability(table, 'row', 1)).toMatchObject({ addHeader: false, moveBefore: true, moveAfter: true, delete: true, separateDuplicate: false })
    expect(lineAvailability(table, 'row', 2)).toMatchObject({ addHeader: false, moveBefore: true, moveAfter: false, separateDuplicate: true })
    expect(lineAvailability(table, 'column', 0)).toMatchObject({ addHeader: false, sort: true, delete: true })
  })

  it('a single header-less row has Add header, Insert, Duplicate, Format and Copy text but no Delete; two rows get Delete', async () => {
    const one = await loadHeaderless(1)
    expect(lineAvailability(tableLocation(one), 'row', 0)).toMatchObject({ addHeader: true, insertBefore: true, duplicate: true, delete: false, moveBefore: false, moveAfter: false })
    expect(lineAvailability(tableLocation(one), 'column', 0)).toMatchObject({ sort: true, delete: true })
    const two = await loadHeaderless(2)
    expect(lineAvailability(tableLocation(two), 'row', 0).delete).toBe(true)
    expect(lineAvailability(tableLocation(two), 'row', 1).delete).toBe(true)
    act(() => { deleteLine(two, line(two, 'row', 0)) })
    expect(grid(two)).toEqual([['a', '1']])
    expect(lineAvailability(tableLocation(two), 'row', 0).delete).toBe(false)
  })

  it('renders the menus in the reference order: Add header and a divider on top, a divider after Duplicate', async () => {
    await loadHeaderless(2)
    const user = userEvent.setup()
    await user.hover(screen.getByText('c', { selector: 'p' }))
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(await items()).toEqual(['Add header', 'Insert above', 'Insert below', 'Move down', 'Duplicate', 'Format', 'Copy text', 'Delete'])
    expect(screen.getByRole('menuitem', { name: 'Add header' }).nextElementSibling?.getAttribute('role')).toBe('separator')
    await user.keyboard('{Escape}')
    await user.hover(screen.getByText('a', { selector: 'p' }))
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(await items()).toEqual(['Insert above', 'Insert below', 'Move up', 'Duplicate', 'Format', 'Copy text', 'Delete'])
  })

  it('Add header turns the first row into header cells in place, in one undo step, and then offers the header row menu', async () => {
    const editor = await loadHeaderless(3)
    // History groups edits closer than its newGroupDelay: let the paste settle so undo only reverts Add header.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 650)) })
    expect(countTransactions(editor, () => addHeaderRow(editor, line(editor, 'row', 0))).count).toBe(1)
    expect(cellTypes(editor)).toEqual(['HH', 'DD', 'DD'])
    expect(grid(editor)).toEqual([['c', '3'], ['a', '1'], ['b', '2']])
    expect(addHeaderRow(editor, line(editor, 'row', 0))).toBe(false)
    expect(lineAvailability(tableLocation(editor), 'row', 0)).toMatchObject({ addHeader: false, insertBefore: false, delete: false })
    act(() => { editor.commands.undo() })
    expect(cellTypes(editor)).toEqual(['DD', 'DD', 'DD'])
  })

  it('Add header keeps widths and marks of the cells', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    paste(editor, { html: '<table data-headerless=""><tbody><tr><td data-colwidth="210"><p><strong>x</strong></p></td><td data-colwidth="300"><p>y</p></td></tr></tbody></table>' })
    act(() => { addHeaderRow(editor, line(editor, 'row', 0)) })
    const cells = tableJson(editor).content![0].content! as Array<{ type: string; attrs?: { colwidth?: number[] }; content?: Json[] }>
    expect(cells.map(cell => cell.type)).toEqual(['tableHeader', 'tableHeader'])
    expect(cells[0].attrs?.colwidth).toEqual([210])
    expect(cells[0].content![0].content![0].marks![0].type).toBe('bold')
  })

  it('sorts every row of a header-less table (the first row included), moves the first row down and inserts above it', async () => {
    const editor = await loadHeaderless(3)
    act(() => { sortColumn(editor, line(editor, 'column', 0), 'asc') })
    expect(grid(editor).map(row => row[0])).toEqual(['a', 'b', 'c'])
    act(() => { moveLine(editor, line(editor, 'row', 0), 'after') })
    expect(grid(editor).map(row => row[0])).toEqual(['b', 'a', 'c'])
    act(() => { moveLine(editor, line(editor, 'row', 1), 'before') })
    expect(grid(editor).map(row => row[0])).toEqual(['a', 'b', 'c'])
    act(() => { insertLine(editor, line(editor, 'row', 0), 'before') })
    expect(grid(editor).map(row => row[0])).toEqual(['', 'a', 'b', 'c'])
    expect(cellTypes(editor)[0]).toBe('DD')
  })
})

describe('row and column menus', () => {
  async function hoverCell(text: string) {
    const user = userEvent.setup()
    await user.hover(screen.getByText(text, { selector: 'p' }))
    return user
  }
  const items = async () => (await screen.findAllByRole('menuitem')).map(item => item.textContent?.replace(/[⌘⌥⇧↑↓←→▶]|Ctrl|Alt|Shift/g, '').trim())

  it('shows the row menu for the hovered row; the first body row has no Move up; opening it tints the row but keeps the selection', async () => {
    const editor = await setup()
    const selection = JSON.stringify(editor.state.selection.toJSON())
    const user = await hoverCell('banana')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(await items()).toEqual(['Insert above', 'Insert below', 'Move down', 'Duplicate', 'Format', 'Copy text', 'Delete'])
    expect(JSON.stringify(editor.state.selection.toJSON())).toBe(selection)
    expect(document.querySelectorAll('.flow-table-line-active')).toHaveLength(2)
    await user.click(screen.getByRole('menuitem', { name: /Move down/ }))
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'Apple', 'banana', 'cherry'])
    await waitFor(() => expect(document.querySelectorAll('.flow-table-line-active')).toHaveLength(0))
  })

  it('the header row menu only has Insert below, Format and Copy text, with a divider only before Copy text', async () => {
    await setup()
    const user = await hoverCell('Name')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(await items()).toEqual(['Insert below', 'Format', 'Copy text'])
    expect(screen.getAllByRole('separator')).toHaveLength(1)
    expect(screen.getByRole('separator').previousElementSibling?.textContent).toContain('Format')
  })

  it('the column menu of a header cell is complete, and Sort is offered with a single body row', async () => {
    await setup('<table><tr><th><p>Name</p></th></tr><tr><td><p>solo</p></td></tr></table><p></p>')
    const user = await hoverCell('Name')
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    expect(await items()).toEqual(['Insert before', 'Insert after', 'Duplicate', 'Sort ascending', 'Sort descending', 'Format', 'Copy text', 'Delete'])
  })

  it('leaves Delete out of a row menu that would remove the last body row, and keeps it for the last column', async () => {
    await setup('<table><tr><th><p>Name</p></th></tr><tr><td><p>solo</p></td></tr></table><p></p>')
    const user = await hoverCell('solo')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(await items()).toEqual(['Insert above', 'Insert below', 'Duplicate', 'Format', 'Copy text'])
  })

  it('draws a divider before Duplicate only when Move up / left is the only move', async () => {
    await setup('<table><tr><th><p>H</p></th><th><p>I</p></th><th><p>J</p></th></tr><tr><td><p>a</p></td><td><p>b</p></td><td><p>c</p></td></tr><tr><td><p>d</p></td><td><p>e</p></td><td><p>f</p></td></tr></table><p></p>')
    const dividersBeforeDuplicate = () => {
      const duplicate = screen.getByRole('menuitem', { name: 'Duplicate' })
      return duplicate.previousElementSibling?.getAttribute('role') === 'separator'
    }
    let user = await hoverCell('a')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(dividersBeforeDuplicate()).toBe(false)
    await user.keyboard('{Escape}')
    user = await hoverCell('d')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(dividersBeforeDuplicate()).toBe(true)
    await user.keyboard('{Escape}')
    user = await hoverCell('I')
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    expect(dividersBeforeDuplicate()).toBe(false)
    await user.keyboard('{Escape}')
    user = await hoverCell('J')
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    expect(dividersBeforeDuplicate()).toBe(true)
  })

  it('renders the reference glyphs (filled with currentColor) next to every item and a shortcut hint on moves', async () => {
    await setup()
    const user = await hoverCell('Apple')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    const menu = screen.getAllByRole('menuitem')
    for (const item of menu) {
      const svg = item.querySelector('svg')
      expect(svg?.getAttribute('viewBox')).toBe('0 0 16 16')
      expect(svg?.getAttribute('fill')).toBe('currentColor')
      expect(svg?.querySelector('path')?.getAttribute('d')?.length).toBeGreaterThan(60)
    }
    const up = screen.getByRole('menuitem', { name: /Move up/ })
    expect(up.querySelector('svg')?.style.transform).toBe('rotate(180deg)')
    expect(up.querySelectorAll('kbd')).toHaveLength(4)
    expect(screen.getByRole('menuitem', { name: /Insert above/ }).querySelector('svg')?.style.transform).toBe('rotate(180deg)')
    expect(screen.getByRole('menuitem', { name: /Insert below/ }).querySelector('svg')?.style.transform).toBe('')
    expect(screen.getByRole('button', { name: 'Row actions' }).querySelector('svg')?.style.transform).toBe('rotate(90deg)')
  })

  it('shows the column menu: first column has no Move left, the last none Move right; sort is present', async () => {
    await setup()
    let user = await hoverCell('Name')
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    expect(await items()).toEqual(['Insert before', 'Insert after', 'Move right', 'Duplicate', 'Sort ascending', 'Sort descending', 'Format', 'Copy text', 'Delete'])
    await user.keyboard('{Escape}')
    user = await hoverCell('Qty')
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    expect(await items()).toEqual(['Insert before', 'Insert after', 'Move left', 'Duplicate', 'Sort ascending', 'Sort descending', 'Format', 'Copy text', 'Delete'])
  })

  it('the Format submenu lists the four marks and applies one to the row', async () => {
    const editor = await setup()
    const user = await hoverCell('Apple')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    // jsdom has no layout, so Radix's pointer grace area closes the submenu under the mouse: open it from the keyboard.
    act(() => { screen.getByRole('menuitem', { name: /Format/ }).focus() })
    await user.keyboard('{ArrowRight}')
    await waitFor(() => expect(screen.getAllByRole('menuitem')).toHaveLength(12))
    const subItems = screen.getAllByRole('menuitem').map(item => item.textContent)
    expect(subItems.slice(-4).map(text => text?.replace(/[⌘⇧]|Ctrl|Shift|[A-Z]$/g, '').trim())).toEqual(['Bold', 'Italic', 'Underline', 'Strikethrough'])
    await user.keyboard('{Enter}')
    expect(JSON.stringify(tableJson(editor).content![2]).match(/"bold"/g)).toHaveLength(2)
  })

  it('Duplicate and Delete from the column menu act on that column', async () => {
    const editor = await setup()
    const user = await hoverCell('Qty')
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Duplicate' }))
    expect(grid(editor)[0]).toEqual(['Name', 'Qty', 'Qty'])
    await user.hover(screen.getAllByText('Qty', { selector: 'p' })[0])
    await user.click(await screen.findByRole('button', { name: 'Column actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    expect(grid(editor)[0]).toEqual(['Name', 'Qty'])
  })
})

describe('"|--" input rule', () => {
  it('turns "|--" typed in an empty paragraph into the default 3 x 2 table with a header row (undoable)', async () => {
    const editor = await setup('<p>Intro</p><p></p>')
    act(() => { editor.commands.focus('end') })
    typeText(editor, '|-')
    expect(tableCount(editor)).toBe(0)
    typeText(editor, '-')
    expect(grid(editor)).toHaveLength(3)
    expect(grid(editor)[0]).toHaveLength(2)
    expect(cellTypes(editor)).toEqual(['HH', 'DD', 'DD'])
    expect(textOf({ content: editor.getJSON().content as Json[] })).toBe('Intro')
    act(() => { editor.commands.undo() })
    expect(tableCount(editor)).toBe(0)
  })

  it('does not fire after other text, in the middle of a line, or inside a code block', async () => {
    const editor = await setup('<p>a</p>')
    act(() => { editor.commands.focus('end') })
    typeText(editor, '|--')
    expect(tableCount(editor)).toBe(0)
    expect(editor.getText()).toBe('a|--')
    act(() => { editor.commands.setContent('<pre><code>|-</code></pre>') })
    act(() => { editor.commands.focus('end') })
    typeText(editor, '-')
    expect(tableCount(editor)).toBe(0)
  })

  it('does not fire when text follows the caret', async () => {
    const editor = await setup('<p>|-x</p>')
    act(() => { editor.commands.setTextSelection(3) })
    typeText(editor, '-')
    expect(tableCount(editor)).toBe(0)
  })
})

describe('spreadsheet paste', () => {
  it('turns a multi-line tab separated paste into a table with a header row', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    const event = paste(editor, { text: 'x\ty\n1\t2' })
    expect(event.defaultPrevented).toBe(true)
    expect(grid(editor)).toEqual([['x', 'y'], ['1', '2']])
    expect(cellTypes(editor)).toEqual(['HH', 'DD'])
  })

  it('turns an html table (Excel / Sheets: plain td cells + a tsv fallback) into a table whose first row is the header', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    paste(editor, { html: '<meta charset="utf-8"><google-sheets-html-origin><table><tbody><tr><td>p</td><td>q</td></tr><tr><td>5</td><td>6</td></tr></tbody></table>', text: 'p\tq\n5\t6' })
    expect(grid(editor)).toEqual([['p', 'q'], ['5', '6']])
    expect(cellTypes(editor)).toEqual(['HH', 'DD'])
  })

  it('keeps an html table that already has header cells, and a copied header-less row stays header-less', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    paste(editor, { html: '<table><tr><th>A</th></tr><tr><td>1</td></tr></table>' })
    expect(cellTypes(editor)).toEqual(['H', 'D'])
    const second = await setup('<p></p>')
    act(() => { second.commands.focus('end') })
    paste(second, { html: '<table data-headerless=""><tbody><tr><td>banana</td><td>10</td></tr></tbody></table>', text: 'banana\n\n10' })
    expect(cellTypes(second)).toEqual(['DD'])
  })

  it('leaves single-line tab text, tab-less lines and Markdown alone', async () => {
    const editor = await setup('<p></p>')
    act(() => { editor.commands.focus('end') })
    paste(editor, { text: 'a\tb' })
    expect(tableCount(editor)).toBe(0)
    expect(editor.getText()).toContain('a')
    paste(editor, { text: 'one\ntwo' })
    expect(tableCount(editor)).toBe(0)
    paste(editor, { text: '| a | b |\n|---|---|\n| 1 | 2 |' })
    expect(tableCount(editor)).toBe(1)
  })

  it('does not interfere with pasting into an existing table cell', async () => {
    const editor = await setup()
    act(() => { editor.commands.setTextSelection(editor.state.doc.resolve(tablePos(editor) + 3).pos) })
    const before = grid(editor)
    paste(editor, { text: 'x\ty\n1\t2' })
    expect(tableCount(editor)).toBe(1)
    expect(grid(editor)).toHaveLength(before.length)
    expect(editor.getText()).toContain('x')
  })

  it('parses tab separated rows, pads short rows and strips quoting', () => {
    expect(parseTabularText('a\tb\tc\n1\t2\n')).toEqual([['a', 'b', 'c'], ['1', '2', '']])
    expect(parseTabularText('"q ""x"""\tb\n1\t2')).toEqual([['q "x"', 'b'], ['1', '2']])
    expect(parseTabularText('a\tb')).toBeNull()
    expect(parseTabularText('a\tb\nno tab')).toBeNull()
    expect(parseTabularText('a\r\nb')).toBeNull()
  })

  it('builds tables and promotes a first row of td cells to th', async () => {
    const editor = await setup('<p></p>')
    const table = tableFromRows(editor.schema, [['a', 'b'], ['', 'd']])
    expect(table.child(0).child(0).type.name).toBe('tableHeader')
    expect(table.child(1).child(0).type.name).toBe('tableCell')
    expect(table.child(1).child(0).textContent).toBe('')
    const plain = editor.schema.nodes.table.create(null, [editor.schema.nodes.tableRow.create(null, [editor.schema.nodes.tableCell.create(null, editor.schema.nodes.paragraph.create())])])
    const slice = withHeaderRow(new Slice(Fragment.from(plain), 0, 0))
    expect(slice.content.firstChild!.child(0).child(0).type.name).toBe('tableHeader')
  })
})
