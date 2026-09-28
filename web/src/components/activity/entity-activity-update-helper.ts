/**
 * LS-0251 EntityActivityUpdateHelper
 * Selection reconcile + most-recent helpers for update → panel deep links.
 */

export type ActivitySelectionSource = 'route' | 'user' | 'panel'

export type ActivityUpdateSelection = {
  id: string
  source: ActivitySelectionSource
  /** When true, focus returns to the feed after closing the selection. */
  restoreFocus?: boolean
}

export type ActivityUpdateSelectionState = {
  boundaryKey: string
  selection?: ActivityUpdateSelection
}

export type ActivityUpdateLike = {
  id: string
  createdAt: string | Date
}

export type ReconcileSelectionInput = {
  boundaryKey: string
  targetedUpdateId?: string
  requestsDifferentActivityTarget?: boolean
}

/**
 * When the entity boundary changes, honor a routed targetedUpdateId; otherwise
 * keep a non-route selection or clear a stale route selection. Always drop
 * selections whose id is no longer present in `updates`.
 */
export function reconcileSelection(
  current: ActivityUpdateSelectionState,
  next: ReconcileSelectionInput,
  updates: ReadonlyArray<Pick<ActivityUpdateLike, 'id'>>,
): ActivityUpdateSelectionState {
  if (current.boundaryKey !== next.boundaryKey) {
    return {
      boundaryKey: next.boundaryKey,
      selection: next.requestsDifferentActivityTarget
        ? next.targetedUpdateId
          ? { id: next.targetedUpdateId, source: 'route', restoreFocus: false }
          : undefined
        : current.selection?.source === 'route'
          ? undefined
          : current.selection,
    }
  }

  const selectedId = current.selection?.id
  if (selectedId && !updates.some(update => update.id === selectedId)) {
    return { ...current, selection: undefined }
  }
  return current
}

/** Newest by createdAt; ties broken by id localeCompare (Linear parity). */
export function getMostRecent<T extends ActivityUpdateLike>(updates: ReadonlyArray<T>): T | undefined {
  let newest: T | undefined
  for (const update of updates) {
    if (!newest) {
      newest = update
      continue
    }
    const nextTime = toTime(update.createdAt)
    const currentTime = toTime(newest.createdAt)
    if (nextTime > currentTime || (nextTime === currentTime && update.id.localeCompare(newest.id) > 0)) {
      newest = update
    }
  }
  return newest
}

export function selectionFromRoute(targetedUpdateId: string | undefined, boundaryKey: string): ActivityUpdateSelectionState {
  return {
    boundaryKey,
    selection: targetedUpdateId
      ? { id: targetedUpdateId, source: 'route', restoreFocus: false }
      : undefined,
  }
}

function toTime(value: string | Date): number {
  if (value instanceof Date) return value.getTime()
  const time = Date.parse(value)
  return Number.isFinite(time) ? time : 0
}
