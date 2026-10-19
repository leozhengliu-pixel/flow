import { useEffect, useMemo, useRef, useState } from 'react'
import { AgentInlineVideo } from '@/components/agent/agent-inline-video'
import { openLightbox } from '@/components/editor/lightbox-bridge'
import { useI18n } from '@/i18n/i18n'
import { fetchCodingSessionArtifacts } from '@/lib/coding-sessions-api'
import type { CodingArtifact } from '@/types/coding-sessions'
import { artifactSrc, buildArtifactSections, galleryImages, pairRole, type ArtifactPair } from './coding-artifacts-model'
import styles from './coding-session-artifacts.module.css'

export type CodingSessionArtifactsProps = {
  sessionId: string
  /** Initial data (e.g. `session.artifacts`); when missing the list is fetched. */
  artifacts?: CodingArtifact[]
  /** The list is refetched whenever this changes (e.g. `session.updatedAt`). */
  refreshKey?: string | number
  className?: string
}

type Status = 'idle' | 'loading' | 'error'
type OpenImage = (artifact: CodingArtifact) => void
type ImageFailed = (artifact: CodingArtifact) => void

/**
 * The before/after gallery of a coding session: comparison cards for "before"/"after" screenshots of the same
 * flow, a thumbnail grid for the other screenshots and the recorded videos. Renders nothing without media.
 */
export function CodingSessionArtifacts({ sessionId, artifacts, refreshKey, className }: CodingSessionArtifactsProps) {
  const { t } = useI18n()
  const [fetched, setFetched] = useState<{ sessionId: string; items: CodingArtifact[] }>()
  const [status, setStatus] = useState<Status>(artifacts ? 'idle' : 'loading')
  const [reload, setReload] = useState(0)
  const initial = useRef({ sessionId, refreshKey })
  const expiredRefetch = useRef(false)
  const hasInitialData = Boolean(artifacts)

  useEffect(() => {
    const unchanged = initial.current.sessionId === sessionId && initial.current.refreshKey === refreshKey
    if (hasInitialData && unchanged && reload === 0) return
    let cancelled = false
    setStatus('loading')
    fetchCodingSessionArtifacts(sessionId).then(
      items => { if (!cancelled) { setFetched({ sessionId, items }); setStatus('idle') } },
      () => { if (!cancelled) setStatus('error') },
    )
    return () => { cancelled = true }
  }, [sessionId, refreshKey, reload, hasInitialData])

  const items = fetched?.sessionId === sessionId ? fetched.items : artifacts
  const sections = useMemo(() => buildArtifactSections(items ?? []), [items])
  const images = useMemo(() => galleryImages(sections), [sections])

  const open: OpenImage = artifact => {
    const index = Math.max(0, images.findIndex(image => image.id === artifact.id))
    const src = artifactSrc(artifact)
    const lightboxItems = images.map(image => ({ src: artifactSrc(image), alt: imageAlt(image, t), title: imageAlt(image, t) }))
    if (!openLightbox(lightboxItems, index)) window.open(src, '_blank', 'noopener')
  }
  // A signed URL may have expired: refetch the list once to get fresh ones (stable contentUrls never expire).
  const failed: ImageFailed = artifact => {
    if (artifact.contentUrl || expiredRefetch.current) return
    expiredRefetch.current = true
    setReload(value => value + 1)
  }
  const retry = () => setReload(value => value + 1)

  const mediaCount = images.length + sections.reduce((total, section) => total + section.videos.length, 0)
  const screenshotCount = images.length
  if (!mediaCount) {
    if (status === 'loading') return <div aria-busy="true" className={join(styles.root, styles.status, className)}>{t('Loading screenshots…')}</div>
    if (status === 'error') return <ErrorRow className={join(styles.root, className)} onRetry={retry}/>
    return null
  }

  const byTurn = sections.length > 1
  return (
    <section aria-label={t('Screenshots')} className={join(styles.root, className)}>
      <header className={styles.header}>
        <h3 className={styles.title}>{screenshotCount ? t('Screenshots') : t('Recordings')}</h3>
        <span className={styles.count}>{mediaCount}</span>
      </header>
      {status === 'error' && <ErrorRow onRetry={retry}/>}
      {sections.map((section, index) => (
        <div className={styles.section} key={section.key}>
          {byTurn && <h4 className={styles.turn}>{section.turnId ? `${t('Turn')} ${index + 1}` : t('Other captures')}</h4>}
          {section.pairs.map(pair => <PairCard key={pair.key} onFailed={failed} onOpen={open} pair={pair}/>)}
          {section.shots.length > 0 && (
            <div className={styles.grid}>
              {section.shots.map(shot => <Thumbnail artifact={shot} key={shot.id} onFailed={failed} onOpen={open}/>)}
            </div>
          )}
          {section.videos.map(video => (
            <figure className={styles.video} key={video.id}>
              <AgentInlineVideo name={video.label} src={artifactSrc(video)}/>
              <figcaption className={styles.caption}><Caption artifact={video}/></figcaption>
            </figure>
          ))}
        </div>
      ))}
    </section>
  )
}

function join(...names: (string | undefined | false)[]) {
  return names.filter(Boolean).join(' ')
}

function imageAlt(artifact: CodingArtifact, t: (source: string) => string) {
  const role = pairRole(artifact)
  if (!role) return artifact.label
  const name = role === 'before' ? t('Before') : t('After')
  return artifact.group ? `${name}: ${artifact.group}` : name
}

function ErrorRow({ className, onRetry }: { className?: string; onRetry: () => void }) {
  const { t } = useI18n()
  return (
    <div className={join(styles.status, styles.error, className)} role="alert">
      <span>{t("Couldn't load screenshots")}</span>
      <button className={styles.retry} onClick={onRetry} type="button">{t('Retry')}</button>
    </div>
  )
}

function Caption({ artifact, label }: { artifact: CodingArtifact; label?: string }) {
  const { formatDate } = useI18n()
  return (
    <>
      {label ? <span className={styles.label}>{label}</span> : <span className={styles.label} data-i18n-ignore>{artifact.label}</span>}
      <time className={styles.time} dateTime={artifact.createdAt} title={formatDate(artifact.createdAt, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' })}>
        {formatDate(artifact.createdAt, { hour: 'numeric', minute: '2-digit' })}
      </time>
    </>
  )
}

function Thumbnail({ artifact, onOpen, onFailed }: { artifact: CodingArtifact; onOpen: OpenImage; onFailed: ImageFailed }) {
  return (
    <figure className={styles.thumb}>
      <button className={styles.thumbButton} onClick={() => onOpen(artifact)} type="button">
        <img alt={artifact.label} className={styles.thumbImage} loading="lazy" onError={() => onFailed(artifact)} src={artifactSrc(artifact)}/>
      </button>
      <figcaption className={styles.caption}><Caption artifact={artifact}/></figcaption>
    </figure>
  )
}

function PairImage({ artifact, caption, onOpen, onFailed }: { artifact: CodingArtifact; caption: string; onOpen: OpenImage; onFailed: ImageFailed }) {
  const { t } = useI18n()
  return (
    <figure className={styles.half}>
      <button className={styles.halfButton} onClick={() => onOpen(artifact)} type="button">
        <img alt={imageAlt(artifact, t)} className={styles.halfImage} onError={() => onFailed(artifact)} src={artifactSrc(artifact)}/>
      </button>
      <figcaption className={styles.caption}><Caption artifact={artifact} label={caption}/></figcaption>
    </figure>
  )
}

function PairCard({ pair, onOpen, onFailed }: { pair: ArtifactPair; onOpen: OpenImage; onFailed: ImageFailed }) {
  const { t } = useI18n()
  const [mode, setMode] = useState<'side' | 'slider'>('side')
  const [position, setPosition] = useState(50)
  const { before, after } = pair
  const title = pair.group || t('Before and after')
  const complete = before && after
  return (
    <div aria-label={title} className={styles.pair} role="group">
      <div className={styles.pairHeader}>
        <span className={styles.pairTitle} data-i18n-ignore={pair.group ? '' : undefined}>{title}</span>
        {complete && (
          <div aria-label={t('Comparison mode')} className={styles.segmented} role="group">
            <button aria-pressed={mode === 'side'} onClick={() => setMode('side')} type="button">{t('Side by side')}</button>
            <button aria-pressed={mode === 'slider'} onClick={() => setMode('slider')} type="button">{t('Slider')}</button>
          </div>
        )}
      </div>
      {complete && mode === 'slider' ? (
        <div className={styles.slider}>
          <img alt={imageAlt(before, t)} className={styles.sliderImage} onError={() => onFailed(before)} src={artifactSrc(before)}/>
          <img
            alt={imageAlt(after, t)}
            className={styles.sliderAfter}
            data-testid="comparison-after"
            onError={() => onFailed(after)}
            src={artifactSrc(after)}
            style={{ clipPath: `inset(0 0 0 ${position}%)` }}
          />
          <span aria-hidden className={styles.divider} style={{ left: `${position}%` }}><span className={styles.handle}/></span>
          <span aria-hidden className={styles.sliderTag} data-side="before">{t('Before')}</span>
          <span aria-hidden className={styles.sliderTag} data-side="after">{t('After')}</span>
          <input
            aria-label={t('Comparison position')}
            className={styles.range}
            max={100}
            min={0}
            onChange={event => setPosition(Number(event.currentTarget.value))}
            step={1}
            type="range"
            value={position}
          />
        </div>
      ) : (
        <div className={styles.sides} data-single={complete ? undefined : ''}>
          {before && <PairImage artifact={before} caption={t('Before')} onFailed={onFailed} onOpen={onOpen}/>}
          {after && <PairImage artifact={after} caption={t('After')} onFailed={onFailed} onOpen={onOpen}/>}
        </div>
      )}
    </div>
  )
}
