import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Download, Maximize, Minimize, Pause, PictureInPicture2, Play, Volume2, VolumeX } from 'lucide-react'
import { downloadImage } from '@/components/editor/lightbox-actions'
import { deleteIcon } from '@/components/issue/editor/image-extension'
import { useI18n } from '@/i18n/i18n'
import { ToolbarIcon } from './agent-inline-image'
import styles from './agent-inline-video.module.css'

const RATES = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2]

function clock(seconds: number) {
  const total = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0
  const pad = (value: number) => String(value).padStart(2, '0')
  const hours = Math.floor(total / 3600)
  return hours ? `${hours}:${pad(Math.floor(total / 60) % 60)}:${pad(total % 60)}` : `${Math.floor(total / 60)}:${pad(total % 60)}`
}

/**
 * A video in the Agent composer or a sent message, as the reference app draws it: a framed player without the
 * browser's controls and a floating bar with play (k), mute (m), seek, speed, download, picture in picture (p) and fullscreen.
 */
export function AgentInlineVideo({ src, name, editable = false, uploading = false, onRemove }: { src: string; name: string; editable?: boolean; uploading?: boolean; onRemove?: () => void }) {
  const { t } = useI18n()
  const frameRef = useRef<HTMLDivElement>(null)
  const videoRef = useRef<HTMLVideoElement>(null)
  const [time, setTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [muted, setMuted] = useState(false)
  const [rate, setRate] = useState(1)
  const [fullscreen, setFullscreen] = useState(false)
  const pipSupported = typeof document !== 'undefined' && document.pictureInPictureEnabled !== false
  useEffect(() => {
    const sync = () => setFullscreen(document.fullscreenElement === frameRef.current)
    document.addEventListener('fullscreenchange', sync)
    return () => document.removeEventListener('fullscreenchange', sync)
  }, [])
  const toggle = () => {
    const video = videoRef.current
    if (!video) return
    if (video.paused) void video.play()?.catch(() => undefined)
    else video.pause()
  }
  const mute = () => { if (videoRef.current) videoRef.current.muted = !videoRef.current.muted }
  const pip = () => {
    const video = videoRef.current
    if (!video) return
    if (document.pictureInPictureElement === video) void document.exitPictureInPicture?.()
    else void video.requestPictureInPicture?.()?.catch(() => undefined)
  }
  const toggleFullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen?.()
    else void frameRef.current?.requestFullscreen?.()
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // Keys in the player (its frame, buttons, speed select) never reach the composer, where Enter would send.
    event.stopPropagation()
    if (uploading || event.target instanceof HTMLSelectElement || event.metaKey || event.ctrlKey || event.altKey) return
    const key = event.key.toLowerCase()
    if (key === 'k') toggle()
    else if (key === 'm') mute()
    else if (key === 'p' && pipSupported) pip()
    else return
    event.preventDefault()
  }
  const button = (label: string, icon: ReactNode, run: () => void) => (
    <button aria-label={t(label)} onClick={event => { event.preventDefault(); event.stopPropagation(); run() }} onMouseDown={event => event.preventDefault()} title={t(label)} type="button">{icon}</button>
  )
  const progress = duration ? Math.min(100, (time / duration) * 100) : 0
  return (
    <div aria-label={name} className={styles.frame} data-editable={editable || undefined} data-uploading={uploading || undefined} onKeyDown={onKeyDown} ref={frameRef} role="group" tabIndex={uploading ? undefined : 0}>
      <video
        className={styles.video}
        onClick={uploading ? undefined : toggle}
        onDurationChange={event => setDuration(event.currentTarget.duration)}
        onEnded={() => setPlaying(false)}
        onLoadedMetadata={event => setDuration(event.currentTarget.duration)}
        onPause={() => setPlaying(false)}
        onPlay={() => setPlaying(true)}
        onRateChange={event => setRate(event.currentTarget.playbackRate)}
        onTimeUpdate={event => setTime(event.currentTarget.currentTime)}
        onVolumeChange={event => setMuted(event.currentTarget.muted)}
        playsInline
        preload="metadata"
        ref={videoRef}
        src={src}
        title={name}
      />
      {!uploading && <div className={styles.bar} role="toolbar" aria-label={t('Video controls')}>
        {button(playing ? 'Pause' : 'Play', playing ? <Pause aria-hidden fill="currentColor" size={16}/> : <Play aria-hidden fill="currentColor" size={16}/>, toggle)}
        {button(muted ? 'Unmute' : 'Mute', muted ? <VolumeX aria-hidden size={16}/> : <Volume2 aria-hidden size={16}/>, mute)}
        <div className={styles.time}>
          <span className={styles.clock}>{clock(time)}</span>
          <input
            aria-label={t('Seek')}
            className={styles.seek}
            max={duration || 0}
            min={0}
            onChange={event => { if (videoRef.current) videoRef.current.currentTime = Number(event.target.value) }}
            step="any"
            style={{ ['--progress' as string]: `${progress}%` }}
            type="range"
            value={Math.min(time, duration || 0)}
          />
          <span className={styles.clock}>{clock(duration)}</span>
        </div>
        <select aria-label={t('Playback speed')} className={styles.rate} onChange={event => { if (videoRef.current) videoRef.current.playbackRate = Number(event.target.value) }} value={rate}>
          {RATES.map(value => <option key={value} value={value}>{`${value}×`}</option>)}
        </select>
        {button('Download', <Download aria-hidden size={16}/>, () => downloadImage({ src, alt: name }))}
        {pipSupported && button('Picture in picture', <PictureInPicture2 aria-hidden size={16}/>, pip)}
        {button(fullscreen ? 'Exit fullscreen' : 'Fullscreen', fullscreen ? <Minimize aria-hidden size={16}/> : <Maximize aria-hidden size={16}/>, toggleFullscreen)}
      </div>}
      {editable && !uploading && <span className={styles.toolbar}>{button('Delete', <ToolbarIcon make={deleteIcon}/>, () => onRemove?.())}</span>}
    </div>
  )
}
