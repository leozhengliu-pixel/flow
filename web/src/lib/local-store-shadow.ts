import type { BootstrapData, Issue, SyncChanges } from '@/types/flow'
import { fetchBootstrap, fetchPagedBootstrap } from './api'
import { ApiError, requestRetryingBusy } from './api-client'
import { localSnapshotBuild } from './local-snapshot'
import { localStoreBootstrap } from './local-store-bootstrap'
import { clearLocalStores, hasLocalStore, isLocalStoreQuotaError, LOCAL_STORE_BUDGET, localStorageBudget, localStoreGeneration, LocalStoreDB, recordLocalStoreVisit, retainedLocalAuthority, retainLocalStoreAuthority, type LocalCollections, type LocalMeta } from './local-store-db'
import {
  LOCAL_STORE_HEADER, LOCAL_STORE_CAPABILITY, LocalStoreRefreshRequired, LocalStoreReset, compareLocalStoreSeq, localChecksum, localKinds, localRowBytes, localRowFingerprint,
  localStoreAuthority, localStoreEnabled, planLocalChanges, projectLocalCollection, type LocalChange, type LocalStoreAuthority,
} from './local-store-model'
import { modelChanges } from './scale-p13-wire'
import { catchUpLocalIssues, localStoreLoadedWorkspace, readLocalIssueChanges, readLocalIssueManifest } from './local-store-resume'
import { openLocalStoreNotices, type LocalStoreNotice } from './local-store-notice'

const reconcileInterval = 60_000
const manifestInterval = 24 * 60 * 60_000
type ResumeReset = (viewerId: string, workspaceKey: string, status?: number, keepDatabase?: boolean) => void
export type LocalIssueCommit = { changes: LocalChange[] } | { issues: Issue[] }
type ResumeCommit = (meta: LocalMeta, update: LocalIssueCommit) => void

function idle() {
  return new Promise<void>(resolve => {
    if (typeof window.requestIdleCallback === 'function') window.requestIdleCallback(() => resolve(), { timeout: 500 })
    else window.setTimeout(resolve, 0)
  })
}

function measure(kind: string, start = performance.now()) {
  const name = `flow:local-store:${kind}`
  if (performance.getEntriesByName(name).length >= 256) performance.clearMeasures(name)
  performance.measure(name, { start, end: performance.now() })
}

async function readAuthority(workspaceKey: string, signal?: AbortSignal) {
  const manifest = await requestRetryingBusy<{ schema?: number; capabilities?: string[] }>('/api/sync/manifest?surface=local-store', {
    headers: { 'X-Workspace-Key': workspaceKey }, signal, quiet: true,
  })
  return manifest.schema === 2 && manifest.capabilities?.includes(LOCAL_STORE_CAPABILITY) ? localStoreAuthority(manifest) : undefined
}

function applyToMemory(rows: LocalCollections, changes: LocalChange[]) {
  for (const { kind, id, row } of changes) {
    const collection = rows.get(kind)!
    if (row) collection.set(id, row)
    else collection.delete(id)
  }
}

function sizeAfter(rows: LocalCollections, changes: LocalChange[], bytes: number) {
  const changed = new Map<string, number>()
  for (const { kind, id, row } of changes) {
    const key = `${kind}:${id}`
    const previous = rows.get(kind)?.get(id)
    const oldSize = changed.get(key) ?? (previous ? localRowBytes(previous) : 0)
    const newSize = row ? localRowBytes(row) : 0
    changed.set(key, newSize)
    bytes += newSize - oldSize
  }
  return bytes
}

class ShadowRun {
  readonly viewerId: string
  readonly workspaceKey: string
  private readonly retainedWorkspaceId?: string
  private readonly onReset?: ResumeReset
  private readonly onCommit?: ResumeCommit
  private readonly controller = new AbortController()
  private readonly rows: LocalCollections = new Map()
  private db?: LocalStoreDB
  private meta?: LocalMeta
  private timer?: ReturnType<typeof setTimeout>
  private checkTimer?: ReturnType<typeof setInterval>
  private running = false
  private queued = false
  private verifyAuthority = false
  private owner: boolean | undefined
  private reader?: AbortController
  private notices?: ReturnType<typeof openLocalStoreNotices>
  private commit?: Extract<LocalStoreNotice, { type: 'commit' }>
  private pagedIssues = false
  private budget = LOCAL_STORE_BUDGET
  private nextManifestAt = Date.now() + manifestInterval
  constructor(viewerId: string, workspaceKey: string, retainedWorkspaceId?: string, onReset?: ResumeReset, onCommit?: ResumeCommit) {
    this.viewerId = viewerId
    this.workspaceKey = workspaceKey
    this.retainedWorkspaceId = retainedWorkspaceId
    this.onReset = onReset
    this.onCommit = onCommit
  }

  async attach({ checkpoint, seeded }: NonNullable<ReturnType<typeof localStoreLoadedWorkspace>>) {
    // Install revocation listeners before opening the database or waiting for
    // the idle checksum. A permission event during that wait must wake sync.
    this.meta = checkpoint
    this.watch(false)
    const db = await LocalStoreDB.openRetained(checkpoint.viewerId, checkpoint.workspaceId, this.workspaceKey, localStoreGeneration())
    if (this.controller.signal.aborted) { db?.close(); return }
    if (!db) {
      const retained = hasLocalStore(checkpoint.viewerId, checkpoint.workspaceId)
      this.stop()
      this.onReset?.(this.viewerId, this.workspaceKey, undefined, retained)
      return
    }
    this.db = db
    db.shareSliceReads()
    this.notices = openLocalStoreNotices(checkpoint, notice => {
      if (notice.type !== 'commit' || this.owner !== false) return
      this.commit = notice
      this.schedule()
    })
    recordLocalStoreVisit()
    try {
      // Verify new baselines on idle. Large repeat visits verify on the timer.
      if (seeded || checkpoint.resume!.issueCount <= 2048) await this.checkIssues(checkpoint)
      else this.nextManifestAt = Date.now() + reconcileInterval
    }
    catch (error) {
      if (error instanceof LocalStoreReset && !this.controller.signal.aborted) {
        await this.recheckBaselineAuthority(checkpoint)
      } else this.fail(error)
      return
    }
  }

  watches(viewerId: string, workspaceKey: string) {
    return !this.controller.signal.aborted && this.viewerId === viewerId && this.workspaceKey === workspaceKey
  }

  accepts(data: BootstrapData, capture: NonNullable<ReturnType<typeof localStoreBootstrap>>) {
    return data.workspace.urlKey === this.workspaceKey && data.viewer.id === this.viewerId && this.db?.valid &&
      this.meta?.workspaceId === capture.authority.workspaceId && this.meta.authzEpoch === capture.authority.authzEpoch &&
      this.pagedIssues === !!data.issueCollectionPaged
  }

  async seed(initial: BootstrapData) {
    const { signal } = this.controller
    let data = initial
    for (let attempt = 0; attempt < 3; attempt++) {
      const capture = localStoreBootstrap(data)
      if (!capture || data.viewer.id !== this.viewerId || signal.aborted) break
      try {
        await this.seedBootstrap(data, capture)
        return
      } catch (error) {
        // The first bootstrap can race startup writes, including grant changes.
        // Retry from a new server snapshot after removing the old grant's rows.
        if (signal.aborted) return
        if (!(error instanceof LocalStoreReset) || this.db || attempt === 2) {
          this.fail(error)
          return
        }
        await clearLocalStores(this.viewerId, this.workspaceKey)
        try {
          data = await this.fetchBaseline(signal)
        } catch (error) {
          this.fail(error)
          return
        }
      }
    }
    this.stop()
  }

  private async seedBootstrap(data: BootstrapData, capture: NonNullable<ReturnType<typeof localStoreBootstrap>>) {
    const { authority, generation } = capture
    const { signal } = this.controller
    this.rows.clear()
    this.pagedIssues = !!data.issueCollectionPaged
    this.budget = await localStorageBudget()
    let bytes = 0
    for (const kind of localKinds) {
      await idle()
      if (signal.aborted || generation !== localStoreGeneration()) { this.stop(); return }
      const rows = projectLocalCollection(data, kind, authority.head)
      this.rows.set(kind, rows)
      for (const row of rows.values()) bytes += localRowBytes(row)
      if (bytes > this.budget) { this.stop(); return }
    }
    const db = await LocalStoreDB.open(authority, this.workspaceKey, generation)
    if (!db || signal.aborted) { db?.close(); this.stop(); return }
    try {
      const previous = await db.readMeta()
      // The stamp predates the bootstrap. Catch up before committing the
      // baseline, and leave a concurrent tab's newer checkpoint intact.
      const page = await this.changes(authority)
      const writes = this.plan(page, authority.head, authority.authzEpoch)
      if (page.hasMore) throw new LocalStoreReset('Bootstrap changed during the read')
      if (signal.aborted || generation !== localStoreGeneration()) { this.stop(); return }
      bytes = sizeAfter(this.rows, writes, bytes)
      applyToMemory(this.rows, writes)
      this.db = db
      const now = Date.now()
      this.meta = await db.writeWithinBudget(bytes - (previous?.bytes ?? 0), available => {
        signal.throwIfAborted()
        return db.seed({ ...authority, head: page.next }, {
          build: localSnapshotBuild(), workspaceKey: this.workspaceKey, baseline: crypto.randomUUID(),
          bootstrapAt: now, lastAccessAt: now, fullCollections: [], bytes,
        }, this.rows, previous, undefined, available)
      }, this.budget)
    } finally {
      if (this.db !== db) db.close()
    }
    if (!this.meta || signal.aborted) { this.stop(); return }
    recordLocalStoreVisit()
    await this.check(this.meta)
    this.watch()
  }

  private readonly onOnline = () => this.schedule(true)
  private readonly onPageShow = (event: PageTransitionEvent) => {
    if (event.persisted) this.onOnline()
  }
  private readonly onStorage = () => {
    if (this.meta?.resume && !this.db?.valid) this.schedule()
  }

  watch(checkNow = true) {
    const { signal } = this.controller
    if (signal.aborted) return
    this.checkTimer ??= setInterval(() => this.schedule(), reconcileInterval)
    window.addEventListener('online', this.onOnline, { signal })
    window.addEventListener('focus', this.onOnline, { signal })
    window.addEventListener('pageshow', this.onPageShow, { signal })
    window.addEventListener('storage', this.onStorage, { signal })
    if (checkNow) this.schedule()
  }

  schedule(verifyAuthority = false) {
    if ((!this.meta && !this.retainedWorkspaceId) || this.controller.signal.aborted) return
    this.verifyAuthority ||= verifyAuthority
    if (this.running) { this.queued = true; return }
    this.timer ??= setTimeout(() => {
      this.timer = undefined
      void this.drain()
    }, 50)
  }

  ownsStream(owned: boolean) {
    if (this.owner === owned) return
    const previous = this.owner
    this.owner = owned
    if (!owned) this.reader?.abort()
    // The first stream starts from the checkpoint that startup just verified.
    if (previous !== undefined) this.schedule()
  }

  private fetchBaseline(signal: AbortSignal) {
    const fetch = this.pagedIssues ? fetchPagedBootstrap : fetchBootstrap
    return fetch(this.workspaceKey, undefined, signal, true)
  }

  private changes({ workspaceId, head, authzEpoch }: LocalStoreAuthority) {
    const params = new URLSearchParams({ surface: 'local-store', workspaceId, after: head, authzEpoch, limit: '100' })
    return requestRetryingBusy<SyncChanges>(`/api/sync/changes?${params}`, {
      headers: { 'X-Workspace-Key': this.workspaceKey, [LOCAL_STORE_HEADER]: this.viewerId }, signal: this.controller.signal, quiet: true,
    }).then(page => {
      if (!Array.isArray(page?.transactions)) throw new LocalStoreReset('Invalid local store journal page')
      return modelChanges(page)
    })
  }

  private plan(page: SyncChanges, head: string, epoch: string) {
    const writes = planLocalChanges(page, head, epoch)
    return this.pagedIssues ? writes.filter(change => change.kind !== 'issue') : writes
  }

  private async drain() {
    if (this.running || this.controller.signal.aborted) return
    this.running = true
    const verifyAuthority = this.verifyAuthority
    this.verifyAuthority = false
    try {
      if (!this.meta || !this.db) {
        await this.verifyRetainedAuthority()
        return
      }
      if (this.meta.resume) {
        if (this.owner === false) {
          const { db, meta } = this
          // The owner can be suspended when a follower reconnects or regains focus.
          if (verifyAuthority) await this.verifyIssueAuthority(meta)
          await this.readOwnerCommit(db, meta)
        } else await this.drainIssues(this.db, this.meta)
        return
      }
      let updated = false
      let meta: LocalMeta = this.meta
      for (let pageIndex = 0; ; pageIndex++) {
        const page = await this.changes(meta)
        const writes = this.plan(page, meta.head, meta.authzEpoch)
        if (page.next !== meta.head) {
          if (this.controller.signal.aborted) return
          let start = performance.now()
          const bytes = sizeAfter(this.rows, writes, meta.bytes)
          if (bytes > this.budget) throw new LocalStoreReset('Local store exceeded its budget')
          const db = this.db
          const committed = await db.writeWithinBudget(bytes - meta.bytes, available => {
            this.controller.signal.throwIfAborted()
            if (bytes > available) throw new LocalStoreReset('Local store exceeded its budget')
            start = performance.now()
            return db.apply(meta, page.next, bytes, writes)
          }, this.budget)
          if (!committed) { this.stop(); return }
          this.meta = meta = committed
          if (writes.length) measure('delta-write', start)
          applyToMemory(this.rows, writes)
          updated ||= writes.length > 0
        }
        if (!page.hasMore) break
        if (pageIndex === 99) throw new LocalStoreReset('Local store catch-up exceeded its page budget')
        await idle()
      }
      const due = Date.now() >= this.nextManifestAt
      if (due && !this.controller.signal.aborted) await this.reconcile(meta)
      if (updated || due) await this.check(meta)
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return
      if (this.meta?.resume && error instanceof LocalStoreReset) {
        await this.recheckBaselineAuthority(this.meta)
        return
      }
      if (this.meta?.resume && isLocalStoreQuotaError(error)) {
        if (!this.controller.signal.aborted) {
          measure('reset')
          this.stop()
          this.onReset?.(this.viewerId, this.workspaceKey, undefined, true)
        }
      } else if (this.meta?.resume && error instanceof ApiError && [400, 401, 403, 404].includes(error.status)) {
        if (!this.controller.signal.aborted) {
          measure('reset')
          this.db?.discard()
          this.stop()
          this.onReset?.(this.viewerId, this.workspaceKey, error.status)
        }
      } else if (error instanceof ApiError && error.status === 409) {
        // A busy workspace can change while the server projects the page.
        // Keep the committed cursor and let the next event or timer retry.
      } else if (error instanceof LocalStoreRefreshRequired) await this.refreshBaseline()
      else this.fail(error)
    }
    finally {
      this.running = false
      if (this.queued) { this.queued = false; this.schedule() }
    }
  }

  private async recheckBaselineAuthority(meta: LocalMeta) {
    const { signal } = this.controller
    let keep = false
    let status: number | undefined
    try {
      const manifest = await readLocalIssueManifest(meta, false, signal)
      const retained = retainedLocalAuthority(meta.viewerId, meta.workspaceId, meta.workspaceKey)
      // A browser can close this connection while the database and outbox remain authorized.
      keep = retained?.authzEpoch === manifest.authzEpoch
      if (keep && this.db?.valid && manifest.authzEpoch === meta.authzEpoch) await this.db.invalidateReadModel(meta)
    } catch (error) {
      if (error instanceof ApiError) status = error.status
      if (error instanceof TypeError || status === 409 || status === 429 || status === 502 || status === 503 || status === 504) return
    }
    if (signal.aborted) return
    measure('reset')
    if (!keep) void this.db?.discard()
    this.stop()
    this.onReset?.(this.viewerId, this.workspaceKey, status, keep)
  }

  private async drainIssues(db: LocalStoreDB, meta: LocalMeta) {
    if (!db.valid) throw new LocalStoreReset('Local store was removed')
    const reader = this.reader = new AbortController()
    this.budget = await localStorageBudget()
    reader.signal.throwIfAborted()
    const latest = await db.readMeta()
    if (latest?.baseline === meta.baseline && latest.head !== meta.head) {
      await this.readOwnerCommit(db, meta)
      meta = this.meta!
    }
    const update = await catchUpLocalIssues(db, meta, reader.signal, this.budget)
    if (this.controller.signal.aborted || reader.signal.aborted) return
    if (update) {
      this.meta = update.checkpoint
      if (update.changes.length) this.onCommit?.(update.checkpoint, { changes: update.changes })
      if (update.checkpoint !== meta) this.notices?.post({ type: 'commit', head: update.checkpoint.head, from: meta.head,
        baseline: meta.baseline, stores: ['issue'], ids: update.changes.length <= 500 ? update.changes.map(change => change.id) : [] })
    } else {
      // Another tab may have applied the same journal page while we read it.
      const latest = await db.readMeta()
      if (!latest || latest.baseline !== meta.baseline) throw new LocalStoreReset('Local store baseline changed')
      const snapshot = await db.readIssueWorkspace(latest)
      if (snapshot && db.valid && !this.controller.signal.aborted) {
        this.meta = latest
        this.onCommit?.(latest, { issues: snapshot.issues })
      }
      this.queued = true
      return
    }
    if (Date.now() >= this.nextManifestAt) {
      await this.reconcile(this.meta)
      await this.checkIssues(this.meta)
    }
  }

  private async readOwnerCommit(db: LocalStoreDB, previous: LocalMeta) {
    if (!db.valid) throw new LocalStoreReset('Local store was removed')
    const notice = this.commit
    this.commit = undefined
    if (notice?.from === previous.head && notice.baseline === previous.baseline && Array.isArray(notice.ids) && notice.ids.length) {
      const read = await db.readCommittedIssues(notice.ids, notice)
      if (read && db.valid && !this.controller.signal.aborted) {
        this.meta = read.meta
        this.onCommit?.(read.meta, { changes: read.changes })
        return
      }
    }
    const latest = await db.readMeta()
    if (!latest?.resume || latest.baseline !== previous.baseline) throw new LocalStoreReset('Local store baseline changed')
    if (latest.head === previous.head) { this.meta = latest; return }
    const view = await db.readIssueWorkspace(latest)
    if (view && db.valid && !this.controller.signal.aborted) {
      this.meta = latest
      this.onCommit?.(latest, { issues: view.issues })
    }
  }

  private async verifyIssueAuthority(meta: LocalMeta) {
    // A move can revoke issue access without changing the workspace epoch.
    let head = meta.head
    for (let pageNumber = 0; pageNumber < 100; pageNumber++) {
      const page = await readLocalIssueChanges(meta, { ...meta, head }, this.controller.signal)
      planLocalChanges(page, head, meta.authzEpoch, true)
      if (!page.hasMore) return
      head = page.next
    }
    throw new LocalStoreReset('Retained authority could not catch up')
  }

  private async verifyRetainedAuthority() {
    const workspaceId = this.retainedWorkspaceId
    if (!workspaceId || !hasLocalStore(this.viewerId, workspaceId)) { this.stop(); return }
    const { signal } = this.controller
    const generation = localStoreGeneration()
    const db = await LocalStoreDB.openRetained(this.viewerId, workspaceId, this.workspaceKey, generation)
    let meta: LocalMeta | undefined
    try {
      meta = await db?.readMeta()
      if (meta?.resume) {
        await this.verifyIssueAuthority(meta)
        return
      }
      const scope = await readAuthority(this.workspaceKey, signal)
      if (signal.aborted || generation !== localStoreGeneration()) return
      if (scope?.viewerId === this.viewerId && scope.workspaceId === workspaceId) {
        await retainLocalStoreAuthority(scope, this.workspaceKey, generation)
      } else {
        await clearLocalStores(this.viewerId, this.workspaceKey)
      }
    } catch (error) {
      if (signal.aborted) return
      if (error instanceof LocalStoreReset && db?.valid) {
        try {
          const manifest = await readLocalIssueManifest({ viewerId: this.viewerId, workspaceId, workspaceKey: this.workspaceKey }, false, signal)
          if (meta?.authzEpoch === manifest.authzEpoch) await db.invalidateReadModel(meta)
          else await retainLocalStoreAuthority(manifest, this.workspaceKey, generation)
        } catch (cause) {
          if (!signal.aborted && cause instanceof ApiError && [401, 403, 404].includes(cause.status)) await db.discard()
        }
      } else if (error instanceof ApiError && [403, 404].includes(error.status)) {
        this.stop()
        this.onReset?.(this.viewerId, this.workspaceKey, error.status)
      }
    } finally { db?.close() }
    if (!hasLocalStore(this.viewerId, workspaceId)) this.stop()
  }

  private async refreshBaseline() {
    const { signal } = this.controller
    if (signal.aborted) return
    measure('reset')
    clearInterval(this.checkTimer)
    this.checkTimer = undefined
    this.db?.close()
    this.db = undefined
    this.meta = undefined
    await clearLocalStores(this.viewerId, this.workspaceKey)
    if (signal.aborted) return
    try {
      await this.seed(await this.fetchBaseline(signal))
    } catch (error) { this.fail(error) }
  }

  private async reconcile(meta: LocalMeta) {
    const { signal } = this.controller
    this.nextManifestAt = Date.now() + reconcileInterval
    if (meta.resume) {
      const manifest = await readLocalIssueManifest(meta, true, signal)
      if (manifest.authzEpoch !== meta.authzEpoch) throw new LocalStoreReset('Issue authorization changed')
      if (manifest.head !== meta.head) { this.queued = true; return }
      if (manifest.revision !== meta.resume.revision || manifest.issueCount !== meta.resume.issueCount) {
        measure('count-mismatch')
        throw new LocalStoreReset('Local active issue count differs from the manifest')
      }
      measure('count-match')
      this.nextManifestAt = Date.now() + manifestInterval
      return
    }
    const scope = await readAuthority(this.workspaceKey, signal)
    if (!scope || scope.viewerId !== this.viewerId || scope.workspaceId !== meta.workspaceId || scope.authzEpoch !== meta.authzEpoch) {
      throw new LocalStoreReset('Local store permission changed')
    }
    if (compareLocalStoreSeq(scope.head, meta.head) < 0) throw new LocalStoreReset('Local store journal rewound')
    if (scope.head !== meta.head) { this.queued = true; return }

    // There are no counts for these projections in the manifest yet. In the
    // shadow phase, compare with a fresh legacy bootstrap at the same head.
    const data = await this.fetchBaseline(signal)
    const capture = localStoreBootstrap(data)
    if (!capture || capture.authority.authzEpoch !== meta.authzEpoch || capture.authority.viewerId !== meta.viewerId ||
        capture.authority.workspaceId !== meta.workspaceId) throw new LocalStoreReset('Local store authority changed')
    const current = await this.changes(meta)
    this.plan(current, meta.head, meta.authzEpoch)
    if (current.head !== meta.head || capture.authority.head !== meta.head || this.pagedIssues !== !!data.issueCollectionPaged) return
    for (const kind of localKinds) {
      await idle()
      if (signal.aborted || !this.db?.valid) return
      const expected = projectLocalCollection(data, kind, meta.head).size
      if (expected !== this.rows.get(kind)!.size) {
        measure('count-mismatch')
        throw new LocalStoreReset('Local store count differs from bootstrap')
      }
    }
    measure('count-match')
    this.nextManifestAt = Date.now() + manifestInterval
  }

  private async checkIssues(meta: LocalMeta) {
    const db = this.db!
    let cursor: string | undefined
    let count = 0
    let checksum = 0
    const limit = 512
    for (;;) {
      await idle()
      if (this.controller.signal.aborted || !db.valid) return
      const rows = await db.readIssueChunk(cursor, limit)
      if (!rows) return
      for (const row of rows) checksum ^= localRowFingerprint(row).hash
      count += rows.length
      if (rows.length < limit) break
      cursor = rows.at(-1)?.id
    }
    const actual = await db.readMeta()
    if (actual?.baseline !== meta.baseline || actual.head !== meta.head || !db.valid) return
    const mismatch = count !== meta.resume?.issueCount || checksum !== meta.resume?.issueFingerprint
    measure(mismatch ? 'checksum-mismatch' : 'checksum-match')
    if (mismatch) throw new LocalStoreReset('Local store checksum mismatch')
  }

  private async check(meta: LocalMeta) {
    const db = this.db!
    let mismatch = false
    for (const kind of localKinds) {
      await idle()
      if (this.controller.signal.aborted || !db.valid) return
      const actual = await db.readRows(kind)
      const expected = this.rows.get(kind)!
      if (!actual) return
      if (actual.length !== expected.size || actual.length && await localChecksum(actual) !== await localChecksum(expected.values())) {
        mismatch = true
        break
      }
    }
    const actual = await db.readMeta()
    if (actual?.baseline !== meta.baseline || actual.head !== meta.head || !db.valid) return
    measure(mismatch ? 'checksum-mismatch' : 'checksum-match')
    if (mismatch) throw new LocalStoreReset('Local store checksum mismatch')
  }

  private fail(error: unknown) {
    if (this.controller.signal.aborted) return
    if (error instanceof ApiError && [429, 502, 503, 504].includes(error.status) || error instanceof TypeError) {
      if (this.meta) return // Keep the previous committed cursor and retry on the next wakeup.
    }
    if (this.meta?.resume) {
      void this.recheckBaselineAuthority(this.meta)
      return
    }
    measure('reset')
    if (import.meta.env.DEV) console.debug('Local store shadow stopped', error instanceof Error ? error.message : '')
    if (this.db) this.db.discard()
    else if (error instanceof LocalStoreReset) void clearLocalStores(this.viewerId, this.workspaceKey)
    this.stop()
  }

  stop() {
    this.controller.abort()
    this.reader?.abort()
    this.notices?.close()
    clearTimeout(this.timer)
    clearInterval(this.checkTimer)
    this.db?.close()
    this.rows.clear()
  }
}

export function createLocalStoreShadow(onReset?: ResumeReset, onCommit?: ResumeCommit) {
  let run: ShadowRun | undefined
  let ready: ShadowRun | undefined
  let wakePending = false
  let ownership: { viewerId: string; workspaceKey: string; owned: boolean } | undefined
  const coordinate = (candidate: ShadowRun) => {
    if (ownership && candidate.watches(ownership.viewerId, ownership.workspaceKey)) candidate.ownsStream(ownership.owned)
  }
  const onOnline = () => {
    wakePending = true
    if (ready) {
      wakePending = false
      ready.schedule(true)
    }
  }
  const settled = (candidate: ShadowRun, clearPending = false) => {
    if (run !== candidate) return
    ready = candidate
    if (clearPending) wakePending = false
    else if (wakePending) { wakePending = false; candidate.schedule(true) }
  }
  return {
    listen() {
      if (!localStoreEnabled()) return
      window.addEventListener('online', onOnline)
      return () => window.removeEventListener('online', onOnline)
    },
    confirm(data: BootstrapData) {
      const restored = localStoreLoadedWorkspace(data)
      if (restored) {
        run?.stop()
        ready = undefined
        const candidate = new ShadowRun(data.viewer.id, data.workspace.urlKey, data.workspace.id, onReset, onCommit)
        run = candidate
        coordinate(candidate)
        void candidate.attach(restored).then(() => settled(candidate))
        return
      }
      const capture = localStoreBootstrap(data)
      if (!capture) {
        const { viewer, workspace } = data
        if (run?.watches(viewer.id, workspace.urlKey)) { run.schedule(); return }
        // A projected route has no complete seed, but it still needs to
        // notice revocations of rows left by a previous full bootstrap.
        if (!localStoreEnabled() || !hasLocalStore(viewer.id, workspace.id)) return
        run?.stop()
        ready = undefined
        const candidate = new ShadowRun(viewer.id, workspace.urlKey, workspace.id, onReset)
        run = candidate
        candidate.watch()
        settled(candidate, true)
        return
      }
      if (run?.accepts(data, capture)) { run.schedule(); return }
      run?.stop()
      ready = undefined
      const candidate = new ShadowRun(data.viewer.id, data.workspace.urlKey)
      run = candidate
      void candidate.seed(data).then(() => settled(candidate, true))
    },
    scope(viewerId?: string, workspaceKey?: string) {
      if (run && (run.viewerId !== viewerId || run.workspaceKey !== workspaceKey)) { run.stop(); ready = undefined; run = undefined }
    },
    schedule(viewerId?: string, workspaceKey?: string) {
      if (run?.viewerId === viewerId && run?.workspaceKey === workspaceKey) run?.schedule()
    },
    owner(viewerId: string, workspaceKey: string, owned: boolean) {
      ownership = { viewerId, workspaceKey, owned }
      if (run) coordinate(run)
    },
    stop() { run?.stop(); ready = undefined; run = undefined },
  }
}
