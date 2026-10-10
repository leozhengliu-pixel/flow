/**
 * One workspace event stream shared by every tab of the same origin, workspace
 * and user.
 *
 * Browsers cap HTTP/1.1 connections per origin (6 in Chrome). A long-lived
 * EventSource per tab starves ordinary API requests once a handful of tabs are
 * open, so exactly one tab (the leader) holds the stream and rebroadcasts the
 * raw messages to the others over a BroadcastChannel. Followers hold no
 * connection at all.
 *
 * Election uses the Web Locks API (the browser releases the lock the instant a
 * tab closes or crashes, so failover is immediate). Where locks are missing
 * (insecure origins, older Safari) a BroadcastChannel heartbeat election is
 * used. Without BroadcastChannel the tab simply keeps its own stream, exactly
 * as before.
 *
 * Gap handling: every tab tracks the newest real stream cursor it has seen
 * (from the leader's rebroadcasts), so a promoted follower reconnects with
 * `since=<cursor>` and the server replays what was missed. If no cursor is
 * known, a synthetic `resync` is delivered instead.
 */

export interface RealtimeMessage {
  data: string
  lastEventId: string
}

export interface StreamLike {
  onopen: ((event: unknown) => void) | null
  onerror: ((event: unknown) => void) | null
  onmessage: ((event: { data: string; lastEventId: string }) => void) | null
  close(): void
}

export interface ChannelLike {
  onmessage: ((event: { data: unknown }) => void) | null
  postMessage(message: unknown): void
  close(): void
}

export interface LocksLike {
  request(
    name: string,
    options: { signal?: AbortSignal },
    callback: () => Promise<void> | void,
  ): Promise<unknown>
}

export interface SharedRealtimeEnv {
  locks?: LocksLike
  createChannel(name: string): ChannelLike | undefined
  createStream(url: string): StreamLike
}

export interface SharedRealtimeOptions {
  workspaceKey: string
  /** Session owner. Tabs of different users never share a stream. */
  userId?: string
  /** Builds the stream URL for an optional resume cursor. */
  url(cursor?: string): string
  /** Persisted cursor used by a leader that has not seen any shared traffic yet. */
  cursor?(): string | undefined
  onMessage(message: RealtimeMessage): void
  onStatus(connected: boolean): void
  onRole?(role: 'leader' | 'follower'): void
  env?: Partial<SharedRealtimeEnv>
}

type Wire =
  | { k: 'hello'; from: string }
  | { k: 'msg'; from: string; to?: string; data: string; lastEventId: string }
  | { k: 'status'; from: string; to?: string; connected: boolean }
  | { k: 'beat'; from: string }
  | { k: 'claim'; from: string }
  | { k: 'resign'; from: string }

/** How long rebroadcast messages stay available for a late-joining tab. */
export const SHARED_REPLAY_WINDOW_MS = 30_000
const replayMaxEvents = 256
const replayMaxBytes = 2 * 1024 * 1024
const joinWaitMs = 300
const claimWaitMs = 200
const beatIntervalMs = 700
const leaderTimeoutMs = 1_800
const reconnectBaseMs = 1_000
const reconnectMaxMs = 15_000
// The server marshals `id` then `type` first; only these two carry the roster.
const presenceMessage = /^\{"id":"[^"]*","type":"(?:connected|presence\.updated)"/

function defaultEnv(): SharedRealtimeEnv {
  return {
    locks: typeof navigator !== 'undefined' ? ((navigator as Navigator & { locks?: LocksLike }).locks ?? undefined) : undefined,
    createChannel: name => {
      if (typeof BroadcastChannel === 'undefined') return undefined
      try { return new BroadcastChannel(name) as unknown as ChannelLike }
      catch { return undefined }
    },
    createStream: url => new EventSource(url) as unknown as StreamLike,
  }
}

function newTabId() {
  const random = typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : Math.random().toString(36).slice(2)
  // Sortable: the heartbeat fallback lets the lowest id win a contested election.
  return `${Date.now().toString(36).padStart(9, '0')}-${random}`
}

export function realtimeChannelName(workspaceKey: string, userId?: string) {
  return `flow:realtime:${encodeURIComponent(workspaceKey)}:${encodeURIComponent(userId || 'anonymous')}`
}

class SharedRealtime {
  private readonly env: SharedRealtimeEnv
  private readonly name: string
  private readonly tabId = newTabId()
  private channel?: ChannelLike
  private role: 'follower' | 'leader' = 'follower'
  private disposed = false
  private stream?: StreamLike
  private releaseLock?: () => void
  private lockAbort?: AbortController
  private reconnectTimer?: ReturnType<typeof setTimeout>
  private retry = 0
  private cursor?: string
  private initialSince?: string
  private sawStream = false
  private connectedNow = false
  private presence?: RealtimeMessage
  private replay: Array<{ at: number; bytes: number; message: RealtimeMessage }> = []
  private replayBytes = 0
  // Heartbeat election (only when Web Locks is unavailable).
  private heartbeat = false
  private lastBeat = 0
  private beatTimer?: ReturnType<typeof setInterval>
  private joinTimer?: ReturnType<typeof setTimeout>
  private claimTimer?: ReturnType<typeof setTimeout>

  private readonly options: SharedRealtimeOptions

  constructor(options: SharedRealtimeOptions) {
    this.options = options
    this.env = { ...defaultEnv(), ...options.env }
    this.name = realtimeChannelName(options.workspaceKey, options.userId)
  }

  start() {
    this.channel = this.env.createChannel(this.name)
    if (!this.channel) {
      // No way to coordinate: keep a private stream like a lone tab.
      this.becomeLeader()
      return
    }
    this.channel.onmessage = event => this.onWire(event.data)
    if (this.env.locks) this.electWithLocks()
    else this.electWithHeartbeat()
    this.post({ k: 'hello', from: this.tabId })
  }

  dispose() {
    if (this.disposed) return
    this.disposed = true
    if (this.role === 'leader' && this.heartbeat) this.post({ k: 'resign', from: this.tabId })
    clearTimeout(this.reconnectTimer)
    clearTimeout(this.joinTimer)
    clearTimeout(this.claimTimer)
    clearInterval(this.beatTimer)
    this.closeStream()
    this.releaseLock?.()
    this.lockAbort?.abort()
    if (this.channel) this.channel.onmessage = null
    this.channel?.close()
    this.channel = undefined
    this.replay = []
    this.replayBytes = 0
    this.presence = undefined
  }

  // ---- election --------------------------------------------------------

  private electWithLocks() {
    const abort = new AbortController()
    this.lockAbort = abort
    const fallback = () => {
      if (!this.disposed && !abort.signal.aborted && !this.heartbeat) this.electWithHeartbeat()
    }
    try {
      void this.env.locks!.request(this.name, { signal: abort.signal }, () => {
        if (this.disposed) return
        this.becomeLeader()
        return new Promise<void>(resolve => { this.releaseLock = resolve })
      }).catch(fallback)
    } catch { fallback() }
  }

  private electWithHeartbeat() {
    this.heartbeat = true
    this.lastBeat = 0
    // Give an existing leader a moment to answer our hello before claiming.
    this.joinTimer = setTimeout(() => { if (this.lastBeat === 0) this.claim() }, joinWaitMs)
    this.beatTimer = setInterval(() => {
      if (this.role === 'leader') this.post({ k: 'beat', from: this.tabId })
      else if (this.lastBeat && Date.now() - this.lastBeat > leaderTimeoutMs) this.claim()
    }, beatIntervalMs)
  }

  private claim() {
    if (this.disposed || this.role === 'leader' || this.claimTimer) return
    this.post({ k: 'claim', from: this.tabId })
    this.claimTimer = setTimeout(() => {
      this.claimTimer = undefined
      if (!this.disposed && this.role !== 'leader') this.becomeLeader()
    }, claimWaitMs)
  }

  private cancelClaim() {
    clearTimeout(this.claimTimer)
    this.claimTimer = undefined
  }

  private becomeLeader() {
    if (this.disposed || this.role === 'leader') return
    this.role = 'leader'
    this.cancelClaim()
    this.options.onRole?.('leader')
    this.options.onStatus(false)
    this.post({ k: 'status', from: this.tabId, connected: false })
    this.retry = 0
    this.initialSince = this.cursor ?? this.options.cursor?.()
    if (this.sawStream && !this.initialSince) {
      // We followed a live stream but never saw a resumable id, so events may
      // have been missed during the handover. Ask every tab for a snapshot.
      this.accept({ data: JSON.stringify({ id: `resync_${Date.now()}`, type: 'resync', createdAt: new Date().toISOString() }), lastEventId: '' }, true)
    }
    if (this.heartbeat) this.post({ k: 'beat', from: this.tabId })
    this.connect()
  }

  private stepDown() {
    if (this.role !== 'leader') return
    this.role = 'follower'
    clearTimeout(this.reconnectTimer)
    this.closeStream()
    this.options.onRole?.('follower')
    this.options.onStatus(false)
    this.post({ k: 'hello', from: this.tabId })
  }

  // ---- leader stream ---------------------------------------------------

  private connect() {
    if (this.disposed || this.role !== 'leader') return
    const stream = this.env.createStream(this.options.url(this.cursor ?? this.initialSince))
    this.stream = stream
    stream.onopen = () => {
      if (this.stream !== stream) return
      this.retry = 0
      this.options.onStatus(true)
      this.post({ k: 'status', from: this.tabId, connected: true })
    }
    stream.onerror = () => {
      if (this.stream !== stream) return
      // Reconnect ourselves instead of relying on EventSource's automatic
      // retry: it resends a `connected_*` marker as Last-Event-ID (no replay)
      // and gives up for good on a non-2xx response.
      this.closeStream()
      this.options.onStatus(false)
      this.post({ k: 'status', from: this.tabId, connected: false })
      const delay = Math.min(reconnectMaxMs, reconnectBaseMs * 2 ** this.retry++)
      this.reconnectTimer = setTimeout(() => this.connect(), delay)
    }
    stream.onmessage = event => {
      if (this.stream !== stream) return
      this.accept({ data: event.data, lastEventId: event.lastEventId }, true)
    }
  }

  private closeStream() {
    const stream = this.stream
    this.stream = undefined
    if (!stream) return
    stream.onopen = stream.onerror = stream.onmessage = null
    stream.close()
  }

  // ---- delivery --------------------------------------------------------

  private accept(message: RealtimeMessage, broadcast: boolean) {
    this.sawStream = true
    // `connected_*` is a per-connection handshake id that the server cannot
    // replay from; never use it as a resume point.
    if (message.lastEventId && !message.lastEventId.startsWith('connected_')) this.cursor = message.lastEventId
    if (presenceMessage.test(message.data)) this.presence = message
    else this.remember(message)
    if (broadcast) this.post({ k: 'msg', from: this.tabId, data: message.data, lastEventId: message.lastEventId })
    this.options.onMessage(message)
  }

  private remember(message: RealtimeMessage) {
    const now = Date.now()
    const bytes = message.data.length * 2
    this.replay.push({ at: now, bytes, message })
    this.replayBytes += bytes
    while (this.replay.length && (this.replay.length > replayMaxEvents || this.replayBytes > replayMaxBytes || now - this.replay[0].at > SHARED_REPLAY_WINDOW_MS)) {
      this.replayBytes -= this.replay.shift()!.bytes
    }
  }

  private answerHello(to: string) {
    this.post({ k: 'status', from: this.tabId, to, connected: this.stream !== undefined && this.connectedNow })
    if (this.presence) this.post({ k: 'msg', from: this.tabId, to, ...this.presence })
    const cutoff = Date.now() - SHARED_REPLAY_WINDOW_MS
    for (const item of this.replay) if (item.at >= cutoff) this.post({ k: 'msg', from: this.tabId, to, ...item.message })
  }

  // ---- wire ------------------------------------------------------------

  private post(message: Wire) {
    if (message.k === 'status' && message.from === this.tabId) this.connectedNow = message.connected
    try { this.channel?.postMessage(message) }
    catch { /* a closed channel must not break the stream */ }
  }

  private onWire(raw: unknown) {
    if (this.disposed || !raw || typeof raw !== 'object') return
    const wire = raw as Wire
    if (wire.from === this.tabId) return
    if ('to' in wire && wire.to && wire.to !== this.tabId) return
    switch (wire.k) {
      case 'hello':
        if (this.role === 'leader') {
          this.answerHello(wire.from)
          if (this.heartbeat) this.post({ k: 'beat', from: this.tabId })
        }
        return
      case 'msg':
        if (this.role === 'leader' && !wire.to) return
        this.accept({ data: wire.data, lastEventId: wire.lastEventId }, false)
        return
      case 'status':
        if (this.role !== 'leader') this.options.onStatus(wire.connected)
        return
      case 'beat':
        if (this.role === 'leader') {
          // Two leaders (partition healed): the lowest id keeps the stream.
          if (wire.from < this.tabId) this.stepDown()
          else this.post({ k: 'beat', from: this.tabId })
        }
        this.lastBeat = Date.now()
        this.cancelClaim()
        return
      case 'claim':
        if (this.role === 'leader') this.post({ k: 'beat', from: this.tabId })
        else if (wire.from < this.tabId) this.cancelClaim()
        return
      case 'resign':
        // The leader left on purpose; do not wait for the heartbeat timeout.
        if (this.role !== 'leader') this.joinTimer = setTimeout(() => this.claim(), Math.floor(Math.random() * 100))
        return
    }
  }
}

/** Joins (or starts) the shared stream. Returns a disposer. */
export function connectSharedRealtime(options: SharedRealtimeOptions): () => void {
  const shared = new SharedRealtime(options)
  shared.start()
  return () => shared.dispose()
}
