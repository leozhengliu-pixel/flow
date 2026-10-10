import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { resetFilePreviewCache } from '@/components/editor/embeds/file-preview'

import type { PMNode } from './document-history-diff'
import { DocumentJsonView } from './document-json-view'
import { documentSourceToDoc } from './document-json-doc'

const text = (value: string, marks?: PMNode['marks']): PMNode => ({ type: 'text', text: value, ...(marks ? { marks } : {}) })
const p = (...content: PMNode[]): PMNode => ({ type: 'paragraph', content })

describe('DocumentJsonView', () => {
  it('renders blocks and marks, escapes unsafe links and never shows raw newlines', () => {
    const doc: PMNode = { type: 'doc', content: [
      { type: 'heading', attrs: { level: 1 }, content: [text('Title')] },
      p(text('plain '), text('bold', [{ type: 'bold' }]), text(' '), text('evil', [{ type: 'link', attrs: { href: 'javascript:alert(1)' } }]), text(' '), text('good', [{ type: 'link', attrs: { href: 'https://e.com' } }])),
      { type: 'orderedList', attrs: { start: 3 }, content: [{ type: 'listItem', content: [p(text('three'))] }] },
      { type: 'taskList', content: [{ type: 'taskItem', attrs: { checked: true }, content: [p(text('done'))] }] },
      { type: 'codeBlock', content: [text('a\nb')] },
      { type: 'callout', attrs: { color: 'green', icon: '🎉' }, content: [p(text('callout body'))] },
      { type: 'details', content: [{ type: 'detailsSummary', content: [text('sum')] }, { type: 'detailsContent', content: [p(text('inner'))] }] },
      { type: 'futureBlock', content: [p(text('still shown'))] },
      p({ type: 'mention', attrs: { label: 'ann', mentionType: 'user' } }),
    ] }
    const { container } = render(<DocumentJsonView doc={doc} label="Preview"/>)
    expect(screen.getByRole('heading', { level: 1, name: 'Title' })).toBeInTheDocument()
    expect(container.querySelector('strong')).toHaveTextContent('bold')
    expect(screen.getByText('evil').closest('a')).toBeNull()
    expect(screen.getByText('good').closest('a')).toHaveAttribute('href', 'https://e.com')
    expect(container.querySelector('ol')).toHaveAttribute('start', '3')
    expect(container.querySelector('li[data-checked="true"]')).toBeTruthy()
    expect(container.querySelector('pre code')?.textContent).toBe('a\nb')
    expect(container.querySelector('aside')).toHaveAttribute('data-color', 'green')
    expect(container.querySelector('details summary')).toHaveTextContent('sum')
    expect(screen.getByText('still shown')).toBeInTheDocument()
    expect(screen.getByText('@ann')).toBeInTheDocument()
    expect(container.textContent).not.toContain('\\n')
  })

  it('marks added / removed content and the top-level blocks that changed', () => {
    const doc: PMNode = { type: 'doc', content: [
      p(text('same')),
      p(text('new', [{ type: 'diffAdded', attrs: { change: 1 } }]), text('old', [{ type: 'diffRemoved', attrs: { change: 1 } }])),
      { type: 'paragraph', attrs: { __diff: 'removed', __change: 2 }, content: [text('gone', [{ type: 'diffRemoved', attrs: { change: 2 } }])] },
    ] }
    const { container } = render(<DocumentJsonView doc={doc}/>)
    expect(container.querySelector('ins[data-change-id="1"]')).toHaveTextContent('new')
    expect(container.querySelector('del[data-change-id="1"]')).toHaveTextContent('old')
    expect(container.querySelector('p.is-diff-removed[data-change-id="2"]')).toBeTruthy()
    expect(container.querySelectorAll('[data-changed]')).toHaveLength(2)
  })

  it('shows the empty label for an empty document and falls back to Markdown without contentData', () => {
    render(<DocumentJsonView doc={documentSourceToDoc({ content: '' })} emptyLabel="Empty document"/>)
    expect(screen.getByText('Empty document')).toBeInTheDocument()
    expect(documentSourceToDoc({ content: '# Hi' }).content?.[0].type).toBe('heading')
    expect(documentSourceToDoc({ content: 'x', contentData: { type: 'doc', content: [p(text('json'))] } as unknown as Record<string, unknown> }).content?.[0].content?.[0].text).toBe('json')
  })
})

describe('DocumentJsonView embeds', () => {
  const youtube = { src: 'https://youtu.be/dQw4w9WgXcQ', embedUrl: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ', provider: 'youtube' }
  const github = { src: 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2', embedUrl: 'https://github.com/acme/web/blob/main/src/app.ts#L1-L2', provider: 'github' }
  const view = (doc: PMNode) => render(<I18nProvider><DocumentJsonView doc={doc} label="Preview"/></I18nProvider>)

  beforeEach(() => {
    resetFilePreviewCache()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ provider: 'github', repo: 'acme/web', path: 'src/app.ts', ref: 'main', language: 'typescript', lines: ['const a = 1', 'export default a'], startLine: 1, endLine: 2, totalLines: 40, truncated: false, htmlUrl: github.src }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('shows a saved player as the sandboxed player, read-only, with only Open link and Copy link', () => {
    const { container } = view({ type: 'doc', content: [p(text('before')), { type: 'embed', attrs: youtube }] })
    const frame = container.querySelector('iframe')
    expect(frame).toHaveAttribute('src', youtube.embedUrl)
    expect(frame?.getAttribute('sandbox')).toContain('allow-scripts')
    const bar = screen.getByRole('toolbar', { name: 'Embed actions' })
    expect([...bar.querySelectorAll('a, button')].map(node => node.getAttribute('aria-label'))).toEqual(['Open link', 'Copy link'])
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('href', youtube.src)
    expect(container.querySelector('[data-embed-shield]')).toBeNull()
  })

  it('shows a saved file preview as the read-only file card from the same endpoint', async () => {
    const { container } = view({ type: 'doc', content: [{ type: 'embed', attrs: github }] })
    await waitFor(() => expect(container.querySelector('[data-file-preview="ready"]')).not.toBeNull())
    expect(container.querySelector('.flow-code-block__code')?.textContent).toBe('const a = 1\nexport default a')
    expect(container).toHaveTextContent('acme/web')
    expect(String(vi.mocked(fetch).mock.calls[0][0])).toBe(`/api/integrations/file-preview?url=${encodeURIComponent(github.src)}`)
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Convert to text link' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('href', github.src)
  })

  it('keeps the change marker on an embed that was added and falls back to the link when there is no player URL', () => {
    const { container } = view({ type: 'doc', content: [
      { type: 'embed', attrs: { ...youtube, __diff: 'added', __change: 3 } },
      { type: 'embed', attrs: { src: 'https://youtu.be/abcdefghijk', embedUrl: '', provider: 'youtube' } },
      { type: 'embed', attrs: { src: 'javascript:alert(1)', embedUrl: 'javascript:alert(1)', provider: 'youtube' } },
    ] })
    expect(container.querySelectorAll('iframe')).toHaveLength(1)
    const added = container.querySelector('.history-embed') as HTMLElement
    expect(added).toHaveAttribute('data-diff', 'added')
    expect(added).toHaveAttribute('data-change-id', '3')
    expect(added).toHaveAttribute('data-changed')
    expect(screen.getByRole('link', { name: 'https://youtu.be/abcdefghijk' })).toBeInTheDocument()
    expect(screen.getByText('javascript:alert(1)').closest('a')).toBeNull()
  })
})

