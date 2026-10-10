import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { RichComment } from '@/components/activity/rich-comment'
import { resetLinkPreviewCache } from './link-preview'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'link-card-test' }))

const FIGMA = 'https://www.figma.com/proto/LKQ4FJ4bTnCSjedbRpk931/Sample-File'
const doc = (provider: string, src: string) => ({ type: 'doc', content: [{ type: 'embed', attrs: { src, embedUrl: src, provider } }] })

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function renderCard(provider = 'figma', src = FIGMA) {
  return render(<I18nProvider><RichComment body="" data={doc(provider, src)}/></I18nProvider>)
}

describe('link preview card (Figma / X)', () => {
  beforeEach(() => resetLinkPreviewCache())
  afterEach(() => vi.unstubAllGlobals())

  it('draws the page title, description, link and preview image as one link that opens in a new tab', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) => respond({ url: FIGMA, siteName: 'Figma', title: 'Figma: The collaborative canvas for design, code, and AI', description: 'Figma is the canvas where design, code, and AI come together.', imageUrl: 'https://static.figma.com/og.png' }))
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    const card = await waitFor(() => {
      const found = document.querySelector('[data-link-preview="ready"]')
      expect(found).not.toBeNull()
      return found as HTMLAnchorElement
    })
    expect(card.tagName).toBe('A')
    expect(card).toHaveAttribute('href', FIGMA)
    expect(card).toHaveAttribute('target', '_blank')
    expect(card).toHaveAttribute('rel', 'noopener noreferrer')
    const rows = [...card.querySelectorAll('span span')].map(node => node.textContent)
    expect(rows).toEqual(['Figma: The collaborative canvas for design, code, and AI', 'Figma is the canvas where design, code, and AI come together.', FIGMA])
    const image = card.querySelector('img') as HTMLImageElement
    expect(image).toHaveAttribute('src', 'https://static.figma.com/og.png')
    expect(image).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(image.alt).toBe('')
    expect(document.querySelector('iframe')).toBeNull()
    expect(String(fetchMock.mock.calls[0][0])).toBe(`/api/integrations/link-preview?url=${encodeURIComponent(FIGMA)}`)
    // Read-only renderings (comments, history) keep only Open link and Copy link on the toolbar.
    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute('href', FIGMA)
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  })

  it('shows the site name over the plain link while loading and when the page has no tags', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    const { unmount } = renderCard()
    const loading = document.querySelector('[data-link-preview="loading"]') as HTMLElement
    expect(loading).toHaveTextContent(`Figma${FIGMA}`)
    expect(loading.querySelector('img')).toBeNull()
    unmount()

    resetLinkPreviewCache()
    vi.stubGlobal('fetch', vi.fn(async () => respond({ url: FIGMA, siteName: 'Figma', title: '', description: '', imageUrl: '' })))
    renderCard()
    const ready = await waitFor(() => {
      const found = document.querySelector('[data-link-preview="ready"]')
      expect(found).not.toBeNull()
      return found as HTMLElement
    })
    expect(ready).toHaveTextContent(`Figma${FIGMA}`)
    expect(ready.querySelector('img')).toBeNull()
  })

  it('keeps the plain card when the preview request fails, and drops a broken image', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => respond({ error: 'Could not load the link preview', code: 'upstream' }, 502)))
    const { unmount } = renderCard('twitter', 'https://x.com/jack/status/20')
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled())
    expect(document.querySelector('[data-link-preview="loading"]')).toHaveTextContent('Xhttps://x.com/jack/status/20')
    unmount()

    resetLinkPreviewCache()
    vi.stubGlobal('fetch', vi.fn(async () => respond({ url: FIGMA, siteName: 'Figma', title: 'Sample', description: '', imageUrl: 'https://static.figma.com/missing.png' })))
    renderCard()
    const image = await waitFor(() => {
      const found = document.querySelector('[data-link-preview="ready"] img')
      expect(found).not.toBeNull()
      return found as HTMLImageElement
    })
    image.dispatchEvent(new Event('error'))
    await waitFor(() => expect(document.querySelector('[data-link-preview] img')).toBeNull())
  })

  it('asks once for the same link shown twice', async () => {
    const fetchMock = vi.fn(async () => respond({ url: FIGMA, siteName: 'Figma', title: 'Sample', description: '', imageUrl: '' }))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RichComment body="" data={{ type: 'doc', content: [{ type: 'embed', attrs: { src: FIGMA, embedUrl: FIGMA, provider: 'figma' } }, { type: 'embed', attrs: { src: FIGMA, embedUrl: FIGMA, provider: 'figma' } }] }}/></I18nProvider>)
    await waitFor(() => expect(document.querySelectorAll('[data-link-preview="ready"]')).toHaveLength(2))
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('renders a saved Figma frame (an older embed) as a link card too', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => respond({ url: FIGMA, siteName: 'Figma', title: 'Sample', description: '', imageUrl: '' })))
    render(<I18nProvider><RichComment body="" data={{ type: 'doc', content: [{ type: 'embed', attrs: { src: FIGMA, embedUrl: `https://www.figma.com/embed?embed_host=flow&url=${encodeURIComponent(FIGMA)}`, provider: 'figma' } }] }}/></I18nProvider>)
    await screen.findByText('Sample')
    expect(document.querySelector('iframe')).toBeNull()
  })
})
