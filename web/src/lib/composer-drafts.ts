import type { Draft } from '@/types/flow'

export type ComposerDraftType = 'comment' | 'project_update' | 'initiative_update'

export interface StoredComposerDraft {
  id?: string
  type: ComposerDraftType
  resourceId: string
  body: string
  title?: string
  metadata?: Record<string, unknown>
  updatedAt: string
}

const prefix = 'flow:composer-draft:'

export function composerDraftKey(type: ComposerDraftType, resourceId: string) {
  return `${prefix}${type}:${resourceId}`
}

export function readComposerDraft(type: ComposerDraftType, resourceId: string): StoredComposerDraft | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(composerDraftKey(type, resourceId)) ?? 'null') as Partial<StoredComposerDraft> | null
    if (!value || value.type !== type || value.resourceId !== resourceId || typeof value.body !== 'string' || !value.body.trim()) return undefined
    return { ...value, type, resourceId, body: value.body, updatedAt: typeof value.updatedAt === 'string' ? value.updatedAt : new Date().toISOString() }
  } catch {
    return undefined
  }
}

export function writeComposerDraft(value: StoredComposerDraft) {
  try { localStorage.setItem(composerDraftKey(value.type, value.resourceId), JSON.stringify(value)) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

export function clearComposerDraft(type: ComposerDraftType, resourceId: string) {
  try { localStorage.removeItem(composerDraftKey(type, resourceId)) } catch { /* Draft cleanup is best-effort. */ }
}

export function readLocalComposerDrafts(userId: string): Draft[] {
  if (typeof localStorage === 'undefined') return []
  const result: Draft[] = []
  try {
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index)
      if (!key?.startsWith(prefix)) continue
      const value = JSON.parse(localStorage.getItem(key) ?? 'null') as Partial<StoredComposerDraft> | null
      if (!value || (value.type !== 'comment' && value.type !== 'project_update' && value.type !== 'initiative_update') || typeof value.resourceId !== 'string' || typeof value.body !== 'string' || !value.body.trim()) continue
      const updatedAt = typeof value.updatedAt === 'string' ? value.updatedAt : new Date().toISOString()
      result.push({ id: `local:${value.type}:${value.resourceId}`, userId, type: value.type, resourceId: value.resourceId, title: typeof value.title === 'string' ? value.title : '', body: value.body, metadata: { ...(value.metadata ?? {}), ...(typeof value.id === 'string' ? { remoteId: value.id } : {}) }, createdAt: updatedAt, updatedAt })
    }
  } catch { /* Local draft discovery is best-effort in private browsing. */ }
  return result
}
