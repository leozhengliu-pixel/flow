import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { IssueAssigneeSlot } from './issue-assignee-slot'

const member = { id: 'usr_1', name: 'Skyler Anderson' }
const agent = { id: 'app_1', name: 'Flow QA agent' }
const options = [
  { id: '', label: 'No assignee', kind: 'assignee' as const },
  { id: 'usr_1', label: 'Skyler Anderson', kind: 'assignee' as const },
  { id: 'app_1', label: 'Flow QA agent', kind: 'assignee' as const, agent: true },
]

function slot(issue: { assignee?: typeof member; delegate?: typeof agent }) {
  return render(<I18nProvider><IssueAssigneeSlot issue={issue} options={options} onChange={vi.fn()} avatarClassName="avatar" emptyClassName="empty"/></I18nProvider>).container
}

describe('IssueAssigneeSlot', () => {
  it('stacks the delegated agent behind the assignee', () => {
    const cluster = slot({ assignee: member, delegate: agent }).querySelector('.issue-assignee-cluster')
    expect(cluster?.getAttribute('data-delegated')).toBe('')
    expect(cluster?.children[0].className).toContain('issue-assignee-cluster__agent')
    expect(cluster?.children[1].className).toContain('issue-assignee-cluster__member')
  })

  it('shows the empty-assignee glyph beside an agent with no human, and on rows with neither', () => {
    const delegated = slot({ delegate: agent })
    expect(delegated.querySelector('.issue-assignee-cluster__empty')).not.toBeNull()
    expect(delegated.querySelector('.issue-assignee-cluster__agent')).not.toBeNull()
    const bare = slot({})
    expect(bare.querySelector('.issue-assignee-cluster[data-delegated]')).toBeNull()
    expect(bare.querySelector('.issue-assignee-cluster__empty')).not.toBeNull()
  })

  it('labels the trigger by who is assigned and delegated', () => {
    expect(slot({ assignee: member }).querySelector('button')?.getAttribute('aria-label')).toBe('Assign to. Current assignee is Skyler Anderson')
    expect(slot({ delegate: agent }).querySelector('button')?.getAttribute('aria-label')).toBe('Change assignee. Flow QA agent is delegated')
    expect(slot({}).querySelector('button')?.getAttribute('aria-label')).toBe('Assign issue')
  })
})
