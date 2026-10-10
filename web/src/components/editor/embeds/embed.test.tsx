import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { RichComment } from '@/components/activity/rich-comment'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { embedSourceForUrl, githubFileForUrl, gitlabFileForUrl, repoFileForUrl } from './embed-providers'
import { resetFilePreviewCache } from './file-preview'
import { resetLinkPreviewCache } from './link-preview'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'embed-test' }))

describe('embedSourceForUrl', () => {
  it('embeds YouTube, Loom, Descript and Tella videos', () => {
    expect(embedSourceForUrl('https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10s')).toMatchObject({ provider: 'youtube', embedUrl: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ', label: 'Embed video', kind: 'iframe', aspect: 'video' })
    expect(embedSourceForUrl('https://youtu.be/dQw4w9WgXcQ')?.embedUrl).toBe('https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(embedSourceForUrl('https://www.youtube.com/shorts/abcDEF12345')?.provider).toBe('youtube')
    expect(embedSourceForUrl('https://www.loom.com/share/0123456789abcdef')).toMatchObject({ provider: 'loom', embedUrl: 'https://www.loom.com/embed/0123456789abcdef', label: 'Embed video' })
    expect(embedSourceForUrl('https://share.descript.com/view/AbCdEf123456')).toMatchObject({ provider: 'descript', embedUrl: 'https://share.descript.com/embed/AbCdEf123456', label: 'Embed video' })
    expect(embedSourceForUrl('https://www.tella.tv/video/my-demo-clx8k2m4n0000abc/')).toMatchObject({ provider: 'tella', embedUrl: 'https://www.tella.tv/video/clx8k2m4n0000abc/embed', label: 'Embed video' })
    expect(embedSourceForUrl('https://tella.tv/video/clx8k2m4n0000abc')?.embedUrl).toBe('https://www.tella.tv/video/clx8k2m4n0000abc/embed')
  })

  it('embeds X posts and Figma as link cards, Miro as a player and GitHub file links as file cards, with their own labels', () => {
    for (const url of ['https://x.com/flow/status/1790000000000000001', 'https://twitter.com/flow/status/1790000000000000001?s=20']) {
      expect(embedSourceForUrl(url)).toMatchObject({ provider: 'twitter', kind: 'link', label: 'Embed post' })
    }
    const figma = 'https://www.figma.com/design/AbC123/Flow-app?node-id=1-2'
    expect(embedSourceForUrl(figma)).toMatchObject({ provider: 'figma', kind: 'link', embedUrl: figma, label: 'Embed preview' })
    for (const kind of ['file', 'design', 'proto', 'board', 'slides']) expect(embedSourceForUrl(`https://figma.com/${kind}/AbC123/x`)?.provider, kind).toBe('figma')
    expect(embedSourceForUrl('https://miro.com/app/board/uXjVKabc123=/')).toMatchObject({ provider: 'miro', embedUrl: 'https://miro.com/app/embed/uXjVKabc123=/', kind: 'iframe', aspect: 'tall', label: 'Embed board' })
    expect(embedSourceForUrl('https://github.com/acme/web/blob/main/src/app.ts#L1-L20')).toMatchObject({ provider: 'github', kind: 'card', label: 'Embed file preview' })
    expect(embedSourceForUrl('https://gitlab.com/a/b/-/blob/main/x.ts#L3-9')).toMatchObject({ provider: 'gitlab', kind: 'card', label: 'Embed file preview' })
    expect(embedSourceForUrl('https://git.example.com/group/sub/proj/-/blob/v1/src/x.rb')?.provider).toBe('gitlab')
  })

  it('splits a GitLab file link into project, ref, path and line range', () => {
    expect(gitlabFileForUrl('https://gitlab.com/group/sub/proj/-/blob/main/src/x.ts#L3-9')).toEqual({ provider: 'gitlab', owner: 'group/sub', repo: 'proj', ref: 'main', path: 'src/x.ts', startLine: 3, endLine: 9 })
    expect(gitlabFileForUrl('http://gitlab.example.com/a/b/-/blob/main/x.ts')).toBeUndefined()
    expect(gitlabFileForUrl('https://gitlab.com/b/-/blob/main/x.ts')).toBeUndefined()
    expect(repoFileForUrl('https://github.com/acme/web/blob/main/a.ts#L2', 'github')).toMatchObject({ provider: 'github', owner: 'acme', startLine: 2 })
  })

  it('splits a GitHub file link into repo, ref, path and line range', () => {
    expect(githubFileForUrl('https://github.com/acme/web/blob/main/src/app.ts#L1-L20')).toEqual({ owner: 'acme', repo: 'web', ref: 'main', path: 'src/app.ts', startLine: 1, endLine: 20 })
    expect(githubFileForUrl('https://github.com/acme/web/blob/abc123/README.md#L7')).toMatchObject({ path: 'README.md', startLine: 7, endLine: undefined })
    expect(githubFileForUrl('https://github.com/acme/web/blob/main/a.ts')).toMatchObject({ startLine: undefined })
  })

  it('leaves every other link alone', () => {
    for (const url of [
      'https://github.com/acme/web/pull/7', 'https://github.com/acme/web/issues/7', 'https://github.com/acme/web/commit/abc123def', 'https://github.com/acme/web', 'https://github.com/acme/web/blob/main', 'https://github.com/acme/web/tree/main/src', 'https://gist.github.com/acme/abc/blob/x/y/z',
      'https://acme.slack.com/archives/C1/p1', 'https://gitlab.com/a/b/-/merge_requests/1', 'https://gitlab.com/a/b/-/blob/main', 'https://gitlab.com/a/b/-/tree/main/src',
      'https://www.youtube.com/', 'https://evil.test/watch?v=dQw4w9WgXcQ', 'https://notyoutube.com/watch?v=dQw4w9WgXcQ',
      'https://www.tella.tv/', 'https://www.tella.tv/video/', 'https://evil.test/video/clx8k2m4n0000abc', 'https://nottella.tv/video/clx8k2m4n0000abc',
      'https://x.com/flow', 'https://x.com/flow/status/abc', 'https://x.com/flow/likes/123', 'https://evil.test/flow/status/123', 'https://notx.com/flow/status/123',
      'https://www.figma.com/', 'https://www.figma.com/community/file/1', 'https://www.figma.com/design', 'https://evilfigma.com/design/AbC/x',
      'https://miro.com/', 'https://miro.com/app/dashboard/', 'https://miro.com/app/board/', 'https://evil.test/app/board/uXjVKabc123=/',
      'javascript:alert(1)', 'not a url',
    ]) expect(embedSourceForUrl(url), url).toBeUndefined()
  })
})

function paste(editor: Editor, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : '' } })
  act(() => { editor.view.dom.dispatchEvent(event) })
}

/** Types a key into the editor as a user would (the editor holds focus after a paste). */
async function press(editor: Editor, keys: string) {
  act(() => { editor.view.dom.focus() })
  await userEvent.setup().keyboard(keys)
}

async function mount(value = '') {
  let editor: Editor | null = null
  render(<I18nProvider><IssueDescriptionEditor value={value} editorRef={next => { editor = next }}/></I18nProvider>)
  await waitFor(() => expect(editor).not.toBeNull())
  return () => editor as Editor
}

describe('pasting an embeddable link', () => {
  beforeEach(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('inserts the link and offers Tab to embed it, without embedding yet', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    const hint = await waitFor(() => {
      const found = document.querySelector('[data-embed-hint]')
      expect(found).not.toBeNull()
      return found as HTMLElement
    })
    expect(hint).toHaveTextContent('Embed videoTabKeep as linkEsc')
    expect(document.querySelector('iframe')).toBeNull()
    expect(editor().getMarkdown()).toContain('[https://youtu.be/dQw4w9WgXcQ](https://youtu.be/dQw4w9WgXcQ)')
  })

  it('turns the link into a sandboxed player with Tab and leaves an empty paragraph after it', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await screen.findByText('Embed video')
    await press(editor(), '{Tab}')
    const frame = await waitFor(() => {
      const found = document.querySelector('iframe')
      expect(found).not.toBeNull()
      return found as HTMLIFrameElement
    })
    expect(frame).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(frame.getAttribute('sandbox')).toContain('allow-scripts')
    expect(document.querySelector('[data-embed-hint]')).toBeNull()
    expect(editor().getMarkdown()).toContain('https://youtu.be/dQw4w9WgXcQ')
    const types = editor().getJSON().content?.map(node => node.type)
    expect(types).toEqual(['embed', 'paragraph'])
    expect(editor().state.selection.$from.parent.type.name).toBe('paragraph')
  })

  it('keeps the plain link with Esc and drops the hint', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://www.loom.com/share/0123456789abcdef')
    await screen.findByText('Embed video')
    await press(editor(), '{Escape}')
    await waitFor(() => expect(document.querySelector('[data-embed-hint]')).toBeNull())
    expect(document.querySelector('iframe')).toBeNull()
    expect(editor().getMarkdown()).toContain('[https://www.loom.com/share/0123456789abcdef](https://www.loom.com/share/0123456789abcdef)')
  })

  it('keeps the link when the author types on', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://www.loom.com/share/0123456789abcdef')
    await screen.findByText('Embed video')
    await press(editor(), 'x')
    await waitFor(() => expect(document.querySelector('[data-embed-hint]')).toBeNull())
    await press(editor(), '{Tab}')
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('embeds with a click on the hint', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://share.descript.com/view/AbCdEf123456')
    await userEvent.setup().click(await screen.findByRole('button', { name: /Embed video/ }))
    await waitFor(() => expect(document.querySelector('iframe')).not.toBeNull())
  })

  it('turns an X post link into a link card that loads the post\'s title and description', async () => {
    resetLinkPreviewCache()
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ url: 'https://x.com/flow/status/1790000000000000001', siteName: 'X', title: 'Flow on X', description: 'Shipping the editor', imageUrl: '' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://x.com/flow/status/1790000000000000001')
    await screen.findByText('Embed post')
    await press(editor(), '{Tab}')
    const card = await waitFor(() => {
      const found = document.querySelector('[data-link-preview="ready"]')
      expect(found).not.toBeNull()
      return found as HTMLAnchorElement
    })
    expect(card).toHaveTextContent('Flow on X')
    expect(card).toHaveTextContent('Shipping the editor')
    expect(card).toHaveAttribute('href', 'https://x.com/flow/status/1790000000000000001')
    expect(document.querySelector('iframe')).toBeNull()
    expect(String(fetchMock.mock.calls[0][0])).toBe(`/api/integrations/link-preview?url=${encodeURIComponent('https://x.com/flow/status/1790000000000000001')}`)
    vi.unstubAllGlobals()
  })

  it.each([
    ['https://www.figma.com/design/AbC123/Flow-app', 'Embed preview'],
    ['https://miro.com/app/board/uXjVKabc123=/', 'Embed board'],
    ['https://www.tella.tv/video/my-demo-clx8k2m4n0000abc', 'Embed video'],
  ])('labels the hint for %s', async (url, label) => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), url)
    expect(await screen.findByText(label)).toBeInTheDocument()
  })

  it('turns a GitHub file link into a file preview card that loads the linked lines', async () => {
    resetFilePreviewCache()
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ provider: 'github', repo: 'acme/web', path: 'src/app.ts', ref: 'main', language: 'typescript', lines: ['const a = 1', 'export default a'], startLine: 1, endLine: 2, totalLines: 40, truncated: false, htmlUrl: 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2')
    await screen.findByText('Embed file preview')
    await press(editor(), '{Tab}')
    const open = await screen.findByRole('link', { name: 'Open link' })
    expect(open).toHaveAttribute('href', 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2')
    const card = open.closest('[data-embed-provider]')?.querySelector('[data-file-preview]') as HTMLElement
    await waitFor(() => expect(card).toHaveAttribute('data-file-preview', 'ready'))
    expect(card).toHaveTextContent('acme/web')
    expect(card).toHaveTextContent('src/app.ts')
    expect(card).toHaveTextContent('L1-L2')
    expect(card.querySelector('.flow-code-block__code')?.textContent).toBe('const a = 1\nexport default a')
    expect(String(fetchMock.mock.calls[0][0])).toBe(`/api/integrations/file-preview?url=${encodeURIComponent('https://github.com/acme/web/blob/main/src/app.ts#L1-L2')}`)
    expect(document.querySelector('iframe')).toBeNull()
    vi.unstubAllGlobals()
  })

  it('does not offer an embed inside a sentence, or for pull requests, issues and commits', async () => {
    const editor = await mount('Watch ')
    editor().commands.focus('end')
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    expect(document.querySelector('[data-embed-hint]')).toBeNull()
    for (const url of ['https://github.com/acme/web/pull/7', 'https://github.com/acme/web/issues/7', 'https://github.com/acme/web/commit/abc123def']) {
      const empty = await mount()
      empty().commands.focus()
      paste(empty(), url)
      expect(document.querySelector('[data-embed-hint]'), url).toBeNull()
    }
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('offers Convert to text link on the player toolbar', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://share.descript.com/view/AbCdEf123456')
    await press(editor(), '{Tab}')
    await userEvent.setup().click(await screen.findByRole('button', { name: 'Convert to text link' }))
    await waitFor(() => expect(document.querySelector('iframe')).toBeNull())
    expect(editor().getText().trim()).toBe('https://share.descript.com/view/AbCdEf123456')
  })

  it('renders a saved player in read-only rich text', async () => {
    const doc = { type: 'doc', content: [{ type: 'embed', attrs: { src: 'https://youtu.be/dQw4w9WgXcQ', embedUrl: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ', provider: 'youtube' } }] }
    render(<I18nProvider><RichComment body="" data={doc}/></I18nProvider>)
    expect(await waitFor(() => { const found = document.querySelector('iframe'); expect(found).not.toBeNull(); return found })).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(screen.queryByRole('button', { name: 'Convert to text link' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('href', 'https://youtu.be/dQw4w9WgXcQ')
  })
})
