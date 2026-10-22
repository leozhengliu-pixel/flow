import { Editor } from '@tiptap/core'
import { Markdown } from '@tiptap/markdown'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { FlowStarterKit } from '@/components/editor/flow-starter-kit'
import { DEFAULT_TABLE } from './table-commands'
import { flowTableKit } from './flow-table'
import { duplicateLine } from './table-lines'
import { defaultColumnWidth, planColumnWidths } from './table-widths'

const editors: Editor[] = []

beforeAll(() => {
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
})
afterEach(() => { while (editors.length) editors.pop()!.destroy() })

function create(content = '<p></p>') {
  const editor = new Editor({ element: document.createElement('div'), content, extensions: [FlowStarterKit.configure({ undoRedo: false }), Markdown, ...flowTableKit] })
  editors.push(editor)
  return editor
}

type Json = { type?: string; attrs?: Record<string, unknown>; content?: Json[] }
function firstTable(editor: Editor) {
  const table = (editor.getJSON().content as Json[]).find(node => node.type === 'table')!
  return table.content!.map(row => row.content!.map(cell => (cell.attrs?.colwidth ?? null) as number[] | null))
}
const widthsOfRow = (editor: Editor, row = 0) => firstTable(editor)[row].map(width => width?.[0] ?? null)
function paste(editor: Editor, html: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/html'], files: [], items: [], getData: (type: string) => (type === 'text/html' ? html : '') } })
  editor.view.dom.dispatchEvent(event)
}
const html = (cols: number) => `<table><tbody><tr>${Array.from({ length: cols }, (_, i) => `<th>h${i}</th>`).join('')}</tr><tr>${Array.from({ length: cols }, (_, i) => `<td>c${i}</td>`).join('')}</tr></tbody></table>`

describe('default column widths', () => {
  it('gives every cell of a new table its column width', () => {
    const editor = create()
    editor.commands.insertTable({ ...DEFAULT_TABLE })
    for (let row = 0; row < 3; row++) expect(widthsOfRow(editor, row)).toEqual([390, 390])
    expect(editor.getHTML().match(/data-colwidth="390"/g)).toHaveLength(6)
  })

  it('splits the default width evenly for pasted tables, never below the minimum column', () => {
    for (const [columns, width] of [[2, 390], [3, 260], [5, 156], [9, 90]] as const) {
      const editor = create()
      paste(editor, html(columns))
      expect(widthsOfRow(editor, 0)).toEqual(Array(columns).fill(width))
      expect(widthsOfRow(editor, 1)).toEqual(Array(columns).fill(width))
      expect(defaultColumnWidth(columns)).toBe(width)
    }
  })

  it('gives the new column 200 when the other columns were resized unevenly', () => {
    const editor = create()
    editor.view.dom.dispatchEvent(Object.assign(new Event('paste', { bubbles: true, cancelable: true }), { clipboardData: { types: ['text/html'], files: [], items: [], getData: (type: string) => (type === 'text/html' ? '<table><tbody><tr><th data-colwidth="300">a</th><th data-colwidth="120">b</th></tr><tr><td data-colwidth="300">c</td><td data-colwidth="120">d</td></tr></tbody></table>' : '') } }))
    editor.commands.addColumnAfter()
    expect(widthsOfRow(editor, 0)).toEqual([300, 120, 200])
  })

  it('keeps the widths a pasted table already carries', () => {
    const editor = create()
    paste(editor, '<table><tbody><tr><th data-colwidth="300">a</th><th data-colwidth="120">b</th></tr><tr><td data-colwidth="300">c</td><td data-colwidth="120">d</td></tr></tbody></table>')
    expect(widthsOfRow(editor, 0)).toEqual([300, 120])
  })

  it('re-splits the default width when a column is inserted and there is room, else the new column is 200 wide', () => {
    const editor = create()
    editor.commands.insertTable({ ...DEFAULT_TABLE })
    editor.commands.addColumnAfter()
    expect(widthsOfRow(editor, 0)).toEqual([260, 260, 260])
    expect(widthsOfRow(editor, 2)).toEqual([260, 260, 260])
    editor.commands.addColumnAfter()
    expect(widthsOfRow(editor, 0)).toEqual([260, 200, 260, 260])
    expect(widthsOfRow(editor, 2)).toEqual([260, 200, 260, 260])
  })

  it('keeps the width of a duplicated column', () => {
    const editor = create()
    editor.commands.insertTable({ ...DEFAULT_TABLE })
    editor.commands.addColumnAfter()
    let tablePos = -1
    editor.state.doc.descendants((node, pos) => { if (node.type.name === 'table') tablePos = pos })
    duplicateLine(editor, { tablePos, axis: 'column', index: 0 })
    expect(widthsOfRow(editor, 0)).toEqual([260, 260, 260, 260])
    expect(widthsOfRow(editor, 1)).toEqual([260, 260, 260, 260])
  })

  it('records a resized column and leaves the others alone', () => {
    const editor = create()
    editor.commands.insertTable({ ...DEFAULT_TABLE })
    const before = firstTable(editor)
    expect(before.flat().every(width => width?.[0] === 390)).toBe(true)
    editor.commands.setCellAttribute('colwidth', [500])
    editor.commands.insertContent('x')
    expect(firstTable(editor).flat().some(width => width === null)).toBe(false)
  })

  it('does not rewrite a stored table that has no widths when it is loaded', () => {
    const editor = create(html(3))
    expect(firstTable(editor).flat().every(width => width === null)).toBe(true)
    expect(editor.getHTML()).not.toContain('data-colwidth')
  })

  it('plans nothing for a table whose columns all have widths', () => {
    const editor = create()
    editor.commands.insertTable({ ...DEFAULT_TABLE })
    const table = editor.state.doc.child(0).type.name === 'table' ? editor.state.doc.child(0) : editor.state.doc.child(1)
    expect(planColumnWidths(table)).toBeNull()
  })
})
