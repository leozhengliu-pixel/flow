import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MutableRefObject,
  type ReactNode,
  type RefObject,
} from 'react'
import { ArrowDown, ArrowUp, Clipboard, Download, Link2, X } from 'lucide-react'
import { createPortal } from 'react-dom'
import { copyImage, copyText, downloadImage } from './lightbox-actions'

import { CommentPopover, type LightboxComment } from './comment-popover'
import { registerLightboxOpener, type LightboxItem } from './lightbox-bridge'
import { PdfViewerHost } from './pdf/pdf-viewer-host'
import { LightboxEditorContext, type LightboxEditorContextValue } from './lightbox-editor-context'
import { WindowControlsAwareLightboxProvider } from './window-controls-aware-lightbox-provider'
import { useWindowControlsInsets } from './window-controls-insets-context'
import { useCommentHashPopover } from '@/hooks/use-comment-hash-popover'

import './lightbox-editor.css'

export type LightboxEditorProviderProps = {
  children: ReactNode
  /** Optional external editor ref (TipTap / host). Provider always keeps an internal mirror. */
  editorInstanceRef?: RefObject<unknown> | MutableRefObject<unknown>
  /** Resolve `#comment-{id}` into popover content. */
  resolveComment?: (id: string) => LightboxComment | undefined
  /** When false, hash→CommentPopover is idle (e.g. read-only disableLightbox hosts). */
  enableCommentHashPopover?: boolean
}

type Session = { items: LightboxItem[]; index: number }

/** The percentage of the image's own size it is shown at. */
function zoomOf(image: HTMLImageElement) {
  return image.naturalWidth ? Math.round((image.clientWidth / image.naturalWidth) * 100) : 100
}

export function LightboxEditorProvider({
  children,
  editorInstanceRef: externalRef,
  resolveComment,
  enableCommentHashPopover = true,
}: LightboxEditorProviderProps) {
  const internalRef = useRef<unknown>(null)
  const editorInstanceRef = (externalRef as MutableRefObject<unknown> | undefined) ?? internalRef
  const indexRef = useRef<number | undefined>(undefined)

  return (
    <WindowControlsAwareLightboxProvider>
      <LightboxEditorProviderInner
        editorInstanceRef={editorInstanceRef}
        enableCommentHashPopover={enableCommentHashPopover}
        indexRef={indexRef}
        resolveComment={resolveComment}
      >
        {children}
      </LightboxEditorProviderInner>
    </WindowControlsAwareLightboxProvider>
  )
}

function LightboxEditorProviderInner({
  children,
  editorInstanceRef,
  enableCommentHashPopover,
  indexRef,
  resolveComment,
}: {
  children: ReactNode
  editorInstanceRef: MutableRefObject<unknown>
  enableCommentHashPopover: boolean
  indexRef: MutableRefObject<number | undefined>
  resolveComment?: (id: string) => LightboxComment | undefined
}) {
  const [session, setSession] = useState<Session | null>(null)
  const insets = useWindowControlsInsets()
  const commentHash = useCommentHashPopover(enableCommentHashPopover)
  const [comment, setComment] = useState<LightboxComment | undefined>()
  const [zoom, setZoom] = useState(100)
  const [zoomed, setZoomed] = useState(false)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const imageRef = useRef<HTMLImageElement | null>(null)
  const stageRef = useRef<HTMLDivElement | null>(null)
  const flyInRef = useRef(false)

  const open = useCallback((items: LightboxItem[], index = 0) => {
    if (!items.length) return
    const nextIndex = Math.max(0, Math.min(index, items.length - 1))
    indexRef.current = nextIndex
    flyInRef.current = true
    setZoomed(false)
    setPan({ x: 0, y: 0 })
    setSession({ items, index: nextIndex })
  }, [indexRef])

  const close = useCallback(() => {
    indexRef.current = undefined
    setZoomed(false)
    setPan({ x: 0, y: 0 })
    setSession(null)
  }, [indexRef])

  const setIndex = useCallback((next: number) => {
    setSession(current => {
      if (!current) return current
      const clamped = Math.max(0, Math.min(next, current.items.length - 1))
      if (indexRef.current !== undefined && indexRef.current !== clamped) {
        // onItemChange: notify editor hosts that the active lightbox item moved
        editorInstanceRef.current = { ...(editorInstanceRef.current as object | null), lightboxIndex: clamped }
      }
      indexRef.current = clamped
      setZoomed(false)
      setPan({ x: 0, y: 0 })
      return { ...current, index: clamped }
    })
  }, [editorInstanceRef, indexRef])

  useEffect(() => {
    registerLightboxOpener(open)
    return () => registerLightboxOpener(null)
  }, [open])

  useEffect(() => {
    if (!commentHash.target) {
      setComment(undefined)
      return
    }
    // Only a comment the host can resolve gets the popover: pages that handle `#comment-` links themselves (issue,
    // document and project activity threads) must not also get a placeholder popover.
    setComment(resolveComment?.(commentHash.target.id))
  }, [commentHash.target, resolveComment])

  useEffect(() => {
    if (!session) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        close()
        return
      }
      if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
        event.preventDefault()
        setIndex(session.index - 1)
      }
      if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
        event.preventDefault()
        setIndex(session.index + 1)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [session, close, setIndex])

  const value = useMemo<LightboxEditorContextValue>(() => ({
    open,
    close,
    editorInstanceRef,
    activeIndex: session?.index ?? -1,
    items: session?.items ?? [],
  }), [open, close, editorInstanceRef, session])

  const active = session?.items[session.index]

  /** The picture grows out of the place it was clicked in, like the page it sits in. */
  const flyIn = useCallback((image: HTMLImageElement) => {
    if (!flyInRef.current) return
    flyInRef.current = false
    const source = [...document.querySelectorAll('img')].find(
      candidate => candidate !== image && candidate.getAttribute('src') === image.getAttribute('src') && !candidate.closest('.flow-lightbox-root'),
    )
    if (!source || typeof image.animate !== 'function') return
    const from = source.getBoundingClientRect()
    const to = image.getBoundingClientRect()
    if (!from.width || !to.width) return
    image.animate(
      [
        { transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width}, ${from.height / to.height})` },
        { transform: 'none' },
      ],
      { duration: 300, easing: 'cubic-bezier(.2,.9,.3,1)' },
    )
  }, [])

  /** Scrolling a zoomed picture moves it around inside the stage. */
  const panBy = useCallback((deltaX: number, deltaY: number) => {
    const image = imageRef.current
    const stage = stageRef.current
    if (!image || !stage) return
    const limitX = Math.max(0, (image.clientWidth * 2 - stage.clientWidth) / 2)
    const limitY = Math.max(0, (image.clientHeight * 2 - stage.clientHeight) / 2)
    setPan(current => ({
      x: Math.max(-limitX, Math.min(limitX, current.x - deltaX)),
      y: Math.max(-limitY, Math.min(limitY, current.y - deltaY)),
    }))
  }, [])

  return (
    <LightboxEditorContext.Provider value={value}>
      {children}
      {session && active && createPortal(
        <div
          aria-label="View image"
          className="flow-lightbox-root"
          data-translucent="false"
          onClick={event => { if (!(event.target as HTMLElement).closest('button, img')) close() }}
          role="dialog"
          style={{
            ['--flow-lightbox-inset-left' as string]: `${insets.left}px`,
            ['--flow-lightbox-inset-right' as string]: `${insets.right}px`,
          }}
        >
          <div className="flow-lightbox-bar">
            <span className="flow-lightbox-cluster start"><button className="flow-lightbox-zoom" onClick={() => setZoomed(false)} type="button">{zoom * (zoomed ? 2 : 1)}%</button></span>
            <span className="flow-lightbox-cluster end">
              <button aria-label="Download" onClick={() => downloadImage(active)} type="button"><Download size={14}/></button>
              <button aria-label="Copy image" onClick={() => void copyImage(active)} type="button"><Clipboard size={14}/></button>
              <button aria-label="Copy link" onClick={() => void copyText(active.src)} type="button"><Link2 size={14}/></button>
              {session.items.length > 1 && (
                <>
                  <span aria-hidden className="flow-lightbox-separator"/>
                  <button aria-label="Next" disabled={session.index >= session.items.length - 1} onClick={() => setIndex(session.index + 1)} type="button"><ArrowDown size={14}/></button>
                  <button aria-label="Previous" disabled={session.index === 0} onClick={() => setIndex(session.index - 1)} type="button"><ArrowUp size={14}/></button>
                </>
              )}
              <span aria-hidden className="flow-lightbox-separator"/>
              <button aria-label="Close" className="flow-lightbox-close" onClick={close} type="button"><X size={14}/></button>
            </span>
          </div>
          <div className="flow-lightbox-stage" onWheel={event => { if (zoomed) panBy(event.deltaX, event.deltaY) }} ref={stageRef}>
            <img
              alt={active.alt ?? ''}
              data-zoomed={zoomed}
              onClick={event => { event.stopPropagation(); setZoomed(value => !value); setPan({ x: 0, y: 0 }) }}
              onLoad={event => { setZoom(zoomOf(event.currentTarget)); flyIn(event.currentTarget) }}
              ref={imageRef}
              src={active.src}
              style={zoomed ? { transform: `translate(${pan.x}px, ${pan.y}px) scale(2)` } : undefined}
            />
          </div>
          {active.alt && <p className="flow-lightbox-caption" data-i18n-ignore onClick={event => event.stopPropagation()}>{active.alt}</p>}
          {comment && (
            <div className="flow-lightbox-comment-slot" onClick={event => event.stopPropagation()}>
              <CommentPopover comment={comment} onClose={commentHash.clear} />
            </div>
          )}
        </div>,
        document.body,
      )}
      <PdfViewerHost insets={insets}/>
      {!session && comment && createPortal(
        <div className="flow-lightbox-comment-slot" style={{ position: 'fixed', left: 24, bottom: 24, zIndex: 1300 }}>
          <CommentPopover comment={comment} onClose={commentHash.clear} />
        </div>,
        document.body,
      )}
    </LightboxEditorContext.Provider>
  )
}
