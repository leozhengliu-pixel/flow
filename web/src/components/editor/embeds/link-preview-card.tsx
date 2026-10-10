import { useEffect, useState } from 'react'
import { LINK_CARD_SITE, safeEmbedHref, type EmbedProvider } from './embed-providers'
import { fetchLinkPreview, type LinkPreview } from './link-preview'
import styles from './embed.module.css'

/**
 * Linear's card for a pasted Figma or X link: a 100px row with the page's title, a two-line description and the link on the
 * left, and the page's preview image on the right. The whole card is the link (it opens in a new tab). While the preview loads,
 * or when the page has no tags (a login wall, a private file), the card shows the site name over the plain link.
 */
export function LinkPreviewCard({ provider, src }: { provider: string; src: string }) {
  const [preview, setPreview] = useState<LinkPreview | null>(null)
  const [imageFailed, setImageFailed] = useState(false)
  const href = safeEmbedHref(src)
  useEffect(() => {
    let live = true
    setPreview(null)
    setImageFailed(false)
    fetchLinkPreview(src).then(next => { if (live) setPreview(next) }, () => undefined)
    return () => { live = false }
  }, [src])
  const title = preview?.title || preview?.siteName || LINK_CARD_SITE[provider as EmbedProvider] || hostOf(src)
  const image = !imageFailed && preview?.imageUrl ? preview.imageUrl : ''
  const content = (
    <>
      <span className={styles.linkText}>
        <span className={styles.linkTitle}>{title}</span>
        {preview?.description ? <span className={styles.linkDescription}>{preview.description}</span> : null}
        <span className={styles.linkUrl}>{src}</span>
      </span>
      {image ? <img alt="" className={styles.linkImage} draggable={false} onError={() => setImageFailed(true)} referrerPolicy="no-referrer" src={image}/> : null}
    </>
  )
  const state = preview ? 'ready' : 'loading'
  return href
    ? <a className={styles.linkCard} data-link-preview={state} draggable={false} href={href} rel="noopener noreferrer" target="_blank">{content}</a>
    : <div className={styles.linkCard} data-link-preview={state}>{content}</div>
}

function hostOf(src: string) {
  try {
    return new URL(src).hostname.replace(/^www\./, '')
  } catch {
    return src
  }
}
