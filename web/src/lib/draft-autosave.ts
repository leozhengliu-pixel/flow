import type { Draft } from '@/types/flow'
import { createDraft, deleteDraft, updateDraft } from '@/lib/api'
import { ApiError, currentWorkspaceKey } from '@/lib/api-client'
import { endsSession, registerSessionCacheClearer, registerSessionFlusher } from '@/lib/session-cache-registry'
import { clearComposerDraft, writeComposerDraft, type ComposerDraftType } from '@/lib/composer-drafts'

/**
 * Autosave for a composer's server draft: one owner per editing context (workspace, draft type, parent resource,
 * composer role), shared by every mount of that composer.
 *
 * - Saves are debounced and serial: at most one request is in flight per context, and edits made meanwhile replace the
 *   content waiting to go, so the latest content is what ends up on the server and a second create can never start while
 *   the first one is pending.
 * - A failed update whose outcome is unknown (network error, timeout, 408/425/429, 5xx, a 404 that is not the API's own)
 *   is retried on the same draft. Only the API's "not found" answer for the draft makes it create a new one, and only
 *   while the context is still being edited.
 * - Every create carries a key generated for this draft, so a create that is retried after a lost response gets the
 *   draft the server already made back instead of a copy.
 * - Sending (detach + discard) waits for the draft's in-flight request before deleting it, so a draft never outlives the
 *   comment it became, and a deleted draft is never shown or reused again.
 */

export const DRAFT_AUTOSAVE_DELAY = 350
const RETRY_FIRST_MS = 1_000
const RETRY_LAST_MS = 30_000
const RETRY_ATTEMPTS = 8
const DELETE_ATTEMPTS = 4

export interface DraftSaveInput {
  type: string
  resourceId: string
  title?: string
  body: string
  metadata?: Record<string, unknown>
}

export interface DraftSeed {
  id?: string
  clientKey?: string
  input?: DraftSaveInput
}

export interface DraftAutosaveHooks {
  /** A save of the context's current draft finished. */
  saved?: (draft: Draft, input: DraftSaveInput) => void
  /** The context no longer has a draft (it was sent, discarded or emptied). */
  cleared?: () => void
}

/** A draft generation taken off the composer (it is being sent): delete it once sent, or give it back if the send failed. */
export interface DetachedDraft {
  discard: () => Promise<void>
  /** Makes the draft the context's draft again; false when the context already has another one. */
  restore: () => boolean
}

interface Generation {
  id?: string
  clientKey: string
  wanted?: { input: DraftSaveInput; fingerprint: string }
  /** The content the server is known to hold. */
  confirmed?: string
  /** Content the server refused for good, or gave up retrying; waits for different content. */
  parked?: string
  /** The input of a create whose outcome is unknown: the server may hold it under clientKey. */
  uncertainCreate?: DraftSaveInput
  inflight?: Promise<void>
  retryTimer?: ReturnType<typeof setTimeout>
  retries: number
  open: boolean
}

type Failure = 'missing' | 'retry' | 'final'

function failureOf(error: unknown): Failure {
  if (error instanceof ApiError) {
    // The API answers a draft the user does not have with its own JSON "not found"; any other 404 (a proxy, a route
    // being deployed) says nothing about the draft.
    if (error.status === 404) return error.code === 'not found' ? 'missing' : 'retry'
    if (error.status === 0 || error.status === 408 || error.status === 425 || error.status === 429 || error.status >= 500) return 'retry'
    return 'final'
  }
  // A network error, timeout or abort: the request may or may not have been applied.
  return 'retry'
}

function newClientKey() {
  const random = typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `${Date.now().toString(36)}${Math.random().toString(36).slice(2)}`
  return `draft-key:${random}`
}

function stable(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stable)
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(Object.keys(value).sort().filter(key => (value as Record<string, unknown>)[key] !== undefined).map(key => [key, stable((value as Record<string, unknown>)[key])]))
}

/** What a save would put on the server, independent of object identity and key order. */
export function draftFingerprint(input: DraftSaveInput) {
  return JSON.stringify(stable({ type: input.type, resourceId: input.resourceId, title: input.title ?? '', body: input.body, metadata: input.metadata ?? {} }))
}

function pristine(generation: Generation) {
  return !generation.id && !generation.wanted && !generation.inflight && !generation.uncertainCreate
}

function freshGeneration(): Generation {
  return { clientKey: newClientKey(), retries: 0, open: true }
}

const later = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))

export class DraftAutosave {
  hooks: DraftAutosaveHooks = {}
  private generation = freshGeneration()
  private debounce?: ReturnType<typeof setTimeout>
  /** Debounced: content to save, or null to discard the draft. */
  private pending?: DraftSaveInput | null

  /** The content this context is saving or holds: what a composer mounting for it should show. */
  get latest(): DraftSaveInput | undefined {
    if (this.pending === null) return undefined
    return this.pending ?? this.generation.wanted?.input
  }

  /** Adopts a draft the server already has (from the drafts list or the local copy) while this context has none. */
  seed(seed?: DraftSeed) {
    const generation = this.generation
    if (!seed?.id || discardedDraftIds.has(seed.id) || !pristine(generation) || this.pending !== undefined) return
    generation.id = seed.id
    if (seed.clientKey) generation.clientKey = seed.clientKey
    if (seed.input) {
      const fingerprint = draftFingerprint(seed.input)
      generation.confirmed = fingerprint
      generation.wanted = { input: seed.input, fingerprint }
    }
  }

  /** Saves `input` (or, with null, discards the draft) after the autosave delay, unless it is what the server already has. */
  schedule(input: DraftSaveInput | null) {
    this.clearDebounce()
    const generation = this.generation
    if (input ? draftFingerprint(input) === (generation.wanted?.fingerprint ?? generation.confirmed) : pristine(generation)) {
      this.pending = undefined
      return
    }
    this.pending = input
    this.debounce = setTimeout(() => this.flush(), DRAFT_AUTOSAVE_DELAY)
  }

  /** Sends what is waiting for the autosave delay now (a composer unmounting). */
  flush() {
    this.clearDebounce()
    const pending = this.pending
    this.pending = undefined
    if (pending === null) void this.discard()
    else if (pending) this.save(pending)
  }

  /** Drops what is waiting for the autosave delay. */
  cancelPending() {
    this.clearDebounce()
    this.pending = undefined
  }

  /** Takes the current draft off the context (it is being sent); later edits start a new draft. */
  detach(): DetachedDraft {
    this.cancelPending()
    const generation = this.generation
    this.close(generation)
    this.generation = freshGeneration()
    return {
      discard: () => this.discardGeneration(generation),
      restore: () => {
        if (this.generation === generation) return true
        if (!pristine(this.generation) || this.pending !== undefined) return false
        generation.open = true
        generation.retries = 0
        this.generation = generation
        this.pump(generation)
        return true
      },
    }
  }

  /**
   * The session ended: nothing waiting for the autosave delay or a retry is sent any more. The server drafts already
   * saved stay on the server (and in the local copy) for their owner; a request already in flight was sent as them.
   */
  abandon() {
    this.cancelPending()
    this.close(this.generation)
    this.generation = freshGeneration()
  }

  /** Sends what is waiting for the autosave delay and resolves once no request of this context is in flight or due. */
  async settle() {
    this.flush()
    for (let generation = this.generation; generation.inflight; generation = this.generation) await generation.inflight
  }

  /** Deletes the context's draft, after whatever request it has in flight. */
  discard() {
    return this.detach().discard()
  }

  /** The draft was deleted elsewhere (the drafts page): the context starts over instead of showing or saving it again. */
  forget(id: string) {
    const generation = this.generation
    if (generation.id !== id) return
    this.cancelPending()
    this.close(generation)
    this.generation = freshGeneration()
  }

  private clearDebounce() {
    if (this.debounce !== undefined) clearTimeout(this.debounce)
    this.debounce = undefined
  }

  private close(generation: Generation) {
    generation.open = false
    if (generation.retryTimer !== undefined) clearTimeout(generation.retryTimer)
    generation.retryTimer = undefined
  }

  private save(input: DraftSaveInput) {
    const generation = this.generation
    generation.wanted = { input, fingerprint: draftFingerprint(input) }
    this.pump(generation)
  }

  private pump(generation: Generation) {
    if (!generation.open || generation.inflight || generation.retryTimer !== undefined || !generation.wanted) return
    const { input, fingerprint } = generation.wanted
    if (fingerprint === generation.confirmed || fingerprint === generation.parked) return
    generation.inflight = this.send(generation, input, fingerprint).finally(() => {
      generation.inflight = undefined
      this.pump(generation)
    })
  }

  private async send(generation: Generation, input: DraftSaveInput, fingerprint: string) {
    const creating = !generation.id
    try {
      const body = { ...input, clientKey: generation.clientKey }
      const saved = generation.id ? await updateDraft(generation.id, body) : await createDraft(body)
      generation.id = saved.id
      generation.confirmed = fingerprint
      generation.parked = undefined
      generation.uncertainCreate = undefined
      generation.retries = 0
      if (generation.open && generation === this.generation) this.hooks.saved?.(saved, input)
    } catch (error) {
      const failure = failureOf(error)
      if (failure === 'missing' && !creating) {
        // The server has no such draft (deleted elsewhere): the next pump creates one, if the context is still edited.
        generation.id = undefined
        generation.confirmed = undefined
        return
      }
      if (failure === 'retry') {
        if (creating) generation.uncertainCreate = input
        this.retryLater(generation, fingerprint)
        return
      }
      generation.parked = fingerprint
    }
  }

  private retryLater(generation: Generation, fingerprint: string) {
    if (!generation.open) return
    if (generation.retries >= RETRY_ATTEMPTS) {
      generation.parked = fingerprint
      generation.retries = 0
      return
    }
    const delay = Math.min(RETRY_LAST_MS, RETRY_FIRST_MS * 2 ** generation.retries)
    generation.retries++
    generation.retryTimer = setTimeout(() => {
      generation.retryTimer = undefined
      this.pump(generation)
    }, delay)
  }

  private async discardGeneration(generation: Generation) {
    this.close(generation)
    if (generation.id) discardedDraftIds.add(generation.id)
    while (generation.inflight) await generation.inflight
    if (!generation.id && generation.uncertainCreate) {
      // A create whose answer was lost may have made the draft: ask again with the same key, then delete what it names.
      try { generation.id = (await createDraft({ ...generation.uncertainCreate, clientKey: generation.clientKey })).id } catch { /* nothing was made, or it cannot be found now */ }
      generation.uncertainCreate = undefined
    }
    const id = generation.id
    generation.id = undefined
    generation.wanted = undefined
    if (id) {
      discardedDraftIds.add(id)
      for (let attempt = 0; attempt < DELETE_ATTEMPTS; attempt++) {
        try { await deleteDraft(id); break } catch (error) {
          if (failureOf(error) !== 'retry') break
          await later(Math.min(RETRY_LAST_MS, RETRY_FIRST_MS * 2 ** attempt))
        }
      }
    }
    if (pristine(this.generation) && this.pending === undefined) this.hooks.cleared?.()
  }
}

const autosaves = new Map<string, DraftAutosave>()
const discardedDraftIds = new Set<string>()

/** The editing context's key: one draft per workspace, draft type, parent resource and composer role. */
export function draftContextKey(type: string, resourceType: string, resourceId: string, role = 'top-level') {
  return [currentWorkspaceKey(), type, resourceType, resourceId, role].join('|')
}

/** The autosave for an editing context, created on first use and shared by every mount of its composer. */
export function draftAutosaveFor(key: string, seed?: DraftSeed) {
  let autosave = autosaves.get(key)
  if (!autosave) autosaves.set(key, autosave = new DraftAutosave())
  autosave.seed(seed)
  return autosave
}

/**
 * The autosave of a comment/update composer's draft, mirrored to the local copy, adopting the saved draft (from the drafts
 * list or the local copy) when the context has none yet.
 */
export function composerDraftAutosave(type: ComposerDraftType, resourceType: string, resourceId: string, persisted?: { id?: string; clientKey?: string; title?: string; body: string; metadata?: Record<string, unknown> }) {
  const autosave = draftAutosaveFor(draftContextKey(type, resourceType, resourceId))
  autosave.hooks = {
    saved: (saved, input) => writeComposerDraft({ id: saved.id, type, resourceId, title: input.title, body: input.body, metadata: input.metadata, updatedAt: saved.updatedAt }),
    cleared: () => clearComposerDraft(type, resourceId),
  }
  const id = serverDraftId(persisted)
  if (persisted && !wasDiscardedDraft(id)) autosave.seed({ id, clientKey: persisted.clientKey, input: { type, resourceId, title: persisted.title ?? '', body: persisted.body, metadata: persisted.metadata } })
  return autosave
}

/** Forgets every context's autosave, as a page load does: tests that render one project or issue in several cases. */
export function resetDraftAutosaves() {
  autosaves.clear()
  discardedDraftIds.clear()
}

/** A draft deleted outside its composer (the drafts page): no composer restores or keeps saving it. */
export function forgetDraft(id?: string) {
  if (!id) return
  discardedDraftIds.add(id)
  for (const autosave of autosaves.values()) autosave.forget(id)
}

/** A draft this page deleted (its comment was sent or it was discarded); a stale copy of it must not come back. */
export function wasDiscardedDraft(id?: string) {
  return Boolean(id && discardedDraftIds.has(id))
}

/** The server draft behind a draft entry: a local copy names it in metadata.remoteId. */
export function serverDraftId(draft?: { id?: string; metadata?: Record<string, unknown> }) {
  if (!draft?.id) return undefined
  if (!draft.id.startsWith('local:')) return draft.id
  const remote = draft.metadata?.remoteId
  return typeof remote === 'string' && remote ? remote : undefined
}

// Logout flushes nothing, and its cookie is gone before this runs, so a save still waiting would be sent as the next
// viewer (a draft they would then own). Waiting and retrying saves are dropped instead; saved drafts are untouched.
registerSessionCacheClearer((_viewerId, workspaceKey) => {
  if (!endsSession(workspaceKey)) return
  for (const autosave of autosaves.values()) autosave.abandon()
  autosaves.clear()
  discardedDraftIds.clear()
})

registerSessionFlusher(() => Promise.all([...autosaves.values()].map(autosave => autosave.settle())))
