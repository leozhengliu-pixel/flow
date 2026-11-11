import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { connectSharedRealtime, type ChannelLike, type LocksLike, type StreamLike } from './shared-realtime'

class Stream implements StreamLike {
  static all: Stream[] = []
  onopen: StreamLike['onopen'] = null
  onerror: StreamLike['onerror'] = null
  onmessage: StreamLike['onmessage'] = null
  closed = false
  readonly url: string
  constructor(url: string) { this.url = url; Stream.all.push(this) }
  close() { this.closed = true }
  emit(id: string, seq?: string) { this.onmessage?.({ data: JSON.stringify({ id, seq, type: 'issue.updated', createdAt: '2026-11-11' }), lastEventId: id }) }
}

class Locks implements LocksLike {
  private held = new Set<string>()
  private queues = new Map<string, Array<() => void>>()
  request(name: string, options: { signal?: AbortSignal }, callback: () => void | Promise<void>) {
    return new Promise<void>((resolve, reject) => {
      const run = async () => {
        this.held.add(name)
        try { await callback() } finally { this.held.delete(name); this.queues.get(name)?.shift()?.(); resolve() }
      }
      if (!this.held.has(name)) { void run(); return }
      const queue = this.queues.get(name) ?? []
      const entry = () => void run()
      queue.push(entry)
      this.queues.set(name, queue)
      options.signal?.addEventListener('abort', () => {
        const index = queue.indexOf(entry)
        if (index >= 0) { queue.splice(index, 1); reject(new Error('aborted')) }
      })
    })
  }
}

describe('scale-p08 per-tab applied frontier', () => {
  let channels: Map<string, Set<ChannelLike>>
  let locks: Locks
  let disposers: Array<() => void>
  beforeEach(() => { vi.useFakeTimers(); channels = new Map(); locks = new Locks(); Stream.all = []; disposers = [] })
  afterEach(async () => { disposers.forEach(dispose => dispose()); await vi.advanceTimersByTimeAsync(0); vi.useRealTimers() })

  function tab(userId = 'u1', schema = 2, appliedCursor = true, baseline: string | null = '40') {
    const acks: Array<() => void> = []
    const messages: string[] = []
    const dispose = connectSharedRealtime({
      workspaceKey: 'acme', userId, schema, appliedCursor, cursor: () => baseline ?? undefined,
      url: cursor => `/events${cursor ? `?since=${cursor}` : ''}`,
      onMessage: (message, acknowledge) => {
        const type = JSON.parse(message.data).type
        messages.push(type)
        if (type === 'resync' && baseline !== null) acknowledge()
        else acks.push(acknowledge)
      },
      onStatus: () => undefined,
      env: {
        locks,
        createStream: url => new Stream(url),
        createChannel: name => {
          const peers = channels.get(name) ?? new Set<ChannelLike>()
          const channel: ChannelLike = {
            onmessage: null,
            postMessage: message => { for (const peer of peers) if (peer !== channel) queueMicrotask(() => peer.onmessage?.({ data: structuredClone(message) })) },
            close: () => { peers.delete(channel) },
          }
          peers.add(channel)
          channels.set(name, peers)
          return channel
        },
      },
    })
    disposers.push(dispose)
    return { acks, messages, dispose }
  }

  it('reconnects from the contiguous applied cursor even when later events finish first', async () => {
    const leader = tab()
    Stream.all[0].emit('41')
    Stream.all[0].emit('42')
    leader.acks[1]()
    Stream.all[0].onerror?.({})
    await vi.advanceTimersByTimeAsync(1000)
    expect(Stream.all[1].url).toBe('/events?since=40')
    leader.acks[0]()
    Stream.all[1].onerror?.({})
    await vi.advanceTimersByTimeAsync(2000)
    expect(Stream.all[2].url).toBe('/events?since=42')
  })

  it('shares one connection but uses the promoted follower own completion frontier after a leader crash', async () => {
    const leader = tab()
    const follower = tab()
    await vi.advanceTimersByTimeAsync(0)
    expect(Stream.all).toHaveLength(1)
    Stream.all[0].emit('41')
    await vi.advanceTimersByTimeAsync(0)
    leader.acks[0]()
    leader.dispose()
    await vi.advanceTimersByTimeAsync(0)
    expect(Stream.all[1].url).toBe('/events?since=40')
    follower.acks[0]()
    Stream.all[1].onerror?.({})
    await vi.advanceTimersByTimeAsync(1000)
    expect(Stream.all[2].url).toBe('/events?since=41')
  })

  it('isolates account, schema and legacy protocol channel ownership', () => {
    tab(); tab('u2'); tab('u1', 3); tab('u1', 2, false)
    expect(Stream.all).toHaveLength(4)
    expect(channels.size).toBe(4)
  })

  it('keeps received-cursor reconnect semantics with the feature disabled', async () => {
    tab('u1', 2, false)
    Stream.all[0].emit('41')
    Stream.all[0].onerror?.({})
    await vi.advanceTimersByTimeAsync(1000)
    expect(Stream.all[1].url).toBe('/events?since=41')
  })

  it('uses exact durable string sequences and keeps side-channel ids out of the cursor', async () => {
    const leader = tab()
    Stream.all[0].emit('evt_opaque~replica-a', '9007199254740993')
    leader.acks[0]()
    Stream.all[0].onmessage?.({ data: JSON.stringify({ id: 'presence_9', type: 'presence.updated' }), lastEventId: 'presence_9' })
    leader.acks[1]()
    Stream.all[0].onerror?.({})
    await vi.advanceTimersByTimeAsync(1000)
    expect(Stream.all[1].url).toBe('/events?since=9007199254740993')
  })

  it('holds a joining follower live cursor behind recovery when live traffic precedes hello replay', async () => {
    const leader = tab()
    const follower = tab('u1', 2, true, null)
    // No hello response has run yet: the first shared message is live traffic.
    Stream.all[0].emit('101')
    await vi.advanceTimersByTimeAsync(0)
    expect(follower.messages[0]).toBe('resync')
    // Every data callback can finish first; the join baseline remains pending.
    follower.acks.slice(1).forEach(acknowledge => acknowledge())
    leader.dispose()
    await vi.advanceTimersByTimeAsync(0)
    expect(Stream.all[1].url).toBe('/events')
    follower.acks[0]()
    Stream.all[1].onerror?.({})
    await vi.advanceTimersByTimeAsync(1000)
    expect(Stream.all[2].url).toBe('/events?since=101')
  })
})
