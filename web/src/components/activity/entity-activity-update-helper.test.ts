import { describe, expect, it } from 'vitest'
import {
  getMostRecent,
  reconcileSelection,
  selectionFromRoute,
  type ActivityUpdateSelectionState,
} from './entity-activity-update-helper'

describe('LS-0251 EntityActivityUpdateHelper', () => {
  const updates = [
    { id: 'u-a', createdAt: '2026-09-20T10:00:00.000Z' },
    { id: 'u-b', createdAt: '2026-09-20T12:00:00.000Z' },
    { id: 'u-c', createdAt: '2026-09-20T12:00:00.000Z' },
  ]

  it('reconcileSelection applies routed targetedUpdateId on boundary change', () => {
    const current: ActivityUpdateSelectionState = {
      boundaryKey: 'project:1',
      selection: { id: 'old', source: 'user' },
    }
    const next = reconcileSelection(
      current,
      { boundaryKey: 'project:2', targetedUpdateId: 'u-b', requestsDifferentActivityTarget: true },
      updates,
    )
    expect(next).toEqual({
      boundaryKey: 'project:2',
      selection: { id: 'u-b', source: 'route', restoreFocus: false },
    })
  })

  it('reconcileSelection clears stale route selection when boundary changes without a new target', () => {
    const current: ActivityUpdateSelectionState = {
      boundaryKey: 'project:1',
      selection: { id: 'u-a', source: 'route' },
    }
    expect(
      reconcileSelection(current, { boundaryKey: 'project:2', requestsDifferentActivityTarget: false }, updates).selection,
    ).toBeUndefined()
  })

  it('reconcileSelection drops selection ids missing from updates', () => {
    const current: ActivityUpdateSelectionState = {
      boundaryKey: 'project:1',
      selection: { id: 'missing', source: 'user' },
    }
    expect(reconcileSelection(current, { boundaryKey: 'project:1' }, updates).selection).toBeUndefined()
  })

  it('getMostRecent prefers newest createdAt then id', () => {
    expect(getMostRecent(updates)?.id).toBe('u-c')
    expect(selectionFromRoute('u-a', 'initiative:1').selection?.source).toBe('route')
  })
})
