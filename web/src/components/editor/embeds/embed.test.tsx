import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { RichComment } from '@/components/activity/rich-comment'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { embedSourceForUrl } from './embed-providers'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'embed-test' }))

describe('embedSourceForUrl', () => {
  it('embeds YouTube, Loom and Descript links', () => {
    expect(embedSourceForUrl('https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10s')).toEqual({ provider: 'youtube', embedUrl: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ' })
    expect(embedSourceForUrl('https://youtu.be/dQw4w9WgXcQ')?.embedUrl).toBe('https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(embedSourceForUrl('https://www.youtube.com/shorts/abcDEF12345')?.provider).toBe('youtube')
    expect(embedSourceForUrl('https://www.loom.com/share/0123456789abcdef')).toEqual({ provider: 'loom', embedUrl: 'https://www.loom.com/embed/0123456789abcdef' })
    expect(embedSourceForUrl('https://share.descript.com/view/AbCdEf123456')).toEqual({ provider: 'descript', embedUrl: 'https://share.descript.com/embed/AbCdEf123456' })
  })

  it('leaves every other link alone', () => {
    for (const url of ['https://github.com/acme/web/pull/7', 'https://acme.slack.com/archives/C1/p1', 'https://gitlab.com/a/b/-/merge_requests/1', 'https://www.youtube.com/', 'https://evil.test/watch?v=dQw4w9WgXcQ', 'https://notyoutube.com/watch?v=dQw4w9WgXcQ', 'javascript:alert(1)', 'not a url']) expect(embedSourceForUrl(url), url).toBeUndefined()
  })
})

function paste(editor: Editor, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : '' } })
  act(() => { editor.view.dom.dispatchEvent(event) })
}

async function mount(value = '') {
  let editor: Editor | null = null
  render(<I18nProvider><IssueDescriptionEditor value={value} editorRef={next => { editor = next }}/></I18nProvider>)
  await waitFor(() => expect(editor).not.toBeNull())
  return () => editor as Editor
}

describe('pasting a video link', () => {
  beforeEach(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('shows the player in an empty paragraph, sandboxed, and writes the URL as its text', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    const frame = await waitFor(() => {
      const found = document.querySelector('iframe')
      expect(found).not.toBeNull()
      return found as HTMLIFrameElement
    })
    expect(frame).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(frame.getAttribute('sandbox')).toContain('allow-scripts')
    expect(editor().getText()).toContain('https://youtu.be/dQw4w9WgXcQ')
    expect(editor().getMarkdown()).toContain('https://youtu.be/dQw4w9WgXcQ')
  })

  it('keeps the plain link with Esc right after the paste', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://www.loom.com/share/0123456789abcdef')
    await waitFor(() => expect(document.querySelector('iframe')).not.toBeNull())
    await userEvent.setup().keyboard('{Escape}')
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    expect(editor().getMarkdown()).toContain('[https://www.loom.com/share/0123456789abcdef](https://www.loom.com/share/0123456789abcdef)')
  })

  it('offers "Keep as link" on the player', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://share.descript.com/view/AbCdEf123456')
    await userEvent.setup().click(await screen.findByRole('button', { name: 'Keep as link' }))
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    expect(editor().getText().trim()).toBe('https://share.descript.com/view/AbCdEf123456')
  })

  it('does not embed inside a sentence, over a selection, or for other links', async () => {
    const editor = await mount('Watch ')
    editor().commands.focus('end')
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    paste(editor(), 'https://github.com/acme/web/pull/7')
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('renders a saved player in read-only rich text', async () => {
    const doc = { type: 'doc', content: [{ type: 'embed', attrs: { src: 'https://youtu.be/dQw4w9WgXcQ', embedUrl: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ', provider: 'youtube' } }] }
    render(<I18nProvider><RichComment body="" data={doc}/></I18nProvider>)
    expect(await waitFor(() => { const found = document.querySelector('iframe'); expect(found).not.toBeNull(); return found })).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(screen.queryByRole('button', { name: 'Keep as link' })).toBeNull()
  })
})
