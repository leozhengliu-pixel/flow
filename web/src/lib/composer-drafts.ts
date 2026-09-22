import type { Draft } from '@/types/flow'
import { ClientStorage } from '@/lib/client-storage'

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
  const value = ClientStorage.get<Partial<StoredComposerDraft>>(composerDraftKey(type, resourceId), {
    storageMechanism: 'local',
    logError: false,
  })
  if (!value || value.type !== type || value.resourceId !== resourceId || typeof value.body !== 'string' || !value.body.trim()) {
    return undefined
  }
  return {
    ...value,
    type,
    resourceId,
    body: value.body,
    updatedAt: typeof value.updatedAt === 'string' ? value.updatedAt : new Date().toISOString(),
  }
}

export function writeComposerDraft(value: StoredComposerDraft) {
  ClientStorage.set(composerDraftKey(value.type, value.resourceId), value, 'local')
}

export function clearComposerDraft(type: ComposerDraftType, resourceId: string) {
  ClientStorage.remove(composerDraftKey(type, resourceId), 'local')
}

export function readLocalComposerDrafts(userId: string): Draft[] {
  const result: Draft[] = []
  for (const key of ClientStorage.getKeys('local')) {
    if (!key.startsWith(prefix)) continue
    const value = ClientStorage.get<Partial<StoredComposerDraft>>(key, { storageMechanism: 'local', logError: false })
    if (
      !value ||
      (value.type !== 'comment' && value.type !== 'project_update' && value.type !== 'initiative_update') ||
      typeof value.resourceId !== 'string' ||
      typeof value.body !== 'string' ||
      !value.body.trim()
    ) {
      continue
    }
    const updatedAt = typeof value.updatedAt === 'string' ? value.updatedAt : new Date().toISOString()
    result.push({
      id: `local:${value.type}:${value.resourceId}`,
      userId,
      type: value.type,
      resourceId: value.resourceId,
      title: typeof value.title === 'string' ? value.title : '',
      body: value.body,
      metadata: { ...(value.metadata ?? {}), ...(typeof value.id === 'string' ? { remoteId: value.id } : {}) },
      createdAt: updatedAt,
      updatedAt,
    })
  }
  return result
}
