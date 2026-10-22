import { toast } from '@/lib/toast'
import { translateToChinese } from '@/i18n/translate'

/** Chrome of the PDF card toolbar and viewer is plain DOM/portal outside the page translator, so it follows the document locale itself. */
export function pdfText(source: string) {
  if (typeof document === 'undefined' || document.documentElement.dataset.locale !== 'zh-CN') return source
  return translateToChinese(source)
}

export function absoluteFileUrl(src: string) {
  try { return new URL(src, window.location.origin).href } catch { return src }
}

/** Copies the absolute file URL (the file itself, as the reference app does) and confirms with a toast. */
export async function copyPdfLink(src: string) {
  try {
    await navigator.clipboard.writeText(absoluteFileUrl(src))
    toast.info(pdfText('PDF URL copied to clipboard'))
  } catch {
    toast.error(pdfText('Could not copy to clipboard'))
  }
}

export function downloadPdf(src: string, name: string) {
  const anchor = document.createElement('a')
  anchor.href = src
  anchor.download = name
  anchor.rel = 'noreferrer'
  anchor.click()
}

const PRINT_CLEANUP_MS = 120_000

/**
 * Prints only the PDF: it is loaded in a hidden frame from a blob URL (uploads carry a CSP that blocks the browser's
 * PDF viewer in frames, a blob URL does not) and the frame's own print dialog is opened. The frame is removed after
 * printing, or after two minutes if the browser never reports it.
 */
export async function printPdf(src: string) {
  let objectUrl = ''
  try {
    let frameSrc = src
    if (!src.startsWith('blob:')) {
      const response = await fetch(src, { credentials: 'same-origin' })
      if (!response.ok) throw new Error('print')
      objectUrl = URL.createObjectURL(new Blob([await response.arrayBuffer()], { type: 'application/pdf' }))
      frameSrc = objectUrl
    }
    const frame = document.createElement('iframe')
    frame.setAttribute('aria-hidden', 'true')
    frame.tabIndex = -1
    frame.style.cssText = 'position:fixed;width:0;height:0;border:0;'
    let timer = 0
    // Printing moves focus into the frame; it goes back to what had it (the viewer, the editor) when the frame is removed.
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const cleanup = () => {
      window.clearTimeout(timer)
      frame.remove()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      if (previous?.isConnected) previous.focus({ preventScroll: true })
    }
    frame.addEventListener('load', () => {
      const target = frame.contentWindow
      if (!target) { cleanup(); return }
      target.addEventListener('afterprint', cleanup, { once: true })
      timer = window.setTimeout(cleanup, PRINT_CLEANUP_MS)
      target.focus()
      target.print()
    }, { once: true })
    frame.src = frameSrc
    document.body.append(frame)
  } catch {
    if (objectUrl) URL.revokeObjectURL(objectUrl)
    toast.error(pdfText('Could not print this PDF'))
  }
}

export function isPdfFile(contentType: string, title: string) {
  return contentType === 'application/pdf' || /\.pdf$/i.test(title)
}
