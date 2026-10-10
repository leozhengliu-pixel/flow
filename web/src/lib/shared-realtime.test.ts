import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  connectSharedRealtime,
  SHARED_REPLAY_WINDOW_MS,
  type ChannelLike,
  type LocksLike,
  type RealtimeMessage,
  type SharedRealtimeEnv,
  type StreamLike,
} from './shared-realtime'

class FakeStream implements StreamLike {
  static all: FakeStream[] = []
  onopen: StreamLike['onopen'] = null
  onerror: StreamLike['onerror'] = null
  onmessage: StreamLike['onmessage'] = null
  closed = false
  readonly url: string
  constructor(url: string) { this.url = url; FakeStream.all.push(this) }
  close() { this.closed = true }
  open() { this.onopen?.({}) }
  fail() { this.onerror?.({}) }
  emit(id: string, event: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify({ id, ...event, createdAt: '2026-01-01T00:00:00Z' }), lastEventId: id })
  }
  static live() { return FakeStream.all.filter(stream => !stream.closed) }
}

/** Exclusive, queueing Web Locks stand-in shared by every simulated tab. */
class FakeLocks implements LocksLike {
  private holders = new Map<string, { release: () => void }>()
  private queues = new Map<string, Array<() => void>>()
  request(name: string, options: { signal?: AbortSignal }, callback: () => Promise<void> | void) {
    return new Promise<void>((resolve, reject) => {
      const run = async () => {
        let release!: () => void
        const done = new Promise<void>(r => { release = r })
        this.holders.set(name, { release })
        try { await Promise.race([Promise.resolve(callback()), done]) }
        finally { this.holders.delete(name); this.queues.get(name)?.shift()?.(); resolve() }
      }
      if (options.signal?.aborted) return reject(new DOMException('aborted', 'AbortError'))
      if (!this.holders.has(name)) { void run(); return }
      const queue = this.queues.get(name) ?? []
      const entry = () => void run()
      queue.push(entry)
      this.queues.set(name, queue)
      options.signal?.addEventListener('abort', () => {
        const index = queue.indexOf(entry)
        if (index >= 0) { queue.splice(index, 1); reject(new DOMException('aborted', 'AbortError')) }
      })
    })
  }
  holderCount() { return this.holders.size }
}

class Bus {
  private channels = new Map<string, Set<FakeChannel>>()
  join(name: string, channel: FakeChannel) {
    const set = this.channels.get(name) ?? new Set()
    set.add(channel)
    this.channels.set(name, set)
  }
  leave(name: string, channel: FakeChannel) { this.channels.get(name)?.delete(channel) }
  post(name: string, from: FakeChannel, message: unknown) {
    const copy = structuredClone(message)
    for (const channel of this.channels.get(name) ?? []) {
      if (channel === from || channel.dead) continue
      queueMicrotask(() => { if (!channel.dead) channel.onmessage?.({ data: copy }) })
    }
  }
}

class FakeChannel implements ChannelLike {
  onmessage: ChannelLike['onmessage'] = null
  dead = false
  private readonly bus: Bus
  private readonly name: string
  constructor(bus: Bus, name: string) { this.bus = bus; this.name = name; bus.join(name, this) }
  postMessage(message: unknown) { if (!this.dead) this.bus.post(this.name, this, message) }
  close() { this.bus.leave(this.name, this) }
}

interface Tab {
  messages: RealtimeMessage[]
  status: boolean[]
  roles: string[]
  dispose: () => void
  /** Simulates a crash: no resign message, no further traffic, the lock is lost. */
  crash: () => void
}

describe('shared realtime stream', () => {
  let bus: Bus
  let locks: FakeLocks | undefined
  let cursors: Record<string, string | undefined>

  beforeEach(() => {
    vi.useFakeTimers()
    FakeStream.all = []
    bus = new Bus()
    locks = new FakeLocks()
    cursors = {}
  })
  afterEach(() => { vi.useRealTimers() })

  function openTab({ workspace = 'acme', user = 'user-1', useLocks = true, stored }: { workspace?: string; user?: string; useLocks?: boolean; stored?: string } = {}): Tab {
    const messages: RealtimeMessage[] = []
    const status: boolean[] = []
    const roles: string[] = []
    const channels: FakeChannel[] = []
    const lockManager = useLocks ? locks : undefined
    const env: Partial<SharedRealtimeEnv> = {
      locks: lockManager,
      createChannel: name => { const channel = new FakeChannel(bus, name); channels.push(channel); return channel },
      createStream: url => new FakeStream(url),
    }
    const dispose = connectSharedRealtime({
      workspaceKey: workspace,
      userId: user,
      url: cursor => `/api/realtime/events?workspace=${workspace}${cursor ? `&since=${cursor}` : ''}`,
      cursor: () => stored ?? cursors[workspace],
      onMessage: message => messages.push(message),
      onStatus: connected => status.push(connected),
      onRole: role => roles.push(role),
      env,
    })
    return {
      messages, status, roles, dispose,
      crash: () => {
        channels.forEach(channel => { channel.dead = true })
        // Chrome drops the lock and the sockets together with the tab.
        dispose()
      },
    }
  }

  const settle = async (ms = 0) => { await vi.advanceTimersByTimeAsync(ms) }
  const types = (tab: Tab) => tab.messages.map(message => JSON.parse(message.data).type as string)

  it('a single tab opens one stream with the stored cursor and behaves like before', async () => {
    cursors.acme = 'evt_5'
    const tab = openTab()
    await settle()
    expect(FakeStream.live()).toHaveLength(1)
    expect(FakeStream.all[0].url).toBe('/api/realtime/events?workspace=acme&since=evt_5')
    expect(tab.roles).toEqual(['leader'])
    FakeStream.all[0].open()
    FakeStream.all[0].emit('connected_1', { type: 'connected' })
    FakeStream.all[0].emit('evt_6', { type: 'issue.updated', aggregateId: 'i1' })
    expect(tab.status.at(-1)).toBe(true)
    expect(types(tab)).toEqual(['connected', 'issue.updated'])
    expect(tab.messages[1].lastEventId).toBe('evt_6')
    tab.dispose()
    expect(FakeStream.live()).toHaveLength(0)
  })

  it('elects one leader across eight tabs and keeps exactly one stream', async () => {
    const tabs = Array.from({ length: 8 }, () => openTab())
    await settle(50)
    expect(FakeStream.live()).toHaveLength(1)
    expect(tabs.filter(tab => tab.roles.includes('leader'))).toHaveLength(1)
    expect(locks!.holderCount()).toBe(1)
    tabs.forEach(tab => tab.dispose())
    await settle()
    expect(FakeStream.live()).toHaveLength(0)
  })

  it('fans every event out to all tabs, including the leader, with identical payloads', async () => {
    const tabs = Array.from({ length: 4 }, () => openTab())
    await settle()
    const stream = FakeStream.live()[0]
    stream.open()
    stream.emit('evt_1', { type: 'issue.updated', aggregateId: 'i1', clientId: 'tab-a' })
    stream.emit('evt_2', { type: 'comment.created', aggregateId: 'i1' })
    await settle()
    for (const tab of tabs) {
      expect(types(tab)).toEqual(['issue.updated', 'comment.created'])
      expect(tab.messages.map(message => message.lastEventId)).toEqual(['evt_1', 'evt_2'])
      expect(JSON.parse(tab.messages[0].data).clientId).toBe('tab-a')
      expect(tab.status.at(-1)).toBe(true)
    }
    tabs.forEach(tab => tab.dispose())
  })

  it('a late tab joins as a follower and receives presence, status and recent events', async () => {
    const first = openTab()
    await settle()
    const stream = FakeStream.live()[0]
    stream.open()
    stream.emit('connected_9', { type: 'connected', payload: { presence: [{ clientId: 'a' }] } })
    stream.emit('evt_1', { type: 'issue.updated', aggregateId: 'i1' })
    stream.emit('presence_1', { type: 'presence.updated', payload: { presence: [{ clientId: 'a' }, { clientId: 'b' }] } })
    await settle(10_000)
    stream.emit('evt_2', { type: 'issue.updated', aggregateId: 'i2' })
    await settle()

    const late = openTab()
    await settle()
    expect(FakeStream.live()).toHaveLength(1)
    expect(late.roles).toEqual([])
    expect(late.status.at(-1)).toBe(true)
    const ids = late.messages.map(message => message.lastEventId)
    expect(ids[0]).toBe('presence_1')
    expect(ids).toEqual(['presence_1', 'evt_1', 'evt_2'])
    // The existing leader and followers do not receive the replay twice.
    expect(first.messages.filter(message => message.lastEventId === 'evt_1')).toHaveLength(1)

    // Events older than the replay window are not replayed: the new tab loaded a fresh snapshot.
    await settle(SHARED_REPLAY_WINDOW_MS + 1)
    const later = openTab()
    await settle()
    expect(later.messages.map(message => message.lastEventId)).toEqual(['presence_1'])
    ;[first, late, later].forEach(tab => tab.dispose())
  })

  it('promotes a follower within two seconds when the leader closes and resumes from the shared cursor', async () => {
    const leader = openTab()
    await settle()
    const follower = openTab()
    const other = openTab()
    await settle()
    const stream = FakeStream.live()[0]
    stream.open()
    stream.emit('evt_1', { type: 'issue.updated', aggregateId: 'i1' })
    stream.emit('connected_x', { type: 'connected' })
    await settle()

    leader.dispose()
    await settle(100)
    const live = FakeStream.live()
    expect(live).toHaveLength(1)
    expect(live[0]).not.toBe(stream)
    // The handshake id is not a resume point; the last real event id is.
    expect(live[0].url).toContain('since=evt_1')
    expect(follower.roles.concat(other.roles).filter(role => role === 'leader')).toHaveLength(1)
    // Subscribers see a disconnect until the new leader is open.
    expect(follower.status.at(-1)).toBe(false)
    live[0].open()
    await settle()
    expect(follower.status.at(-1)).toBe(true)
    expect(other.status.at(-1)).toBe(true)

    live[0].emit('evt_2', { type: 'issue.updated', aggregateId: 'i1' })
    await settle()
    expect(follower.messages.at(-1)?.lastEventId).toBe('evt_2')
    expect(other.messages.at(-1)?.lastEventId).toBe('evt_2')
    ;[follower, other].forEach(tab => tab.dispose())
  })

  it('failover with no resumable cursor asks every tab to resync', async () => {
    const leader = openTab()
    await settle()
    const follower = openTab()
    const other = openTab()
    await settle()
    FakeStream.live()[0].open()
    FakeStream.live()[0].emit('connected_1', { type: 'connected' })
    await settle()
    leader.crash()
    await settle(100)
    // The promoted tab and its follower both saw it, once each.
    const resyncCounts = [follower, other].map(tab => types(tab).filter(type => type === 'resync').length)
    expect(resyncCounts.sort()).toEqual([1, 1])
    expect(FakeStream.live()).toHaveLength(1)
    ;[follower, other].forEach(tab => tab.dispose())
  })

  it('does not resync on failover when the leader never delivered anything to followers', async () => {
    const leader = openTab()
    await settle()
    const follower = openTab()
    await settle()
    leader.dispose()
    await settle(100)
    expect(types(follower)).toEqual([])
    follower.dispose()
  })

  it('a leader reconnects itself with the latest cursor and backs off', async () => {
    const tab = openTab()
    await settle()
    const stream = FakeStream.live()[0]
    stream.open()
    stream.emit('evt_3', { type: 'issue.updated', aggregateId: 'i1' })
    stream.emit('connected_2', { type: 'connected' })
    stream.fail()
    expect(stream.closed).toBe(true)
    expect(tab.status.at(-1)).toBe(false)
    await settle(1_000)
    const second = FakeStream.live()[0]
    expect(second.url).toContain('since=evt_3')
    second.fail()
    await settle(1_000)
    expect(FakeStream.live()).toHaveLength(0)
    await settle(1_000)
    expect(FakeStream.live()).toHaveLength(1)
    tab.dispose()
  })

  it('keeps separate streams for different workspaces and different users', async () => {
    const a = openTab({ workspace: 'acme' })
    const b = openTab({ workspace: 'globex' })
    const c = openTab({ workspace: 'acme', user: 'user-2' })
    await settle(50)
    expect(FakeStream.live()).toHaveLength(3)
    FakeStream.live()[0].open()
    FakeStream.live()[0].emit('evt_1', { type: 'issue.updated' })
    await settle()
    expect([a, b, c].map(tab => tab.messages.length).sort()).toEqual([0, 0, 1])
    ;[a, b, c].forEach(tab => tab.dispose())
  })

  it('a workspace switch releases the old stream and the old lock', async () => {
    const first = openTab({ workspace: 'acme' })
    await settle()
    const peer = openTab({ workspace: 'acme' })
    await settle()
    first.dispose()
    const switched = openTab({ workspace: 'globex' })
    await settle(100)
    // peer took over acme, the switching tab leads globex.
    expect(FakeStream.live().map(stream => stream.url).sort()).toEqual([
      '/api/realtime/events?workspace=acme',
      '/api/realtime/events?workspace=globex',
    ])
    peer.dispose()
    switched.dispose()
    await settle()
    expect(FakeStream.live()).toHaveLength(0)
    expect(locks!.holderCount()).toBe(0)
  })

  it('a tab disposed while still waiting for the lock never opens a stream', async () => {
    const leader = openTab()
    await settle()
    const waiting = openTab()
    await settle()
    waiting.dispose()
    leader.dispose()
    await settle(100)
    expect(FakeStream.live()).toHaveLength(0)
  })

  it('without BroadcastChannel it keeps a private stream like a lone tab', async () => {
    const events: RealtimeMessage[] = []
    const dispose = connectSharedRealtime({
      workspaceKey: 'acme', userId: 'u',
      url: () => '/api/realtime/events?workspace=acme',
      onMessage: message => events.push(message), onStatus: () => undefined,
      env: { locks: undefined, createChannel: () => undefined, createStream: url => new FakeStream(url) },
    })
    expect(FakeStream.live()).toHaveLength(1)
    FakeStream.live()[0].emit('evt_1', { type: 'issue.updated' })
    expect(events).toHaveLength(1)
    dispose()
    expect(FakeStream.live()).toHaveLength(0)
  })

  describe('heartbeat election (no Web Locks)', () => {
    it('elects a single leader and fans out events', async () => {
      const tabs = Array.from({ length: 5 }, () => openTab({ useLocks: false }))
      await settle(1_000)
      expect(FakeStream.live()).toHaveLength(1)
      FakeStream.live()[0].open()
      FakeStream.live()[0].emit('evt_1', { type: 'issue.updated' })
      await settle()
      tabs.forEach(tab => expect(tab.messages.map(message => message.lastEventId)).toEqual(['evt_1']))
      tabs.forEach(tab => tab.dispose())
    })

    it('fails over within about two seconds when the leader crashes', async () => {
      const tabs = Array.from({ length: 3 }, () => openTab({ useLocks: false }))
      await settle(1_000)
      const stream = FakeStream.live()[0]
      stream.open()
      stream.emit('evt_1', { type: 'issue.updated' })
      await settle()
      const leader = tabs.find(tab => tab.roles.includes('leader'))!
      leader.crash()
      expect(FakeStream.live()).toHaveLength(0)
      await settle(2_600)
      const live = FakeStream.live()
      expect(live).toHaveLength(1)
      expect(live[0].url).toContain('since=evt_1')
      tabs.filter(tab => tab !== leader).forEach(tab => tab.dispose())
    })

    it('a gracefully closing leader hands over without waiting for the heartbeat timeout', async () => {
      const tabs = Array.from({ length: 3 }, () => openTab({ useLocks: false }))
      await settle(1_000)
      const leader = tabs.find(tab => tab.roles.includes('leader'))!
      leader.dispose()
      await settle(500)
      expect(FakeStream.live()).toHaveLength(1)
      tabs.filter(tab => tab !== leader).forEach(tab => tab.dispose())
    })
  })
})
