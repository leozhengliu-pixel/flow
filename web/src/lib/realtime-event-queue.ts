import { supersedingEntityKey } from '@/lib/realtime-entity-sync'
import type { RealtimeEvent } from '@/types/flow'
import type { RealtimeAcknowledge } from '@/lib/shared-realtime'
import { appliedRealtimeCursorEnabled } from '@/lib/scale-p08'

const maxEvents = 256
const maxBytes = 4 * 1024 * 1024

function fullIssueReplacement(event: RealtimeEvent, applied: boolean) {
  const entity = event.payload?.issue ?? event.payload?.entity
  return event.type === 'issue.updated' && entity && typeof entity === 'object' && 'identifier' in entity && 'state' in entity && (!applied || 'description' in entity && !('isSummary' in entity && entity.isSummary))
}

function olderReplacement(incoming: RealtimeEvent, previous: RealtimeEvent) {
  const next = (incoming.payload?.issue ?? incoming.payload?.entity) as { version?: number | string; updatedAt?: string } | undefined
  const held = (previous.payload?.issue ?? previous.payload?.entity) as typeof next
  if (!next || !held) return false
  if (next.version !== undefined && held.version !== undefined && /^\d+$/.test(String(next.version)) && /^\d+$/.test(String(held.version))) {
    const nextVersion = BigInt(next.version), heldVersion = BigInt(held.version)
    if (nextVersion !== heldVersion) return nextVersion < heldVersion
  }
  const nextTime = Date.parse(next.updatedAt ?? ''), heldTime = Date.parse(held.updatedAt ?? '')
  return Number.isFinite(nextTime) && Number.isFinite(heldTime) && nextTime < heldTime
}

export interface QueuedRealtimeDelivery {
  event: RealtimeEvent
  bytes: number
  completions: RealtimeAcknowledge[]
}

export class RealtimeEventQueue {
  private items: QueuedRealtimeDelivery[] = []
  private bytes = 0
  private readonly applied = appliedRealtimeCursorEnabled()

  get length() { return this.items.length }

  push(event: RealtimeEvent, wireLength: number, acknowledge?: RealtimeAcknowledge | RealtimeAcknowledge[]) {
    let completions = acknowledge ? Array.isArray(acknowledge) ? acknowledge : [acknowledge] : []
    if (['oauth_token.created', 'api_key.used', 'oauth_authorization.reused'].includes(event.type)) {
      completions.forEach(complete => complete())
      return
    }
    // The transport replaces an excessive unfinished frontier with one fresh
    // snapshot barrier. Discard callbacks belonging to that older generation.
    for (const item of this.items) item.completions = item.completions.filter(complete => complete.pending?.() ?? true)
    completions = completions.filter(complete => complete.pending?.() ?? true)
    // Account for UTF-16 strings and parsed object overhead without serializing
    // the event again. Overflow requests one fresh snapshot instead of drops.
    const bytes = wireLength * 4 + 256
    // Full entity replacements supersede earlier pending replacements for the
    // same entity. Remove and append so cross-entity and delete ordering stays
    // intact. Partial payloads and comments must not be collapsed.
    if (event.aggregateId && fullIssueReplacement(event, this.applied)) {
      const last = this.items.findLastIndex(item => item.event.aggregateId === event.aggregateId)
      const previous = this.items[last]
      if (previous && fullIssueReplacement(previous.event, this.applied) && (!this.applied || !olderReplacement(event, previous.event))) {
        this.items.splice(last, 1)
        this.bytes -= previous.bytes
        completions.push(...previous.completions)
      }
    }
    // A document, project or initiative event carries the entity as the server holds it when it is sent, so a
    // later one replaces a pending one (an autosave burst of a large document must not overflow into a resync).
    const entityKey = this.applied && this.summary(event) ? undefined : supersedingEntityKey(event)
    if (entityKey) {
      const last = this.items.findLastIndex(item => supersedingEntityKey(item.event) === entityKey)
      const previous = this.items[last]
      if (previous && (!this.applied || !this.summary(previous.event) && !olderReplacement(event, previous.event)) && !this.items.slice(last + 1).some(item => item.event.aggregateId === event.aggregateId)) {
        this.items.splice(last, 1)
        this.bytes -= previous.bytes
        completions.push(...previous.completions)
      }
    }
    const pendingResync = this.items.findIndex(item => item.event.type === 'resync')
    if (event.type === 'resync' || pendingResync >= 0 || this.items.length >= maxEvents || this.bytes + bytes > maxBytes) {
      completions.push(...this.items.flatMap(item => item.completions))
      this.items = [{ event: { id: event.id, type: 'resync', createdAt: event.createdAt }, bytes: 256, completions }]
      this.bytes = 256
      return
    }
    this.items.push({ event, bytes, completions })
    this.bytes += bytes
  }

  peek() { return this.items[0]?.event }

  shiftDelivery() {
    const item = this.items.shift()
    if (item) this.bytes -= item.bytes
    return item
  }

  shift() { return this.shiftDelivery()?.event }

  complete(delivery: QueuedRealtimeDelivery) {
    delivery.completions.forEach(acknowledge => acknowledge())
    delivery.completions = []
  }

  recover(delivery: QueuedRealtimeDelivery) {
    this.push({ id: delivery.event.id, type: 'resync', createdAt: delivery.event.createdAt }, 0, delivery.completions)
  }

  private summary(event: RealtimeEvent) {
    const entity = event.payload?.issue ?? event.payload?.entity
    if (!entity || typeof entity !== 'object') return false
    if ('isSummary' in entity && entity.isSummary) return true
    if (event.type.startsWith('document.')) return !('content' in entity)
    if (/^(project|initiative)\./.test(event.type)) return !('description' in entity)
    return false
  }
}
