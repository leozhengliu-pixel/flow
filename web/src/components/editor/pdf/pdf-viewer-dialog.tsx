import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import type { PDFDocumentProxy } from 'pdfjs-dist'
import { canvasDensity, destroyPdfDocument, loadPdfDocument, needsTiles, PDF_POINT_TO_CSS, PDF_ZOOM_LEVELS, planTileRegion, regionCovers, type PageRegion } from './pdf-document'
import { copyPdfLink, downloadPdf, pdfText, printPdf } from './pdf-file-actions'
import { CLOSE_GLYPH, NEXT_GLYPH, PREVIOUS_GLYPH, PRINT_GLYPH, ZOOM_IN_GLYPH, ZOOM_OUT_GLYPH, type GlyphPath } from './pdf-icons'
import { downloadIcon, linkIcon } from '@/components/issue/editor/image-extension'
import './pdf-viewer.css'

const DEFAULT_ZOOM_INDEX = 2
const FALLBACK_PAGE = { width: 612, height: 792 }

type Insets = { left: number; right: number }
type PageSize = { width: number; height: number }

export function PdfViewerDialog({ src, title, insets, onClose }: { src: string; title: string; insets?: Insets; onClose: () => void }) {
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [pdf, setPdf] = useState<PDFDocumentProxy | null>(null)
  const [sizes, setSizes] = useState<PageSize[]>([])
  const [zoomIndex, setZoomIndex] = useState(DEFAULT_ZOOM_INDEX)
  const [current, setCurrent] = useState(1)
  const [scroller, setScroller] = useState<HTMLDivElement | null>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  const pageRefs = useRef<Array<HTMLDivElement | null>>([])
  const keepRatio = useRef<number | null>(null)
  const frame = useRef(0)
  const zoom = PDF_ZOOM_LEVELS[zoomIndex]
  const count = pdf?.numPages ?? 0

  useEffect(() => {
    dialogRef.current?.focus({ preventScroll: true })
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    let loaded: PDFDocumentProxy | null = null
    setStatus('loading')
    setPdf(null)
    setSizes([])
    setCurrent(1)
    loadPdfDocument(src, controller.signal).then(async file => {
      if (controller.signal.aborted) { destroyPdfDocument(file); return }
      loaded = file
      setPdf(file)
      setStatus('ready')
      // Page sizes arrive in batches so a long document lays out its true heights without blocking the first pages.
      const measured: PageSize[] = []
      for (let start = 1; start <= file.numPages; start += 24) {
        const numbers = Array.from({ length: Math.min(24, file.numPages - start + 1) }, (_, index) => start + index)
        const batch = await Promise.all(numbers.map(async number => {
          const viewport = (await file.getPage(number)).getViewport({ scale: 1 })
          return { width: viewport.width, height: viewport.height }
        }))
        if (controller.signal.aborted) return
        measured.push(...batch)
        setSizes([...measured])
      }
    }).catch(() => { if (!controller.signal.aborted) setStatus('error') })
    return () => {
      controller.abort()
      if (loaded) destroyPdfDocument(loaded)
    }
  }, [src])

  const sizeOf = useCallback((index: number) => sizes[index] ?? sizes[0] ?? FALLBACK_PAGE, [sizes])

  const updateCurrent = useCallback(() => {
    if (!scroller) return
    const top = scroller.scrollTop + 1
    let page = 1
    for (let index = 0; index < pageRefs.current.length; index += 1) {
      const element = pageRefs.current[index]
      if (element && element.offsetTop <= top) page = index + 1
    }
    setCurrent(page)
  }, [scroller])

  const onScroll = () => {
    cancelAnimationFrame(frame.current)
    frame.current = requestAnimationFrame(updateCurrent)
  }
  useEffect(() => () => cancelAnimationFrame(frame.current), [])

  // A zoom change keeps the same part of the document in view.
  useLayoutEffect(() => {
    if (!scroller || keepRatio.current == null) return
    scroller.scrollTop = keepRatio.current * scroller.scrollHeight
    keepRatio.current = null
    updateCurrent()
  }, [zoomIndex, scroller, updateCurrent])

  const changeZoom = (delta: number) => {
    const next = Math.max(0, Math.min(PDF_ZOOM_LEVELS.length - 1, zoomIndex + delta))
    if (next === zoomIndex) return
    if (scroller) keepRatio.current = scroller.scrollTop / Math.max(1, scroller.scrollHeight)
    setZoomIndex(next)
  }

  const goTo = useCallback((page: number) => {
    const target = pageRefs.current[Math.max(1, Math.min(count, page)) - 1]
    if (target && scroller) scroller.scrollTo({ top: target.offsetTop, behavior: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
  }, [count, scroller])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        onClose()
        return
      }
      if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return
      if (event.key === 'ArrowRight' && count) { event.preventDefault(); goTo(current + 1) }
      if (event.key === 'ArrowLeft' && count) { event.preventDefault(); goTo(current - 1) }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [count, current, goTo, onClose])

  // The dialog is modal: Tab cycles through its own buttons.
  const trapTab = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'Tab') return
    const focusable = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled)')]
    if (!focusable.length) { event.preventDefault(); return }
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    const active = document.activeElement
    if (event.shiftKey && (active === first || active === event.currentTarget)) { event.preventDefault(); last.focus() }
    else if (!event.shiftKey && active === last) { event.preventDefault(); first.focus() }
  }

  const style = {
    '--flow-lightbox-inset-left': `${insets?.left ?? 0}px`,
    '--flow-lightbox-inset-right': `${insets?.right ?? 0}px`,
  } as CSSProperties
  const label = (source: string) => pdfText(source)

  return <div aria-label={title} aria-modal="true" className="pdf-viewer" onKeyDown={trapTab} ref={dialogRef} role="dialog" style={style} tabIndex={-1}>
    <div className="pdf-viewer-header">
      <div className="pdf-viewer-group pdf-viewer-group-start">
        <ViewerButton disabled={zoomIndex === 0} label={label('Zoom out')} onClick={() => changeZoom(-1)}><Glyph paths={ZOOM_OUT_GLYPH}/></ViewerButton>
        <span className="pdf-viewer-label pdf-viewer-zoom" data-i18n-ignore>{zoom}%</span>
        <ViewerButton disabled={zoomIndex === PDF_ZOOM_LEVELS.length - 1} label={label('Zoom in')} onClick={() => changeZoom(1)}><Glyph paths={ZOOM_IN_GLYPH}/></ViewerButton>
        <span aria-hidden className="pdf-viewer-separator pdf-viewer-separator-start"/>
        <ViewerButton disabled={current <= 1} label={label('Previous page')} onClick={() => goTo(current - 1)}><Glyph paths={PREVIOUS_GLYPH}/></ViewerButton>
        <span aria-live="polite" className="pdf-viewer-label pdf-viewer-indicator" data-i18n-ignore>{pageLabel(count ? current : 0, count)}</span>
        <ViewerButton disabled={!count || current >= count} label={label('Next page')} onClick={() => goTo(current + 1)}><Glyph paths={NEXT_GLYPH}/></ViewerButton>
      </div>
      <div className="pdf-viewer-group">
        <ViewerButton label={label('Download')} onClick={() => downloadPdf(src, title)}><ElementGlyph make={downloadIcon}/></ViewerButton>
        <ViewerButton label={label('Copy link')} onClick={() => void copyPdfLink(src)}><ElementGlyph make={linkIcon}/></ViewerButton>
        <ViewerButton label={label('Print')} onClick={() => void printPdf(src)}><Glyph paths={PRINT_GLYPH}/></ViewerButton>
        <span aria-hidden className="pdf-viewer-separator pdf-viewer-separator-end"/>
        <ViewerButton label={label('Close')} onClick={onClose} tooltip={false}><Glyph paths={CLOSE_GLYPH}/></ViewerButton>
      </div>
    </div>
    <div aria-label={`${title}, ${label('PDF preview')}`} className="pdf-viewer-scroll" data-i18n-ignore onScroll={onScroll} ref={setScroller} role="group">
      {status === 'loading' && <p className="pdf-viewer-status" role="status">{label('Loading PDF…')}</p>}
      {status === 'error' && <p className="pdf-viewer-status" role="alert">{label('Could not load this PDF')}</p>}
      {pdf && scroller && Array.from({ length: count }, (_, index) => {
        const natural = sizeOf(index)
        return <PdfPage
          file={pdf}
          height={natural.height * PDF_POINT_TO_CSS * zoom / 100}
          key={index}
          number={index + 1}
          pageRef={element => { pageRefs.current[index] = element }}
          root={scroller}
          width={natural.width * PDF_POINT_TO_CSS * zoom / 100}
        />
      })}
    </div>
  </div>
}

function pageLabel(current: number, count: number) {
  if (!count) return ''
  return pdfText(`Page ${current} of ${count}`)
}

/** Every header button but Close shows the app-wide tooltip (label only, no shortcut hint), as in the reference app. */
function ViewerButton({ label, disabled, onClick, children, tooltip = true }: { label: string; disabled?: boolean; onClick: () => void; children: ReactNode; tooltip?: boolean }) {
  return <button aria-label={label} className="pdf-viewer-button" data-tooltip-text={tooltip ? label : undefined} disabled={disabled} onClick={onClick} type="button">{children}</button>
}

function Glyph({ paths }: { paths: GlyphPath[] }) {
  return <svg aria-hidden fill="currentColor" height="14" viewBox="0 0 16 16" width="14">
    {paths.map((item, index) => item.stroke
      ? <path d={item.d} fill="none" key={index} stroke="currentColor" strokeLinecap="round" strokeWidth="1.5"/>
      : <path clipRule={item.evenodd ? 'evenodd' : undefined} d={item.d} fillRule={item.evenodd ? 'evenodd' : undefined} key={index} transform={item.transform}/>)}
  </svg>
}

/** An icon the editor already builds as a DOM element (download, link): its markup is reused so the glyphs stay identical. */
function ElementGlyph({ make }: { make: () => SVGSVGElement }) {
  return <span aria-hidden className="pdf-viewer-glyph" dangerouslySetInnerHTML={{ __html: make().outerHTML }}/>
}

/**
 * One page. It only renders its canvas while it is near the viewport (and frees it again when far away), so a long
 * document or a high zoom never holds every page bitmap in memory. A page that is too big for one bitmap at screen
 * density (high zoom) is drawn in tiles: only the part in view, plus a margin, is rendered, at full density, and it is
 * redrawn as the scroller moves, so 400% stays as sharp as 100% while no canvas passes the pixel cap.
 */
function PdfPage({ file, number, width, height, root, pageRef }: { file: PDFDocumentProxy; number: number; width: number; height: number; root: HTMLElement; pageRef: (element: HTMLDivElement | null) => void }) {
  const wrapper = useRef<HTMLDivElement | null>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const [near, setNear] = useState(number <= 2)
  const tiled = needsTiles(width, height)
  // The tiled region together with the page size it was planned for (a zoom change makes it stale until the tracker replans).
  const [planned, setPlanned] = useState<{ region: PageRegion; pageWidth: number; pageHeight: number } | null>(null)
  const fresh = !tiled || (planned != null && planned.pageWidth === width && planned.pageHeight === height)
  const active: PageRegion = !tiled ? { x: 0, y: 0, width, height } : fresh && planned ? planned.region : { x: 0, y: 0, width, height }

  useEffect(() => {
    const element = wrapper.current
    if (!element || typeof IntersectionObserver !== 'function') return
    const observer = new IntersectionObserver(entries => setNear(entries.some(entry => entry.isIntersecting)), { root, rootMargin: '100% 0px' })
    observer.observe(element)
    return () => observer.disconnect()
  }, [root])

  // Tiled pages follow the scroller: the region to render is whatever part of the page is in view.
  useEffect(() => {
    const element = wrapper.current
    if (!tiled || !near || !element) return
    let timer = 0
    const update = () => {
      const page = element.getBoundingClientRect()
      const view = root.getBoundingClientRect()
      const clamp = (value: number, max: number) => Math.max(0, Math.min(max, value))
      const x0 = clamp(view.left - page.left, width)
      const y0 = clamp(view.top - page.top, height)
      const visible: PageRegion = { x: x0, y: y0, width: Math.max(1, clamp(view.right - page.left, width) - x0), height: Math.max(1, clamp(view.bottom - page.top, height) - y0) }
      setPlanned(current => current && current.pageWidth === width && current.pageHeight === height && regionCovers(current.region, visible)
        ? current
        : { region: planTileRegion(visible, width, height), pageWidth: width, pageHeight: height })
    }
    const schedule = () => { window.clearTimeout(timer); timer = window.setTimeout(update, 80) }
    update()
    root.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    return () => {
      window.clearTimeout(timer)
      root.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
    }
  }, [tiled, near, root, width, height])

  useEffect(() => {
    const target = canvas.current
    if (!target || !fresh) return
    if (!near) {
      target.width = 0
      target.height = 0
      return
    }
    let cancelled = false
    let task: { cancel: () => void } | null = null
    void (async () => {
      try {
        const page = await file.getPage(number)
        if (cancelled) return
        const natural = page.getViewport({ scale: 1 })
        const scale = (width / natural.width) || 1
        const density = canvasDensity(active.width, active.height)
        const viewport = page.getViewport({ scale: scale * density })
        // Drawn on a detached canvas first, so a zoom or scroll change never shows a blank or half-painted page.
        const buffer = target.ownerDocument.createElement('canvas')
        buffer.width = Math.max(1, Math.floor(active.width * density))
        buffer.height = Math.max(1, Math.floor(active.height * density))
        const render = page.render({ canvas: buffer, viewport, transform: active.x || active.y ? [1, 0, 0, 1, -active.x * density, -active.y * density] : undefined })
        task = render
        await render.promise
        if (cancelled) return
        target.width = buffer.width
        target.height = buffer.height
        target.style.left = `${active.x}px`
        target.style.top = `${active.y}px`
        target.style.width = `${active.width}px`
        target.style.height = `${active.height}px`
        target.getContext('2d')?.drawImage(buffer, 0, 0)
      } catch {
        // A cancelled render (zoom changed, page scrolled away) is expected.
      }
    })()
    return () => {
      cancelled = true
      task?.cancel()
    }
  }, [file, number, near, fresh, width, height, active.x, active.y, active.width, active.height]) // eslint-disable-line react-hooks/exhaustive-deps

  return <div className="pdf-viewer-page" data-tiled={tiled ? '' : undefined} ref={element => { wrapper.current = element; pageRef(element) }} style={{ width, height }}>
    <canvas aria-label={pdfText(`Page ${number}`)} className="pdf-viewer-canvas" ref={canvas} role="img"/>
  </div>
}
