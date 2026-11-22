import { useEffect, useState } from 'react'
import { useLatestRef } from '@/hooks/use-latest-ref'
import { realtimeClientId, updatePresence } from '@/lib/api'
import { AGENT_TASK_ACTIVITY_EVENT } from '@/lib/application-agents'
// coding sessions (track G)
import { dispatchCodingSessionRealtimeEvent, notifyCodingSessionChanged } from '@/components/coding-sessions/coding-session-events'
import { INBOX_ACTIVITY_EVENT, inboxUnread } from '@/lib/inbox-unread'
import { loadRealtimeCursor, saveRealtimeCursor } from '@/lib/realtime-cache'
import { canApplyRealtimePatch } from '@/store/apply-realtime-patch'
import { RealtimeEventQueue } from '@/lib/realtime-event-queue'
import { connectSharedRealtime, type RealtimeAcknowledge, type RealtimeMessage } from '@/lib/shared-realtime'
import { APPLIED_CURSOR_SCHEMA, appliedRealtimeCursorEnabled, boundedBrowserCacheEnabled } from '@/lib/scale-p08'
import { SYNC_V2_STREAM_SCHEMA } from '@/lib/scale-p13-wire'
import { retainOpenIssueDetail } from '@/lib/issue-detail-cache'
import { clearNavigationCache } from '@/lib/navigation-cache'
import type { BootstrapData, Presence, RealtimeEvent } from '@/types/flow'
import { ISSUE_QUERY_INVALIDATED, type IssueQueryInvalidation } from '@/components/issue-explorer/paged-issue-invalidation'

export function useWorkspaceRealtime({ workspaceKey, viewerId, issueId, route, syncV2 = false, syncCursor, onRemoteSync }: {
  workspaceKey?: string
  /** Tabs share one stream only with other tabs of the same user. */
  viewerId?: string
  issueId?: string
  route: string
  snapshot?: BootstrapData | null
  syncV2?: boolean
  syncCursor?: string
  onRemoteSync: (event: RealtimeEvent) => Promise<void>
}) {
  const [presence, setPresence] = useState<Presence[]>([])
  const [connected, setConnected] = useState(false)
  const syncRef = useLatestRef(onRemoteSync)
  const appliedCursor = syncV2 || appliedRealtimeCursorEnabled()
  const cursorSchema = syncV2 ? SYNC_V2_STREAM_SCHEMA : APPLIED_CURSOR_SCHEMA
  const presenceViewerId = appliedCursor ? viewerId : undefined
  const boundedCache = boundedBrowserCacheEnabled()

  useEffect(() => boundedCache ? retainOpenIssueDetail(issueId) : undefined, [boundedCache, issueId, workspaceKey, viewerId])

  // Presence starts empty and the previous subscription's cleanup empties it again, so there is nothing to
  // reset here before subscribing.
  useEffect(() => {
    if (!workspaceKey) return
    const clientId = realtimeClientId()
    let disposed = false
    let timer: number | undefined
    let syncing = false
    const queue = new RealtimeEventQueue()
    let cursorTimer: number | undefined
    let latestCursor = ''
    const checkpoint = (cursor: string) => {
      if (disposed) return
      latestCursor = cursor
      window.clearTimeout(cursorTimer)
      cursorTimer = window.setTimeout(() => {
        if (!disposed && latestCursor) void saveRealtimeCursor(workspaceKey, latestCursor, appliedCursor ? viewerId : undefined, cursorSchema)
      }, 300)
    }
    // Consecutive self-contained entity patches (a bulk edit, an agent run) are applied in one task, so React
    // renders the collection once for the burst instead of once per event. Anything that fetches, or reads
    // the render-time snapshot, still yields between events.
    const MAX_EVENTS_PER_TASK = 64
    const drain = async () => {
      timer = undefined
      if (disposed || syncing || !queue.length) return
      syncing = true
      let failed = false
      try {
        for (let handled = 0; handled < MAX_EVENTS_PER_TASK && !disposed && queue.length && !failed; handled++) {
          const delivery = queue.shiftDelivery()!
          const event = delivery.event
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
            if (appliedCursor && !disposed) queue.complete(delivery)
            const next = queue.peek()
            if (!canApplyRealtimePatch(event) || !next || !canApplyRealtimePatch(next)) break
          }
          catch {
            failed = true
            queue.recover(delivery)
          }
        }
      } finally {
        syncing = false
        if (!disposed && queue.length) timer = window.setTimeout(() => void drain(), failed ? 1000 : 0)
      }
    }
    const schedule = (event: RealtimeEvent, wireLength: number, acknowledge?: RealtimeAcknowledge) => {
      queue.push(event, wireLength, acknowledge)
      if (timer === undefined && !syncing) timer = window.setTimeout(() => void drain(), 80)
    }
    // One tab per origin/workspace/user holds the EventSource and rebroadcasts
    // it; every tab (leader or not) receives the same raw messages here.
    const onMessage = (message: RealtimeMessage, acknowledge: RealtimeAcknowledge) => {
      if (disposed) return
      // `connected_*` ids are per-connection handshakes the server cannot resume from.
      if (!appliedCursor && message.lastEventId && !message.lastEventId.startsWith('connected_')) checkpoint(message.lastEventId)
      let event: RealtimeEvent
      try {
        event = JSON.parse(message.data) as RealtimeEvent
        if (appliedCursor && (!event || typeof event !== 'object' || typeof event.type !== 'string' || !event.type || typeof event.id !== 'string')) throw new Error('Invalid realtime event')
        if (appliedCursor && event.payload != null && (typeof event.payload !== 'object' || Array.isArray(event.payload) || event.payload.presence !== undefined && !Array.isArray(event.payload.presence))) throw new Error('Invalid realtime payload')
      }
      catch {
        if (appliedCursor) schedule({ id: message.lastEventId, type: 'resync', createdAt: new Date().toISOString() }, 0, acknowledge)
        return
      }
      if (appliedCursor && /^(resync$|workspace\.resync_required$|.*permission|issue\.(shared|unshared)$|team_member\.|workspace_member\.|membership\.)/.test(event.type)) {
        clearNavigationCache(viewerId, workspaceKey)
        // Revoke cached query rows immediately, even while an older apply is
        // waiting on the network. Reconciliation below remains ordered.
        window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail: { workspaceKey, issueId: event.aggregateId, force: true } satisfies IssueQueryInvalidation }))
      }
      // Agent task pushes (streamed thoughts and output) refresh only the task panels of their resource. They skip the
      // workspace sync queue so each chunk reaches the thread at once and never triggers a workspace reload.
      if (event.type.startsWith('agent_task.')) {
        window.dispatchEvent(new CustomEvent(AGENT_TASK_ACTIVITY_EVENT, { detail: event.aggregateId ?? '' }))
        // An agent's reply in a thread updated the Inbox notifications of the people in it.
        if (event.type === 'agent_task.replied') {
          inboxUnread.schedule(workspaceKey, 300)
          window.dispatchEvent(new CustomEvent(INBOX_ACTIVITY_EVENT, { detail: event }))
        }
        acknowledge()
        return
      }
      // coding sessions (track G): coding_session.* pushes refresh open session views without a workspace sync.
      if (dispatchCodingSessionRealtimeEvent(event)) { acknowledge(); return }
      // A resync means pushes were missed (cursor outside the server's replay window, a restart, a new leader that
      // saw no resumable id). Task panels and coding session views refetch only on their own pushes, so tell them
      // all to reload as well (an empty id matches every listener); the workspace sync below reloads the rest.
      if (event.type === 'resync' || event.type === 'workspace.resync_required') {
        window.dispatchEvent(new CustomEvent(AGENT_TASK_ACTIVITY_EVENT, { detail: '' }))
        notifyCodingSessionChanged({ sessionId: '' })
      }
      if (event.payload?.presence) setPresence(event.payload.presence)
      if (event.type === 'connected') {
        acknowledge()
        return
      }
      if (event.type === 'presence.updated' || !appliedCursor && event.clientId === clientId) { acknowledge(); return }
      schedule(event, message.data.length, appliedCursor ? acknowledge : undefined)
    }
    const disconnect = connectSharedRealtime({
      workspaceKey,
      userId: viewerId,
      schema: cursorSchema,
      appliedCursor,
      url: cursor => {
        const params = new URLSearchParams({ workspace: workspaceKey })
        if (import.meta.env.VITE_PAGED_ISSUES === 'true') params.set('issues', 'paged')
        if (syncV2) params.set('sync', '2')
        if (cursor) params.set('since', cursor)
        return `/api/realtime/events?${params.toString()}`
      },
      cursor: () => syncV2 ? syncCursor : appliedCursor ? undefined : loadRealtimeCursor(workspaceKey),
      onAppliedCursor: checkpoint,
      onStatus: connected => { if (!disposed) setConnected(connected) },
      onMessage,
    })
    return () => {
      disposed = true
      disconnect()
      window.clearTimeout(timer)
      window.clearTimeout(cursorTimer)
      setConnected(false)
      setPresence([])
    }
  }, [workspaceKey, viewerId, syncRef, appliedCursor, cursorSchema, syncV2, syncCursor])

  useEffect(() => {
    if (!workspaceKey) return
    const clientId = realtimeClientId()
    let active = true
    const heartbeat = () => void updatePresence(clientId, issueId, route).then(rows => { if (Array.isArray(rows) && (!appliedCursor || active)) setPresence(rows) }).catch(() => undefined)
    heartbeat()
    const timer = window.setInterval(heartbeat, 20_000)
    return () => {
      active = false
      window.clearInterval(timer)
      const body = new Blob([JSON.stringify({ clientId, issueId, route, active: false })], { type: 'application/json' })
      navigator.sendBeacon(`/api/realtime/presence?workspace=${encodeURIComponent(workspaceKey)}${import.meta.env.VITE_PAGED_ISSUES === 'true' ? '&issues=paged' : ''}`, body)
    }
  }, [issueId, route, workspaceKey, appliedCursor, presenceViewerId])

  return { connected, presence, clientId: realtimeClientId() }
}
