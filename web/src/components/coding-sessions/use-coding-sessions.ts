import { useCallback, useEffect, useRef, useState } from 'react'
import { AGENT_TASK_ACTIVITY_EVENT } from '@/lib/application-agents'
import { fetchCodingModels, fetchCodingSession, fetchCodingSessions, fetchCodingStatus } from '@/lib/coding-sessions-api'
import type { BootstrapData } from '@/types/flow'
import type { CodingModelOption, CodingSession, CodingSessionState } from '@/types/coding-sessions'
import { CODING_SESSION_EVENT, type CodingSessionEventDetail } from './coding-session-events'
import { isCodingSessionLive } from './coding-session-model'

/** A slow safety poll while a session is live, in case a push is missed (pushes are the normal path). */
export const CODING_SESSION_FALLBACK_POLL_MS = 10_000
const AVAILABILITY_TTL_MS = 5 * 60_000

let availability: { at: number; promise: Promise<boolean> } | undefined

/** Clears the cached `/api/coding/status` answer (tests, and after an admin toggles coding sessions). */
export function resetCodingAvailabilityCache() { availability = undefined; modelOptions = undefined; listRequests.clear() }

let modelOptions: Promise<CodingModelOption[]> | undefined

/** The catalog label of a model ("GPT-5 Codex"), loaded once from `/api/coding/models`; the id until it loads. */
export function useCodingModelLabel(model: string | undefined, harness: string | undefined) {
  const [options, setOptions] = useState<CodingModelOption[]>()
  useEffect(() => {
    if (!model) return
    let active = true
    modelOptions ??= fetchCodingModels().catch(() => { modelOptions = undefined; return [] })
    void modelOptions.then(list => { if (active) setOptions(list) })
    return () => { active = false }
  }, [model])
  if (!model) return ''
  return options?.find(option => option.id === model && !option.auto && (!harness || option.harness === harness))?.label || model
}

function loadAvailability(): Promise<boolean> {
  if (availability && Date.now() - availability.at < AVAILABILITY_TTL_MS) return availability.promise
  const promise = fetchCodingStatus().then(status => Boolean(status?.enabled), () => false)
  availability = { at: Date.now(), promise }
  return promise
}

/** The workspace's `coding-sessions` flag (on unless turned off, like the settings page). */
export const codingFeatureEnabled = (data: Pick<BootstrapData, 'workspaceSettings'> | undefined) => (data?.workspaceSettings?.featureFlags?.['coding-sessions'] ?? true) !== false

/**
 * Whether coding session entry points show: the workspace flag and the server runtime (`GET /api/coding/status`,
 * cached for five minutes and shared by every caller).
 */
export function useCodingAvailability(data?: Pick<BootstrapData, 'workspaceSettings'>, enabled = true): boolean {
  const flag = codingFeatureEnabled(data)
  const [available, setAvailable] = useState(false)
  useEffect(() => {
    if (!flag || !enabled) { setAvailable(false); return }
    let active = true
    void loadAvailability().then(value => { if (active) setAvailable(value) })
    return () => { active = false }
  }, [flag, enabled])
  return flag && enabled && available
}

const matchesSession = (detail: CodingSessionEventDetail | undefined, session: Pick<CodingSession, 'id' | 'agentSessionId' | 'issueId'> | undefined, sessionId: string) => {
  if (!detail || !detail.sessionId) return true
  return detail.sessionId === sessionId || Boolean(session && (detail.agentSessionId === session.agentSessionId || (detail.issueId && detail.issueId === session.issueId)))
}

/**
 * One session, kept live: refetched on `coding_session.*` pushes for it, on agent task pushes (activities on its
 * anchor), and by a fallback poll while it is live.
 */
export function useCodingSession(sessionId: string | undefined, initial?: CodingSession) {
  const [session, setSession] = useState<CodingSession | undefined>(sessionId && initial?.id === sessionId ? initial : undefined)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const latest = useRef(session)
  latest.current = session
  useEffect(() => {
    if (!sessionId) return
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let debounce: ReturnType<typeof setTimeout> | undefined
    let loading = false, queued = false
    const load = async () => {
      clearTimeout(timer); timer = undefined
      if (loading) { queued = true; return }
      loading = true
      let live = false
      try {
        const next = await fetchCodingSession(sessionId)
        if (abort.signal.aborted) return
        setSession(previous => previous && previous.id === next.id && previous.version > next.version ? previous : next)
        setError('')
        live = isCodingSessionLive(next)
      } catch (cause) {
        if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause))
      } finally {
        loading = false
        if (!abort.signal.aborted && (queued || live)) timer = setTimeout(() => void load(), queued ? 0 : CODING_SESSION_FALLBACK_POLL_MS)
        queued = false
      }
    }
    const schedule = () => { clearTimeout(debounce); debounce = setTimeout(() => void load(), 150) }
    const onCoding = (event: Event) => { if (matchesSession((event as CustomEvent<CodingSessionEventDetail>).detail, latest.current, sessionId)) schedule() }
    const onTask = (event: Event) => {
      const id = (event as CustomEvent<unknown>).detail
      const current = latest.current
      if (!id || !current || id === current.issueId || id === current.agentSessionId || id === current.sourceId) schedule()
    }
    window.addEventListener(CODING_SESSION_EVENT, onCoding)
    window.addEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
    void load()
    return () => {
      abort.abort(); clearTimeout(timer); clearTimeout(debounce)
      window.removeEventListener(CODING_SESSION_EVENT, onCoding)
      window.removeEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
    }
  }, [sessionId, revision])
  /** Applies an action's result at once (it is newer than the last fetch). */
  const apply = useCallback((next: CodingSession) => setSession(previous => previous && previous.id === next.id && previous.version > next.version ? previous : next), [])
  const refresh = useCallback(() => setRevision(value => value + 1), [])
  return { session, error, apply, refresh }
}

const listRequests = new Map<string, Promise<CodingSession[]>>()
/** Several views of one issue (one per agent task) share a list request while it is in flight. */
function fetchSessionsShared(params: { issueId?: string; creatorId?: string; state?: CodingSessionState }) {
  const key = JSON.stringify([params.issueId ?? '', params.creatorId ?? '', params.state ?? ''])
  const pending = listRequests.get(key)
  if (pending) return pending
  const promise = fetchCodingSessions(params).finally(() => listRequests.delete(key))
  listRequests.set(key, promise)
  return promise
}

/** A list of sessions (by issue, creator or state), refetched on any coding session push. */
export function useCodingSessions(params: { issueId?: string; creatorId?: string; state?: CodingSessionState }, enabled = true) {
  const [sessions, setSessions] = useState<CodingSession[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const { issueId, creatorId, state } = params
  useEffect(() => {
    if (!enabled) return
    let active = true
    let debounce: ReturnType<typeof setTimeout> | undefined
    let timer: ReturnType<typeof setTimeout> | undefined
    const load = async () => {
      clearTimeout(timer)
      try {
        const next = await fetchSessionsShared({ issueId, creatorId, state })
        if (!active) return
        setSessions(Array.isArray(next) ? next : []); setError('')
        if (Array.isArray(next) && next.some(isCodingSessionLive)) timer = setTimeout(() => void load(), CODING_SESSION_FALLBACK_POLL_MS)
      } catch (cause) {
        if (active) setError(cause instanceof Error ? cause.message : String(cause))
      } finally {
        if (active) setLoaded(true)
      }
    }
    const schedule = () => { clearTimeout(debounce); debounce = setTimeout(() => void load(), 150) }
    const onCoding = (event: Event) => {
      const detail = (event as CustomEvent<CodingSessionEventDetail>).detail
      if (!issueId || !detail?.issueId || detail.issueId === issueId) schedule()
    }
    const onTask = (event: Event) => {
      const id = (event as CustomEvent<unknown>).detail
      if (issueId && (!id || id === issueId)) schedule()
    }
    window.addEventListener(CODING_SESSION_EVENT, onCoding)
    window.addEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
    void load()
    return () => {
      active = false; clearTimeout(debounce); clearTimeout(timer)
      window.removeEventListener(CODING_SESSION_EVENT, onCoding)
      window.removeEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
    }
  }, [enabled, issueId, creatorId, state, revision])
  return { sessions, loaded, error, refresh: useCallback(() => setRevision(value => value + 1), []) }
}

const dismissedKey = (workspace: string, viewerId: string) => `flow:coding-sessions:dismissed:${workspace}:${viewerId}`

/** Sessions the viewer dismissed from lists and chat cards (per viewer, in this browser). */
export function useDismissedCodingSessions(workspace: string, viewerId: string) {
  const key = dismissedKey(workspace, viewerId)
  const read = () => {
    try { return new Set<string>(JSON.parse(localStorage.getItem(key) ?? '[]') as string[]) } catch { return new Set<string>() }
  }
  const [dismissed, setDismissed] = useState<Set<string>>(read)
  const dismiss = useCallback((sessionId: string) => {
    setDismissed(previous => {
      const next = new Set(previous).add(sessionId)
      try { localStorage.setItem(key, JSON.stringify([...next].slice(-200))) } catch { /* a convenience only */ }
      return next
    })
  }, [key])
  return { dismissed, dismiss }
}

/** Ticks once a second while `live`, for running timers. */
export function useNow(live: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!live) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [live])
  return now
}
