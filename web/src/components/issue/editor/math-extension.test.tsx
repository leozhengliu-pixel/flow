import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Editor as CoreEditor, getSchema } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor, type Editor } from '@tiptap/react'
import { prosemirrorJSONToYDoc, yDocToProsemirrorJSON } from '@tiptap/y-tiptap'
import { applyUpdate, Doc, encodeStateAsUpdate } from 'yjs'
import { useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MathBlock, MathBlockSchema } from './math-extension'
import { MathRender } from './math-render'
import { markdownToDoc } from '@/components/documents/document-markdown-doc'
import { DocumentJsonView } from '@/components/documents/document-json-view'
import { looksLikeMarkdown } from '@/components/editor/markdown-paste/markdown-detect'
import { tryMarkdownToDocJSON } from '@/components/editor/markdown-paste/markdown-to-doc'
import { RichComment } from '@/components/activity/rich-comment'
import { IssueDescriptionEditor } from '../issue-description-editor'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }), useOptionalTranslate: () => (value: string) => value }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'math-test' }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const extensions = () => [StarterKit.configure({ trailingNode: false }), Markdown, MathBlockSchema]
const formula = '\\frac{a_b}{c_d}\n+ \\sum_{i=1}^{n} i'
const markdown = `Literal $x^2$\n\n$$\n${formula}\n$$`

function Harness({ onReady, editable = true }: { onReady: (editor: Editor) => void; editable?: boolean }) {
  const editor = useEditor({ extensions: [StarterKit, Markdown, MathBlock], content: markdown, contentType: 'markdown', editable })
  useEffect(() => { if (editor) onReady(editor) }, [editor, onReady])
  return <><EditorContent editor={editor}/><button type="button">Outside</button></>
}

describe('block-only LaTeX', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('round-trips display source verbatim, leaving inline dollars literal', () => {
    const editor = new CoreEditor({ extensions: extensions(), content: markdown, contentType: 'markdown' })
    const json = editor.getJSON()
    expect(json.content?.[0]).toMatchObject({ type: 'paragraph', content: [{ type: 'text', text: 'Literal $x^2$' }] })
    expect(json.content?.[1]).toEqual({ type: 'mathBlock', content: [{ type: 'text', text: formula }] })
    expect(editor.getMarkdown()).toBe(markdown)
    editor.commands.setContent(editor.getMarkdown(), { contentType: 'markdown' })
    expect(editor.getJSON()).toEqual(json)
    editor.commands.setContent(editor.getHTML())
    expect(editor.getJSON()).toEqual(json)
    expect(looksLikeMarkdown(`$$\n${formula}\n$$`)).toBe(true)
    expect(tryMarkdownToDocJSON(editor, markdown)?.content?.[1]).toEqual(json.content?.[1])
    editor.destroy()
  })

  it('preserves empty blocks, adjacent paragraphs, code fences and unmatched dollars', () => {
    const editor = new CoreEditor({ extensions: extensions(), content: 'Before\n$$\n\n$$\nAfter', contentType: 'markdown' })
    expect(editor.getJSON().content?.map(node => node.type)).toEqual(['paragraph', 'mathBlock', 'paragraph'])
    for (const content of ['$x^2$', '$$\nunclosed', '```tex\n$$\nx\n$$\n```']) {
      editor.commands.setContent(content, { contentType: 'markdown' })
      expect(editor.getJSON().content?.some(node => node.type === 'mathBlock')).toBe(false)
    }
    editor.destroy()
  })

  it('creates a block only on $$ plus Enter, supports newlines and undo, never inline rules', () => {
    const editor = new CoreEditor({ extensions: extensions(), content: '<p>$$</p>' })
    editor.commands.setTextSelection(3)
    expect(editor.commands.keyboardShortcut('Enter')).toBe(true)
    expect(editor.state.doc.firstChild?.type.name).toBe('mathBlock')
    editor.commands.insertContent('x^2')
    editor.commands.keyboardShortcut('Enter')
    editor.commands.insertContent('+ y')
    expect(editor.state.doc.firstChild?.textContent).toBe('x^2\n+ y')
    editor.commands.undo()
    expect(editor.state.doc.firstChild?.textContent).not.toBe('x^2\n+ y')
    editor.commands.setContent('<p>$x^2$</p>')
    editor.commands.setTextSelection(6)
    editor.commands.keyboardShortcut('Enter')
    expect(editor.getJSON().content?.every(node => node.type === 'paragraph')).toBe(true)
    expect(MathBlockSchema.config.addInputRules).toBeUndefined()
    editor.destroy()
  })

  it('retains source in a persisted collaborative snapshot', () => {
    const editor = new CoreEditor({ extensions: extensions(), content: markdown, contentType: 'markdown' })
    const original = prosemirrorJSONToYDoc(getSchema(extensions()), editor.getJSON(), 'prosemirror')
    const restored = new Doc()
    applyUpdate(restored, encodeStateAsUpdate(original))
    expect(yDocToProsemirrorJSON(restored, 'prosemirror')).toEqual(editor.getJSON())
    original.destroy(); restored.destroy(); editor.destroy()
  })

  it('shows source only while editing and returns to a rendered formula on blur', async () => {
    let editor: Editor | undefined
    const { container } = render(<Harness onReady={value => { editor = value }}/>)
    await waitFor(() => expect(container.querySelector('.katex')).not.toBeNull())
    fireEvent.mouseDown(container.querySelector('.math-block-render')!)
    await waitFor(() => expect(container.querySelector('.math-block-container')).toHaveAttribute('data-editing'))
    expect(container.querySelector('.math-block-source code')).toHaveTextContent('\\frac{a_b}{c_d}')
    expect(container.querySelector('.math-block-render')).toBeNull()
    act(() => { editor!.commands.insertContent(' + z') })
    await userEvent.click(screen.getByRole('button', { name: 'Outside' }))
    await waitFor(() => expect(container.querySelector('.math-block-container')).not.toHaveAttribute('data-editing'))
    expect(container.querySelector('.katex')).not.toBeNull()
    expect(editor!.getMarkdown()).toContain(' + z\n$$')
  })

  it('provides copy, source, fullscreen and undoable delete actions', async () => {
    const user = userEvent.setup()
    let editor: Editor | undefined
    const { container } = render(<Harness onReady={value => { editor = value }}/>)
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    await user.click(await screen.findByRole('button', { name: 'Formula actions' }))
    expect(screen.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Copy formula', 'Show source', 'Fullscreen', 'Delete'])
    await user.click(screen.getByRole('menuitem', { name: 'Copy formula' }))
    expect(writeText).toHaveBeenCalledWith(formula)
    await user.click(screen.getByRole('button', { name: 'Formula actions' }))
    await user.click(screen.getByRole('menuitem', { name: 'Fullscreen' }))
    expect(await screen.findByRole('dialog', { name: 'LaTeX math block' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    await user.click(screen.getByRole('button', { name: 'Formula actions' }))
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }))
    expect(container.querySelector('.math-block-container')).toBeNull()
    act(() => { editor!.commands.undo() })
    await waitFor(() => expect(container.querySelector('.math-block-container')).not.toBeNull())
  })

  it('inserts an empty source block through /latex in the shared editor', async () => {
    let editor: Editor | null = null
    const { container } = render(<IssueDescriptionEditor value="" editorRef={value => { editor = value }}/>)
    await waitFor(() => expect(editor).not.toBeNull())
    act(() => { editor!.commands.insertContent('/latex') })
    const option = await screen.findByRole('option', { name: /LaTeX math block/ })
    expect(screen.getAllByRole('option')).toHaveLength(1)
    await userEvent.click(option)
    await waitFor(() => expect(container.querySelector('.math-block-container')).toHaveAttribute('data-editing'))
    expect(editor!.state.selection.$from.parent.type.name).toBe('mathBlock')
  })

  it('renders read-only comments, previews and document history without editable math', async () => {
    const doc = markdownToDoc(markdown)
    expect(doc.content?.[1]).toEqual({ type: 'mathBlock', content: [{ type: 'text', text: formula }] })
    const { container } = render(<><RichComment body={markdown}/><DocumentJsonView doc={doc}/></>)
    await waitFor(() => expect(container.querySelectorAll('.katex')).toHaveLength(2))
    expect(container.querySelector('[data-editing]')).toBeNull()
    await userEvent.click(await screen.findByRole('button', { name: 'Formula actions' }))
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull()
  })

  it('renders invalid input safely without trusting HTML or links', () => {
    const { container } = render(<><MathRender source={'\\href{javascript:alert(1)}{click}'}/><MathRender source={'\\unknown{<img src=x onerror=alert(1)>}'}/><MathRender source={'\\frac{'}/></>)
    expect(container.querySelector('a')).toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('[onerror]')).toBeNull()
    expect(container.querySelector('.katex-error')).not.toBeNull()
  })
})
