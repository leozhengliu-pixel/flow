/** Imperative bridge so editor DOM node views (file cards) can open the React PDF viewer. */

export type PdfViewerRequest = {
  src: string
  title: string
  /** Every PDF of the page the file sits in, in order (the viewer's next / previous buttons step through them). */
  siblings?: { src: string; title: string }[]
}

type OpenHandler = (request: PdfViewerRequest) => void

let openHandler: OpenHandler | null = null

export function registerPdfViewerOpener(handler: OpenHandler | null) {
  openHandler = handler
}

/** Opens the viewer; without a registered host the file opens in a new tab instead. */
export function openPdfViewer(request: PdfViewerRequest) {
  if (openHandler) {
    openHandler(request)
    return true
  }
  window.open(request.src, '_blank', 'noopener')
  return false
}
