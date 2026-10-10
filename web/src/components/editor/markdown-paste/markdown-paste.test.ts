import { Editor, type Extensions, type JSONContent } from '@tiptap/core'
import { TableKit } from '@tiptap/extension-table'
import { Markdown } from '@tiptap/markdown'
import { StarterKit } from '@tiptap/starter-kit'
import { afterEach, describe, expect, it } from 'vitest'
import { DescriptionCallout } from '@/components/issue/editor/callout-extension'
import { DescriptionImage } from '@/components/issue/editor/image-extension'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { MarkdownPaste, importTextFile, isPlainTextWrapperHtml, looksLikeMarkdown, markdownToDocJSON, normalizeGithubHtml } from '.'

const editors: Editor[] = []

function makeEditor(content = '<p></p>', extra: Extensions = []) {
  const element = document.createElement('div')
  document.body.append(element)
  const editor = new Editor({
    element,
    content,
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2, 3, 4] }, link: { openOnClick: false, autolink: true, linkOnPaste: true }, undoRedo: false, trailingNode: false }),
      TableKit.configure({ table: { resizable: false } }),
      ...structuredBlocks,
      Markdown,
      DescriptionImage,
      DescriptionCallout,
      MarkdownPaste,
      ...extra,
    ],
  })
  editors.push(editor)
  return editor
}

function paste(editor: Editor, clipboard: { text?: string; html?: string; files?: unknown[]; vscode?: string }) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  const values: Record<string, string> = { 'text/plain': clipboard.text ?? '', 'text/html': clipboard.html ?? '', 'vscode-editor-data': clipboard.vscode ?? '' }
  Object.defineProperty(event, 'clipboardData', {
    value: { types: Object.keys(values).filter(type => values[type]), files: clipboard.files ?? [], items: [], getData: (type: string) => values[type] ?? '' },
  })
  editor.view.dom.dispatchEvent(event)
  return event
}

const blocks = (editor: Editor) => (editor.getJSON().content ?? []) as JSONContent[]
const types = (editor: Editor) => blocks(editor).map(block => block.type)
const textOf = (node: JSONContent): string => node.text ?? (node.content ?? []).map(textOf).join('')

afterEach(() => {
  while (editors.length) editors.pop()?.destroy()
  document.body.innerHTML = ''
})

describe('looksLikeMarkdown', () => {
  it('accepts block syntax and multi-line inline syntax', () => {
    for (const text of ['# Title', '- a\n- b', '1. a\n2. b', '- [ ] todo', '> quote', '---', '```ts\ncode\n```', '| a | b |\n|---|---|\n| 1 | 2 |', '+++ Title\nbody\n+++', '![alt](https://x.test/a.png)', 'line one\nsome **bold** here', 'see [docs](https://x.test)\nand more', '<details>\n<summary>x</summary>\n\nbody\n\n</details>']) expect(looksLikeMarkdown(text), text).toBe(true)
  })

  it('rejects plain prose, single-line inline syntax and URLs', () => {
    for (const text of ['hello world', 'a **bold** word', 'two lines\nof plain text', 'https://example.com/a', '#hashtag', '-5 degrees', '', '   ', '```\nunterminated']) expect(looksLikeMarkdown(text), text).toBe(false)
  })

  it('treats editor wrappers as plain text and rendered html as structure', () => {
    expect(isPlainTextWrapperHtml('<meta charset="utf-8"><div style="font-family: Menlo; white-space: pre;"><div><span style="color:red"># Title</span></div></div>')).toBe(true)
    expect(isPlainTextWrapperHtml('<h1>Title</h1><ul><li>a</li></ul>')).toBe(false)
    expect(isPlainTextWrapperHtml('<p data-pm-slice="1 1 []">x</p>')).toBe(false)
  })
})

describe('pasting Markdown', () => {
  it('converts a heading', () => {
    const editor = makeEditor()
    const event = paste(editor, { text: '# Hello\n\nbody text' })
    expect(event.defaultPrevented).toBe(true)
    expect(types(editor)).toEqual(['heading', 'paragraph'])
    expect(blocks(editor)[0].attrs?.level).toBe(1)
    expect(textOf(blocks(editor)[0])).toBe('Hello')
  })

  it('clamps heading levels the editor does not offer', () => {
    const editor = makeEditor()
    paste(editor, { text: '##### Deep\n\ntext' })
    expect(blocks(editor)[0].attrs?.level).toBe(4)
  })

  it('keeps a Heading 4 and serializes it back to ####', () => {
    const editor = makeEditor()
    paste(editor, { text: '#### Fourth\n\nbody' })
    expect(blocks(editor)[0]).toMatchObject({ type: 'heading', attrs: { level: 4 } })
    expect(editor.getMarkdown()).toContain('#### Fourth')
  })

  it('keeps an <h4> from pasted HTML', () => {
    const editor = makeEditor()
    paste(editor, { html: '<h4>From page</h4><p>body</p>', text: 'From page\nbody' })
    expect(blocks(editor)[0]).toMatchObject({ type: 'heading', attrs: { level: 4 } })
  })

  it('converts bullet and numbered lists', () => {
    const bullets = makeEditor()
    paste(bullets, { text: '- one\n- two\n- three' })
    expect(types(bullets)).toEqual(['bulletList'])
    expect(blocks(bullets)[0].content).toHaveLength(3)
    const numbered = makeEditor()
    paste(numbered, { text: '1. one\n2. two' })
    expect(types(numbered)).toEqual(['orderedList'])
    expect(blocks(numbered)[0].content).toHaveLength(2)
  })

  it('converts task lists with their checked state', () => {
    const editor = makeEditor()
    paste(editor, { text: '- [x] done\n- [ ] todo' })
    expect(types(editor)).toEqual(['taskList'])
    const items = blocks(editor)[0].content ?? []
    expect(items.map(item => [item.type, item.attrs?.checked, textOf(item)])).toEqual([['taskItem', true, 'done'], ['taskItem', false, 'todo']])
  })

  it('converts a GFM table with a header row', () => {
    const editor = makeEditor()
    paste(editor, { text: '| a | b |\n|---|---|\n| 1 | **2** |' })
    expect(types(editor)).toEqual(['table'])
    const rows = blocks(editor)[0].content ?? []
    expect(rows.map(row => (row.content ?? []).map(cell => cell.type))).toEqual([['tableHeader', 'tableHeader'], ['tableCell', 'tableCell']])
    expect(JSON.stringify(rows[1])).toContain('"bold"')
  })

  it('converts fenced code with its language', () => {
    const editor = makeEditor()
    paste(editor, { text: 'before\n\n```ts\nconst a = 1\n```' })
    const code = blocks(editor).find(block => block.type === 'codeBlock')
    expect(code?.attrs?.language).toBe('ts')
    expect(textOf(code!)).toBe('const a = 1')
  })

  it('converts links and inline marks in a multi-line paste', () => {
    const editor = makeEditor()
    paste(editor, { text: 'See [the docs](https://example.com/docs) for **bold**, *italic*, ~~gone~~ and `code`.\nSecond line.' })
    const json = JSON.stringify(blocks(editor))
    expect(json).toContain('"href":"https://example.com/docs"')
    for (const mark of ['bold', 'italic', 'strike', 'code']) expect(json).toContain(`"type":"${mark}"`)
    expect(json).toContain('hardBreak')
  })

  it('drops links with unsafe schemes', () => {
    const editor = makeEditor()
    paste(editor, { text: 'a [bad](javascript:alert(1)) link\n- item' })
    expect(JSON.stringify(blocks(editor))).not.toContain('javascript')
  })

  it('converts blockquotes and horizontal rules', () => {
    const editor = makeEditor()
    paste(editor, { text: '> quoted line\n\n---\n\nafter' })
    expect(types(editor)).toEqual(['blockquote', 'horizontalRule', 'paragraph'])
  })

  it('turns GitHub alerts into callouts with their colour', () => {
    const cases = { NOTE: 'cyan', TIP: 'green', IMPORTANT: 'purple', WARNING: 'orange', CAUTION: 'red' } as const
    for (const [kind, color] of Object.entries(cases)) {
      const editor = makeEditor()
      paste(editor, { text: `> [!${kind}]\n> Watch out\n> second line\n\nafter` })
      const callout = blocks(editor)[0]
      expect(callout.type, kind).toBe('callout')
      expect(callout.attrs?.color, kind).toBe(color)
      expect(textOf(callout)).not.toContain('[!')
      expect(textOf(callout)).toContain('Watch out')
    }
  })

  it('only passes the callout icon when the schema defines it', () => {
    const IconCallout = DescriptionCallout.extend({ addAttributes() { return { color: { default: 'cyan' }, icon: { default: '' } } } })
    const element = document.createElement('div')
    const editor = new Editor({ element, content: '<p></p>', extensions: [StarterKit.configure({ undoRedo: false }), TableKit, ...structuredBlocks, Markdown, DescriptionImage, IconCallout, MarkdownPaste] })
    editors.push(editor)
    paste(editor, { text: '> [!TIP]\n> bulb' })
    expect(blocks(editor)[0].attrs).toMatchObject({ color: 'green', icon: '💡' })
    const ColorOnlyCallout = DescriptionCallout.extend({ addAttributes() { return { color: { default: 'cyan' } } } })
    const plain = new Editor({ element: document.createElement('div'), content: '<p></p>', extensions: [StarterKit.configure({ undoRedo: false }), TableKit, ...structuredBlocks, Markdown, DescriptionImage, ColorOnlyCallout, MarkdownPaste] })
    editors.push(plain)
    paste(plain, { text: '> [!WARNING]\n> careful' })
    expect(blocks(plain)[0].attrs).toEqual({ color: 'orange' })
  })

  it('converts +++ title sections into collapsible details', () => {
    const editor = makeEditor()
    paste(editor, { text: '+++ More info\nhidden **text**\n\n- a\n- b\n+++\n\nafter' })
    expect(types(editor)).toEqual(['details', 'paragraph'])
    const details = blocks(editor)[0]
    expect(details.content?.map(child => child.type)).toEqual(['detailsSummary', 'detailsContent'])
    expect(textOf(details.content![0])).toBe('More info')
    expect(details.content![1].content?.map(child => child.type)).toEqual(['paragraph', 'bulletList'])
    expect(details.attrs?.open).toBe(false)
  })

  it('converts <details> blocks and nested +++ sections', () => {
    const editor = makeEditor()
    paste(editor, { text: '<details open>\n<summary>Logs</summary>\n\nbody\n\n</details>\n\n+++ outer\n+++ inner\ndeep\n+++\n+++' })
    expect(types(editor)).toEqual(['details', 'details'])
    expect(blocks(editor)[0].attrs?.open).toBe(true)
    expect(textOf(blocks(editor)[0].content![0])).toBe('Logs')
    const inner = blocks(editor)[1].content![1].content![0]
    expect(inner.type).toBe('details')
    expect(textOf(inner)).toContain('deep')
  })

  it('does not split +++ inside fenced code', () => {
    const editor = makeEditor()
    paste(editor, { text: '```\n+++ not a section\n+++\n```\n\n# After' })
    expect(types(editor)).toEqual(['codeBlock', 'heading'])
  })

  it('converts an image on its own line to an image block', () => {
    const editor = makeEditor()
    paste(editor, { text: '![A diagram](https://example.com/a.png "Title")\n\ntext' })
    expect(blocks(editor)[0]).toMatchObject({ type: 'image', attrs: { src: 'https://example.com/a.png', alt: 'A diagram' } })
  })

  it('lifts inline images out of a paragraph', () => {
    const editor = makeEditor()
    paste(editor, { text: 'before ![pic](https://example.com/p.png) after\n- x' })
    expect(types(editor)).toEqual(['paragraph', 'image', 'paragraph', 'bulletList'])
  })

  it('replaces the empty paragraph it is pasted into', () => {
    const editor = makeEditor()
    paste(editor, { text: '# Only a heading' })
    expect(types(editor)).toEqual(['heading'])
  })

  it('inserts between the text around a cursor in a filled paragraph', () => {
    const editor = makeEditor('<p>Hello world</p>')
    editor.commands.setTextSelection(6)
    paste(editor, { text: '- a\n- b' })
    expect(types(editor)).toEqual(['paragraph', 'bulletList', 'paragraph'])
  })

  it('converts through the Markdown helper without an editor view', () => {
    const doc = markdownToDocJSON(makeEditor(), '- [x] a\n\n```js\nx\n```')
    expect(doc.content?.map(block => block.type)).toEqual(['taskList', 'codeBlock'])
  })

  it('prefers the Markdown text when the html is only an editor wrapper', () => {
    const editor = makeEditor()
    paste(editor, { text: '# From VS Code\n\n- a', html: '<meta charset="utf-8"><div style="white-space: pre;"><div><span># From VS Code</span></div></div>', vscode: '{"mode":"markdown"}' })
    expect(types(editor)).toEqual(['heading', 'bulletList'])
  })
})

describe('what stays with the other paste handlers', () => {
  it('leaves single-line plain text and inline-only text alone', () => {
    // (Tiptap's own mark paste rules may still style inline syntax; the point is that no blocks are created.)
    for (const text of ['just some words', 'with **bold** inline', 'see [docs](https://example.com/docs) now']) {
      const editor = makeEditor()
      const event = paste(editor, { text })
      expect(types(editor), text).toEqual(['paragraph'])
      expect(JSON.stringify(blocks(editor)), text).not.toContain('hardBreak')
      expect(event.defaultPrevented).toBe(true) // ProseMirror's own plain-text paste
    }
    const words = makeEditor()
    paste(words, { text: 'just some words' })
    expect(textOf(blocks(words)[0])).toBe('just some words')
  })

  it('leaves a single URL to the link / embed / mention handlers', () => {
    const editor = makeEditor()
    const view = editor.view
    const event = new Event('paste', { cancelable: true }) as ClipboardEvent
    Object.defineProperty(event, 'clipboardData', { value: { files: [], getData: (type: string) => type === 'text/plain' ? 'https://example.com/a-b' : '' } })
    expect(view.someProp('handlePaste', handler => handler(view, event, view.state.doc.slice(0, 0)) ? true : undefined) ?? false).toBe(false)
  })

  it('does not convert while a file is on the clipboard', () => {
    const editor = makeEditor()
    paste(editor, { text: '# Heading\n\nbody', files: [new File(['x'], 'a.bin')] })
    expect(types(editor)).not.toContain('heading')
  })

  it('keeps Markdown pasted into a code block as plain text', () => {
    const editor = makeEditor('<pre><code>x</code></pre>')
    editor.commands.setTextSelection(2)
    paste(editor, { text: '# not a heading\n- not a list' })
    expect(types(editor)).toEqual(['codeBlock'])
    expect(textOf(blocks(editor)[0])).toContain('# not a heading')
  })

  it('leaves VS Code code from other languages to the code-block handler', () => {
    const editor = makeEditor()
    paste(editor, { text: '- [x] looks like markdown\n- but is yaml', vscode: '{"mode":"yaml"}' })
    expect(types(editor)).toEqual(['codeBlock'])
  })

  it('does nothing in a read-only editor', () => {
    const editor = makeEditor()
    editor.setEditable(false)
    const before = JSON.stringify(editor.getJSON())
    paste(editor, { text: '# nope\n\n- a' })
    expect(JSON.stringify(editor.getJSON())).toBe(before)
  })
})

describe('GitHub html', () => {
  const taskList = '<div class="markdown-body"><ul class="contains-task-list"><li class="task-list-item"><input type="checkbox" class="task-list-item-checkbox" disabled checked> done</li><li class="task-list-item"><input type="checkbox" class="task-list-item-checkbox" disabled> todo</li></ul></div>'

  it('normalises task lists, code languages, alerts, tables and anchors', () => {
    const html = normalizeGithubHtml(`${taskList}
      <div class="highlight highlight-source-js notranslate"><pre><span class="pl-k">const</span> a = 1</pre></div>
      <div class="markdown-alert markdown-alert-warning"><p class="markdown-alert-title"><svg class="octicon"></svg>Warning</p><p>Careful</p></div>
      <div class="markdown-heading"><h2>Title <a class="anchor" href="#title"><svg class="octicon"></svg></a></h2></div>
      <table><thead><tr><th>a</th></tr></thead><tbody><tr><td>1</td></tr></tbody></table>`)
    const body = new DOMParser().parseFromString(html, 'text/html').body
    expect(body.querySelector('ul[data-type="taskList"]')).not.toBeNull()
    expect([...body.querySelectorAll('li[data-type="taskItem"]')].map(item => item.getAttribute('data-checked'))).toEqual(['true', 'false'])
    expect(body.querySelector('input')).toBeNull()
    expect(body.querySelector('pre > code.language-js')?.textContent).toBe('const a = 1')
    const aside = body.querySelector('aside[data-callout]')
    expect(aside?.getAttribute('data-color')).toBe('orange')
    expect(aside?.textContent).toBe('Careful')
    expect(body.querySelector('svg, a.anchor, .markdown-heading')).toBeNull()
    expect(body.querySelector('h2')?.textContent?.trim()).toBe('Title')
    expect(body.querySelector('table')).not.toBeNull()
  })

  it('adds a header row to tables that have none and leaves non-GitHub html alone', () => {
    const html = normalizeGithubHtml('<div class="markdown-body"><table><tbody><tr><td>a</td><td>b</td></tr><tr><td>1</td><td>2</td></tr></tbody></table></div>')
    const table = new DOMParser().parseFromString(html, 'text/html').body.querySelector('table')!
    expect(table.querySelectorAll('tr')[0].querySelectorAll('th')).toHaveLength(2)
    expect(table.querySelectorAll('tr')[1].querySelectorAll('th')).toHaveLength(0)
    const untouched = '<p>hello <b>world</b></p>'
    expect(normalizeGithubHtml(untouched)).toBe(untouched)
  })

  it('pastes rendered GitHub html as task items, a language code block, a callout and a table', () => {
    const editor = makeEditor()
    paste(editor, {
      text: 'ignored plain text',
      html: `${taskList}<div class="highlight highlight-source-ts"><pre>let x: number</pre></div><div class="markdown-alert markdown-alert-note"><p class="markdown-alert-title">Note</p><p>Remember</p></div><table><thead><tr><th>h</th></tr></thead><tbody><tr><td>c</td></tr></tbody></table>`,
    })
    expect(types(editor)).toEqual(['taskList', 'codeBlock', 'callout', 'table'])
    const list = blocks(editor)[0]
    expect(list.content?.map(item => item.attrs?.checked)).toEqual([true, false])
    expect(blocks(editor)[1].attrs?.language).toBe('ts')
    expect(blocks(editor)[2].attrs?.color).toBe('cyan')
    expect(textOf(blocks(editor)[2])).toBe('Remember')
    expect(blocks(editor)[3].content?.[0].content?.[0].type).toBe('tableHeader')
  })

  it('keeps summary and body of a GitHub <details>', () => {
    const editor = makeEditor()
    paste(editor, { text: 'x', html: '<div class="markdown-body"><details><summary>More</summary><p>hidden</p></details></div>' })
    expect(types(editor)).toEqual(['details'])
    expect(textOf(blocks(editor)[0].content![0])).toBe('More')
    expect(textOf(blocks(editor)[0].content![1])).toBe('hidden')
  })
})

describe('importTextFile', () => {
  it('converts a .md file into blocks at the selection', async () => {
    const editor = makeEditor('<p>Intro</p>')
    editor.commands.setTextSelection(editor.state.doc.content.size - 1)
    const done = await importTextFile(editor, new File(['# Doc\n\n- [x] a\n- [ ] b\n'], 'notes.md', { type: 'text/markdown' }))
    expect(done).toBe(true)
    expect(types(editor)).toEqual(['paragraph', 'heading', 'taskList'])
  })

  it('imports a .md file that has no obvious Markdown as plain paragraphs, and a plain .txt as paragraphs', async () => {
    const editor = makeEditor()
    expect(await importTextFile(editor, new File(['first\n\nsecond line\nthird'], 'a.txt', { type: 'text/plain' }))).toBe(true)
    expect(types(editor)).toEqual(['paragraph', 'paragraph'])
    expect(JSON.stringify(blocks(editor))).toContain('hardBreak')
  })

  it('converts Markdown inside a .txt file', async () => {
    const editor = makeEditor()
    expect(await importTextFile(editor, new File(['## Title\n\ntext'], 'a.txt', { type: 'text/plain' }))).toBe(true)
    expect(types(editor)).toEqual(['heading', 'paragraph'])
  })

  it('inserts at an explicit position and refuses other files and empty ones', async () => {
    const editor = makeEditor('<p>one</p><p>two</p>')
    expect(await importTextFile(editor, new File(['# Mid'], 'a.md'), editor.state.doc.child(0).nodeSize)).toBe(true)
    expect(types(editor)).toEqual(['paragraph', 'heading', 'paragraph'])
    expect(await importTextFile(editor, new File(['x'], 'a.pdf', { type: 'application/pdf' }))).toBe(false)
    expect(await importTextFile(editor, new File(['  \n'], 'empty.md'))).toBe(false)
  })
})
