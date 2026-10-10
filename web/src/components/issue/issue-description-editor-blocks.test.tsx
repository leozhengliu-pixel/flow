/// <reference types="node" />
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { Editor as ReactEditor } from '@tiptap/react'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { IssueDescriptionEditor } from './issue-description-editor'
import { detectSlashCommand } from './editor/slash-command-extension'
import { handleBlockShortcut } from './editor/editor-keyboard'
import { appendTrailingParagraph } from './editor/trailing-paragraph'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'editor-test' }))

async function mount(value: string, props: Partial<Parameters<typeof IssueDescriptionEditor>[0]> = {}) {
  let editor: ReactEditor | null = null
  render(<IssueDescriptionEditor value={value} editorRef={current => { editor = current }} {...props}/>)
  await waitFor(() => expect(editor).not.toBeNull())
  return editor as unknown as ReactEditor
}

/** Puts the caret at the end of the first textblock (the trailing paragraph Tiptap appends would otherwise take focus). */
function caretAtEndOfFirstTextblock(editor: ReactEditor) {
  let target = -1
  editor.state.doc.descendants((node, pos) => {
    if (target < 0 && node.isTextblock) target = pos + node.nodeSize - 1
    return target < 0
  })
  editor.commands.focus(target)
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
})

describe('block rhythm and list markers (Linear parity)', () => {
  const css = readFileSync(resolve(process.cwd(), 'src/components/issue/issue-description-editor.css'), 'utf8')
  it('draws bullet, nested and numbered markers instead of the reset list-style: none', () => {
    expect(css).toMatch(/ul:not\(\[data-type="taskList"\]\)\s*\{\s*list-style:\s*disc outside/)
    expect(css).toMatch(/ul:not\(\[data-type="taskList"\]\) ul:not\(\[data-type="taskList"\]\)\s*\{\s*list-style:\s*circle/)
    expect(css).toMatch(/ul:not\(\[data-type="taskList"\]\) ul:not\(\[data-type="taskList"\]\) ul:not\(\[data-type="taskList"\]\)\s*\{\s*list-style:\s*square/)
    expect(css).toMatch(/\.description-editor ol\s*\{\s*list-style:\s*decimal outside/)
  })
  it('keeps a 30px list item pitch (24px line + 6px gap), a light grey divider and a non-overlapping checkbox', () => {
    expect(css).toMatch(/li \+ li\s*\{\s*margin-top:\s*6px/)
    expect(css).toMatch(/hr\s*\{[^}]*border-top:\s*1px solid var\(--theme-border\)/)
    expect(css).toMatch(/ul\[data-type="taskList"\] > li\s*\{[^}]*padding-left:\s*29px/)
    expect(css).toMatch(/input\[type="checkbox"\]\s*\{[^}]*opacity:\s*0/)
  })
  it('renders every list kind from Markdown', async () => {
    await mount('- a\n  - b\n\n1. one\n2. two\n\n- [x] done\n- [ ] todo')
    expect(document.querySelector('ul:not([data-type]) ul')).toBeTruthy()
    expect(document.querySelectorAll('ol li')).toHaveLength(2)
    expect(document.querySelectorAll('ul[data-type="taskList"] li')).toHaveLength(2)
    expect(document.querySelector('ul[data-type="taskList"] li[data-checked="true"]')).toBeTruthy()
  })
})

describe('slash menu', () => {
  it('opens after a space mid-line and inside list items and tables, but not glued to a word', async () => {
    const editor = await mount('- item\n\nplain')
    const slash = (text: string, at: 'list' | 'paragraph') => {
      editor.commands.setContent(at === 'list' ? `<ul><li><p>${text}</p></li></ul>` : `<p>${text}</p>`)
      caretAtEndOfFirstTextblock(editor)
      return detectSlashCommand(editor.state)
    }
    expect(slash('/', 'paragraph')).toMatchObject({ active: true, query: '' })
    expect(slash('hello /he', 'paragraph')).toMatchObject({ active: true, query: 'he' })
    expect(slash('hello /', 'list')).toMatchObject({ active: true, query: '' })
    expect(slash('and/or', 'paragraph').active).toBe(false)
    expect(slash('https://x.test', 'paragraph').active).toBe(false)
    expect(slash('/done now', 'paragraph').active).toBe(false)
  })

  it('lists Linear groups in order with dividers, search-only entries and shortcuts', async () => {
    const editor = await mount('')
    act(() => { editor.chain().focus().insertContent('/').run() })
    const menu = await screen.findByRole('listbox', { name: 'Insert block' })
    const labels = within(menu).getAllByRole('option').map(option => option.querySelector('.description-command-copy')?.textContent)
    expect(labels).toEqual(['Heading 1', 'Heading 2', 'Heading 3', 'Heading 4', 'Bulleted list', 'Numbered list', 'Checklist', 'Insert media…', 'Attach files…', 'Code block', 'Diagram', 'Collapsible section', 'Blockquote', 'Callout'])
    expect(within(menu).getAllByRole('separator')).toHaveLength(3)
    expect(within(menu).queryByText('Text')).not.toBeInTheDocument()
    expect(within(menu).queryByRole('option', { name: /^Table/ })).not.toBeInTheDocument()
    act(() => { editor.chain().focus().insertContent('tab').run() })
    expect(await within(await screen.findByRole('listbox', { name: 'Insert block' })).findByRole('option', { name: /Table/ })).toBeVisible()
    expect(within(screen.getByRole('listbox', { name: 'Insert block' })).queryByRole('option', { name: /Heading/ })).not.toBeInTheDocument()
  })

  it('shows "No results found" with a Dismiss button that closes the menu', async () => {
    const editor = await mount('')
    act(() => { editor.chain().focus().insertContent('/zzzzzz').run() })
    const menu = await screen.findByRole('listbox', { name: 'Insert block' })
    expect(within(menu).getByText('No results found')).toBeVisible()
    fireEvent.click(within(menu).getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(screen.queryByRole('listbox', { name: 'Insert block' })).not.toBeInTheDocument())
  })

  it('adds the table row/column commands first while the caret is in a table', async () => {
    const editor = await mount('| a | b |\n|---|---|\n| 1 | 2 |')
    caretAtEndOfFirstTextblock(editor)
    act(() => { editor.chain().focus().insertContent(' /').run() })
    const menu = await screen.findByRole('listbox', { name: 'Insert block' })
    const labels = within(menu).getAllByRole('option').map(option => option.querySelector('.description-command-copy')?.textContent ?? '')
    expect(labels.slice(0, 5)).toEqual(['Insert row', 'Insert column', 'Delete row', 'Delete column', 'Delete table'])
    expect(labels).toContain('Heading 1')
  })

  it('runs a command: Heading 4 (⌘⌥4) replaces the slash and styles the block', async () => {
    const editor = await mount('')
    act(() => { editor.chain().focus().insertContent('/h4').run() })
    const option = await screen.findByRole('option', { name: /Heading 4/ })
    expect(option).toHaveTextContent('⌘⌥4')
    fireEvent.click(option)
    expect(editor.isActive('heading', { level: 4 })).toBe(true)
    expect(editor.getText().trim()).toBe('')
  })

  it('runs a command: Heading 1 replaces the slash and styles the block', async () => {
    const editor = await mount('')
    act(() => { editor.chain().focus().insertContent('/').run() })
    fireEvent.click(await screen.findByRole('option', { name: /Heading 1/ }))
    expect(editor.isActive('heading', { level: 1 })).toBe(true)
    expect(editor.getText().trim()).toBe('')
  })
})

describe('placeholders', () => {
  it('shows the editor placeholder only while the whole document is empty and a "/" hint on later empty lines', async () => {
    const editor = await mount('', { placeholder: 'Start writing…' })
    expect(document.querySelector('p.is-editor-empty')?.getAttribute('data-placeholder')).toBe('Start writing…')
    editor.commands.setContent('<p>first</p><p></p>')
    editor.commands.focus('end')
    await waitFor(() => expect(document.querySelector('.description-empty-hint kbd')?.textContent).toBe('/'))
    expect(document.querySelector('.description-empty-hint')?.textContent).toContain('Type')
    expect(document.querySelector('.description-empty-hint')?.textContent).toContain('for commands…')
    expect([...document.querySelectorAll('p[data-placeholder]')].every(node => !node.getAttribute('data-placeholder'))).toBe(true)
  })
})

describe('keyboard shortcuts and trailing paragraph', () => {
  const press = (editor: ReactEditor, init: KeyboardEventInit) => handleBlockShortcut(new KeyboardEvent('keydown', { cancelable: true, ...init }), editor)
  it('maps the physical keys of Linear shortcuts', async () => {
    const editor = await mount('text')
    editor.commands.focus('end')
    expect(press(editor, { metaKey: true, shiftKey: true, code: 'Digit8', key: '*' })).toBe(true)
    expect(editor.isActive('bulletList')).toBe(true)
    expect(press(editor, { metaKey: true, shiftKey: true, code: 'Digit9', key: '(' })).toBe(true)
    expect(editor.isActive('orderedList')).toBe(true)
    expect(press(editor, { metaKey: true, shiftKey: true, code: 'Digit7', key: '&' })).toBe(true)
    expect(editor.isActive('taskList')).toBe(true)
    expect(press(editor, { metaKey: true, altKey: true, code: 'Digit0', key: 'º' })).toBe(true)
    expect(editor.isActive('paragraph')).toBe(true)
    expect(press(editor, { metaKey: true, altKey: true, code: 'Digit2', key: '™' })).toBe(true)
    expect(editor.isActive('heading', { level: 2 })).toBe(true)
    expect(press(editor, { metaKey: true, shiftKey: true, code: 'Backslash', key: '|' })).toBe(true)
    expect(editor.isActive('codeBlock')).toBe(true)
    editor.commands.setParagraph()
    expect(press(editor, { altKey: true, shiftKey: true, code: 'Period', key: '˘' })).toBe(true)
    expect(editor.isActive('blockquote')).toBe(true)
    expect(press(editor, { code: 'KeyA', key: 'a' })).toBe(false)
  })

  it('turns a block into Heading 4 with ⌘⌥4 (physical Digit4) and with the "#### " input rule', async () => {
    const editor = await mount('text')
    editor.commands.focus('end')
    expect(press(editor, { metaKey: true, altKey: true, code: 'Digit4', key: '¢' })).toBe(true)
    expect(editor.isActive('heading', { level: 4 })).toBe(true)
    editor.commands.setContent('<p></p>')
    editor.commands.focus('start')
    const view = editor.view
    for (const char of '#### ') {
      const { from, to } = view.state.selection
      const handled = view.someProp('handleTextInput', fn => fn(view, from, to, char, () => view.state.tr.insertText(char, from, to)))
      if (!handled) view.dispatch(view.state.tr.insertText(char, from, to))
    }
    expect(editor.isActive('heading', { level: 4 })).toBe(true)
  })

  it('round-trips Heading 4 through Markdown and styles it like Linear (15px / 600 / 24px)', async () => {
    const editor = await mount('#### Fourth\n\nbody')
    expect(document.querySelector('.description-editor h4')?.textContent).toBe('Fourth')
    expect(editor.getMarkdown()).toContain('#### Fourth')
    const tokens = readFileSync(resolve(process.cwd(), 'src/styles/tokens.css'), 'utf8')
    expect(tokens).toMatch(/\.issue-description-root \.description-editor h4\{margin:22px 0 6px;font-size:15px;line-height:24px;font-weight:600/)
  })

  it('appends one paragraph after a final code block (collaborative editors run without the trailing node)', () => {
    const editor = new Editor({ extensions: [StarterKit.configure({ trailingNode: false }), Markdown], content: '```js\nconst a = 1\n```', contentType: 'markdown' })
    expect(editor.getJSON().content?.at(-1)?.type).toBe('codeBlock')
    editor.commands.focus('end')
    expect(appendTrailingParagraph(editor.view)).toBe(true)
    expect(editor.getJSON().content?.map(node => node.type)).toEqual(['codeBlock', 'paragraph'])
    expect(appendTrailingParagraph(editor.view)).toBe(false)
    editor.destroy()
  })
})

describe('uploads and outline props', () => {
  it('hands the upload function to image/file inserts so no blob: URL is persisted', async () => {
    const upload = vi.fn().mockResolvedValue('https://flow.test/uploads/a.png')
    const editor = await mount('', { onInsertImage: upload })
    await waitFor(() => expect(editor.storage.image.upload).toBe(upload))
  })

  it('renders the heading outline only when asked for and the document has headings', async () => {
    await mount('# One\n\n## Two', { outline: true })
    expect(await screen.findByRole('navigation', { name: 'Document outline' })).toBeInTheDocument()
  })

  it('lists Heading 4 in the outline', async () => {
    await mount('# One\n\n#### Four', { outline: true })
    const outline = await screen.findByRole('navigation', { name: 'Document outline' })
    expect(outline.querySelector('.doc-outline__row--l4')?.textContent).toContain('Four')
  })
})

describe('file paste and drop', () => {
  const paste = (editor: ReactEditor, files: File[], types: Record<string, string> = {}) => {
    const event = { clipboardData: { files, getData: (type: string) => types[type] ?? '' }, preventDefault: vi.fn() } as unknown as ClipboardEvent
    return { handled: editor.view.someProp('handlePaste', handler => handler(editor.view, event, null as never)), event }
  }
  it('turns a pasted Markdown file into document content instead of a file card', async () => {
    const editor = await mount('')
    const { handled, event } = paste(editor, [new File(['## Imported\n\n- one\n- two'], 'notes.md', { type: 'text/markdown' })])
    expect(handled).toBe(true)
    expect(event.preventDefault).toHaveBeenCalled()
    await waitFor(() => expect(editor.getJSON().content?.map(node => node.type)).toContain('bulletList'))
    expect(editor.getJSON().content?.some(node => node.type === 'file')).toBe(false)
    expect(editor.getText()).toContain('Imported')
  })

  it('uploads a pasted image through the provided uploader and never keeps the blob URL', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:flow-test')
    URL.revokeObjectURL = vi.fn()
    const upload = vi.fn().mockResolvedValue('/uploads/shot.png')
    const editor = await mount('', { onInsertImage: upload })
    await waitFor(() => expect(editor.storage.image.upload).toBe(upload))
    const file = new File([new Uint8Array([137, 80, 78, 71])], 'shot.png', { type: 'image/png' })
    expect(paste(editor, [file]).handled).toBe(true)
    await waitFor(() => expect(upload).toHaveBeenCalledWith(file))
    await waitFor(() => expect(JSON.stringify(editor.getJSON())).toContain('/uploads/shot.png'))
    expect(JSON.stringify(editor.getJSON())).not.toContain('blob:')
  })
})
