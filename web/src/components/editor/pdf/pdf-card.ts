import { deleteIcon, downloadIcon, linkIcon, viewIcon } from '@/components/issue/editor/image-extension'
import { createGlyph, PRINT_GLYPH } from './pdf-icons'
import { destroyPdfDocument, loadPdfDocument } from './pdf-document'
import { pdfText } from './pdf-file-actions'

const PDF_PREVIEW_MAX_BYTES = 20 * 1024 * 1024

type ToolbarActions = {
  view: () => void
  download: () => void
  copyLink: () => void
  print: () => void
  remove: () => void
}

/**
 * The toolbar on a PDF card (measured against the reference app): View PDF, Download, Copy link, Print, a hairline and
 * Delete in a 143x30 pill at the end of the card's header row. It shows while the card is hovered, selected or holds
 * keyboard focus. Delete is hidden by CSS when the editor is read-only (comments, history). Tooltips come from the
 * app-wide title tooltips (`data-tooltip-text`), which leave the node view's DOM alone.
 */
export function createPdfToolbar(actions: ToolbarActions) {
  const dom = document.createElement('div')
  dom.className = 'description-file-toolbar'
  dom.setAttribute('role', 'toolbar')
  const buttons: Array<{ button: HTMLButtonElement; label: string }> = []
  const add = (label: string, icon: SVGElement, run: () => void, className = '') => {
    const button = document.createElement('button')
    button.type = 'button'
    if (className) button.className = className
    button.append(icon)
    // Keep the editor selection: the card stays selected while its actions are used.
    button.addEventListener('mousedown', event => event.preventDefault())
    button.addEventListener('click', event => {
      event.preventDefault()
      event.stopPropagation()
      run()
    })
    buttons.push({ button, label })
    dom.append(button)
    return button
  }
  add('View PDF', viewIcon(), actions.view)
  add('Download', downloadIcon(), actions.download)
  add('Copy link', linkIcon(), actions.copyLink)
  add('Print', createGlyph(PRINT_GLYPH), actions.print)
  const separator = document.createElement('span')
  separator.className = 'description-file-toolbar-separator description-file-toolbar-delete'
  separator.setAttribute('role', 'separator')
  separator.setAttribute('aria-orientation', 'vertical')
  dom.append(separator)
  add('Delete', deleteIcon(), actions.remove, 'description-file-toolbar-delete')
  const localize = () => {
    for (const { button, label } of buttons) {
      const text = pdfText(label)
      button.setAttribute('aria-label', text)
      button.setAttribute('data-tooltip-text', text)
    }
    dom.setAttribute('aria-label', pdfText('PDF actions'))
  }
  localize()
  return { dom, localize }
}

/**
 * The strip under a PDF card: the first page rendered to a canvas (PDF.js, loaded on demand) at the card's width,
 * clipped to a 480px box. The canvas ignores the pointer, so a click on the strip only selects the card.
 */
export function createPdfPreview() {
  const dom = document.createElement('div')
  dom.className = 'description-file-preview'
  dom.contentEditable = 'false'
  const page = document.createElement('div')
  page.className = 'description-file-preview-page'
  const canvas = document.createElement('canvas')
  canvas.className = 'description-file-preview-canvas'
  canvas.setAttribute('aria-hidden', 'true')
  page.append(canvas)
  dom.append(page)
  let current = ''
  let token = 0
  let controller: AbortController | null = null
  let document_: Awaited<ReturnType<typeof loadPdfDocument>> | null = null
  let renderTask: { cancel: () => void } | null = null
  let observer: ResizeObserver | null = null
  let renderedWidth = 0

  const releaseDocument = () => {
    renderTask?.cancel()
    renderTask = null
    observer?.disconnect()
    observer = null
    controller?.abort()
    controller = null
    const loaded = document_
    document_ = null
    renderedWidth = 0
    if (loaded) destroyPdfDocument(loaded)
  }

  const draw = async (mine: number) => {
    const loaded = document_
    const width = Math.floor(page.clientWidth)
    if (!loaded || width < 1 || width === renderedWidth) return
    renderedWidth = width
    try {
      const first = await loaded.getPage(1)
      if (mine !== token) return
      const natural = first.getViewport({ scale: 1 })
      const scale = width / natural.width
      const density = Math.max(1, Math.min(window.devicePixelRatio || 1, 3))
      const viewport = first.getViewport({ scale: scale * density })
      canvas.width = Math.floor(viewport.width)
      canvas.height = Math.floor(viewport.height)
      canvas.style.aspectRatio = `${natural.width} / ${natural.height}`
      renderTask?.cancel()
      const task = first.render({ canvas, viewport })
      renderTask = task
      await task.promise
      if (mine === token) dom.dataset.state = 'ready'
    } catch (error) {
      // A cancelled render (the card resized again, the file changed) is not a failure.
      if (mine !== token || (error as { name?: string })?.name === 'RenderingCancelledException') return
      dom.dataset.state = 'error'
    }
  }

  return {
    dom,
    show(src: string, size: number) {
      if (src === current) return
      current = src
      token += 1
      const mine = token
      releaseDocument()
      dom.dataset.state = src ? 'loading' : 'empty'
      if (!src || size > PDF_PREVIEW_MAX_BYTES || typeof fetch !== 'function') return
      controller = new AbortController()
      void loadPdfDocument(src, controller.signal).then(loaded => {
        if (mine !== token) { destroyPdfDocument(loaded); return }
        document_ = loaded
        // The card may still be laid out (or detached) when the file arrives: render once it has a width, and again when it changes.
        if (typeof ResizeObserver === 'function') {
          observer = new ResizeObserver(() => { void draw(mine) })
          observer.observe(page)
        }
        void draw(mine)
      }).catch(() => { if (mine === token) dom.dataset.state = 'error' })
    },
    destroy() {
      token += 1
      releaseDocument()
    },
  }
}
