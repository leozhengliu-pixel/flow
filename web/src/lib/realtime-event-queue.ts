import type { RealtimeEvent } from '@/types/flow'

const maxEvents = 256
const maxBytes = 4 * 1024 * 1024

function fullIssueReplacement(event: RealtimeEvent) {
  const entity = event.payload?.issue ?? event.payload?.entity
  return event.type === 'issue.updated' && entity && typeof entity === 'object' && 'identifier' in entity && 'state' in entity
}

export class RealtimeEventQueue {
  private items: Array<{ event: RealtimeEvent; bytes: number }> = []
  private bytes = 0

  get length() { return this.items.length }

  push(event: RealtimeEvent, wireLength: number) {
    if (['oauth_token.created', 'api_key.used', 'oauth_authorization.reused'].includes(event.type)) return
    // Account for UTF-16 strings and parsed object overhead without serializing
    // the event again. Overflow requests one fresh snapshot instead of drops.
    const bytes = wireLength * 4 + 256
    // Full entity replacements supersede earlier pending replacements for the
    // same entity. Remove and append so cross-entity and delete ordering stays
    // intact. Partial payloads and comments must not be collapsed.
    if (event.aggregateId && fullIssueReplacement(event)) {
      const last = this.items.findLastIndex(item => item.event.aggregateId === event.aggregateId)
      const previous = this.items[last]
      if (previous && fullIssueReplacement(previous.event)) {
        this.items.splice(last, 1)
        this.bytes -= previous.bytes
      }
    }
    const pendingResync = this.items.findIndex(item => item.event.type === 'resync')
    if (event.type === 'resync' || pendingResync >= 0 || this.items.length >= maxEvents || this.bytes + bytes > maxBytes) {
      this.items = [{ event: { id: event.id, type: 'resync', createdAt: event.createdAt }, bytes: 256 }]
      this.bytes = 256
      return
    }
    this.items.push({ event, bytes })
    this.bytes += bytes
  }

  shift() {
    const item = this.items.shift()
    if (item) this.bytes -= item.bytes
    return item?.event
  }
}
