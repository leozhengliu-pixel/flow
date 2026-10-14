import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { OPEN_FLOATING_AGENT_EVENT } from '@/components/agent/agent-floating-request'
import { CREATE_ISSUE_INLINE_EVENT, type InlineIssueRequest } from '@/components/editor/selection-issue'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { Issue } from '@/types/flow'
import { useDescriptionSelectionActions } from './description-selection-actions'

vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe('issue description selection actions', () => {
  it('makes a sub-issue in place (no New issue dialog) and asks the floating chat with the issue as context', () => {
    const data = makeBootstrap()
    const issue = { ...data.issues[0], project: { id: 'project-2', name: 'Beta' } } as Issue
    const wrapper = ({ children }: { children: ReactNode }) => <I18nProvider>{children}</I18nProvider>
    const { result } = renderHook(() => useDescriptionSelectionActions(issue, data, vi.fn()), { wrapper })
    const creates: InlineIssueRequest['input'][] = []
    const chats: unknown[] = []
    const onCreate = (event: Event) => {
      const detail = (event as CustomEvent<InlineIssueRequest>).detail
      creates.push(detail.input)
      detail.result = Promise.resolve({ ...issue, id: 'issue-new', title: detail.input.title } as Issue)
    }
    const onChat = (event: Event) => chats.push((event as CustomEvent).detail)
    window.addEventListener(CREATE_ISSUE_INLINE_EVENT, onCreate)
    window.addEventListener(OPEN_FLOATING_AGENT_EVENT, onChat)
    result.current.actions.onCreateIssue?.({ text: 'Title line\nBody line', from: 1, to: 20 })
    result.current.actions.onAskAgent?.({ text: 'Body line', from: 12, to: 21 })
    window.removeEventListener(CREATE_ISSUE_INLINE_EVENT, onCreate)
    window.removeEventListener(OPEN_FLOATING_AGENT_EVENT, onChat)
    expect(creates).toEqual([expect.objectContaining({ title: 'Title line', description: 'Body line', teamId: issue.team.id, projectId: 'project-2', parentId: issue.id })])
    expect(chats).toEqual([{ quote: 'Body line', issueId: issue.id }])
  })
})
