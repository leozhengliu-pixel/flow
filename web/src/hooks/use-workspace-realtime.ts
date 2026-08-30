import { useEffect, useRef, useState } from 'react'
import { realtimeClientId, updatePresence } from '@/lib/api'
import type { Presence, RealtimeEvent } from '@/types/flow'

export function useWorkspaceRealtime({ workspaceKey, issueId, route, onRemoteSync }: {
  workspaceKey?: string
  issueId?: string
  route: string
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
    if (!workspaceKey) return
    const clientId = realtimeClientId()
    const stream = new EventSource(`/api/realtime/events?workspace=${encodeURIComponent(workspaceKey)}`)
    let initialized = false
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
    stream.onopen = () => setConnected(true)
    stream.onerror = () => setConnected(false)
    stream.onmessage = message => {
      let event: RealtimeEvent
      try { event = JSON.parse(message.data) as RealtimeEvent }
      catch { return }
      if (event.payload?.presence) setPresence(event.payload.presence)
      if (event.type === 'connected') {
        if (initialized) schedule(event)
        else initialized = true
        return
      }
      if (event.type === 'presence.updated' || event.clientId === clientId) return
      schedule(event)
    }
    return () => {
      stream.close()
      window.clearTimeout(timerRef.current)
      setConnected(false)
    }
  }, [workspaceKey])

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
