import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const document_ = vi.hoisted(() => {
  const page = {
    getViewport: ({ scale }: { scale: number }) => ({ width: 612 * scale, height: 792 * scale }),
    render: vi.fn<(options: { canvas: HTMLCanvasElement }) => { promise: Promise<void>; cancel: () => void }>(() => ({ promise: Promise.resolve(), cancel: vi.fn() })),
  }
  return { numPages: 3, getPage: vi.fn(async () => page), loadingTask: { destroy: vi.fn(async () => undefined) } }
})
const actions = vi.hoisted(() => ({ copyPdfLink: vi.fn(), downloadPdf: vi.fn(), printPdf: vi.fn() }))

vi.mock('./pdf-document', async importOriginal => ({
  ...(await importOriginal<typeof import('./pdf-document')>()),
  loadPdfDocument: vi.fn(async () => document_),
}))
vi.mock('./pdf-file-actions', async importOriginal => ({
  ...(await importOriginal<typeof import('./pdf-file-actions')>()),
  ...actions,
}))

import { PdfViewerDialog } from './pdf-viewer-dialog'
import { loadPdfDocument } from './pdf-document'

const scrollTo = vi.fn()

beforeEach(() => {
  scrollTo.mockReset()
  HTMLElement.prototype.scrollTo = scrollTo as unknown as typeof HTMLElement.prototype.scrollTo
  HTMLCanvasElement.prototype.getContext = vi.fn(() => ({ drawImage: vi.fn() })) as unknown as typeof HTMLCanvasElement.prototype.getContext
})
afterEach(() => {
  vi.unstubAllGlobals()
  cleanup()
  vi.clearAllMocks()
})

async function open(onClose = vi.fn()) {
  render(<PdfViewerDialog onClose={onClose} src="/uploads/asset_2.pdf" title="report.pdf"/>)
  await screen.findByText('Page 1 of 3')
  return onClose
}

describe('PdfViewerDialog', () => {
  it('is a modal dialog named after the file with a labelled page group, and takes focus', async () => {
    await open()
    const dialog = screen.getByRole('dialog', { name: 'report.pdf' })
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(dialog)
    expect(screen.getByRole('group', { name: 'report.pdf, PDF preview' })).toBeTruthy()
    expect(screen.getAllByRole('img', { name: /^Page \d$/ })).toHaveLength(3)
  })

  it('shows 100% to start and steps through the reference zoom levels, disabling the ends', async () => {
    const user = userEvent.setup()
    await open()
    const levels = [50, 75, 100, 125, 150, 200, 300, 400]
    expect(screen.getByText('100%')).toBeTruthy()
    for (const level of levels.slice(3)) {
      await user.click(screen.getByRole('button', { name: 'Zoom in' }))
      expect(screen.getByText(`${level}%`)).toBeTruthy()
    }
    expect(screen.getByRole('button', { name: 'Zoom in' }).hasAttribute('disabled')).toBe(true)
    for (const level of [...levels].reverse().slice(1)) {
      await user.click(screen.getByRole('button', { name: 'Zoom out' }))
      expect(screen.getByText(`${level}%`)).toBeTruthy()
    }
    expect(screen.getByRole('button', { name: 'Zoom out' }).hasAttribute('disabled')).toBe(true)
  })

  it('sizes pages from the PDF (816px wide at 100%, 8px between pages is layout) and scales with zoom', async () => {
    const user = userEvent.setup()
    await open()
    const pages = () => [...document.querySelectorAll<HTMLElement>('.pdf-viewer-page')]
    await waitFor(() => expect(pages()[0].style.width).toBe('816px'))
    expect(pages()[0].style.height).toBe('1056px')
    await user.click(screen.getByRole('button', { name: 'Zoom out' }))
    await waitFor(() => expect(pages()[0].style.width).toBe('612px'))
  })

  it('shows the app tooltip on every header button except Close, with the label only', async () => {
    await open()
    for (const name of ['Zoom out', 'Zoom in', 'Previous page', 'Next page', 'Download', 'Copy link', 'Print']) {
      expect(screen.getByRole('button', { name }).getAttribute('data-tooltip-text')).toBe(name)
    }
    expect(screen.getByRole('button', { name: 'Close' }).hasAttribute('data-tooltip-text')).toBe(false)
  })

  it('renders a page that is too big for one bitmap (high zoom on a dense screen) as a capped tile, not the whole page', async () => {
    vi.stubGlobal('devicePixelRatio', 2)
    const user = userEvent.setup()
    await open()
    expect(document.querySelectorAll('[data-tiled]')).toHaveLength(0)
    for (let step = 0; step < 5; step += 1) await user.click(screen.getByRole('button', { name: 'Zoom in' }))
    await waitFor(() => expect(document.querySelectorAll('[data-tiled]').length).toBeGreaterThan(0))
    const page = document.querySelector<HTMLElement>('[data-tiled]')!
    const canvas = page.querySelector('canvas')!
    await waitFor(() => expect(canvas.width).toBeGreaterThan(1))
    expect(canvas.width * canvas.height).toBeLessThanOrEqual(24_000_000)
    expect(canvas.width * canvas.height).toBeLessThan(parseFloat(page.style.width) * parseFloat(page.style.height) * 4)
    // The tile is drawn at the full screen density: two device pixels per CSS pixel.
    expect(canvas.width / parseFloat(canvas.style.width)).toBeCloseTo(2, 1)
  })

  it('moves between pages with the arrows and keeps the first page\'s previous button off', async () => {
    const user = userEvent.setup()
    await open()
    expect(screen.getByRole('button', { name: 'Previous page' }).hasAttribute('disabled')).toBe(true)
    await user.keyboard('{ArrowRight}')
    expect(scrollTo).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Next page' }))
    expect(scrollTo).toHaveBeenCalledTimes(2)
  })

  it('closes with Escape and with the close button', async () => {
    const user = userEvent.setup()
    const onClose = await open()
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('downloads, copies the link and prints from the header', async () => {
    const user = userEvent.setup()
    await open()
    await user.click(screen.getByRole('button', { name: 'Download' }))
    await user.click(screen.getByRole('button', { name: 'Copy link' }))
    await user.click(screen.getByRole('button', { name: 'Print' }))
    expect(actions.downloadPdf).toHaveBeenCalledWith('/uploads/asset_2.pdf', 'report.pdf')
    expect(actions.copyPdfLink).toHaveBeenCalledWith('/uploads/asset_2.pdf')
    expect(actions.printPdf).toHaveBeenCalledWith('/uploads/asset_2.pdf')
  })

  it('keeps Tab inside the dialog', async () => {
    const user = userEvent.setup()
    await open()
    const close = screen.getByRole('button', { name: 'Close' })
    close.focus()
    await user.tab()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Zoom out' }))
  })

  it('says so when the file cannot be loaded and frees the document on close', async () => {
    vi.mocked(loadPdfDocument).mockRejectedValueOnce(new Error('404'))
    render(<PdfViewerDialog onClose={vi.fn()} src="/uploads/missing.pdf" title="missing.pdf"/>)
    expect((await screen.findByRole('alert')).textContent).toBe('Could not load this PDF')
    cleanup()
    vi.mocked(loadPdfDocument).mockResolvedValueOnce(document_ as never)
    await act(async () => { render(<PdfViewerDialog onClose={vi.fn()} src="/uploads/a.pdf" title="a.pdf"/>) })
    await screen.findByText('Page 1 of 3')
    cleanup()
    expect(document_.loadingTask.destroy).toHaveBeenCalled()
  })
})
