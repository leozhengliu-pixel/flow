import { backoffDelay, sleep as defaultSleep } from './backoff.js'
import { errorMessage, silentLogger, type Logger } from './log.js'
import { MAX_EVENTS_PER_REQUEST, type RunnerEvent } from './protocol.js'
import { AuthError, RuntimeError } from './runtime-client.js'

// Buffers runner events and ships them to POST /events in batches (every
// 250 ms or every 20 events, at most 100 per request). While Flow is
// unreachable events stay queued and are retried with jittered backoff. The
// queue is bounded (8 MiB by default): when it overflows, the oldest thought
// events go first, then the oldest ephemeral events, then the oldest events.
// Event ids are assigned at creation, so a retried batch is idempotent.

export interface EventBufferOptions {
  post: (events: RunnerEvent[]) => Promise<void>
  logger?: Logger
  maxBytes?: number
  batchSize?: number
  flushIntervalMs?: number
  maxBackoffMs?: number
  sleep?: (ms: number) => Promise<void>
  random?: () => number
  /** Called once when Flow rejects the token (the sandbox is going away). */
  onFatal?: (error: Error) => void
}

interface Entry {
  event: RunnerEvent
  size: number
  inflight: boolean
}

export const DEFAULT_EVENT_BUFFER_BYTES = 8 * 1024 * 1024

export class EventBuffer {
  private readonly queue: Entry[] = []
  private bytes = 0
  private timer: NodeJS.Timeout | undefined
  private pumping: Promise<void> | undefined
  private closed = false
  private fatal: Error | undefined
  private dropped = 0
  private waiters: Array<() => void> = []
  private readonly post: (events: RunnerEvent[]) => Promise<void>
  private readonly logger: Logger
  private readonly maxBytes: number
  private readonly batchSize: number
  private readonly flushIntervalMs: number
  private readonly maxBackoffMs: number
  private readonly sleep: (ms: number) => Promise<void>
  private readonly random: () => number
  private readonly onFatal?: (error: Error) => void

  constructor(options: EventBufferOptions) {
    this.post = options.post
    this.logger = options.logger ?? silentLogger
    this.maxBytes = options.maxBytes ?? DEFAULT_EVENT_BUFFER_BYTES
    this.batchSize = options.batchSize ?? 20
    this.flushIntervalMs = options.flushIntervalMs ?? 250
    this.maxBackoffMs = options.maxBackoffMs ?? 30_000
    this.sleep = options.sleep ?? defaultSleep
    this.random = options.random ?? Math.random
    this.onFatal = options.onFatal
  }

  get pending(): number {
    return this.queue.length
  }

  get pendingBytes(): number {
    return this.bytes
  }

  /** Number of events evicted because the buffer overflowed. */
  get droppedCount(): number {
    return this.dropped
  }

  push(...events: RunnerEvent[]): void {
    if (this.closed || this.fatal) return
    for (const event of events) {
      const size = Buffer.byteLength(JSON.stringify(event))
      this.queue.push({ event, size, inflight: false })
      this.bytes += size
    }
    this.evict()
    this.schedule()
  }

  /** Waits until every queued event is delivered, or the timeout passes. Returns true when drained. */
  async flush(timeoutMs = 30_000): Promise<boolean> {
    if (this.queue.length === 0) return true
    if (this.fatal) return false
    this.clearTimer()
    void this.pump()
    let timer: NodeJS.Timeout | undefined
    const drained = new Promise<boolean>((resolve) => this.waiters.push(() => resolve(true)))
    const timeout = new Promise<boolean>((resolve) => {
      timer = setTimeout(() => resolve(false), timeoutMs)
    })
    const result = await Promise.race([drained, timeout])
    clearTimeout(timer)
    return result
  }

  /** Stops accepting events. Pending events are kept for a final flush. */
  close(): void {
    this.closed = true
  }

  private evict(): void {
    while (this.bytes > this.maxBytes) {
      const index =
        this.findEvictable((entry) => entry.event.type === 'thought') ??
        this.findEvictable((entry) => entry.event.ephemeral === true) ??
        this.findEvictable(() => true)
      if (index === undefined) return
      const [entry] = this.queue.splice(index, 1)
      this.bytes -= entry.size
      this.dropped++
    }
  }

  private findEvictable(match: (entry: Entry) => boolean): number | undefined {
    const index = this.queue.findIndex((entry) => !entry.inflight && match(entry))
    return index === -1 ? undefined : index
  }

  private schedule(): void {
    if (this.pumping) return
    if (this.queue.length >= this.batchSize) {
      this.clearTimer()
      void this.pump()
      return
    }
    if (!this.timer) {
      this.timer = setTimeout(() => {
        this.timer = undefined
        void this.pump()
      }, this.flushIntervalMs)
      this.timer.unref?.()
    }
  }

  private clearTimer(): void {
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
  }

  private pump(): Promise<void> {
    if (!this.pumping) {
      this.pumping = this.run().finally(() => {
        this.pumping = undefined
        if (this.queue.length > 0 && !this.fatal) this.schedule()
      })
    }
    return this.pumping
  }

  private async run(): Promise<void> {
    let attempt = 0
    while (this.queue.length > 0 && !this.fatal) {
      const batch = this.queue.slice(0, MAX_EVENTS_PER_REQUEST)
      for (const entry of batch) entry.inflight = true
      try {
        await this.post(batch.map((entry) => entry.event))
        this.remove(batch)
        attempt = 0
        if (this.dropped > 0) {
          this.logger.warn('event buffer overflowed while Flow was unreachable', { dropped: this.dropped })
        }
      } catch (error) {
        for (const entry of batch) entry.inflight = false
        if (error instanceof AuthError) {
          this.fatal = error
          this.logger.error('events rejected: sandbox token revoked', { status: error.status })
          this.onFatal?.(error)
          break
        }
        if (error instanceof RuntimeError && !error.retryable && error.status >= 400) {
          // A malformed batch would block the queue forever: drop it.
          this.logger.error('events rejected by Flow, dropping batch', { status: error.status, count: batch.length, error: error.message })
          this.remove(batch)
          continue
        }
        const delay = backoffDelay(attempt++, 1000, this.maxBackoffMs, this.random)
        this.logger.warn('posting events failed, will retry', { error: errorMessage(error), pending: this.queue.length, delayMs: delay })
        await this.sleep(delay)
      }
    }
    if (this.queue.length === 0) this.resolveWaiters()
  }

  private remove(batch: Entry[]): void {
    const set = new Set(batch)
    for (let i = this.queue.length - 1; i >= 0; i--) {
      if (set.has(this.queue[i])) {
        this.bytes -= this.queue[i].size
        this.queue.splice(i, 1)
      }
    }
  }

  private resolveWaiters(): void {
    const waiters = this.waiters
    this.waiters = []
    for (const resolve of waiters) resolve()
  }
}
