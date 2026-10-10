import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Editor } from '@tiptap/react'
import { toast } from 'sonner'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { showsSelectionToolbar } from '@/components/issue/editor/selection-toolbar-visibility'
import { RichComment } from '@/components/activity/rich-comment'
import { EmbedToolbar } from './embed-toolbar'
import { resetFilePreviewCache } from './file-preview'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'embed-toolbar-test' }))
vi.mock('sonner', () => ({ toast: { info: vi.fn(), error: vi.fn(), success: vi.fn() } }))

const VIDEO = { src: 'https://youtu.be/dQw4w9WgXcQ', embedUrl: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ', provider: 'youtube' }
const FILE = { src: 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2', embedUrl: 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2', provider: 'github' }
const embed = (attrs = VIDEO) => ({ type: 'embed', attrs })
const paragraph = (text = '') => ({ type: 'paragraph', content: text ? [{ type: 'text', text }] : undefined })

async function mount(content: object[]) {
  let editor: Editor | null = null
  render(<I18nProvider><IssueDescriptionEditor value="" editorRef={next => { editor = next }}/></I18nProvider>)
  await waitFor(() => expect(editor).not.toBeNull())
  const get = () => editor as Editor
  act(() => { get().commands.setContent({ type: 'doc', content }) })
  if (content.some(node => (node as { type: string }).type === 'embed')) await screen.findByRole('toolbar', { name: 'Embed actions' })
  return get
}

const toolbar = () => screen.getByRole('toolbar', { name: 'Embed actions' })

function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
}
const labels = () => [...toolbar().querySelectorAll('a, button')].map(node => node.getAttribute('aria-label'))

describe('embed toolbar', () => {
  beforeEach(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    vi.mocked(toast.info).mockClear()
    vi.mocked(toast.error).mockClear()
    resetFilePreviewCache()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ provider: 'github', repo: 'acme/web', path: 'src/app.ts', ref: 'main', language: 'typescript', lines: ['const a = 1'], startLine: 1, endLine: 1, totalLines: 1, truncated: false, htmlUrl: FILE.src }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists Open link, Convert to text link, Copy link and Delete in Linear order, with a divider before Delete', async () => {
    await mount([embed(), paragraph()])
    expect(labels()).toEqual(['Open link', 'Convert to text link', 'Copy link', 'Delete'])
    const children = [...toolbar().children]
    expect(children[3].getAttribute('aria-hidden')).toBe('true')
    expect(children[4].getAttribute('aria-label')).toBe('Delete')
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('href', VIDEO.src)
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('target', '_blank')
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('copies the link and confirms with a toast', async () => {
    const writeText = vi.fn(async (_text: string) => undefined)
    const user = userEvent.setup()
    stubClipboard(writeText)
    await mount([embed(), paragraph()])
    await user.click(screen.getByRole('button', { name: 'Copy link' }))
    expect(writeText).toHaveBeenCalledWith(VIDEO.src)
    await waitFor(() => expect(toast.info).toHaveBeenCalledWith('Link copied to clipboard'))
  })

  it('says so when the clipboard is unavailable', async () => {
    const user = userEvent.setup()
    stubClipboard(async () => { throw new Error('denied') })
    await mount([embed(), paragraph()])
    await user.click(screen.getByRole('button', { name: 'Copy link' }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Could not copy link'))
    expect(toast.info).not.toHaveBeenCalled()
  })

  it('converts the block to a linked paragraph and keeps typing after it', async () => {
    const editor = await mount([paragraph('before'), embed(), paragraph('after')])
    await userEvent.setup().click(screen.getByRole('button', { name: 'Convert to text link' }))
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    const content = editor().getJSON().content ?? []
    expect(content.map(node => node.type)).toEqual(['paragraph', 'paragraph', 'paragraph'])
    expect(content[1].content?.[0]).toMatchObject({ type: 'text', text: VIDEO.src, marks: [{ type: 'link', attrs: { href: VIDEO.src } }] })
    expect(editor().state.selection.$from.parent.textContent).toBe(VIDEO.src)
    expect(editor().state.selection.empty).toBe(true)
  })

  it('deletes the block, and leaves an empty paragraph when it was the whole document', async () => {
    const editor = await mount([paragraph('keep'), embed(), paragraph('tail')])
    await userEvent.setup().click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    expect(editor().getJSON().content?.map(node => node.type)).toEqual(['paragraph', 'paragraph'])
    expect(editor().getText()).toContain('keep')

    const only = await mount([embed()])
    await userEvent.setup().click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    expect(only().getJSON().content?.map(node => node.type)).toEqual(['paragraph'])
  })

  it.each(['{Backspace}', '{Delete}'])('removes a selected block with %s', async key => {
    const editor = await mount([paragraph('before'), embed(), paragraph('after')])
    act(() => { editor().commands.focus(); editor().commands.setNodeSelection(editor().state.doc.child(0).nodeSize) })
    act(() => { editor().view.dom.focus() })
    await userEvent.setup().keyboard(key)
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    expect(editor().getJSON().content?.map(node => node.type)).toEqual(['paragraph', 'paragraph'])
  })

  it('puts a shield over an unselected player so a click selects the block, and removes it once selected', async () => {
    const editor = await mount([paragraph('before'), embed(), paragraph('after')])
    expect(document.querySelector('[data-embed-shield]')).not.toBeNull()
    act(() => { editor().commands.setNodeSelection(editor().state.doc.child(0).nodeSize) })
    await waitFor(() => expect(document.querySelector('[data-embed-shield]')).toBeNull())
    expect(document.querySelector('[data-embed-provider]')?.className).toContain('selected')
  })

  it('does not offer the text formatting toolbar for a selected block, only for a range of text', async () => {
    const editor = await mount([paragraph('before'), embed(), paragraph('after')])
    const range = (from: number, to: number) => ({ from, to, editor: editor() })
    act(() => { editor().commands.setNodeSelection(editor().state.doc.child(0).nodeSize) })
    expect(editor().isActive('embed')).toBe(true)
    expect(showsSelectionToolbar(range(editor().state.selection.from, editor().state.selection.to))).toBe(false)
    act(() => { editor().commands.setTextSelection({ from: 1, to: 4 }) })
    expect(showsSelectionToolbar(range(1, 4))).toBe(true)
    expect(showsSelectionToolbar(range(2, 2))).toBe(false)
  })

  it('serves file preview cards too, whose header leaves Open link to the toolbar', async () => {
    await mount([embed(FILE), paragraph()])
    await waitFor(() => expect(document.querySelector('[data-file-preview="ready"]')).not.toBeNull())
    expect(labels()).toEqual(['Open link', 'Convert to text link', 'Copy link', 'Delete'])
    expect(screen.queryByRole('link', { name: /Open in GitHub/ })).toBeNull()
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('href', FILE.src)
  })

  it('keeps only Open link and Copy link in read-only rich text, for players and file cards', async () => {
    render(<I18nProvider><RichComment body="" data={{ type: 'doc', content: [embed(), embed(FILE)] }}/></I18nProvider>)
    await waitFor(() => expect(screen.getAllByRole('toolbar', { name: 'Embed actions' })).toHaveLength(2))
    for (const bar of screen.getAllByRole('toolbar', { name: 'Embed actions' })) {
      expect([...bar.querySelectorAll('a, button')].map(node => node.getAttribute('aria-label'))).toEqual(['Open link', 'Copy link'])
    }
    expect(document.querySelector('[data-embed-shield]')).toBeNull()
  })

  it('omits Open link for a source that is not http(s)', () => {
    render(<I18nProvider><EmbedToolbar src="javascript:alert(1)"/></I18nProvider>)
    expect(within(toolbar()).queryByRole('link')).toBeNull()
    expect(labels()).toEqual(['Copy link'])
  })
})

describe('embed toolbar in Chinese', () => {
  it('has Chinese labels', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    render(<I18nProvider><EmbedToolbar onConvert={vi.fn()} onDelete={vi.fn()} src="https://youtu.be/dQw4w9WgXcQ"/></I18nProvider>)
    const bar = screen.getByRole('toolbar')
    expect([...bar.querySelectorAll('a, button')].map(node => node.getAttribute('aria-label'))).toEqual(['打开链接', '转换为文本链接', '复制链接', '删除'])
    expect(bar).toHaveAttribute('aria-label', '嵌入内容操作')
    localStorage.removeItem('flow:locale')
  })
})
