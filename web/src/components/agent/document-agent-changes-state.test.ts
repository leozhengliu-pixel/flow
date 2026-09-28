import { beforeEach, describe, expect, it } from 'vitest'
import { documentAgentChangesState } from './document-agent-changes-state'

describe('DocumentAgentChangesState', () => {
  beforeEach(() => {
    documentAgentChangesState.reset()
  })

  it('tracks pending → active → cleared availability', () => {
    expect(documentAgentChangesState.availabilityFor('doc-1')).toBe('cleared')
    documentAgentChangesState.setPending('doc-1')
    expect(documentAgentChangesState.availabilityFor('doc-1')).toBe('pending')
    documentAgentChangesState.setActive('doc-1', 'iteration')
    expect(documentAgentChangesState.availabilityFor('doc-1')).toBe('active')
    expect(documentAgentChangesState.visibleFor('doc-1')).toBe(true)
    documentAgentChangesState.setCleared('doc-1')
    expect(documentAgentChangesState.availabilityFor('doc-1')).toBe('cleared')
    expect(documentAgentChangesState.visibleFor('doc-1')).toBe(false)
  })

  it('tracks checkpoint operation pending ids', () => {
    documentAgentChangesState.setCheckpointOperationPending('content-1')
    expect(documentAgentChangesState.hasPendingCheckpointOperation('content-1')).toBe(true)
    documentAgentChangesState.clearCheckpointOperationPending('content-1')
    expect(documentAgentChangesState.hasPendingCheckpointOperation('content-1')).toBe(false)
  })

  it('syncs editor highlight visibility without clearing pending', () => {
    documentAgentChangesState.setPending('doc-2')
    documentAgentChangesState.syncEditorHighlightVisible('doc-2', false)
    expect(documentAgentChangesState.availabilityFor('doc-2')).toBe('pending')
    documentAgentChangesState.setActive('doc-2', 'checkpoint')
    documentAgentChangesState.syncEditorHighlightVisible('doc-2', false)
    expect(documentAgentChangesState.availabilityFor('doc-2')).toBe('cleared')
  })
})
