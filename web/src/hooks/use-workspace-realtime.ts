import { useEffect, useRef, useState } from 'react'
import { realtimeClientId, updatePresence } from '@/lib/api'
import { loadRealtimeCursor, saveRealtimeCursor } from '@/lib/realtime-cache'
import { RealtimeEventQueue } from '@/lib/realtime-event-queue'
import type { BootstrapData, Presence, RealtimeEvent } from '@/types/flow'
import { ISSUE_QUERY_INVALIDATED, type IssueQueryInvalidation } from '@/components/issue-explorer/paged-issue-invalidation'

export function useWorkspaceRealtime({ workspaceKey, issueId, route, onRemoteSync }: {
  workspaceKey?: string
  issueId?: string
  route: string
  snapshot?: BootstrapData | null
  onRemoteSync: (event: RealtimeEvent) => Promise<void>
}) {
  const [presence, setPresence] = useState<Presence[]>([])
  const [connected, setConnected] = useState(false)
  const syncRef = useRef(onRemoteSync)
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
    let timer: number | undefined
    let syncing = false
    const queue = new RealtimeEventQueue()
    let cursorTimer: number | undefined
    let latestCursor = ''
    const drain = async () => {
      timer = undefined
      if (disposed || syncing || !queue.length) return
      syncing = true
      const event = queue.shift()!
      let failed = false
      try {
        // Drop protected rows before awaiting permission reconciliation. A
        // background refresh must never keep revoked data visible on failure.
        const force = /^(resync$|workspace\.resync_required$|issue\.(permissions_updated|permission_updated|permission_deleted|shared|unshared)$|issue_permission\.|team_member\.|workspace_member\.|membership\.)/.test(event.type)
        if (force || event.type === 'issue.deleted') {
          const detail: IssueQueryInvalidation = { workspaceKey, issueId: event.aggregateId, issue: event.payload?.issue, force }
          window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail }))
        }
        if (['label.deleted', 'issue_label.deleted', 'label_group.deleted'].includes(event.type)) {
          const labelIds = (event.payload as { labelIds?: unknown } | undefined)?.labelIds
          if (Array.isArray(labelIds)) window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail: { workspaceKey, labelIds: labelIds.filter((id): id is string => typeof id === 'string') } satisfies IssueQueryInvalidation }))
        }
        await syncRef.current(event)
      }
      catch {
        failed = true
        queue.push({ id: event.id, type: 'resync', createdAt: event.createdAt }, 0)
      } finally {
        syncing = false
        if (!disposed && queue.length) timer = window.setTimeout(() => void drain(), failed ? 1000 : 0)
      }
    }
    const schedule = (event: RealtimeEvent, wireLength: number) => {
      queue.push(event, wireLength)
      if (timer === undefined && !syncing) timer = window.setTimeout(() => void drain(), 80)
    }
    const connect = (cursor?: string) => {
      if (disposed) return
      const params = new URLSearchParams({ workspace: workspaceKey })
      if (import.meta.env.VITE_PAGED_ISSUES === 'true') params.set('issues', 'paged')
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
        schedule(event, message.data.length)
      }
    }
    connect(loadRealtimeCursor(workspaceKey))
    return () => {
      disposed = true
      stream?.close()
      window.clearTimeout(timer)
      window.clearTimeout(cursorTimer)
      setConnected(false)
      setPresence([])
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
      navigator.sendBeacon(`/api/realtime/presence?workspace=${encodeURIComponent(workspaceKey)}${import.meta.env.VITE_PAGED_ISSUES === 'true' ? '&issues=paged' : ''}`, body)
    }
  }, [issueId, route, workspaceKey])

  return { connected, presence, clientId: realtimeClientId() }
}
