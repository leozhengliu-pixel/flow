import type { RealtimeEvent } from '@/types/flow'

const maxEvents = 256
const maxBytes = 4 * 1024 * 1024

export class RealtimeEventQueue {
  private items: Array<{ event: RealtimeEvent; bytes: number }> = []
  private bytes = 0

  get length() { return this.items.length }

  push(event: RealtimeEvent, wireLength: number) {
    // Account for UTF-16 strings and parsed object overhead without serializing
    // the event again. Overflow requests one fresh snapshot instead of drops.
    const bytes = wireLength * 4 + 256
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
