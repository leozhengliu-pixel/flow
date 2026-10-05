import { useSyncExternalStore } from 'react'
import { fetchPulseSummaryAudio } from '@/lib/api'

/**
 * One shared player for Pulse summary audio ("Listen"). The inbox "Your Pulse"
 * view starts it; the sidebar Pulse item shows an inline Play/Pause button
 * while a summary is playing or paused.
 */
export type PulseAudioStatus = 'idle' | 'loading' | 'playing' | 'paused' | 'error'
export type PulseAudioState = {
  notificationId?: string
  status: PulseAudioStatus
  currentTime: number
  duration: number
  playbackRate: number
}

export const PULSE_PLAYBACK_RATES = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.25] as const
const RATE_KEY = 'flow.pulse.playbackRate'

let state: PulseAudioState = { status: 'idle', currentTime: 0, duration: 0, playbackRate: readRate() }
let audio: HTMLAudioElement | undefined
let objectUrl: string | undefined
let controller: AbortController | undefined
const listeners = new Set<() => void>()

function readRate() {
  try {
    const value = Number(localStorage.getItem(RATE_KEY))
    return PULSE_PLAYBACK_RATES.includes(value as typeof PULSE_PLAYBACK_RATES[number]) ? value : 1
  } catch { return 1 }
}

function set(next: Partial<PulseAudioState>) {
  state = { ...state, ...next }
  for (const listener of listeners) listener()
}

function element() {
  if (audio) return audio
  audio = new Audio()
  audio.preload = 'auto'
  audio.addEventListener('timeupdate', () => set({ currentTime: audio!.currentTime }))
  audio.addEventListener('durationchange', () => set({ duration: Number.isFinite(audio!.duration) ? audio!.duration : 0 }))
  audio.addEventListener('play', () => set({ status: 'playing' }))
  audio.addEventListener('pause', () => { if (state.status === 'playing') set({ status: 'paused' }) })
  audio.addEventListener('ended', () => set({ status: 'paused', currentTime: 0 }))
  return audio
}

export const pulseAudio = {
  get state() { return state },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => { listeners.delete(listener) }
  },
  /** Loads (once) and plays the summary's audio. */
  async play(notificationId: string) {
    const player = element()
    if (state.notificationId === notificationId && objectUrl) {
      player.playbackRate = state.playbackRate
      await player.play().catch(() => set({ status: 'paused' }))
      return
    }
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    player.pause()
    if (objectUrl) URL.revokeObjectURL(objectUrl)
    objectUrl = undefined
    set({ notificationId, status: 'loading', currentTime: 0, duration: 0 })
    try {
      const blob = await fetchPulseSummaryAudio(notificationId, signal)
      if (signal.aborted) return
      objectUrl = URL.createObjectURL(blob)
      player.src = objectUrl
      player.playbackRate = state.playbackRate
      await player.play()
    } catch {
      if (!signal.aborted) set({ status: 'error' })
    }
  },
  pause() {
    audio?.pause()
    if (state.status === 'playing') set({ status: 'paused' })
  },
  toggle(notificationId = state.notificationId) {
    if (!notificationId) return
    if (state.notificationId === notificationId && state.status === 'playing') pulseAudio.pause()
    else void pulseAudio.play(notificationId)
  },
  setPlaybackRate(rate: number) {
    if (audio) audio.playbackRate = rate
    try { localStorage.setItem(RATE_KEY, String(rate)) } catch { /* in-memory only */ }
    set({ playbackRate: rate })
  },
  stop() {
    controller?.abort()
    audio?.pause()
    if (objectUrl) URL.revokeObjectURL(objectUrl)
    objectUrl = undefined
    set({ notificationId: undefined, status: 'idle', currentTime: 0, duration: 0 })
  },
}

export function usePulseAudio() {
  return useSyncExternalStore(pulseAudio.subscribe, () => state, () => state)
}

/** mm:ss for the Listen button. */
export function formatPulseAudioTime(seconds: number) {
  const total = Math.max(0, Math.floor(seconds))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

export function formatPlaybackRate(rate: number) {
  return `${rate}×`
}
