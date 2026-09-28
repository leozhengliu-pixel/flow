import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MutableRefObject,
  type ReactNode,
  type RefObject,
} from 'react'
import { createPortal } from 'react-dom'

import { CommentPopover, type LightboxComment } from './comment-popover'
import { openLightbox, registerLightboxOpener, type LightboxItem } from './lightbox-bridge'
import { WindowControlsAwareLightboxProvider, useWindowControlsInsets } from './window-controls-aware-lightbox-provider'
import { useCommentHashPopover } from '@/hooks/use-comment-hash-popover'

import './lightbox-editor.css'

export type LightboxEditorContextValue = {
  open: (items: LightboxItem[], index?: number) => void
  close: () => void
  editorInstanceRef: MutableRefObject<unknown>
  activeIndex: number
  items: LightboxItem[]
}

const LightboxEditorContext = createContext<LightboxEditorContextValue | null>(null)

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

  const open = useCallback((items: LightboxItem[], index = 0) => {
    if (!items.length) return
    const nextIndex = Math.max(0, Math.min(index, items.length - 1))
    indexRef.current = nextIndex
    setSession({ items, index: nextIndex })
  }, [indexRef])

  const close = useCallback(() => {
    indexRef.current = undefined
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
    setComment(resolveComment?.(commentHash.target.id) ?? {
      id: commentHash.target.id,
      body: `Comment ${commentHash.target.id}`,
      authorName: 'Comment',
    })
  }, [commentHash.target, resolveComment])

  useEffect(() => {
    if (!session) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        close()
        return
      }
      if (event.key === 'ArrowLeft') {
        event.preventDefault()
        setIndex(session.index - 1)
      }
      if (event.key === 'ArrowRight') {
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

  return (
    <LightboxEditorContext.Provider value={value}>
      {children}
      {session && active && createPortal(
        <div
          aria-label="View image"
          className="flow-lightbox-root"
          data-translucent="false"
          onClick={close}
          role="dialog"
          style={{
            ['--flow-lightbox-inset-left' as string]: `${insets.left}px`,
            ['--flow-lightbox-inset-right' as string]: `${insets.right}px`,
          }}
        >
          <button aria-label="Close" className="flow-lightbox-close" onClick={close} type="button">
            ×
          </button>
          <div className="flow-lightbox-stage" onClick={event => event.stopPropagation()}>
            {session.items.length > 1 && (
              <button
                aria-label="Previous image"
                className="flow-lightbox-nav prev"
                disabled={session.index === 0}
                onClick={() => setIndex(session.index - 1)}
                type="button"
              >
                ‹
              </button>
            )}
            <img alt={active.alt ?? ''} src={active.src} />
            {session.items.length > 1 && (
              <button
                aria-label="Next image"
                className="flow-lightbox-nav next"
                disabled={session.index >= session.items.length - 1}
                onClick={() => setIndex(session.index + 1)}
                type="button"
              >
                ›
              </button>
            )}
            {session.items.length > 1 && (
              <span className="flow-lightbox-index">
                {session.index + 1} / {session.items.length}
              </span>
            )}
          </div>
          {comment && (
            <div className="flow-lightbox-comment-slot" onClick={event => event.stopPropagation()}>
              <CommentPopover comment={comment} onClose={commentHash.clear} />
            </div>
          )}
        </div>,
        document.body,
      )}
      {!session && comment && createPortal(
        <div className="flow-lightbox-comment-slot" style={{ position: 'fixed', left: 24, bottom: 24, zIndex: 1300 }}>
          <CommentPopover comment={comment} onClose={commentHash.clear} />
        </div>,
        document.body,
      )}
    </LightboxEditorContext.Provider>
  )
}

export function useLightboxEditor() {
  const value = useContext(LightboxEditorContext)
  if (!value) throw new Error('useLightboxEditor must be used within LightboxEditorProvider')
  return value
}

export function useOptionalLightboxEditor() {
  return useContext(LightboxEditorContext)
}

/** Prefer React portal; fall back to legacy DOM overlay when provider is absent. */
export function openDescriptionLightboxViaProvider(src: string, alt = '', siblings?: LightboxItem[]) {
  const items = siblings?.length ? siblings : [{ src, alt }]
  const index = Math.max(0, items.findIndex(item => item.src === src))
  if (openLightbox(items, index === -1 ? 0 : index)) return true
  return false
}
