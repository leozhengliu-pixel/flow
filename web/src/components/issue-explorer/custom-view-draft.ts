/**
 * LS-0224 EditCustomViewHeader — ClientStorage draft persist + discard.
 */

export type CustomViewDraft = {
  name?: string
  description?: string
  icon?: string
  color?: string
  filters?: unknown
  display?: unknown
  insights?: unknown
  updatedAt: string
}

export function customViewDraftKey(orgKey: string, viewId: string | 'new'): string {
  return `custom_view_draft_${orgKey}_${viewId}`
}

export function readCustomViewDraft(orgKey: string, viewId: string | 'new'): CustomViewDraft | undefined {
  if (typeof localStorage === 'undefined') return undefined
  try {
    const raw = localStorage.getItem(customViewDraftKey(orgKey, viewId))
    if (!raw) return undefined
    const parsed = JSON.parse(raw) as CustomViewDraft
    if (!parsed || typeof parsed !== 'object') return undefined
    return parsed
  } catch {
    return undefined
  }
}

export function writeCustomViewDraft(orgKey: string, viewId: string | 'new', draft: Omit<CustomViewDraft, 'updatedAt'> & { updatedAt?: string }) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(
      customViewDraftKey(orgKey, viewId),
      JSON.stringify({ ...draft, updatedAt: draft.updatedAt ?? new Date().toISOString() }),
    )
  } catch {
    /* best-effort */
  }
}

export function discardCustomViewDraft(orgKey: string, viewId: string | 'new') {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(customViewDraftKey(orgKey, viewId))
  } catch {
    /* best-effort */
  }
}
