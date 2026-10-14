import { afterEach, describe, expect, it, vi } from 'vitest'
import { CREATE_ISSUE_INLINE_EVENT, type InlineIssueRequest } from '@/components/editor/selection-issue'
import { makeBootstrap } from '@/test/fixtures'
import type { Issue } from '@/types/flow'
import { documentSelectionActions } from './document-selection-actions'

vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const t = (value: string) => value
const document = { teamIds: ['team-2'], projectIds: ['project-9'] }
const data = makeBootstrap()

/** Stands in for the app: answers the in-place create request. */
function answerCreates() {
  const requests: InlineIssueRequest['input'][] = []
  const listener = (event: Event) => {
    const detail = (event as CustomEvent<InlineIssueRequest>).detail
    requests.push(detail.input)
    detail.result = Promise.resolve({ ...data.issues[0], id: 'issue-new', title: detail.input.title } as Issue)
  }
  window.addEventListener(CREATE_ISSUE_INLINE_EVENT, listener)
  return { requests, stop: () => window.removeEventListener(CREATE_ISSUE_INLINE_EVENT, listener) }
}

afterEach(() => vi.restoreAllMocks())

describe('document selection actions', () => {
  it('creates the issue right away in the document\'s team and project, first line as the title', () => {
    const app = answerCreates()
    documentSelectionActions({ data, document, t, onAskAgent: vi.fn() }).onCreateIssue?.({ text: 'Ship the editor\nwith tables', from: 1, to: 28 })
    app.stop()
    expect(app.requests).toEqual([expect.objectContaining({ title: 'Ship the editor', description: 'with tables', teamId: 'team-2', projectId: 'project-9', parentId: undefined })])
  })

  it('hands the selection to the agent as-is (a chip, not prefilled prose) and falls back to the first team', () => {
    const onAskAgent = vi.fn()
    const actions = documentSelectionActions({ data, document: { teamIds: [], projectIds: [] }, t, onAskAgent })
    actions.onAskAgent?.({ text: 'line one\nline two', from: 1, to: 18 })
    expect(onAskAgent).toHaveBeenCalledWith({ text: 'line one\nline two', from: 1, to: 18 }, undefined)
    actions.onAskAgent?.({ text: '   ', from: 1, to: 4 })
    expect(onAskAgent).toHaveBeenCalledTimes(1)
    const app = answerCreates()
    actions.onCreateIssue?.({ text: 'x', from: 1, to: 2 })
    app.stop()
    expect(app.requests[0]).toMatchObject({ teamId: data.teams[0].id, projectId: undefined })
  })
})
