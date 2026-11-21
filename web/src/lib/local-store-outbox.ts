import type { Issue, IssueUpdateInput } from '@/types/flow'
import { ApiError, isConnectivityError, jsonRequest, realtimeClientId, request } from './api-client'
import { clearLocalStores, LocalStoreDB, localStoreGeneration, retainedLocalAuthority } from './local-store-db'
import { LOCAL_STORE_HEADER } from './local-store-model'
import { openLocalStoreNotices, localStoreOwnerName, type LocalStoreScope } from './local-store-notice'
import type { OutboxEdit, OutboxOutcome } from './local-store-outbox-model'
import { readLocalIssueManifest } from './local-store-resume'

const tabs = new Map<string, ReturnType<typeof createLocalOutbox>>()
const receiptWindowMs = 7 * 24 * 60 * 60_000
const forbiddenFields = ['description', 'descriptionData', 'descriptionState', 'contentState', 'expectedDocumentVersion', 'documentUpdateIds']

export type LocalOutboxView = { scope: LocalStoreScope; token: string; edits: OutboxEdit[] }

export function queueDurableIssueEdit(scope: LocalStoreScope, issueId: string, input: IssueUpdateInput) {
  if (input.expectedVersion === undefined || forbiddenFields.some(field => Object.hasOwn(input, field))) return undefined
  const tab = tabs.get(localStoreOwnerName(scope))
  return tab ? tab.enqueue(issueId, input) : Promise.reject(new ApiError('Could not update issue', 503))
}

export function connectLocalOutbox(scope: LocalStoreScope, changed: () => void, rejected: (error: ApiError) => void, showPending: (view?: LocalOutboxView) => void) {
  const name = localStoreOwnerName(scope)
  const tab = createLocalOutbox(scope, changed, rejected, showPending)
  tabs.set(name, tab)
  return {
    owner: tab.owner,
    close() {
      if (tabs.get(name) === tab) tabs.delete(name)
      tab.close()
    },
  }
}

function createLocalOutbox(scope: LocalStoreScope, changed: () => void, rejected: (error: ApiError) => void, showPending: (view?: LocalOutboxView) => void) {
  const { viewerId, workspaceId, workspaceKey } = scope
  const session = new AbortController()
  const initial = retainedLocalAuthority(viewerId, workspaceId, workspaceKey)
  let eligible: boolean | undefined
  let owner: AbortController | undefined
  let pending = false
  let running = false
  let waitUntil = 0
  const waits = new Map<string, { issueId: string; resolve(issue: Issue): void; reject(error: unknown): void }>()
  const acknowledging = new Set<string>()
  const valid = () => !session.signal.aborted && initial?.token === retainedLocalAuthority(viewerId, workspaceId, workspaceKey)?.token && !!initial
  const open = () => valid() ? LocalStoreDB.openRetained(viewerId, workspaceId, workspaceKey, localStoreGeneration(), true) : undefined
  const path = (id: string) => `/api/issue-records/${encodeURIComponent(id)}`
  const headers = () => ({ 'X-Workspace-Key': workspaceKey, [LOCAL_STORE_HEADER]: viewerId })
  const authorized = async (signal: AbortSignal) => {
    if (!valid()) throw new ApiError('Sync authorization changed', 403)
    const manifest = await readLocalIssueManifest(scope, false, signal).catch(async error => {
      if (!signal.aborted && valid() && error instanceof ApiError && error.status === 404) await clearLocalStores(viewerId, workspaceKey)
      throw error
    })
    if (valid() && manifest.authzEpoch !== initial?.authzEpoch) await clearLocalStores(viewerId, workspaceKey)
    if (!valid()) throw new ApiError('Sync authorization changed', 403)
    eligible = manifest.outbox
    return eligible
  }
  let checking: Promise<boolean> | undefined
  const eligibility = () => checking ??= authorized(session.signal).finally(() => { checking = undefined })

  let pendingRead = 0
  let displayed = ''
  async function refreshPending() {
    const stamp = ++pendingRead
    if (!valid()) return
    const db = await open()
    if (!db) return
    const edits = await db.pendingEdits().finally(() => db.close())
    if (!edits || stamp !== pendingRead || !valid()) return
    const key = edits.map(edit => edit.id).join(',')
    if (key === displayed) return
    if (edits.length && !(eligible ?? await eligibility())) return
    if (!valid() || stamp !== pendingRead) return
    displayed = key
    showPending(edits.length ? { scope, token: initial!.token, edits } : undefined)
  }
  const readPending = () => { void refreshPending().catch(() => undefined) }
  readPending()

  const acknowledge = async (id: string) => {
    const waiting = waits.get(id)
    if (!waiting || acknowledging.has(id)) return
    acknowledging.add(id)
    try {
      const db = await open()
      if (!db || !valid()) {
        db?.close()
        throw new ApiError('Sync authorization changed', 403)
      }
      const outcome = await db.editOutcome(id).finally(() => db.close())
      if (!outcome) return
      await eligibility()
      const current = outcome.ok || outcome.status === 409
        ? await request<Issue>(path(waiting.issueId), { headers: headers(), signal: session.signal }) : undefined
      if (!valid()) throw new ApiError('Sync authorization changed', 403)
      waits.delete(id)
      if (outcome.ok) waiting.resolve(current!)
      else waiting.reject(new ApiError(outcome.message, outcome.status, outcome.code, current))
    } catch (error) {
      if (valid() && isConnectivityError(error)) return
      if (waits.delete(id)) waiting.reject(error)
    } finally { acknowledging.delete(id) }
  }

  const bus = openLocalStoreNotices(scope, notice => {
    if (notice.type === 'outbox' || notice.type === 'settled') readPending()
    if (notice.type === 'outbox') wake()
    if (notice.type === 'settled' && typeof notice.id === 'string') void acknowledge(notice.id)
  })

  const send = async (edit: OutboxEdit, signal: AbortSignal): Promise<OutboxOutcome> => {
    const init = jsonRequest('PATCH', edit.input)
    let rebaseVersion: number | undefined
    await request<Issue>(path(edit.issueId), {
      ...init, signal,
      onResponse(response) {
        const version = Number(response.headers.get('X-Flow-Local-Rebase-Version'))
        if (edit.input.expectedVersion !== undefined && version === edit.input.expectedVersion + 1) rebaseVersion = version
      },
      headers: { ...init.headers, ...headers(), 'X-Flow-Local-Mutation': edit.id, 'X-Flow-Local-Authz-Epoch': initial!.authzEpoch },
    })
    return { ok: true, rebaseVersion }
  }

  async function flush(signal: AbortSignal) {
    const db = await open()
    if (!db) return
    try {
      const edits = await db.pendingEdits()
      if (!edits?.length || !await authorized(signal)) return
      for (const { id } of edits) {
        signal.throwIfAborted()
        if (!db.valid || !valid()) return
        const edit = await db.markEditSent(id)
        if (!edit) continue
        let outcome: OutboxOutcome
        try {
          if (Date.now() - edit.createdAt > receiptWindowMs) throw new ApiError('Could not update issue', 409)
          outcome = await send(edit, signal)
        } catch (error) {
          if (signal.aborted) return
          if (!(error instanceof ApiError) || isConnectivityError(error) || error.status === 429 || error.status >= 500) throw error
          outcome = { ok: false, status: error.status, code: error.code, message: error.message }
        }
        signal.throwIfAborted()
        if (await db.settleEdit(edit, outcome)) {
          if (outcome.ok || outcome.status === 403 || outcome.status === 404) changed()
          if (!outcome.ok && !waits.has(edit.id)) {
            rejected(new ApiError(outcome.message, outcome.status, outcome.code))
          }
          bus.post({ type: 'settled', id: edit.id })
          void acknowledge(edit.id)
          readPending()
        }
      }
      await db.clearSettledEdits(Date.now() - 60 * 60_000)
    } finally { db.close() }
  }

  function wake() {
    if (!owner) return
    if (running) { pending = true; return }
    if (Date.now() < waitUntil) return
    running = true
    const { signal } = owner
    void flush(signal).catch(() => { if (!signal.aborted) waitUntil = Date.now() + 1500 }).finally(() => {
      running = false
      if (pending) {
        pending = false
        wake()
      }
    })
  }
  const timer = setInterval(() => {
    wake()
    readPending()
    for (const id of waits.keys()) void acknowledge(id)
  }, 2000)
  window.addEventListener('online', wake)

  return {
    async enqueue(issueId: string, input: IssueUpdateInput) {
      if (!(eligible ?? await eligibility())) return undefined
      const db = await open()
      if (!db) throw new ApiError('Could not update issue', 503)
      const bytes = JSON.stringify(input).length * 2 + 256
      const edit = { issueId, input, id: crypto.randomUUID(), editor: realtimeClientId(), createdAt: Date.now(), bytes }
      try {
        if (bytes > 64_000 || !await db.writeWithinBudget(bytes, budget => db.appendEdit(edit, budget))) {
          throw new ApiError('Could not update issue', 503)
        }
      } finally { db.close() }
      if (!valid()) throw new ApiError('Sync authorization changed', 403)
      const acknowledged = new Promise<Issue>((resolve, reject) => {
        waits.set(edit.id, { issueId, resolve, reject })
      })
      readPending()
      bus.post({ type: 'outbox' })
      wake()
      return { acknowledged }
    },
    owner(owns: boolean) {
      owner?.abort()
      owner = owns ? new AbortController() : undefined
      if (owner) { waitUntil = 0; wake() }
    },
    close() {
      session.abort()
      owner?.abort()
      owner = undefined
      clearInterval(timer)
      window.removeEventListener('online', wake)
      bus.close()
      if (displayed) showPending(undefined)
      for (const waiting of waits.values()) waiting.reject(new ApiError('Could not update issue', 503))
      waits.clear()
    },
  }
}
