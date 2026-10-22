import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { Fragment, Slice } from '@tiptap/pm/model'
import { CellSelection } from '@tiptap/pm/tables'
import { EditorContent, useEditor } from '@tiptap/react'
import type { Editor } from '@tiptap/core'
import { useEffect } from 'react'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { MarkdownPaste } from '@/components/editor/markdown-paste'
import { flowTableKit } from '.'
import {
  compareCellText, deleteLine, duplicateLine, formatLine, insertLine, lineClipboard, moveLine, moveSelectedLine, selectLine, sortColumn,
  type TableLine,
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

  it('duplicates the header row as ordinary cells', async () => {
    const editor = await setup()
    act(() => { duplicateLine(editor, line(editor, 'row', 0)) })
    expect(cellTypes(editor)).toEqual(['HH', 'DD', 'DD', 'DD', 'DD'])
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

  it('selects a whole row / column', async () => {
    const editor = await setup()
    act(() => { selectLine(editor, line(editor, 'row', 2)) })
    expect((editor.state.selection as CellSelection).isRowSelection()).toBe(true)
    act(() => { selectLine(editor, line(editor, 'column', 1)) })
    expect((editor.state.selection as CellSelection).isColSelection()).toBe(true)
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

describe('row and column menus', () => {
  async function hoverCell(text: string) {
    const user = userEvent.setup()
    await user.hover(screen.getByText(text, { selector: 'p' }))
    return user
  }
  const items = async () => (await screen.findAllByRole('menuitem')).map(item => item.textContent?.replace(/[⌘⌥⇧↑↓←→]|Ctrl|Alt|Shift/g, '').trim())

  it('shows the row menu for the hovered row; the first body row has no Move up', async () => {
    const editor = await setup()
    const user = await hoverCell('banana')
    await user.click(await screen.findByRole('button', { name: 'Row actions' }))
    expect(await items()).toEqual(['Insert above', 'Insert below', 'Move down', 'Duplicate', 'Format', 'Copy text', 'Delete'])
    expect((editor.state.selection as CellSelection).isRowSelection()).toBe(true)
    await user.click(screen.getByRole('menuitem', { name: /Move down/ }))
    expect(grid(editor).map(row => row[0])).toEqual(['Name', 'Apple', 'banana', 'cherry'])
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
