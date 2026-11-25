import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { registerPdfViewerOpener, type PdfViewerRequest } from './pdf-bridge'

// The viewer (and PDF.js behind it) is a separate chunk, fetched the first time a PDF is opened.
const PdfViewerDialog = lazy(() => import('./pdf-viewer-dialog').then(module => ({ default: module.PdfViewerDialog })))

/** Owns the open PDF viewer. File cards in editors open it through the bridge; closing returns focus to what held it. */
export function PdfViewerHost({ insets }: { insets?: { left: number; right: number } }) {
  const [request, setRequest] = useState<PdfViewerRequest | null>(null)
  const [step, setStep] = useState(0)
  const opener = useRef<HTMLElement | null>(null)

  useEffect(() => {
    registerPdfViewerOpener(next => {
      opener.current = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null
      setRequest(next)
      setStep(Math.max(0, next.siblings?.findIndex(item => item.src === next.src) ?? 0))
    })
    return () => registerPdfViewerOpener(null)
  }, [])

  useEffect(() => {
    if (request) return
    const element = opener.current
    opener.current = null
    if (element?.isConnected) element.focus({ preventScroll: true })
  }, [request])

  const close = useCallback(() => setRequest(null), [])
  if (!request) return null
  const items = request.siblings?.length ? request.siblings : null
  const shown = items ? items[Math.min(step, items.length - 1)] : request
  return createPortal(
    <Suspense fallback={null}><PdfViewerDialog insets={insets} onClose={close} onNext={items && step < items.length - 1 ? () => setStep(step + 1) : undefined} onPrevious={items && step > 0 ? () => setStep(step - 1) : undefined} src={shown.src} stepper={items != null} title={shown.title}/></Suspense>,
    document.body,
  )
}
