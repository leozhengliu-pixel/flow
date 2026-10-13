import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentMessagePart } from '@/types/flow'
import { AgentQuestionPrompt } from './agent-approval-question'
import { agentApprovalChoices, agentApprovalMarkdown } from './agent-approval-model'

const options = [{ label: 'Approve', value: 'approve' }, { label: 'Reject', value: 'reject' }]

function approvalPart(overrides: Partial<AgentMessagePart> = {}): AgentMessagePart {
  return {
    id: 'agent_message_1_approval_0',
    type: 'question',
    status: 'pending',
    text: 'Delete these 2 items? They go to the trash and can be restored.\n- {"id":"raw"}',
    question: {
      options,
      allowFreeText: true,
      approval: {
        kind: 'delete',
        targets: [
          { kind: 'issue', id: 'issue_1', label: 'DEV-50', title: 'QA fix – one', href: '/dev/issue/DEV-50', subIssues: 2 },
          { kind: 'project', id: 'project_1', label: 'QA fix – Export', affected: { issues: 3, milestones: 0 } },
        ],
      },
    },
    ...overrides,
  }
}

function renderPrompt(part: AgentMessagePart, locale: 'en-US' | 'zh-CN', onSelect = vi.fn()) {
  localStorage.setItem('flow:locale', locale)
  render(<I18nProvider><AgentQuestionPrompt className="answer" disabled={part.status !== 'pending'} onSelect={onSelect} part={part}/></I18nProvider>)
  return onSelect
}

afterEach(() => localStorage.removeItem('flow:locale'))

describe('agent approvals', () => {
  it('previews a bulk delete in the viewer\'s language with Approve / Reject pills, never raw JSON', () => {
    const onSelect = renderPrompt(approvalPart(), 'zh-CN')
    expect(document.body.textContent).toContain('删除这 2 项？它们会移到回收站，可以恢复。')
    expect(document.body.textContent).toContain('QA fix – one')
    expect(document.body.textContent).toContain('还会影响 2 个子事项')
    expect(document.body.textContent).toContain('3 个事项')
    expect(document.body.textContent).not.toContain('{"id"')
    fireEvent.click(screen.getByRole('button', { name: '批准' }))
    expect(onSelect).toHaveBeenCalledWith({ label: '批准', value: 'approve' })
    expect(screen.getByRole('button', { name: '驳回' })).toBeInTheDocument()
  })

  it('quotes the full update an agent wrote before it is posted', () => {
    const markdown = agentApprovalMarkdown({ kind: 'publish', body: 'Export is on track.\nNext: beta.', health: 'onTrack', parent: { kind: 'project', id: 'p', label: 'Export revamp', href: '/dev/project/export/overview' } }, source => source)
    expect(markdown).toBe('Post this update to [Export revamp](/dev/project/export/overview)?\n\n> Export is on track.\n> Next: beta.\n\nHealth: On track')
    expect(agentApprovalChoices(options, source => source === 'Approve' ? '批准' : source)).toEqual([{ label: '批准', value: 'approve' }, { label: 'Reject', value: 'reject' }])
  })

  it('collapses to the decision once answered', () => {
    renderPrompt(approvalPart({ status: 'completed', question: { ...approvalPart().question!, selected: 'reject' } }), 'en-US')
    expect(screen.getByRole('button', { name: 'Reject' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
  })

  it('still shows an ordinary ask_user question as written', () => {
    renderPrompt({ id: 'q', type: 'question', status: 'pending', text: 'Which priority?', question: { options: [{ label: 'High', value: 'High' }, { label: 'Low', value: 'Low' }], allowFreeText: true } }, 'en-US')
    expect(document.body.textContent).toContain('Which priority?')
    expect(screen.getByRole('button', { name: 'High' })).toBeInTheDocument()
  })
})
