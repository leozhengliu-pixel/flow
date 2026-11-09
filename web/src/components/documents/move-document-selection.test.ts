import { afterEach, describe, expect, it, vi } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import Collaboration from '@tiptap/extension-collaboration'
import { Doc } from 'yjs'
import { moveDocumentSelection } from './move-document-selection'

let editor: Editor
afterEach(() => editor?.destroy())
const setup = (content: string) => {
  editor = new Editor({ extensions: [StarterKit.configure({ trailingNode: false }), Markdown], content })
  return editor.getJSON()
}
function select(text: string, occurrence = 0) {
  const ranges: { from: number; to: number; text: string }[] = []
  editor.state.doc.descendants((node, pos) => {
    if (node.isText && node.text === text) ranges.push({ from: pos, to: pos + text.length, text })
  })
  const selection = ranges[occurrence]
  editor.commands.setTextSelection(selection)
  return selection
}
function pendingMove(selection: ReturnType<typeof select>) {
  let release!: () => void
  const create = vi.fn(() => new Promise<string>(resolve => { release = () => resolve('created') }))
  return { promise: moveDocumentSelection(editor, selection, create), release, create }
}

describe('moving the original document selection', () => {
  it('creates the new document from rich blocks and link targets before awaiting the request', async () => {
    setup('<blockquote><ul><li><p><strong>Bold</strong> and <a href="https://example.com">link</a></p><ul><li><p>Nested item</p></li></ul></li></ul></blockquote><p>Outside</p>')
    const original = editor.state.doc
    const from = 4, to = original.child(0).nodeSize - 3
    const contentData = original.cut(from, to).toJSON()
    const create = vi.fn(async (content: { content: string; contentData: Record<string, unknown> }) => content)
    const result = await moveDocumentSelection(editor, { from, to, text: original.textBetween(from, to) }, create)
    expect(create).toHaveBeenCalledWith({ content: expect.stringContaining('**Bold**'), contentData })
    expect(result.created.content).toContain('[link](https://example.com)')
    expect(result.created.content).toContain('Nested item')
    expect(result.created.content).not.toContain('Outside')
    expect(JSON.stringify(result.created.contentData)).toContain('blockquote')
    expect(JSON.stringify(result.created.contentData)).toContain('bulletList')
    expect(JSON.stringify(result.created.contentData)).toContain('https://example.com')
    editor.commands.undo()
    expect(editor.state.doc.eq(original)).toBe(true)
  })

  it('does not delete a newly selected paragraph while creation is pending, and supports undo', async () => {
    const original = setup('<p>Move me</p><p>Keep me</p>')
    const pending = pendingMove(select('Move me'))
    select('Keep me')
    pending.release()
    expect(await pending.promise).toEqual({ created: 'created', removed: true })
    expect(editor.getText()).not.toContain('Move me')
    expect(editor.getText()).toContain('Keep me')
    expect(editor.commands.undo()).toBe(true)
    expect(editor.getJSON()).toEqual(original)
    expect(editor.commands.redo()).toBe(true)
    expect(editor.getText()).toContain('Keep me')
  })

  it('removes the selected duplicate in nested blocks, not a matching paragraph elsewhere', async () => {
    const original = setup('<p>Duplicate</p><blockquote><ul><li><p>Duplicate</p></li><li><p>Nested sibling</p></li></ul></blockquote><p>Unrelated</p>')
    const pending = pendingMove(select('Duplicate', 1))
    select('Unrelated')
    pending.release()
    await pending.promise
    expect(editor.state.doc.firstChild?.textContent).toBe('Duplicate')
    expect(editor.state.doc.child(1).textContent).toBe('Nested sibling')
    expect(editor.state.doc.lastChild?.textContent).toBe('Unrelated')
    editor.commands.undo()
    expect(editor.getJSON()).toEqual(original)
  })

  it('maps edits before the range and keeps undo separate from those edits', async () => {
    setup('<p>Before</p><p>Move me</p><p>Keep me</p>')
    const pending = pendingMove(select('Move me'))
    editor.commands.insertContentAt(1, 'New ')
    const edited = editor.getJSON()
    pending.release()
    expect((await pending.promise).removed).toBe(true)
    expect(editor.getText()).toContain('New Before')
    editor.commands.undo()
    expect(editor.getJSON()).toEqual(edited)
  })

  it('keeps source content if it was edited or replaced, even with identical duplicate text', async () => {
    setup('<p>Duplicate</p><p>Duplicate</p>')
    const selected = select('Duplicate')
    const pending = pendingMove(selected)
    editor.commands.insertContentAt({ from: selected.from, to: selected.to }, 'Duplicate')
    const edited = editor.getJSON()
    pending.release()
    expect((await pending.promise).removed).toBe(false)
    expect(editor.getJSON()).toEqual(edited)
  })

  it('keeps source content when creation fails or the editor is destroyed', async () => {
    const original = setup('<p>Move me</p>')
    await expect(moveDocumentSelection(editor, select('Move me'), () => Promise.reject(new Error('offline')))).rejects.toThrow('offline')
    expect(editor.getJSON()).toEqual(original)
    const pending = pendingMove(select('Move me'))
    editor.destroy()
    pending.release()
    expect((await pending.promise).removed).toBe(false)
  })

  it('keeps collaborative undo separate from preceding edits', async () => {
    const document = new Doc()
    editor = new Editor({ extensions: [StarterKit.configure({ undoRedo: false, trailingNode: false }), Markdown, Collaboration.configure({ document })] })
    editor.commands.setContent('<p>Before</p><p>Move me</p><p>Keep me</p>')
    const pending = pendingMove(select('Move me'))
    editor.commands.insertContentAt(1, 'New ')
    const edited = editor.getJSON()
    select('Keep me')
    pending.release()
    expect((await pending.promise).removed).toBe(true)
    expect(editor.getText()).not.toContain('Move me')
    expect(editor.commands.undo()).toBe(true)
    expect(editor.getJSON()).toEqual(edited)
    editor.destroy()
    document.destroy()
  })
})
