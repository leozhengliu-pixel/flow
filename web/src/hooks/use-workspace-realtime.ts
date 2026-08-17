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
  const queuedRef = useRef<RealtimeEvent | undefined>(undefined)
  const syncingRef = useRef(false)
  syncRef.current = onRemoteSync

  useEffect(() => {
    if (!workspaceKey) return
    const clientId = realtimeClientId()
    const stream = new EventSource(`/api/realtime/events?workspace=${encodeURIComponent(workspaceKey)}`)
    const drain = async () => {
      if (syncingRef.current || !queuedRef.current) return
      syncingRef.current = true
      const event = queuedRef.current
      queuedRef.current = undefined
      try { await syncRef.current(event) }
      finally { syncingRef.current = false; if (queuedRef.current) void drain() }
    }
    const schedule = (event: RealtimeEvent) => {
      queuedRef.current = event
      window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(() => void drain(), 80)
    }
    stream.onopen = () => setConnected(true)
    stream.onerror = () => setConnected(false)
    stream.onmessage = message => {
      const event = JSON.parse(message.data) as RealtimeEvent
      if (event.payload?.presence) setPresence(event.payload.presence)
      if (event.type === 'connected' || event.type === 'presence.updated' || event.clientId === clientId) return
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
