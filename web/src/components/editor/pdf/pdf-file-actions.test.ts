import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const toast = vi.hoisted(() => ({ info: vi.fn(), error: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast }))

import { absoluteFileUrl, copyPdfLink, isPdfFile, pdfText, printPdf } from './pdf-file-actions'
import { openPdfViewer, registerPdfViewerOpener } from './pdf-bridge'

beforeEach(() => {
  toast.info.mockReset()
  toast.error.mockReset()
})
afterEach(() => {
  vi.restoreAllMocks()
  document.documentElement.removeAttribute('data-locale')
  registerPdfViewerOpener(null)
  document.querySelectorAll('iframe').forEach(frame => frame.remove())
})

describe('PDF file actions', () => {
  it('detects PDFs by type or extension', () => {
    expect(isPdfFile('application/pdf', 'a')).toBe(true)
    expect(isPdfFile('', 'Report.PDF')).toBe(true)
    expect(isPdfFile('text/markdown', 'a.md')).toBe(false)
  })

  it('copies the absolute file URL and confirms with a toast', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await copyPdfLink('/uploads/asset_2.pdf')
    expect(writeText).toHaveBeenCalledWith(absoluteFileUrl('/uploads/asset_2.pdf'))
    expect(absoluteFileUrl('/uploads/asset_2.pdf')).toBe(`${window.location.origin}/uploads/asset_2.pdf`)
    expect(toast.info).toHaveBeenCalledWith('PDF URL copied to clipboard')
  })

  it('reports a failed copy', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) }, configurable: true })
    await copyPdfLink('/uploads/a.pdf')
    expect(toast.error).toHaveBeenCalledWith('Could not copy to clipboard')
  })

  it('translates its chrome from the document locale', () => {
    expect(pdfText('View PDF')).toBe('View PDF')
    document.documentElement.dataset.locale = 'zh-CN'
    expect(pdfText('Page 2 of 5')).not.toBe('Page 2 of 5')
  })

  it('has Chinese for every label of the toolbar and viewer', async () => {
    const { zhCN } = await import('@/i18n/translations')
    for (const text of ['View PDF', 'Download', 'Copy link', 'Print', 'Delete', 'Close', 'PDF actions', 'PDF preview', 'PDF URL copied to clipboard', 'Could not copy to clipboard', 'Could not print this PDF', 'Could not load this PDF', 'Loading PDF…', 'Zoom out', 'Zoom in', 'Previous page', 'Next page']) {
      expect(zhCN[text], text).toBeTruthy()
    }
  })

  it('prints through a hidden frame holding the PDF and removes it afterwards', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(new Uint8Array([37, 80, 68, 70])))
    const create = vi.fn(() => 'blob:print/1')
    const revoke = vi.fn()
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke })
    await printPdf('/uploads/asset_2.pdf')
    const frame = document.querySelector('iframe')
    expect(frame).not.toBeNull()
    expect(frame?.style.width).toBe('0px')
    expect(frame?.getAttribute('src')).toBe('blob:print/1')
    expect(frame?.getAttribute('aria-hidden')).toBe('true')
    // The browser's print dialog belongs to the frame's own window.
    const target = frame!.contentWindow!
    const print = vi.fn()
    target.print = print
    target.focus = vi.fn()
    frame!.dispatchEvent(new Event('load'))
    expect(print).toHaveBeenCalledTimes(1)
    target.dispatchEvent(new Event('afterprint'))
    expect(document.querySelector('iframe')).toBeNull()
    expect(revoke).toHaveBeenCalledWith('blob:print/1')
  })

  it('tells the person when the file cannot be printed', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('no', { status: 404 }))
    await printPdf('/uploads/missing.pdf')
    expect(toast.error).toHaveBeenCalledWith('Could not print this PDF')
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('opens the registered viewer, or a new tab when there is none', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    expect(openPdfViewer({ src: '/uploads/a.pdf', title: 'a.pdf' })).toBe(false)
    expect(open).toHaveBeenCalledWith('/uploads/a.pdf', '_blank', 'noopener')
    const handler = vi.fn()
    registerPdfViewerOpener(handler)
    expect(openPdfViewer({ src: '/uploads/a.pdf', title: 'a.pdf' })).toBe(true)
    expect(handler).toHaveBeenCalledWith({ src: '/uploads/a.pdf', title: 'a.pdf' })
  })
})
