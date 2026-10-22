import type { PDFDocumentProxy } from 'pdfjs-dist'

/**
 * PDF.js is large, so it is only fetched when a PDF card first needs a page: the library and its worker are separate
 * lazy chunks that no other screen loads. The worker file ships with the package (no CDN).
 */
let library: Promise<typeof import('pdfjs-dist')> | null = null

export function loadPdfLibrary() {
  library ??= (async () => {
    const [pdfjs, worker] = await Promise.all([
      import('pdfjs-dist'),
      import('pdfjs-dist/build/pdf.worker.min.mjs?url'),
    ])
    pdfjs.GlobalWorkerOptions.workerSrc = worker.default
    return pdfjs
  })().catch(error => {
    library = null
    throw error
  })
  return library
}

/** Reads the file bytes. Uploads are same-origin, `blob:` sources are files that are still being uploaded. */
export async function fetchPdfBytes(src: string, signal?: AbortSignal) {
  const response = await fetch(src, { credentials: 'same-origin', signal })
  if (!response.ok) throw new Error(`PDF request failed (${response.status})`)
  return new Uint8Array(await response.arrayBuffer())
}

/** The bytes are fetched first (the library is only loaded for a file that can be read), then parsed in the worker. */
export async function loadPdfDocument(src: string, signal?: AbortSignal): Promise<PDFDocumentProxy> {
  const bytes = await fetchPdfBytes(src, signal)
  const pdfjs = await loadPdfLibrary()
  if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
  const task = pdfjs.getDocument({ data: bytes })
  signal?.addEventListener('abort', () => { void task.destroy() }, { once: true })
  return task.promise
}

/** Frees the worker-side document; safe to call more than once. */
export function destroyPdfDocument(document: PDFDocumentProxy) {
  void document.loadingTask.destroy().catch(() => undefined)
}

/** Zoom steps of the viewer's controls (percent), as in the reference app. */
export const PDF_ZOOM_LEVELS = [50, 75, 100, 125, 150, 200, 300, 400]

/** CSS pixels per PDF point at 100% (a letter page is 816px wide). */
export const PDF_POINT_TO_CSS = 96 / 72

/** Bitmap size cap: very large zooms render at a lower density instead of allocating gigabyte canvases. */
const MAX_CANVAS_PIXELS = 24_000_000

export function canvasDensity(cssWidth: number, cssHeight: number) {
  const wanted = Math.max(1, Math.min(window.devicePixelRatio || 1, 3))
  const area = Math.max(1, cssWidth * cssHeight)
  return Math.min(wanted, Math.sqrt(MAX_CANVAS_PIXELS / area))
}
