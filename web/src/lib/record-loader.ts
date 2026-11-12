import type { RecordKey, SyncRecord, SyncRecordBatch } from '@/types/flow'

export const RECORD_BATCH_DELAY_MS = 10
export const RECORD_BATCH_MAX_KEYS = 50
export const RECORD_BATCH_MAX_BYTES = 256 * 1024
export const RECORD_BATCH_CONCURRENCY = 4
export const recordKey = (key: RecordKey) => JSON.stringify([key.kind, key.id])

type Waiter = { resolve: (value: SyncRecord | undefined) => void; reject: (error: unknown) => void }
type Pending = { key: RecordKey; waiters: Set<Waiter>; batch?: Pending[]; controller?: AbortController }

/** A loader belongs to exactly one workspace, viewer and authorization epoch. */
export class RecordLoader {
  private pending = new Map<string, Pending>()
  private queue: Pending[] = []
  private timer?: ReturnType<typeof setTimeout>
  private active = 0
  private closed = false
  private readonly options: {
    authzEpoch: string
    fetch: (keys: RecordKey[], signal: AbortSignal) => Promise<SyncRecordBatch>
    onEpochChange: (epoch: string) => void
  }

  constructor(options: {
    authzEpoch: string
    fetch: (keys: RecordKey[], signal: AbortSignal) => Promise<SyncRecordBatch>
    onEpochChange: (epoch: string) => void
  }) { this.options = options }

  load(key: RecordKey, signal?: AbortSignal): Promise<SyncRecord | undefined> {
    if (this.closed || signal?.aborted) return Promise.reject(signal?.reason ?? new DOMException('Aborted', 'AbortError'))
    if (new TextEncoder().encode(JSON.stringify(key)).byteLength > RECORD_BATCH_MAX_BYTES - 129) return Promise.reject(new Error('Record key exceeds batch budget'))
    const token = recordKey(key)
    let pending = this.pending.get(token)
    if (pending?.controller?.signal.aborted) pending = undefined
    if (!pending) {
      if (this.pending.size >= 2000) return Promise.reject(new Error('Record loader queue is full'))
      pending = { key: { ...key }, waiters: new Set() }
      this.pending.set(token, pending)
      this.queue.push(pending)
    }
    const entry = pending
    return new Promise((resolve, reject) => {
      const finish = () => { entry.waiters.delete(waiter); signal?.removeEventListener('abort', abort) }
      const waiter: Waiter = { resolve: value => { finish(); resolve(value) }, reject: error => { finish(); reject(error) } }
      const abort = () => {
        waiter.reject(signal?.reason ?? new DOMException('Aborted', 'AbortError'))
        if (entry.batch?.every(item => !item.waiters.size)) entry.controller?.abort()
      }
      entry.waiters.add(waiter)
      signal?.addEventListener('abort', abort, { once: true })
      if (!this.timer) this.timer = setTimeout(() => { this.timer = undefined; this.pump() }, RECORD_BATCH_DELAY_MS)
    })
  }

  dispose(reason: unknown = new DOMException('Aborted', 'AbortError')) {
    this.closed = true
    clearTimeout(this.timer)
    this.timer = undefined
    for (const item of this.pending.values()) {
      item.controller?.abort()
      for (const waiter of [...item.waiters]) waiter.reject(reason)
    }
    this.pending.clear()
    this.queue = []
  }

  private pump() {
    while (!this.closed && this.active < RECORD_BATCH_CONCURRENCY && this.queue.length) {
      const batch: Pending[] = []
      let bytes = 128
      while (batch.length < RECORD_BATCH_MAX_KEYS && this.queue.length) {
        const item = this.queue[0]
        const size = new TextEncoder().encode(JSON.stringify(item.key)).byteLength + 1
        if (bytes + size > RECORD_BATCH_MAX_BYTES) break
        this.queue.shift()
        if (!item.waiters.size) { if (this.pending.get(recordKey(item.key)) === item) this.pending.delete(recordKey(item.key)); continue }
        bytes += size
        batch.push(item)
      }
      if (!batch.length) continue
      const controller = new AbortController()
      for (const item of batch) { item.controller = controller; item.batch = batch }
      this.active++
      void this.options.fetch(batch.map(item => item.key), controller.signal).then(response => {
        if (this.closed || controller.signal.aborted) throw new DOMException('Aborted', 'AbortError')
        if (response.authzEpoch !== this.options.authzEpoch) {
          this.options.onEpochChange(response.authzEpoch)
          throw new DOMException('Authorization changed', 'AbortError')
        }
        const allowed = new Set(batch.map(item => recordKey(item.key)))
        if (response.items.some(item => !allowed.has(recordKey(item.key)))) throw new Error('Batch returned an unrequested record')
        const records = new Map(response.items.map(item => [recordKey(item.key), item]))
        for (const item of batch) {
          if (this.pending.get(recordKey(item.key)) === item) this.pending.delete(recordKey(item.key))
          for (const waiter of [...item.waiters]) waiter.resolve(records.get(recordKey(item.key)))
        }
      }).catch(error => {
        for (const item of batch) for (const waiter of [...item.waiters]) waiter.reject(error)
      }).finally(() => {
        for (const item of batch) if (this.pending.get(recordKey(item.key)) === item) this.pending.delete(recordKey(item.key))
        this.active--
        this.pump()
      })
    }
  }
}
