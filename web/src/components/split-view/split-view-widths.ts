/** LS-0560 — shared SplitView list-width preference keys (Linear SplitView). */

export const SPLIT_VIEW_LIST_WIDTH_KEYS = {
  inbox: 'flow.split-view.inbox.list-width',
  reviews: 'flow.split-view.reviews.list-width',
  search: 'flow.split-view.search.list-width',
  triage: 'flow.split-view.triage.list-width',
  agents: 'flow.split-view.agents.list-width',
  issueView: 'flow.split-view.issue-view.list-width',
} as const

export type SplitViewSurface = keyof typeof SPLIT_VIEW_LIST_WIDTH_KEYS

/** Legacy inbox key — kept so existing Inbox preferences continue to apply. */
export const LEGACY_INBOX_LIST_WIDTH_KEY = 'flow.inbox.list-width'

export const SPLIT_VIEW_DEFAULT_LIST_WIDTH: Record<SplitViewSurface, number> = {
  inbox: 300,
  reviews: 360,
  search: 360,
  triage: 320,
  agents: 340,
  issueView: 360,
}

export const SPLIT_VIEW_MIN_LIST_WIDTH = 300
export const SPLIT_VIEW_MIN_DETAIL_WIDTH = 608

export function splitViewListWidthKey(surface: SplitViewSurface) {
  return SPLIT_VIEW_LIST_WIDTH_KEYS[surface]
}

export function readSplitViewListWidth(surface: SplitViewSurface, fallback?: number) {
  const defaults = fallback ?? SPLIT_VIEW_DEFAULT_LIST_WIDTH[surface]
  try {
    const keys =
      surface === 'inbox'
        ? [SPLIT_VIEW_LIST_WIDTH_KEYS.inbox, LEGACY_INBOX_LIST_WIDTH_KEY]
        : [SPLIT_VIEW_LIST_WIDTH_KEYS[surface]]
    for (const key of keys) {
      const raw = globalThis.localStorage?.getItem(key)
      if (raw == null) continue
      const width = Number(raw)
      if (Number.isFinite(width) && width > 0) return width
    }
  } catch {
    /* Preferences are best-effort in private browsing. */
  }
  return defaults
}

export function persistSplitViewListWidth(surface: SplitViewSurface, width: number) {
  const value = String(Math.round(width))
  try {
    globalThis.localStorage?.setItem(SPLIT_VIEW_LIST_WIDTH_KEYS[surface], value)
    if (surface === 'inbox') {
      globalThis.localStorage?.setItem(LEGACY_INBOX_LIST_WIDTH_KEY, value)
    }
  } catch {
    /* Preferences are best-effort in private browsing. */
  }
}
