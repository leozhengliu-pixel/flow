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

/** Bitmap size cap: no canvas holds more pixels than this, so memory stays bounded at any zoom. */
export const MAX_CANVAS_PIXELS = 24_000_000

/** Device pixels per CSS pixel the viewer would like (the screen's density, up to 3). */
export function wantedDensity() {
  return Math.max(1, Math.min(window.devicePixelRatio || 1, 3))
}

/** The density a canvas of this CSS size can have under the pixel cap. */
export function canvasDensity(cssWidth: number, cssHeight: number) {
  const area = Math.max(1, cssWidth * cssHeight)
  return Math.min(wantedDensity(), Math.sqrt(MAX_CANVAS_PIXELS / area))
}

/** A rectangle in CSS pixels, relative to the page's top-left corner. */
export type PageRegion = { x: number; y: number; width: number; height: number }

/** A page too big to hold at full screen density in one bitmap is rendered in tiles: only the part in view. */
export function needsTiles(cssWidth: number, cssHeight: number) {
  return cssWidth * cssHeight * wantedDensity() ** 2 > MAX_CANVAS_PIXELS
}

const TILE_GRID = 64

/** True when `inner` lies completely inside `outer`. */
export function regionCovers(outer: PageRegion, inner: PageRegion) {
  return outer.x <= inner.x && outer.y <= inner.y && outer.x + outer.width >= inner.x + inner.width && outer.y + outer.height >= inner.y + inner.height
}

/**
 * The part of a page to keep rendered for the visible rectangle `view`: the view plus as much margin around it (up to half
 * the view on each side, so the next scroll steps stay sharp) as fits under the pixel cap at screen density, snapped to a
 * 64px grid and clipped to the page. A view that alone exceeds the cap (a huge display) gets the cap by a lower density.
 */
export function planTileRegion(view: PageRegion, pageWidth: number, pageHeight: number, density = wantedDensity()): PageRegion {
  let margin = 0
  for (const factor of [0.5, 0.25, 0.1]) {
    const area = (view.width * (1 + 2 * factor)) * (view.height * (1 + 2 * factor))
    if (area * density * density <= MAX_CANVAS_PIXELS) { margin = factor; break }
  }
  const snapDown = (value: number) => Math.floor(value / TILE_GRID) * TILE_GRID
  const snapUp = (value: number) => Math.ceil(value / TILE_GRID) * TILE_GRID
  const x0 = Math.max(0, snapDown(view.x - view.width * margin))
  const y0 = Math.max(0, snapDown(view.y - view.height * margin))
  const x1 = Math.min(pageWidth, snapUp(view.x + view.width * (1 + margin)))
  const y1 = Math.min(pageHeight, snapUp(view.y + view.height * (1 + margin)))
  return { x: x0, y: y0, width: Math.max(1, x1 - x0), height: Math.max(1, y1 - y0) }
}
