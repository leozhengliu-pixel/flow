import { useEffect, useRef, useState } from 'react'
import { realtimeClientId, updatePresence } from '@/lib/api'
import { loadRealtimeCache, saveRealtimeCache, saveRealtimeCursor } from '@/lib/realtime-cache'
import type { BootstrapData, Presence, RealtimeEvent } from '@/types/flow'

export function useWorkspaceRealtime({ workspaceKey, issueId, route, snapshot, onRemoteSync }: {
  workspaceKey?: string
  issueId?: string
  route: string
  snapshot?: BootstrapData | null
  onRemoteSync: (event: RealtimeEvent) => Promise<void>
}) {
  const [presence, setPresence] = useState<Presence[]>([])
  const [connected, setConnected] = useState(false)
  const syncRef = useRef(onRemoteSync)
  const timerRef = useRef<number | undefined>(undefined)
  const queuedRef = useRef<RealtimeEvent[]>([])
  const syncingRef = useRef(false)
  syncRef.current = onRemoteSync

  useEffect(() => {
    if (!workspaceKey) {
      setPresence([])
      return
    }
    setPresence([])
    const clientId = realtimeClientId()
    let stream: EventSource | undefined
    let disposed = false
    let cursorTimer: number | undefined
    let latestCursor = ''
    const drain = async () => {
      if (syncingRef.current || !queuedRef.current.length) return
      syncingRef.current = true
      const event = queuedRef.current.shift()!
      try { await syncRef.current(event) }
      finally { syncingRef.current = false; if (queuedRef.current.length) void drain() }
    }
    const schedule = (event: RealtimeEvent) => {
      if (event.type === 'issue.updated' && event.aggregateId && queuedRef.current.every(item => item.type === 'issue.updated')) {
        const index = queuedRef.current.findIndex(item => item.aggregateId === event.aggregateId)
        if (index >= 0) queuedRef.current[index] = event
        else queuedRef.current.push(event)
      } else {
        // A bootstrap resync for a broad event also includes any earlier issue
        // patches, so it safely replaces the pending queue.
        queuedRef.current = [event]
      }
      window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(() => void drain(), 80)
    }
    const connect = (cursor?: string) => {
      if (disposed) return
      const params = new URLSearchParams({ workspace: workspaceKey })
      if (cursor) params.set('since', cursor)
      stream = new EventSource(`/api/realtime/events?${params.toString()}`)
      stream.onopen = () => setConnected(true)
      stream.onerror = () => setConnected(false)
      stream.onmessage = message => {
        if (message.lastEventId) {
          latestCursor = message.lastEventId
          window.clearTimeout(cursorTimer)
          cursorTimer = window.setTimeout(() => {
            if (latestCursor) void saveRealtimeCursor(workspaceKey, latestCursor)
          }, 300)
        }
        let event: RealtimeEvent
        try { event = JSON.parse(message.data) as RealtimeEvent }
        catch { return }
        if (event.payload?.presence) setPresence(event.payload.presence)
        if (event.type === 'connected') {
          return
        }
        if (event.type === 'presence.updated' || event.clientId === clientId) return
        schedule(event)
      }
    }
    void loadRealtimeCache(workspaceKey).then(cache => connect(cache?.cursor))
    return () => {
      disposed = true
      stream?.close()
      window.clearTimeout(timerRef.current)
      window.clearTimeout(cursorTimer)
      setConnected(false)
      setPresence([])
      queuedRef.current = []
    }
  }, [workspaceKey])

  useEffect(() => {
    if (!workspaceKey || !snapshot) return
    const timer = window.setTimeout(() => {
      void loadRealtimeCache(workspaceKey).then(cache => saveRealtimeCache({
        workspaceKey,
        cursor: cache?.cursor,
        snapshot,
        updatedAt: new Date().toISOString(),
      }))
    }, 1200)
    return () => window.clearTimeout(timer)
  }, [snapshot, workspaceKey])

  useEffect(() => {
    if (!workspaceKey) return
    const clientId = realtimeClientId()
    const heartbeat = () => void updatePresence(clientId, issueId, route).then(setPresence).catch(() => undefined)
    heartbeat()
    const timer = window.setInterval(heartbeat, 20_000)
    return () => {
      window.clearInterval(timer)
      const body = new Blob([JSON.stringify({ clientId, issueId, route, active: false })], { type: 'application/json' })
      navigator.sendBeacon(`/api/realtime/presence?workspace=${encodeURIComponent(workspaceKey)}`, body)
    }
  }, [issueId, route, workspaceKey])

  return { connected, presence, clientId: realtimeClientId() }
}
