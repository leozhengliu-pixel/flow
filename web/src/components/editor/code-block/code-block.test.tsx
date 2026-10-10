import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import Document from '@tiptap/extension-document'
import Paragraph from '@tiptap/extension-paragraph'
import Text from '@tiptap/extension-text'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor, type Editor } from '@tiptap/react'
import { useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FlowCodeBlock, FlowCodeBlockSchema } from './code-block-extension'
import { highlightCode, tokenRanges } from './code-block-highlight'
import { resetCodeBlockSettingsCache } from './code-block-settings'
import { CODE_LANGUAGES, detectLanguage, languageLabel, resolveLanguage } from './languages'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

function Harness({ content, editable = true, onReady }: { content: string; editable?: boolean; onReady: (editor: Editor) => void }) {
  const editor = useEditor({ extensions: [Document, Paragraph, Text, Markdown, FlowCodeBlock], content, contentType: 'markdown', editable })
  useEffect(() => { if (editor) onReady(editor) }, [editor, onReady])
  return <EditorContent editor={editor}/>
}

async function mount(content: string, editable = true) {
  let editor: Editor | null = null
  const view = render(<Harness content={content} editable={editable} onReady={next => { editor = next }}/>)
  await waitFor(() => expect(editor).not.toBeNull())
  await screen.findByRole('button', { name: 'Copy code' })
  return { ...view, editor: editor as unknown as Editor }
}

const lines = (count: number) => Array.from({ length: count }, (_, index) => `line ${index + 1}`).join('\n')

describe('code block node view', () => {
  beforeEach(() => {
    window.localStorage.clear()
    resetCodeBlockSettingsCache()
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  })
  afterEach(() => { vi.unstubAllGlobals(); window.localStorage.clear(); resetCodeBlockSettingsCache() })

  it('labels an unset block with the detected language or Plaintext, and a set block with its language', async () => {
    const auto = await mount('```\nhello\n```')
    expect(screen.getByRole('button', { name: 'Code language' })).toHaveTextContent('Plaintext')
    auto.unmount()
    const detected = await mount('```\nimport os\nimport sys\n\nclass A:\n    def f(self):\n        pass\n```')
    expect(screen.getByRole('button', { name: 'Code language' })).toHaveTextContent('Python')
    detected.unmount()
    await mount('```js\nconst a = 1\n```')
    expect(screen.getByRole('button', { name: 'Code language' })).toHaveTextContent('JavaScript')
  })

  it('lists auto detect first and sets the language attribute when one is picked (null for auto)', async () => {
    const user = userEvent.setup()
    const { editor } = await mount('```\nprint(1)\n```')
    await user.click(screen.getByRole('button', { name: 'Code language' }))
    const options = await screen.findAllByRole('menuitemradio')
    expect(options[0]).toHaveTextContent('Auto detect')
    expect(options.length).toBe(CODE_LANGUAGES.length + 1)
    expect(options[0]).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('separator')).toBeInTheDocument()
    const labels = options.slice(1).map(option => option.textContent)
    expect(labels).toEqual([...labels].sort((a, b) => (a! < b! ? -1 : a! > b! ? 1 : 0)))
    await user.type(screen.getByPlaceholderText('Search languages…'), 'pyth')
    expect(screen.getAllByRole('menuitemradio')).toHaveLength(1)
    await user.click(screen.getByRole('menuitemradio', { name: /Python/ }))
    expect(editor.getJSON().content?.[0].attrs?.language).toBe('python')
    expect(editor.getMarkdown()).toContain('```python')
    expect(screen.getByRole('button', { name: 'Code language' })).toHaveTextContent('Python')
    await user.click(screen.getByRole('button', { name: 'Code language' }))
    await user.click(await screen.findByRole('menuitemradio', { name: /Auto detect/ }))
    expect(editor.getJSON().content?.[0].attrs?.language).toBeNull()
  })

  it('highlights tokens with hljs classes while editing', async () => {
    const { container, editor } = await mount('```js\nconst a = "x" // note\n```')
    await waitFor(() => expect(container.querySelector('.hljs-keyword')).toHaveTextContent('const'))
    expect(container.querySelector('.hljs-string')).toHaveTextContent('"x"')
    expect(container.querySelector('.hljs-comment')).toHaveTextContent('// note')
    act(() => { editor.commands.insertContentAt(editor.state.doc.content.size - 1, '\nlet b') })
    await waitFor(() => expect(container.querySelectorAll('.hljs-keyword').length).toBe(2))
    expect(container.querySelector('code')?.textContent).toBe('const a = "x" // note\nlet b')
  })

  it('has no header row and floats the controls at the top right', async () => {
    const { container } = await mount('```js\na\n```')
    expect(container.querySelector('.flow-code-block__header')).toBeNull()
    expect(container.querySelector('.flow-code-block > .flow-code-block__controls')).not.toBeNull()
  })

  it('auto-detects the language when none is set', async () => {
    const { container } = await mount('```\nimport os\nimport sys\n\nclass A:\n    def f(self):\n        pass\n```')
    await waitFor(() => expect(container.querySelector('.hljs-keyword')).not.toBeNull())
  })

  it('toggles line numbers and remembers the choice', async () => {
    const user = userEvent.setup()
    const { container } = await mount('```js\na\nb\nc\n```')
    const gutter = container.querySelector('.flow-code-block__gutter')
    expect(gutter?.textContent).toBe('123')
    await user.click(screen.getByRole('button', { name: 'Code block settings' }))
    await user.click(await screen.findByRole('checkbox', { name: 'Show line numbers' }))
    expect(container.querySelector('.flow-code-block__gutter')).toBeNull()
    expect(JSON.parse(window.localStorage.getItem('flow.editor.code-block') ?? '{}').lineNumbers).toBe(false)
  })

  it('toggles line wrapping', async () => {
    const user = userEvent.setup()
    const { container } = await mount('```js\nlong\n```')
    const block = container.querySelector('.flow-code-block') as HTMLElement
    expect(block).not.toHaveAttribute('data-wrap')
    // Long lines scroll sideways by default: the node view's inline `pre-wrap` must not win.
    expect((container.querySelector('.flow-code-block__code') as HTMLElement).style.whiteSpace).toBe('pre')
    await user.click(screen.getByRole('button', { name: 'Code block settings' }))
    await user.click(await screen.findByRole('checkbox', { name: 'Wrap lines' }))
    expect(block).toHaveAttribute('data-wrap')
    expect((container.querySelector('.flow-code-block__code') as HTMLElement).style.whiteSpace).toBe('pre-wrap')
    expect(container.querySelector('.flow-code-block__gutter')).toBeNull()
  })

  it('never throws when localStorage is unavailable', async () => {
    const user = userEvent.setup()
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    const { container } = await mount('```js\na\n```')
    expect(container.querySelector('.flow-code-block__gutter')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: 'Code block settings' }))
    await user.click(await screen.findByRole('checkbox', { name: 'Wrap lines' }))
    expect(container.querySelector('.flow-code-block')).toHaveAttribute('data-wrap')
    vi.restoreAllMocks()
  })

  it('copies the code to the clipboard', async () => {
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    await mount('```js\nconst a = 1\nlet b\n```')
    await user.click(screen.getByRole('button', { name: 'Copy code' }))
    expect(writeText).toHaveBeenCalledWith('const a = 1\nlet b')
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
  })

  it('collapses blocks over 30 lines to 15 and expands on request', async () => {
    const user = userEvent.setup()
    const { container } = await mount(`Intro\n\n\`\`\`js\n${lines(40)}\n\`\`\``)
    const block = container.querySelector('.flow-code-block') as HTMLElement
    expect(block).toHaveAttribute('data-collapsed')
    expect(block.querySelector('.flow-code-block__body')?.getAttribute('style')).toContain('--flow-code-lines: 15')
    await user.click(screen.getByRole('button', { name: 'Show all 40 lines' }))
    expect(block).not.toHaveAttribute('data-collapsed')
    await user.click(screen.getByRole('button', { name: 'Collapse' }))
    expect(block).toHaveAttribute('data-collapsed')
  })

  it('does not collapse a block of 30 lines or fewer', async () => {
    const { container } = await mount(`\`\`\`js\n${lines(30)}\n\`\`\``)
    expect(container.querySelector('.flow-code-block')).not.toHaveAttribute('data-collapsed')
    expect(screen.queryByRole('button', { name: /Show all/ })).toBeNull()
  })

  it('expands while the caret is inside a long block', async () => {
    const { container, editor } = await mount(`Intro\n\n\`\`\`js\n${lines(40)}\n\`\`\``)
    const block = container.querySelector('.flow-code-block') as HTMLElement
    expect(block).toHaveAttribute('data-collapsed')
    act(() => { editor.commands.focus(); editor.commands.setTextSelection(editor.state.doc.content.size - 3) })
    await waitFor(() => expect(block).not.toHaveAttribute('data-collapsed'))
  })

  it('keeps the language picker read-only when the editor is not editable', async () => {
    await mount('```js\na\n```', false)
    expect(screen.queryByRole('button', { name: 'Code language' })).toBeNull()
    expect(screen.getByText('JavaScript')).toBeInTheDocument()
    expect(within(document.body).getByRole('button', { name: 'Copy code' })).toBeInTheDocument()
  })
})

describe('code block schema and input rule', () => {
  it('round-trips a fenced block with its language through markdown', async () => {
    const { Editor: CoreEditor } = await import('@tiptap/core')
    const editor = new CoreEditor({ extensions: [Document, Paragraph, Text, Markdown, FlowCodeBlockSchema], content: 'Hi\n\n```js\nconst a = 1\n```\n\n```\nplain\n```', contentType: 'markdown' })
    const json = editor.getJSON().content ?? []
    expect(json[1]).toMatchObject({ type: 'codeBlock', attrs: { language: 'js' }, content: [{ type: 'text', text: 'const a = 1' }] })
    expect(json[2].attrs?.language).toBeNull()
    expect(editor.getMarkdown()).toBe('Hi\n\n```js\nconst a = 1\n```\n\n```\nplain\n```')
    editor.destroy()
  })

  it('turns "```js " in a paragraph into a js code block', async () => {
    const { Editor: CoreEditor } = await import('@tiptap/core')
    const editor = new CoreEditor({ extensions: [Document, Paragraph, Text, FlowCodeBlock], content: '<p>```js</p>' })
    editor.commands.setTextSelection(editor.state.doc.content.size - 1)
    const { from, to } = editor.state.selection
    const handled = editor.view.someProp('handleTextInput', handler => handler(editor.view, from, to, ' ', () => editor.state.tr))
    expect(handled).toBe(true)
    expect(editor.getJSON().content?.[0]).toMatchObject({ type: 'codeBlock', attrs: { language: 'js' } })
    editor.destroy()
  })
})

describe('language helpers', () => {
  it('resolves aliases to picker ids and labels them', () => {
    expect(resolveLanguage('js')).toBe('javascript')
    expect(resolveLanguage('language-ts')).toBe('typescript')
    expect(resolveLanguage('html')).toBe('xml')
    expect(resolveLanguage('sh')).toBe('bash')
    expect(resolveLanguage('Python')).toBe('python')
    expect(resolveLanguage('nope')).toBeUndefined()
    expect(resolveLanguage(null)).toBeUndefined()
    expect(languageLabel('cpp')).toBe('C++')
    expect(languageLabel('unknownlang')).toBe('unknownlang')
    expect(CODE_LANGUAGES.every(language => resolveLanguage(language.id) === language.id)).toBe(true)
  })

  it('detects the language of a snippet and gives up on empty or huge input', () => {
    expect(detectLanguage('import os\nimport sys\n\nclass A:\n    def f(self):\n        pass')).toBe('python')
    expect(detectLanguage('   ')).toBeNull()
    expect(detectLanguage('def f(a): pass')).not.toBe('css')
    expect(detectLanguage('{"a": 1, "b": [1, 2]}')).toBe('json')
    expect(detectLanguage('.a { color: red; }')).toBe('css')
    expect(detectLanguage('hello world, this is just prose')).toBeNull()
    expect(detectLanguage('short')).toBeNull()
    expect(detectLanguage('x'.repeat(10_000))).toBeNull()
  })

  it('turns highlight output into text ranges, decoding entities', () => {
    expect(tokenRanges('<span class="hljs-keyword">if</span> a &lt; b &amp;&amp; <span class="hljs-string">&quot;x&quot;</span>')).toEqual([
      { from: 0, to: 2, className: 'hljs-keyword' },
      { from: 12, to: 15, className: 'hljs-string' },
    ])
    expect(highlightCode('a < b', 'plaintext')).toEqual([])
    expect(highlightCode('a < b', 'unknown')).toEqual([])
    expect(highlightCode('const a = 1', 'js').some(range => range.className === 'hljs-keyword')).toBe(true)
  })
})
