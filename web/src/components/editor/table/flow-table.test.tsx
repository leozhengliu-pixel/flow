import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { columnResizingPluginKey } from '@tiptap/pm/tables'
import { EditorContent, useEditor } from '@tiptap/react'
import type { Editor } from '@tiptap/core'
import { useEffect } from 'react'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { flowTableKit, insertDefaultTable, tableSlashCommands } from '.'

let current: Editor | null = null

function Harness({ content = '<p>Intro</p>', editable = true }: { content?: string; editable?: boolean }) {
  const editor = useEditor({ extensions: [StarterKit, Markdown, ...flowTableKit], content, editable })
  useEffect(() => { current = editor }, [editor])
  return editor ? <EditorContent editor={editor}/> : null
}

async function setup(props: { content?: string; editable?: boolean } = {}) {
  render(<I18nProvider><Harness {...props}/></I18nProvider>)
  await waitFor(() => expect(current).not.toBeNull())
  await waitFor(() => expect(document.querySelector('.ProseMirror')).not.toBeNull())
  return current!
}

type Json = { type?: string; attrs?: Record<string, unknown>; content?: Json[] }
const tableJson = (editor: Editor) => (editor.getJSON().content as Json[] | undefined)?.find(node => node.type === 'table')
const rowCount = (editor: Editor) => tableJson(editor)?.content?.length ?? 0
const colCount = (editor: Editor) => tableJson(editor)?.content?.[0]?.content?.length ?? 0

beforeAll(() => {
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
})
afterEach(() => { current?.destroy(); current = null })

async function withTable(props: { content?: string } = {}) {
  const editor = await setup(props)
  act(() => { insertDefaultTable(editor) })
  await waitFor(() => expect(document.querySelector('.flow-table table')).not.toBeNull())
  // Tiptap's chain.focus() lands a frame later; let it settle so it cannot close a menu opened straight away.
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 50)) })
  return editor
}

describe('default table', () => {
  it('inserts 3 rows x 2 columns with a header row', async () => {
    const editor = await withTable()
    const table = tableJson(editor)!
    expect(rowCount(editor)).toBe(3)
    expect(colCount(editor)).toBe(2)
    expect(table.content![0].content!.every(cell => cell.type === 'tableHeader')).toBe(true)
    expect(table.content![1].content!.every(cell => cell.type === 'tableCell')).toBe(true)
    expect(document.querySelectorAll('.flow-table th.flow-table-header')).toHaveLength(2)
    expect(document.querySelectorAll('.flow-table td.flow-table-cell')).toHaveLength(4)
  })

  it('keeps the resizable table DOM (colgroup) inside the Flow wrapper', async () => {
    await withTable()
    const wrapper = document.querySelector('.flow-table')!
    expect(wrapper.querySelector(':scope > .tableWrapper > table > colgroup')).not.toBeNull()
    expect(wrapper.querySelectorAll('colgroup > col')).toHaveLength(2)
  })

  it('keeps column resizing registered and applies stored column widths to the cols', async () => {
    const editor = await withTable()
    expect(columnResizingPluginKey.getState(editor.state)).toBeTruthy()
    const firstCell = tableJson(editor)!.content![0].content![0]
    expect(firstCell.attrs!.colwidth).toBeNull()
    act(() => { editor.commands.setContent('<table><tr><th colwidth="150"><p>A</p></th><th><p>B</p></th></tr><tr><td><p>1</p></td><td><p>2</p></td></tr></table>') })
    await waitFor(() => expect(document.querySelector<HTMLElement>('.flow-table colgroup > col')?.style.width).toBe('150px'))
  })

  it('serialises to a GFM table with a header row and reads one back', async () => {
    const editor = await withTable()
    const markdown = editor.getMarkdown()
    expect(markdown).toMatch(/^\| +\| +\|$/m)
    expect(markdown).toMatch(/\| -+ \| -+ \|/)
    act(() => { editor.commands.setContent('| A | B |\n| --- | --- |\n| 1 | 2 |', { contentType: 'markdown' }) })
    expect(rowCount(editor)).toBe(2)
    expect(tableJson(editor)!.content![0].content![0].type).toBe('tableHeader')
    expect(editor.getMarkdown()).toContain('| A')
  })
})

describe('hover affordances', () => {
  it('Add row appends a row and Add column appends a column', async () => {
    const editor = await withTable()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Add row' }))
    expect(rowCount(editor)).toBe(4)
    await user.click(screen.getByRole('button', { name: 'Add column' }))
    expect(colCount(editor)).toBe(3)
  })

  it('adds at the end even when the caret is in the first row', async () => {
    const editor = await withTable()
    act(() => { editor.commands.setTextSelection(4) })
    await userEvent.click(screen.getByRole('button', { name: 'Add row' }))
    const last = tableJson(editor)!.content!.at(-1)!
    expect(last.content!.every(cell => cell.type === 'tableCell')).toBe(true)
    expect(rowCount(editor)).toBe(4)
  })

  it('renders no chrome when the editor is read-only', async () => {
    await setup({ content: '<table><tr><th><p>A</p></th></tr><tr><td><p>1</p></td></tr></table>', editable: false })
    expect(document.querySelector('.flow-table table')).not.toBeNull()
    expect(screen.queryByRole('button', { name: 'Add row' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Table actions' })).toBeNull()
  })
})

describe('Table actions menu', () => {
  async function choose(item: string) {
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Table actions' }))
    await user.click(await screen.findByRole('menuitem', { name: item }))
  }

  it('lists every action', async () => {
    await withTable()
    await userEvent.click(screen.getByRole('button', { name: 'Table actions' }))
    const names = (await screen.findAllByRole('menuitem')).map(item => item.textContent)
    expect(names).toEqual(['Insert row above', 'Insert row below', 'Insert column left', 'Insert column right', 'Toggle header row', 'Toggle header column', 'Delete row', 'Delete column', 'Delete table'])
  })

  it('inserts rows and columns around the caret', async () => {
    const editor = await withTable()
    await choose('Insert row below')
    expect(rowCount(editor)).toBe(4)
    await choose('Insert row above')
    expect(rowCount(editor)).toBe(5)
    await choose('Insert column right')
    expect(colCount(editor)).toBe(3)
    await choose('Insert column left')
    expect(colCount(editor)).toBe(4)
  })

  it('deletes rows and columns, toggles header row / column', async () => {
    const editor = await withTable()
    let lastCell = 0
    editor.state.doc.descendants((node, pos) => { if (node.type.name === 'tableCell') lastCell = pos })
    act(() => { editor.commands.setTextSelection(lastCell + 2) })
    await choose('Delete row')
    expect(rowCount(editor)).toBe(2)
    await choose('Delete column')
    expect(colCount(editor)).toBe(1)
    await choose('Toggle header row')
    expect(tableJson(editor)!.content![0].content![0].type).toBe('tableCell')
    await choose('Toggle header row')
    expect(tableJson(editor)!.content![0].content![0].type).toBe('tableHeader')
    await choose('Toggle header column')
    expect(tableJson(editor)!.content!.every(row => row.content![0].type === 'tableHeader')).toBe(true)
  })

  it('deletes the table', async () => {
    const editor = await withTable()
    await choose('Delete table')
    expect(tableJson(editor)).toBeUndefined()
    await waitFor(() => expect(document.querySelector('.flow-table')).toBeNull())
  })
})

describe('left block menu', () => {
  async function choose(item: string) {
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Table menu' }))
    await user.click(await screen.findByRole('menuitem', { name: item }))
  }

  it('Copy writes the table as markdown to the clipboard', async () => {
    const editor = await withTable()
    act(() => { editor.commands.setContent('| A | B |\n| --- | --- |\n| 1 | 2 |', { contentType: 'markdown' }) })
    await waitFor(() => expect(document.querySelector('.flow-table td')).not.toBeNull())
    await choose('Copy')
    // user-event installs its own clipboard stub on setup(), so read back what Copy wrote.
    const copied = await navigator.clipboard.readText()
    expect(copied).toMatch(/\| A +\| B +\|\n\| -+ \| -+ \|\n\| 1 +\| 2 +\|/)
  })

  it('Select selects every cell and Delete removes the table', async () => {
    const editor = await withTable()
    await choose('Select')
    const { selection } = editor.state
    expect(selection.constructor.name).toBe('CellSelection')
    expect((selection as unknown as { ranges: unknown[] }).ranges).toHaveLength(6)
    await choose('Delete')
    expect(tableJson(editor)).toBeUndefined()
  })
})

describe('tableSlashCommands', () => {
  it('is empty outside a table', async () => {
    const editor = await setup()
    expect(tableSlashCommands(editor)).toEqual([])
  })

  it('offers the five table items inside a table and each works', async () => {
    const editor = await withTable()
    const labels = () => tableSlashCommands(editor).map(item => item.label)
    expect(labels()).toEqual(['Insert row', 'Insert column', 'Delete row', 'Delete column', 'Delete table'])
    const run = (label: string) => act(() => { tableSlashCommands(editor).find(item => item.label === label)!.run() })
    run('Insert row'); expect(rowCount(editor)).toBe(4)
    run('Insert column'); expect(colCount(editor)).toBe(3)
    run('Delete row'); expect(rowCount(editor)).toBe(3)
    run('Delete column'); expect(colCount(editor)).toBe(2)
    run('Delete table'); expect(tableJson(editor)).toBeUndefined()
    expect(tableSlashCommands(editor)).toEqual([])
  })

  it('returns [] when the editor is read-only', async () => {
    const editor = await withTable()
    editor.setEditable(false)
    expect(tableSlashCommands(editor)).toEqual([])
  })
})
