import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import { zhCN } from '@/i18n/translations'
import type { CodingArtifact } from '@/types/coding-sessions'

const lightbox = vi.hoisted(() => ({ openLightbox: vi.fn((..._args: unknown[]) => true) }))
vi.mock('@/components/editor/lightbox-bridge', () => lightbox)
const api = vi.hoisted(() => ({ fetchCodingSessionArtifacts: vi.fn<(sessionId: string) => Promise<CodingArtifact[]>>() }))
vi.mock('@/lib/coding-sessions-api', () => api)

import { CodingSessionArtifacts } from './coding-session-artifacts'
import { buildArtifactSections } from './coding-artifacts-model'
import componentSource from './coding-session-artifacts.tsx?raw'

let counter = 0
function artifact(overrides: Partial<CodingArtifact> = {}): CodingArtifact {
  counter += 1
  return {
    id: `a${counter}`,
    sessionId: 's1',
    kind: 'screenshot',
    label: 'shot',
    url: `/signed/a${counter}.png`,
    contentUrl: `/api/coding/artifacts/a${counter}/content`,
    contentType: 'image/png',
    sizeBytes: 1000,
    createdAt: `2026-10-18T10:00:${String(counter % 60).padStart(2, '0')}Z`,
    ...overrides,
  }
}

const renderGallery = (props: Parameters<typeof CodingSessionArtifacts>[0]) => render(<I18nProvider><CodingSessionArtifacts {...props}/></I18nProvider>)

beforeEach(() => {
  lightbox.openLightbox.mockClear()
  api.fetchCodingSessionArtifacts.mockReset()
})

describe('CodingSessionArtifacts', () => {
  it('pairs before and after screenshots by group and keeps other shots in the grid', () => {
    const items = [
      artifact({ label: 'before', group: 'settings-page' }),
      artifact({ label: 'after', group: 'login' }),
      artifact({ label: 'After', group: 'settings-page' }),
      artifact({ label: 'Empty state' }),
      artifact({ kind: 'log', label: 'console' }),
    ]
    const [section] = buildArtifactSections(items)
    expect(section.pairs.map(pair => [pair.group, pair.before?.id, pair.after?.id])).toEqual([
      ['settings-page', items[0].id, items[2].id],
      ['login', undefined, items[1].id],
    ])
    expect(section.shots.map(shot => shot.label)).toEqual(['Empty state'])

    renderGallery({ sessionId: 's1', artifacts: items })
    expect(screen.getByRole('heading', { name: 'Screenshots' })).toBeInTheDocument()
    expect(screen.getByText('4')).toBeInTheDocument()
    const settings = screen.getByRole('group', { name: 'settings-page' })
    expect(within(settings).getByRole('img', { name: 'Before: settings-page' })).toBeInTheDocument()
    expect(within(settings).getByRole('img', { name: 'After: settings-page' })).toBeInTheDocument()
    expect(within(settings).getByRole('button', { name: 'Slider' })).toBeInTheDocument()
    // A pair missing one half shows the single image with its caption and no mode toggle.
    const login = screen.getByRole('group', { name: 'login' })
    expect(within(login).getAllByRole('img')).toHaveLength(1)
    expect(within(login).getByText('After')).toBeInTheDocument()
    expect(within(login).queryByRole('button', { name: 'Slider' })).toBeNull()
    expect(screen.getByRole('img', { name: 'Empty state' })).toBeInTheDocument()
    expect(screen.queryByText('console')).toBeNull()
  })

  it('switches between side by side and slider, and the range moves the clip', async () => {
    const user = userEvent.setup()
    renderGallery({ sessionId: 's1', artifacts: [artifact({ label: 'before' }), artifact({ label: 'after' })] })
    expect(screen.getByRole('button', { name: 'Side by side' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByRole('slider')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Slider' }))
    expect(screen.getByRole('button', { name: 'Slider' })).toHaveAttribute('aria-pressed', 'true')
    const range = screen.getByRole('slider', { name: 'Comparison position' })
    const after = screen.getByTestId('comparison-after')
    expect(after.style.clipPath).toBe('inset(0 0 0 50%)')
    fireEvent.change(range, { target: { value: '30' } })
    expect(after.style.clipPath).toBe('inset(0 0 0 30%)')

    await user.click(screen.getByRole('button', { name: 'Side by side' }))
    expect(screen.queryByRole('slider')).toBeNull()
    expect(screen.getByRole('img', { name: 'Before' })).toBeInTheDocument()
  })

  it('opens the lightbox with every gallery image at the clicked one', async () => {
    const user = userEvent.setup()
    const items = [artifact({ label: 'before' }), artifact({ label: 'after' }), artifact({ label: 'Modal open' })]
    renderGallery({ sessionId: 's1', artifacts: items })
    await user.click(screen.getByRole('button', { name: /Modal open/ }))
    expect(lightbox.openLightbox).toHaveBeenCalledWith([
      { src: items[0].contentUrl, alt: 'Before', title: 'Before' },
      { src: items[1].contentUrl, alt: 'After', title: 'After' },
      { src: items[2].contentUrl, alt: 'Modal open', title: 'Modal open' },
    ], 2)
  })

  it('falls back to a new tab when no lightbox is registered', async () => {
    const user = userEvent.setup()
    lightbox.openLightbox.mockReturnValueOnce(false)
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const item = artifact({ label: 'Dashboard' })
    renderGallery({ sessionId: 's1', artifacts: [item] })
    await user.click(screen.getByRole('button', { name: /Dashboard/ }))
    expect(open).toHaveBeenCalledWith(item.contentUrl, '_blank', 'noopener')
    open.mockRestore()
  })

  it('renders recordings with the inline player', () => {
    const { container } = renderGallery({ sessionId: 's1', artifacts: [artifact({ kind: 'video', label: 'checkout flow', contentType: 'video/webm' })] })
    expect(screen.getByRole('heading', { name: 'Recordings' })).toBeInTheDocument()
    expect(container.querySelector('video')).toHaveAttribute('src', expect.stringContaining('/content'))
    expect(screen.getByText('checkout flow')).toBeInTheDocument()
  })

  it('prefers contentUrl over the signed url, and uses url when there is no contentUrl', () => {
    const stable = artifact({ label: 'Stable' })
    const signed = artifact({ label: 'Signed', contentUrl: undefined })
    renderGallery({ sessionId: 's1', artifacts: [stable, signed] })
    expect(screen.getByRole('img', { name: 'Stable' })).toHaveAttribute('src', stable.contentUrl)
    expect(screen.getByRole('img', { name: 'Signed' })).toHaveAttribute('src', signed.url)
  })

  it('refetches the list once when a signed url fails to load', async () => {
    const signed = artifact({ label: 'Signed', contentUrl: undefined })
    api.fetchCodingSessionArtifacts.mockResolvedValue([{ ...signed, url: '/signed/fresh.png' }])
    renderGallery({ sessionId: 's1', artifacts: [signed] })
    fireEvent.error(screen.getByRole('img', { name: 'Signed' }))
    await waitFor(() => expect(screen.getByRole('img', { name: 'Signed' })).toHaveAttribute('src', '/signed/fresh.png'))
    fireEvent.error(screen.getByRole('img', { name: 'Signed' }))
    expect(api.fetchCodingSessionArtifacts).toHaveBeenCalledTimes(1)
  })

  it('fetches when artifacts are not provided and when refreshKey changes', async () => {
    api.fetchCodingSessionArtifacts.mockResolvedValue([artifact({ label: 'Fetched' })])
    const { rerender } = renderGallery({ sessionId: 's1', refreshKey: 1 })
    expect(await screen.findByRole('img', { name: 'Fetched' })).toBeInTheDocument()
    expect(api.fetchCodingSessionArtifacts).toHaveBeenCalledWith('s1')
    api.fetchCodingSessionArtifacts.mockResolvedValue([artifact({ label: 'Refreshed' })])
    rerender(<I18nProvider><CodingSessionArtifacts refreshKey={2} sessionId="s1"/></I18nProvider>)
    expect(await screen.findByRole('img', { name: 'Refreshed' })).toBeInTheDocument()
    expect(api.fetchCodingSessionArtifacts).toHaveBeenCalledTimes(2)
  })

  it('does not fetch when initial artifacts are given', () => {
    renderGallery({ sessionId: 's1', artifacts: [artifact()], refreshKey: 'x' })
    expect(api.fetchCodingSessionArtifacts).not.toHaveBeenCalled()
  })

  it('shows an error with a Retry that refetches', async () => {
    const user = userEvent.setup()
    api.fetchCodingSessionArtifacts.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce([artifact({ label: 'Recovered' })])
    renderGallery({ sessionId: 's1' })
    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load screenshots")
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('img', { name: 'Recovered' })).toBeInTheDocument()
    expect(api.fetchCodingSessionArtifacts).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows the empty state, not an error, for an empty list or a session it cannot read; only a real failure is an error', async () => {
    api.fetchCodingSessionArtifacts.mockResolvedValueOnce([])
    const empty = renderGallery({ sessionId: 'never-ran' })
    await waitFor(() => expect(api.fetchCodingSessionArtifacts).toHaveBeenCalledWith('never-ran'))
    await waitFor(() => expect(empty.container).toBeEmptyDOMElement())
    expect(screen.queryByRole('alert')).toBeNull()
    empty.unmount()

    api.fetchCodingSessionArtifacts.mockRejectedValueOnce(new ApiError('Coding session not found', 404))
    const missing = renderGallery({ sessionId: 'gone' })
    await waitFor(() => expect(missing.container).toBeEmptyDOMElement())
    expect(screen.queryByText("Couldn't load screenshots")).toBeNull()
    missing.unmount()

    api.fetchCodingSessionArtifacts.mockRejectedValueOnce(new ApiError('Could not read session artifacts', 500))
    renderGallery({ sessionId: 'broken' })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load screenshots")
    expect(within(alert).getByRole('button', { name: 'Retry' })).toBeVisible()
  })

  it('renders nothing without screenshots or videos', async () => {
    const { container } = renderGallery({ sessionId: 's1', artifacts: [artifact({ kind: 'log', label: 'console' })] })
    expect(container).toBeEmptyDOMElement()
    api.fetchCodingSessionArtifacts.mockResolvedValue([])
    const fetched = renderGallery({ sessionId: 's2' })
    await waitFor(() => expect(fetched.container).toBeEmptyDOMElement())
  })

  it('groups by turn only when more than one turn is present', () => {
    const { unmount } = renderGallery({ sessionId: 's1', artifacts: [artifact({ turnId: 't1', label: 'One' }), artifact({ turnId: 't1', label: 'Two' })] })
    expect(screen.queryByRole('heading', { name: /Turn/ })).toBeNull()
    unmount()
    renderGallery({ sessionId: 's1', artifacts: [artifact({ turnId: 't2', label: 'Late', createdAt: '2026-10-18T11:00:00Z' }), artifact({ turnId: 't1', label: 'Early', createdAt: '2026-10-18T09:00:00Z' })] })
    const headings = screen.getAllByRole('heading', { level: 4 }).map(heading => heading.textContent)
    expect(headings).toEqual(['Turn 1', 'Turn 2'])
    const images = screen.getAllByRole('img').map(image => image.getAttribute('alt'))
    expect(images).toEqual(['Early', 'Late'])
  })

  it('has a Chinese translation for every t() string', () => {
    const missing = [...componentSource.matchAll(/\bt\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1\s*\)/g)]
      .map(match => match[2].replace(/\\(["'])/g, '$1'))
      .filter(text => !zhCN[text])
    expect(missing).toEqual([])
  })
})
