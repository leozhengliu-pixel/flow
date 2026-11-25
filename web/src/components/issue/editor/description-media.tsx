import { useRef, useState, useSyncExternalStore } from 'react'
import { Download, Pause, Play, Volume2, VolumeX } from 'lucide-react'
import { AgentInlineVideo } from '@/components/agent/agent-inline-video'
import { useOptionalTranslate } from '@/i18n/i18n'
import { formatFileSize } from '@/lib/format-file-size'
import { isUploadInFlight, readUploadProgress, subscribeUploadProgress } from '@/lib/upload-progress'
import { downloadPdf } from '@/components/editor/pdf/pdf-file-actions'
import './description-media.css'

const RATES = [0.5, 1, 1.25, 1.5, 2]
const clock = (seconds: number) => {
  const total = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(Math.floor(total / 60))}:${pad(total % 60)}`
}

function AudioButton({ label, run, className, children }: { label: string; run: () => void; className?: string; children: React.ReactNode }) {
  return <button aria-label={label} className={className} onClick={event => { event.preventDefault(); event.stopPropagation(); run() }} onMouseDown={event => event.preventDefault()} title={label} type="button">{children}</button>
}

/** An audio attachment (the design reference's layout): a card with play, mute, time, seek, speed and download, and the file's name and size beneath it. */
export function DescriptionAudio({ src, name, size }: { src: string; name: string; size?: number | null }) {
  const t = useOptionalTranslate()
  const audio = useRef<HTMLAudioElement>(null)
  const [time, setTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [muted, setMuted] = useState(false)
  const [rate, setRate] = useState(1)
  const progress = duration ? Math.min(100, (time / duration) * 100) : 0
  const toggle = () => { const node = audio.current; if (!node) return; if (node.paused) void node.play()?.catch(() => undefined); else node.pause() }
  const next = () => { const node = audio.current; if (!node) return; const value = RATES[(RATES.indexOf(rate) + 1) % RATES.length]; node.playbackRate = value; setRate(value) }
  return (
    <div className="description-audio" role="group" aria-label={name}>
      <div className="description-audio-card" aria-label={t('Media player')} role="group">
        <audio onDurationChange={event => setDuration(event.currentTarget.duration)} onEnded={() => setPlaying(false)} onLoadedMetadata={event => setDuration(event.currentTarget.duration)} onPause={() => setPlaying(false)} onPlay={() => setPlaying(true)} onTimeUpdate={event => setTime(event.currentTarget.currentTime)} onVolumeChange={event => setMuted(event.currentTarget.muted)} preload="metadata" ref={audio} src={src}/>
        <div className="description-audio-row">
          <AudioButton label={playing ? 'Pause' : 'Play'} run={toggle}>{playing ? <Pause aria-hidden fill="currentColor" size={14}/> : <Play aria-hidden fill="currentColor" size={14}/>}</AudioButton>
          <AudioButton label={muted ? 'Unmute' : 'Mute'} run={() => { if (audio.current) audio.current.muted = !audio.current.muted }}>{muted ? <VolumeX aria-hidden size={14}/> : <Volume2 aria-hidden size={14}/>}</AudioButton>
          <time>{clock(time)}</time>
          <div className="description-audio-seek" aria-label={t('Seek slider')}>
            <span className="description-audio-track"/>
            <span className="description-audio-buffer"/>
            <span className="description-audio-played" style={{ width: `${progress}%` }}/>
            <span className="description-audio-thumb" style={{ left: `${progress}%` }}/>
            <input aria-label="Seek" max={duration || 0} min={0} onChange={event => { if (audio.current) audio.current.currentTime = Number(event.target.value) }} step="any" type="range" value={Math.min(time, duration || 0)}/>
          </div>
          <time>{clock(duration)}</time>
          <AudioButton className="description-audio-rate" label="Change playback speed" run={next}><span>{`${rate}×`}</span></AudioButton>
          <AudioButton label="Download" run={() => downloadPdf(src, name)}><Download aria-hidden size={14}/></AudioButton>
        </div>
      </div>
      <div className="description-audio-caption"><span>{name}</span>{size != null && <><span className="description-audio-dot">·</span><span>{formatFileSize(Number(size) || 0)}</span></>}</div>
    </div>
  )
}

const DOT_COLUMNS = 5
const DOT_PITCH = 3.5
const DOTS = Array.from({ length: DOT_COLUMNS * DOT_COLUMNS }, (_, index) => ({ cx: 1 + (index % DOT_COLUMNS) * DOT_PITCH, cy: 1 + Math.floor(index / DOT_COLUMNS) * DOT_PITCH }))

/** A 5 by 5 grid of dots that fills left to right, top to bottom, as the file is sent; the newest dot blinks. */
export function UploadDots({ fraction }: { fraction: number }) {
  const t = useOptionalTranslate()
  const lit = Math.floor(fraction * DOTS.length)
  return <svg aria-label={t('Progress {percent}%').replace('{percent}', () => String(Math.round(fraction * 100)))} className="description-upload-dots" height={16} role="img" viewBox="0 0 16 16" width={16}>
    {DOTS.map((dot, index) => <circle className={index === lit - 1 ? 'description-upload-dot-pulse' : undefined} cx={dot.cx} cy={dot.cy} fill="currentColor" key={index} opacity={index < lit ? 1 : 0.3} r={1}/>)}
  </svg>
}

/** The progress strip of a file still uploading (a dot grid and "Uploading…" after a hairline); `src` names the upload. */
export function DescriptionUploading({ src, onRemove }: { src: string; onRemove?: () => void }) {
  const t = useOptionalTranslate()
  const fraction = useSyncExternalStore(listener => subscribeUploadProgress(src, listener), () => readUploadProgress(src))
  const running = useSyncExternalStore(listener => subscribeUploadProgress(src, listener), () => isUploadInFlight(src))
  // A placeholder that no upload in this page is feeding was saved by a page that closed (or lost its connection) mid-upload.
  if (!running) return <div className="description-file-uploading" data-stale><span className="description-file-uploading-rule"/><span className="description-file-uploading-label">{t("Upload didn't complete")}</span>{onRemove && <button className="description-file-uploading-remove" onClick={event => { event.preventDefault(); event.stopPropagation(); onRemove() }} type="button">{t('Remove')}</button>}</div>
  return <div className="description-file-uploading"><span className="description-file-uploading-rule"/><UploadDots fraction={fraction}/><span className="description-file-uploading-label">Uploading…</span></div>
}

export function DescriptionVideoPlayer({ src, name }: { src: string; name: string }) {
  return <AgentInlineVideo hairline name={name} src={src}/>
}
