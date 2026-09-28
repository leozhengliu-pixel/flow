/** Shared localStorage draft helpers for page + entity agent composers (LS-0253 drafts). */

export type AgentComposeDraft = {
  input: string
  skillIds: string[]
  updatedAt?: string
}

export function agentDraftStorageKey(scope: string) {
  return `flow:agent-draft:${scope}`
}

export function conversationDraftKeyFor(entityId: string, conversationId?: string) {
  return conversationId ? `${entityId}:${conversationId}` : `${entityId}:default`
}

export function readAgentDraft(key: string): AgentComposeDraft | null {
  try {
    const value = JSON.parse(localStorage.getItem(key) ?? 'null') as { input?: unknown; skillIds?: unknown; updatedAt?: unknown } | null
    if (!value || typeof value.input !== 'string') return null
    return {
      input: value.input,
      skillIds: Array.isArray(value.skillIds) ? value.skillIds.filter((id): id is string => typeof id === 'string') : [],
      updatedAt: typeof value.updatedAt === 'string' ? value.updatedAt : undefined,
    }
  } catch {
    return null
  }
}

export function writeAgentDraft(key: string, draft: { input: string; skillIds?: string[] }) {
  try {
    localStorage.setItem(key, JSON.stringify({ input: draft.input, skillIds: draft.skillIds ?? [], updatedAt: new Date().toISOString() }))
  } catch {
    /* Draft persistence is best-effort in private browsing. */
  }
}

export function clearAgentDraft(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    /* Draft cleanup is best-effort in private browsing. */
  }
}
