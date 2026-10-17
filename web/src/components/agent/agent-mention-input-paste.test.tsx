import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn(() => new Promise(() => undefined)) }))

import { AgentMentionInput, type AgentMention, type AgentUpload } from './agent-mention-input'
import { splitAgentBlocks } from './agent-content-model'
import { classifyPastedUrl, htmlToFragment, markdownToFragment, remoteImageSource } from './agent-paste'

const state = { id: 's', name: 'Todo', color: '#999', type: 'unstarted' }
const data = {
  workspace: { urlKey: 'ws' },
  users: [],
  issues: [{ id: 'issue_1', identifier: 'FLO-12', title: 'Fix paste', state }],
  projects: [],
  documents: [],
  initiatives: [],
} as unknown as BootstrapData

type Clipboard = { html?: string; text?: string }
const clipboard = ({ html = '', text = '' }: Clipboard) => ({ files: [], getData: (type: string) => type === 'text/html' ? html : type === 'text/plain' ? text : '' })

function setup(extra: Partial<Parameters<typeof AgentMentionInput>[0]> = {}) {
  const onChange = vi.fn<(value: string, mentions: AgentMention[]) => void>()
  render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={onChange} onSubmit={vi.fn()} placeholder="Ask" value="" {...extra}/></I18nProvider>)
  const box = screen.getByRole('textbox', { name: 'Ask' })
  const paste = (content: Clipboard) => fireEvent.paste(box, { clipboardData: clipboard(content) })
  const value = () => onChange.mock.lastCall![0].trim()
  return { box, paste, onChange, value }
}

afterEach(() => { vi.restoreAllMocks() })

describe('pasting rich text into the Agent composer', () => {
  it('keeps links, bold, italic, inline code and strikethrough, and sends them as Markdown', () => {
    const { box, paste, value } = setup()
    paste({ html: '<p>See <a href="https://example.com/a">the docs</a>, <b>bold</b>, <i>soft</i>, <code>x()</code> and <del>old</del></p>', text: 'See the docs, bold, soft, x() and old' })
    expect(box.querySelector('a')).toHaveAttribute('href', 'https://example.com/a')
    expect(box.querySelector('strong')).toHaveTextContent('bold')
    expect(value()).toBe('See [the docs](https://example.com/a), **bold**, *soft*, `x()` and ~~old~~')
  })

  it('escapes link destinations and labels so the sent Markdown keeps the whole link', () => {
    const { paste, value } = setup()
    paste({ html: '<p><a href="https://en.wikipedia.org/wiki/Foo_(bar)">Foo [1]</a></p>', text: 'Foo [1]' })
    expect(value()).toBe('[Foo \\[1\\]](https://en.wikipedia.org/wiki/Foo_%28bar%29)')
    expect(splitAgentBlocks(value())).toHaveLength(1)
  })

  it('escapes existing backslashes before brackets in link labels', () => {
    const { paste, value } = setup()
    paste({ html: String.raw`<p><a href="https://example.com/docs">name\]</a></p>`, text: String.raw`name\]` })
    expect(value()).toBe(String.raw`[name\\\]](https://example.com/docs)`)
  })

  it('keeps lists, nested lists and ordered lists', () => {
    const { box, paste, value } = setup()
    paste({ html: '<ul><li>one<ul><li>inner</li></ul></li><li>two</li></ul><ol><li>first</li><li>second</li></ol>', text: 'x' })
    expect(box.querySelectorAll('ul')).toHaveLength(2)
    expect(value()).toBe('- one\n  - inner\n- two\n\n1. first\n2. second')
  })

  it('keeps a code block with its language', () => {
    const { box, paste, value } = setup()
    paste({ html: '<pre><code class="language-ts">const a = 1\nconst b = 2</code></pre>', text: 'const a = 1\nconst b = 2' })
    expect(box.querySelector('pre')).toHaveAttribute('data-lang', 'ts')
    expect(value()).toBe('```ts\nconst a = 1\nconst b = 2\n```')
  })

  it('turns headings into bold lines and drops scripts and unsafe links', () => {
    const { paste, value } = setup()
    // Built at runtime so the source has no literal script URL (the functional-controls check flags those).
    const unsafe = ['javascript', 'alert(1)'].join(':')
    paste({ html: `<h1>Title</h1><p>body <a href="${unsafe}">bad</a><script>alert(1)</script></p>`, text: 'Title\nbody bad' })
    expect(value()).toBe('**Title**\n\nbody bad')
  })

  it('does not treat the html of a plain text editor as rich text', () => {
    const insertText = vi.fn(() => true)
    document.execCommand = insertText
    const { paste } = setup()
    paste({ html: '<meta charset="utf-8"><div style="white-space: pre;">plain text</div>', text: 'plain text' })
    expect(insertText).toHaveBeenCalledWith('insertText', false, 'plain text')
  })

  it('pastes as typed inside a code block', () => {
    const insertText = vi.fn(() => true)
    document.execCommand = insertText
    const { box, paste } = setup()
    paste({ html: '<pre><code>a</code></pre>', text: 'a' })
    const code = box.querySelector('code')!.firstChild!
    const range = document.createRange()
    range.setStart(code, 1)
    range.collapse(true)
    window.getSelection()!.removeAllRanges()
    window.getSelection()!.addRange(range)
    paste({ html: '<b>bold</b>', text: 'bold' })
    expect(insertText).toHaveBeenCalledWith('insertText', false, 'bold')
  })
})

describe('pasting Markdown into the Agent composer', () => {
  it('converts lists, bold and fenced code blocks (with language), and leaves headings literal', () => {
    const { box, paste, value } = setup()
    const text = '# Plan\n\n- **first** item\n- second with `code`\n\n```python\nprint("hi")\n```\n'
    paste({ text })
    expect(box.querySelector('ul')).not.toBeNull()
    expect(box.querySelector('strong')).toHaveTextContent('first')
    expect(box.querySelector('pre')).toHaveAttribute('data-lang', 'python')
    expect(box.querySelector('h1')).toBeNull()
    expect(value()).toBe('# Plan\n\n- **first** item\n- second with `code`\n\n```python\nprint("hi")\n```')
  })

  it('keeps a single "# heading" line as literal text', () => {
    const { box, paste, value } = setup()
    paste({ text: '# heading' })
    expect(box.querySelector('h1')).toBeNull()
    expect(value()).toBe('# heading')
  })

  it('converts ordered lists and links in Markdown', () => {
    const fragment = markdownToFragment('1. a [link](https://x.dev)\n2. b')!
    const host = document.createElement('div')
    host.append(fragment)
    expect(host.querySelector('ol')!.children).toHaveLength(2)
    expect(host.querySelector('a')).toHaveAttribute('href', 'https://x.dev')
  })
})

describe('pasting a URL into the Agent composer', () => {
  it('makes a link with a "Keep as link (Esc)" popup that Esc dismisses', () => {
    const { box, paste, value } = setup()
    paste({ text: 'https://example.com/page' })
    expect(box.querySelector('a')).toHaveAttribute('href', 'https://example.com/page')
    expect(value()).toBe('[https://example.com/page](https://example.com/page)')
    expect(screen.getByRole('button', { name: /Keep as link/ })).toHaveTextContent('Esc')
    expect(screen.queryByRole('button', { name: /Embed/ })).toBeNull()
    fireEvent.keyDown(box, { key: 'Escape' })
    expect(screen.queryByRole('button', { name: /Keep as link/ })).toBeNull()
    expect(box.querySelector('a')).not.toBeNull()
  })

  it('offers "Embed video (Tab)" for YouTube and Tab turns the link into an embedded video block', () => {
    const { box, paste, value } = setup()
    paste({ text: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ' })
    expect(screen.getByRole('button', { name: /Embed video/ })).toHaveTextContent('Tab')
    fireEvent.keyDown(box, { key: 'Tab' })
    expect(box.querySelector(':scope > a')).toBeNull()
    const block = box.querySelector<HTMLElement>('[data-agent-block][data-kind="embed"]')!
    expect(block.querySelector('iframe')).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(value()).toBe('::embed[https://www.youtube.com/watch?v=dQw4w9WgXcQ](https://www.youtube.com/watch?v=dQw4w9WgXcQ)')
    expect(screen.queryByRole('button', { name: /Keep as link/ })).toBeNull()
    expect(splitAgentBlocks(value())).toEqual([{ type: 'embed', src: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ' }])
  })

  it('offers "Embed preview" for a Figma URL and keeps the link on Esc', () => {
    const { box, paste, value } = setup()
    paste({ text: 'https://www.figma.com/design/abc123/My-file' })
    expect(screen.getByRole('button', { name: /Embed preview/ })).toBeInTheDocument()
    fireEvent.keyDown(box, { key: 'Escape' })
    expect(value()).toBe('[https://www.figma.com/design/abc123/My-file](https://www.figma.com/design/abc123/My-file)')
  })

  it('dismisses the popup when the user types on', () => {
    const { box, paste } = setup()
    paste({ text: 'https://example.com' })
    fireEvent.input(box)
    expect(screen.queryByRole('button', { name: /Keep as link/ })).toBeNull()
  })

  it('does not offer an embed for a link in the middle of a sentence', () => {
    const { box, paste } = setup()
    box.append(document.createTextNode('watch '))
    const range = document.createRange()
    range.setStart(box.firstChild!, 6)
    range.collapse(true)
    window.getSelection()!.removeAllRanges()
    window.getSelection()!.addRange(range)
    paste({ text: 'https://youtu.be/dQw4w9WgXcQ' })
    expect(screen.queryByRole('button', { name: /Embed video/ })).toBeNull()
    expect(screen.getByRole('button', { name: /Keep as link/ })).toBeInTheDocument()
  })

  it('turns a Flow issue URL into an issue mention chip', () => {
    const { box, paste, onChange } = setup()
    paste({ text: `${window.location.origin}/ws/issue/FLO-12/fix-paste` })
    const chip = box.querySelector<HTMLElement>('[data-mention-id]')!
    expect(chip).toHaveAttribute('data-mention-type', 'issue')
    expect(chip).toHaveTextContent('FLO-12')
    expect(chip).toHaveTextContent('Fix paste')
    expect(onChange.mock.lastCall![0].trim()).toBe('@FLO-12')
    expect(onChange.mock.lastCall![1]).toEqual([{ type: 'issue', id: 'issue_1', label: 'FLO-12' }])
    expect(box.querySelector('a')).toBeNull()
  })

  it('falls back to a link for an issue the workspace data does not hold', () => {
    const { box, paste } = setup()
    paste({ text: `${window.location.origin}/ws/issue/FLO-999` })
    expect(box.querySelector('a')).not.toBeNull()
    expect(box.querySelector('[data-mention-id]')).toBeNull()
  })

  it('classifies URLs', () => {
    expect(classifyPastedUrl('https://other.example/ws/issue/FLO-1', 'https://flow.example')).toMatchObject({ kind: 'link' })
    expect(classifyPastedUrl('https://flow.example/ws/issue/FLO-1/x', 'https://flow.example')).toMatchObject({ kind: 'issue', identifier: 'FLO-1' })
    expect(classifyPastedUrl('two words', 'https://flow.example')).toBeUndefined()
  })
})

describe('pasting an image copied from a web page', () => {
  const html = '<meta charset="utf-8"><img src="https://cdn.example.com/photos/cat.png" alt="cat">'

  it('recognises html with only a remote image', () => {
    expect(remoteImageSource(html)).toBe('https://cdn.example.com/photos/cat.png')
    expect(remoteImageSource('<p>text</p><img src="https://x.dev/a.png">')).toBeUndefined()
    expect(remoteImageSource('<img src="data:image/png;base64,AAAA">')).toBeUndefined()
    expect(htmlToFragment('<img src="https://x.dev/a.png">')).toBeUndefined()
  })

  it('downloads it and uploads it through the upload path into an image block', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:preview')
    URL.revokeObjectURL = vi.fn()
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['png'], { type: 'image/png' }) })))
    const onUploadFile = vi.fn(async (file: File): Promise<AgentUpload> => ({ name: file.name, contentType: file.type, size: file.size, url: `/uploads/agent_upload_0123456789abcdef_1_${file.name}` }))
    const { box, paste, value } = setup({ onUploadFile })
    paste({ html, text: 'https://cdn.example.com/photos/cat.png' })
    const block = box.querySelector<HTMLElement>('[data-agent-block][data-kind="image"]')!
    await waitFor(() => expect(onUploadFile).toHaveBeenCalled())
    expect(onUploadFile.mock.calls[0][0].name).toBe('cat.png')
    await waitFor(() => expect(value()).toBe('![cat.png](/uploads/agent_upload_0123456789abcdef_1_cat.png)'))
    expect(block.querySelector('img')).toHaveAttribute('src', '/uploads/agent_upload_0123456789abcdef_1_cat.png')
    vi.unstubAllGlobals()
  })

  it('shows "Image upload didn\'t complete" with Retry when it fails, and Retry uploads it', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:preview')
    URL.revokeObjectURL = vi.fn()
    let ok = false
    vi.stubGlobal('fetch', vi.fn(async () => { if (!ok) throw new TypeError('Failed to fetch'); return { ok: true, blob: async () => new Blob(['png'], { type: 'image/png' }) } }))
    const onUploadFile = vi.fn(async (file: File): Promise<AgentUpload> => ({ name: file.name, contentType: file.type, size: file.size, url: `/uploads/agent_upload_0123456789abcdef_1_${file.name}` }))
    const onBusyChange = vi.fn<(busy: boolean) => void>()
    const { box, paste, value } = setup({ onUploadFile, onBusyChange })
    paste({ html, text: '' })
    expect(await screen.findByText("Image upload didn't complete")).toBeInTheDocument()
    expect(box.querySelector('[data-agent-block]')).not.toBeNull()
    await waitFor(() => expect(onBusyChange).toHaveBeenLastCalledWith(false))
    ok = true
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(value()).toBe('![cat.png](/uploads/agent_upload_0123456789abcdef_1_cat.png)'))
    expect(screen.queryByText("Image upload didn't complete")).toBeNull()
    vi.unstubAllGlobals()
  })
})
