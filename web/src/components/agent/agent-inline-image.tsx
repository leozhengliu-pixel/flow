import { useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { copyIcon, deleteIcon, downloadIcon, linkIcon, viewIcon } from '@/components/issue/editor/image-extension'
import { copyImage, copyText, downloadImage } from '@/components/editor/lightbox-editor-provider'
import { openLightbox } from '@/components/editor/lightbox-bridge'
import { useI18n } from '@/i18n/i18n'
import styles from './agent-inline-image.module.css'

export function ToolbarIcon({ make }: { make: () => SVGSVGElement }) {
  const ref = useRef<HTMLSpanElement>(null)
  useLayoutEffect(() => { ref.current?.replaceChildren(make()) }, [make])
  return <span aria-hidden className={styles.icon} ref={ref}/>
}

/**
 * An image in the Agent composer or a sent message, as the reference app draws it: at most two thirds of its pixel
 * width (screenshots are taken at 1.5x or more) and 600px tall, with a hover toolbar. In the composer the toolbar can
 * also delete it and side handles resize it.
 */
export function AgentInlineImage({ src, alt, editable = false, uploading = false, onRemove }: { src: string; alt: string; editable?: boolean; uploading?: boolean; onRemove?: () => void }) {
  const { t } = useI18n()
  const [natural, setNatural] = useState<number>()
  const [width, setWidth] = useState<number>()
  const imageRef = useRef<HTMLImageElement>(null)
  const item = { src, alt }
  const view = () => { if (!openLightbox([item])) window.open(src, '_blank', 'noopener') }
  const resize = (side: 'left' | 'right') => (event: ReactPointerEvent<HTMLSpanElement>) => {
    const image = imageRef.current
    if (!image) return
    event.preventDefault()
    event.stopPropagation()
    const start = event.clientX
    const startWidth = image.getBoundingClientRect().width
    const limit = image.parentElement?.parentElement?.getBoundingClientRect().width ?? startWidth
    const move = (moved: PointerEvent) => {
      const delta = (moved.clientX - start) * (side === 'right' ? 1 : -1)
      setWidth(Math.round(Math.min(limit, Math.max(48, startWidth + delta))))
    }
    const stop = () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', stop) }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
  }
  const button = (label: string, make: () => SVGSVGElement, run: () => void) => (
    <button aria-label={t(label)} onClick={event => { event.preventDefault(); event.stopPropagation(); run() }} onMouseDown={event => event.preventDefault()} title={t(label)} type="button">
      <ToolbarIcon make={make}/>
    </button>
  )
  return (
    <span className={styles.frame} data-editable={editable || undefined} data-uploading={uploading || undefined}>
      <img
        alt={alt}
        className={styles.image}
        draggable={false}
        onClick={editable ? undefined : view}
        onLoad={event => setNatural(event.currentTarget.naturalWidth)}
        ref={imageRef}
        src={src}
        style={{ maxWidth: natural ? `min(100%, ${Math.round(natural / 1.5 * 100) / 100}px)` : '100%', width }}
      />
      {!uploading && <span className={styles.toolbar}>
        {button('View image', viewIcon, view)}
        {button('Download', downloadIcon, () => downloadImage(item))}
        {button('Copy image', copyIcon, () => void copyImage(item))}
        {button('Copy link', linkIcon, () => void copyText(new URL(src, window.location.origin).href))}
        {editable && <><span aria-hidden className={styles.separator}/>{button('Delete', deleteIcon, () => onRemove?.())}</>}
      </span>}
      {editable && !uploading && <>
        <span aria-hidden className={styles.handle} data-side="left" onPointerDown={resize('left')}/>
        <span aria-hidden className={styles.handle} data-side="right" onPointerDown={resize('right')}/>
      </>}
    </span>
  )
}
