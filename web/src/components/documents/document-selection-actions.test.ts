import { afterEach, describe, expect, it, vi } from 'vitest'
import { CREATE_ISSUE_FROM_SELECTION } from '@/components/detail/description-selection-actions'
import { documentSelectionActions } from './document-selection-actions'

const t = (value: string) => value
const document = { id: 'doc-1', slugId: 'plan-abc123', title: 'Plan', teamIds: ['team-2'], projectIds: ['project-9'] }
const data = { workspace: { urlKey: 'acme' }, teams: [{ id: 'team-1' }] } as never

afterEach(() => vi.restoreAllMocks())

describe('document selection actions', () => {
  it('creates an issue from the selection with a link back to the document', () => {
    const listener = vi.fn()
    window.addEventListener(CREATE_ISSUE_FROM_SELECTION, listener)
    documentSelectionActions({ data, document, t, onAskAgent: vi.fn() }).onCreateIssue?.({ text: 'Ship the editor', from: 1, to: 16 })
    window.removeEventListener(CREATE_ISSUE_FROM_SELECTION, listener)
    const detail = (listener.mock.calls[0][0] as CustomEvent).detail as { text: string; teamId: string; projectId: string }
    expect(detail.text).toContain('Ship the editor')
    expect(detail.text).toMatch(/From https?:\/\/[^\s]+\/acme\/document\/plan-abc123/)
    expect(detail).toMatchObject({ teamId: 'team-2', projectId: 'project-9' })
  })

  it('asks the document agent with the selection quoted as context and falls back to the first team', () => {
    const onAskAgent = vi.fn()
    documentSelectionActions({ data, document: { ...document, teamIds: [], projectIds: [] }, t, onAskAgent }).onAskAgent?.({ text: 'line one\nline two', from: 1, to: 18 })
    expect(onAskAgent).toHaveBeenCalledWith('About this selection from the document:\n\n> line one\n> line two\n\n')
    const listener = vi.fn()
    window.addEventListener(CREATE_ISSUE_FROM_SELECTION, listener)
    documentSelectionActions({ data, document: { ...document, teamIds: [] }, t, onAskAgent }).onCreateIssue?.({ text: 'x', from: 1, to: 2 })
    window.removeEventListener(CREATE_ISSUE_FROM_SELECTION, listener)
    expect(((listener.mock.calls[0][0] as CustomEvent).detail as { teamId: string }).teamId).toBe('team-1')
  })
})
