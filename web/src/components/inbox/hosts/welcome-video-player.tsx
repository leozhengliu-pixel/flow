import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { useI18n } from '@/i18n/i18n'
import './welcome-video-player.css'

/** Flow's own product walkthrough, rendered by web/scripts/render-intro-video.mjs (see public/media/README.md). */
const INTRO_VIDEO = {
  mp4: '/media/flow-intro.mp4',
  webm: '/media/flow-intro.webm',
  poster: '/media/flow-intro-poster.jpg',
  captions: '/media/flow-intro.en.vtt',
} as const

const RATES = [0.5, 1, 1.25, 1.5, 2]

function formatTime(seconds: number) {
  if (!Number.isFinite(seconds)) return '--:--'
  const whole = Math.max(0, Math.floor(seconds))
  return `${String(Math.floor(whole / 60)).padStart(2, '0')}:${String(whole % 60).padStart(2, '0')}`
}

function Icon({ children }: { children: React.ReactNode }) {
  return <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">{children}</svg>
}

/**
 * The welcome notification's video player. Never autoplays (so reduced-motion needs no special case beyond the
 * transitions in the stylesheet), starts muted-state-agnostic, and shows its captions in an overlay of its own.
 */
export function WelcomeVideoPlayer() {
  const { t } = useI18n()
  const box = useRef<HTMLDivElement>(null)
  const video = useRef<HTMLVideoElement>(null)
  const [playing, setPlaying] = useState(false)
  const [time, setTime] = useState(0)
  const [duration, setDuration] = useState(Number.NaN)
  const [muted, setMuted] = useState(false)
  const [volume, setVolume] = useState(1)
  const [rate, setRate] = useState(1)
  const [caption, setCaption] = useState('')
  const pictureInPicture = typeof document !== 'undefined' && document.pictureInPictureEnabled === true

  useEffect(() => {
    const track = video.current?.textTracks[0]
    if (!track) return
    track.mode = 'hidden'
    const update = () => setCaption(Array.from(track.activeCues ?? []).map(cue => (cue as VTTCue).text).join(' '))
    track.addEventListener('cuechange', update)
    return () => track.removeEventListener('cuechange', update)
  }, [])

  const toggle = () => {
    const element = video.current
    if (!element) return
    if (element.paused) void element.play().catch(() => {})
    else element.pause()
  }
  const toggleMute = () => { if (video.current) video.current.muted = !video.current.muted }
  const seekTo = (seconds: number) => {
    const element = video.current
    if (!element || !Number.isFinite(element.duration)) return
    element.currentTime = Math.min(Math.max(seconds, 0), element.duration)
  }
  const setPlaybackRate = (next: number) => {
    if (video.current) video.current.playbackRate = next
    setRate(next)
  }
  const fullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen()
    else void box.current?.requestFullscreen?.()
  }
  const pip = () => {
    if (document.pictureInPictureElement) void document.exitPictureInPicture()
    else void video.current?.requestPictureInPicture?.().catch(() => {})
  }
  const download = () => {
    const link = document.createElement('a')
    link.href = INTRO_VIDEO.mp4
    link.download = 'flow-intro.mp4'
    link.click()
  }
  const seekFromPointer = (event: PointerEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    seekTo(((event.clientX - rect.left) / rect.width) * (video.current?.duration ?? 0))
  }
  const volumeFromPointer = (event: PointerEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    const next = Math.min(Math.max(1 - (event.clientY - rect.top) / rect.height, 0), 1)
    if (video.current) { video.current.volume = next; video.current.muted = next === 0 }
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return
    const target = event.target as HTMLElement
    if (target.tagName === 'SELECT') return
    const key = event.key.toLowerCase()
    if (key === 'k' || (key === ' ' && target.tagName !== 'BUTTON' && target.tagName !== 'A')) toggle()
    else if (key === 'm') toggleMute()
    else if (key === 'f') fullscreen()
    else if (key === 'p' && pictureInPicture) pip()
    else if (key === '>' || key === '<') setPlaybackRate(RATES[Math.min(Math.max(RATES.indexOf(rate) + (key === '>' ? 1 : -1), 0), RATES.length - 1)])
    else return
    event.preventDefault()
  }
  const onSeekKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === 'ArrowRight' || event.key === 'ArrowUp' ? 5 : event.key === 'ArrowLeft' || event.key === 'ArrowDown' ? -5 : 0
    if (!step) return
    event.preventDefault()
    event.stopPropagation()
    seekTo(time + step)
  }

  const progress = Number.isFinite(duration) && duration > 0 ? Math.min(time / duration, 1) : 0
  return <div ref={box} className="flow-welcome-player" tabIndex={-1} onKeyDown={onKeyDown} role="group" aria-label={t('Welcome video')}>
    <video
      ref={video}
      className="flow-welcome-player__video"
      poster={INTRO_VIDEO.poster}
      preload="none"
      playsInline
      aria-label={t('Flow product walkthrough: create an issue, move it on the board and ask the Flow Agent')}
      onClick={toggle}
      onPlay={() => setPlaying(true)}
      onPause={() => setPlaying(false)}
      onEnded={() => setPlaying(false)}
      onTimeUpdate={event => setTime(event.currentTarget.currentTime)}
      onLoadedMetadata={event => setDuration(event.currentTarget.duration)}
      onDurationChange={event => setDuration(event.currentTarget.duration)}
      onVolumeChange={event => { setMuted(event.currentTarget.muted); setVolume(event.currentTarget.volume) }}
    >
      <source src={INTRO_VIDEO.webm} type="video/webm"/>
      <source src={INTRO_VIDEO.mp4} type="video/mp4"/>
      <track kind="captions" srcLang="en" label="English" src={INTRO_VIDEO.captions}/>
    </video>
    {caption && <p className="flow-welcome-player__caption" aria-hidden="true">{caption}</p>}
    <div className="flow-welcome-player__controls">
      <div className="flow-welcome-player__bar">
        <button type="button" className="flow-welcome-player__button" aria-label={`${playing ? t('Pause') : t('Play')} (${t('keyboard shortcut')} k)`} onClick={toggle}>
          {playing
            ? <Icon><path d="M4.5 3.5v9M11.5 3.5v9" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/></Icon>
            : <Icon><path d="M4.5 3.1v9.8c0 .5.5.8.9.5l7.4-4.9a.6.6 0 0 0 0-1L5.4 2.6c-.4-.3-.9 0-.9.5Z" fill="currentColor"/></Icon>}
        </button>
        <div className="flow-welcome-player__volume">
          <button type="button" className="flow-welcome-player__button" aria-label={`${muted ? t('Unmute') : t('Mute')} (${t('keyboard shortcut')} m)`} onClick={toggleMute}>
            <Icon>
              <path d="M1 5.3v5.4h2.6L7 14V2L3.6 5.3H1Z" fill="currentColor"/>
              {!muted && volume > 0 && <path d="M9.6 5.5a3.5 3.5 0 0 1 0 5M11.4 3.7a6 6 0 0 1 0 8.6" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>}
              {(muted || volume === 0) && <path d="M10 6l4 4m0-4l-4 4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>}
            </Icon>
          </button>
          <div className="flow-welcome-player__volume-popover">
            <div className="flow-welcome-player__volume-track" role="slider" aria-label={t('Volume')} aria-orientation="vertical" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round((muted ? 0 : volume) * 100)} tabIndex={0}
              onPointerDown={event => { event.currentTarget.setPointerCapture(event.pointerId); volumeFromPointer(event) }}
              onPointerMove={event => { if (event.buttons) volumeFromPointer(event) }}
              onKeyDown={event => {
                const step = event.key === 'ArrowUp' || event.key === 'ArrowRight' ? 0.1 : event.key === 'ArrowDown' || event.key === 'ArrowLeft' ? -0.1 : 0
                if (!step || !video.current) return
                event.preventDefault()
                event.stopPropagation()
                video.current.volume = Math.min(Math.max(video.current.volume + step, 0), 1)
              }}>
              <span style={{ height: `${(muted ? 0 : volume) * 100}%` }}/>
            </div>
          </div>
        </div>
        <div className="flow-welcome-player__timeline">
          <time className="flow-welcome-player__time" role="timer" aria-label={t('Elapsed')}>{formatTime(time)}</time>
          <div className="flow-welcome-player__seek" role="slider" aria-label={t('Seek')} aria-valuemin={0} aria-valuemax={Number.isFinite(duration) ? Math.round(duration) : 0} aria-valuenow={Math.round(time)} aria-valuetext={`${formatTime(time)} / ${formatTime(duration)}`} tabIndex={0}
            onPointerDown={event => { event.currentTarget.setPointerCapture(event.pointerId); seekFromPointer(event) }}
            onPointerMove={event => { if (event.buttons) seekFromPointer(event) }}
            onKeyDown={onSeekKey}>
            <div className="flow-welcome-player__seek-fill"><span style={{ width: `${progress * 100}%` }}/></div>
          </div>
          <time className="flow-welcome-player__time" aria-label={t('Duration')}>{formatTime(duration)}</time>
        </div>
        <select className="flow-welcome-player__rate" aria-label={`${t('Change playback rate')} (${t('keyboard shortcut')} > ${t('or')} <)`} value={rate} onChange={event => setPlaybackRate(Number(event.target.value))}>
          {RATES.map(value => <option key={value} value={value}>{`${value}×`}</option>)}
        </select>
        <button type="button" className="flow-welcome-player__button flow-welcome-player__download" aria-label={t('Download')} onClick={download}>
          <Icon><path d="M8 2v7M5 6.5 8 9.5l3-3M2.5 12.5h11" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/></Icon>
        </button>
        <button type="button" className="flow-welcome-player__button flow-welcome-player__pip" aria-label={`${t('Picture-in-Picture')} (${t('keyboard shortcut')} p)`} disabled={!pictureInPicture} onClick={pip}>
          <Icon><rect x="2.5" y="3" width="11" height="9" rx="1.6" stroke="currentColor" strokeWidth="1.3"/><rect x="7.5" y="7.5" width="5" height="3.5" rx=".8" fill="currentColor"/></Icon>
        </button>
        <button type="button" className="flow-welcome-player__button" aria-label={`${t('Full window')} (${t('keyboard shortcut')} f)`} onClick={fullscreen}>
          <Icon><path d="M9.5 2.5h4v4M6.5 13.5h-4v-4M13.5 2.5 9 7M2.5 13.5 7 9" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/></Icon>
        </button>
      </div>
    </div>
  </div>
}
