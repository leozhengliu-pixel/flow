import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { TooltipProvider } from '@/components/ui/tooltip'
import { backlog, makeIssue, started } from '@/test/fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'

const api = vi.hoisted(() => ({
  updateIssue: vi.fn(),
  createComment: vi.fn(),
  createRelation: vi.fn(),
  listIssueRecords: vi.fn(),
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  realtimeClientId: () => 'triage-dialog-test',
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { TriageHeaderActions } from './triage-actions'

const canceled = { id: 'state-canceled', name: 'Canceled', color: '#999', type: 'canceled', position: 3 }

function setup() {
  const issue = makeIssue({ state: backlog, triagedAt: undefined })
  const data = mentionFixture({ issues: [issue], states: [backlog, started, canceled] as never, teamSettings: { 'team-1': { triageEnabled: true } } as never })
  return { issue, data }
}

beforeEach(() => {
  stubEditorEnvironment()
  resetAgentRecordCache()
  vi.clearAllMocks()
  api.updateIssue.mockImplementation(async (id: string, input: object) => makeIssue({ id, ...input, triagedAt: '2026-09-28T00:00:00.000Z' }))
  api.createComment.mockResolvedValue({})
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
})
afterEach(() => { vi.unstubAllGlobals() })

// The dialogs are Radix modal dialogs: the page outside them is inert, so the "@" option is picked with the keyboard.
describe.each([
  ['Accept issue from triage', 'Accept issue…', 'Comment for accepting issue', 'Accept'],
  ['Decline triage issue', 'Decline issue…', 'Comment for declining issue', 'Decline'],
])('%s dialog comment', (trigger, title, label, submit) => {
  it('turns "@" and pasted Flow URLs into chips and posts them as markdown links in the issue comment', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup()
    render(<MentionShell data={data}><TooltipProvider><TriageHeaderActions issue={issue} data={data} onDone={vi.fn()}/></TooltipProvider></MentionShell>)
    await user.click(screen.getByRole('button', { name: trigger }))
    const dialog = await screen.findByRole('dialog', { name: title })
    const box = within(dialog).getByRole('textbox', { name: label })
    await user.click(box)
    await user.keyboard('Tracked in @Launch')
    await screen.findByRole('option', { name: /Launch plan/ })
    await user.keyboard('{Enter}')
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(within(dialog).getByRole('textbox', { name: label }).querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    await user.click(within(dialog).getByRole('button', { name: submit }))
    await waitFor(() => expect(api.createComment).toHaveBeenCalledTimes(1))
    const saved = api.createComment.mock.calls[0][1] as string
    expect(saved).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(saved).toContain('[Project one](/workspace/project/project-one/overview)')
  })
})

describe('Accept dialog status', () => {
  it('accepts a sub-team issue into the parent workflow it inherits and offers only those statuses', async () => {
    const user = userEvent.setup()
    const intake = { id: 'parent-intake', name: 'Intake', color: '#999', type: 'backlog', position: 0, teamId: 'team-parent' }
    const doing = { id: 'parent-doing', name: 'Doing', color: '#999', type: 'started', position: 1, teamId: 'team-parent' }
    const shipped = { id: 'parent-shipped', name: 'Shipped', color: '#999', type: 'completed', position: 2, teamId: 'team-parent' }
    const issue = makeIssue({ state: intake as never, triagedAt: undefined })
    const data = mentionFixture({
      issues: [issue], states: [backlog, started, canceled, intake, doing, shipped] as never,
      teamSettings: { 'team-1': { triageEnabled: true, inheritWorkflowStatuses: true, parentTeamId: 'team-parent' }, 'team-parent': {} } as never,
    })
    render(<MentionShell data={data}><TooltipProvider><TriageHeaderActions issue={issue} data={data} onDone={vi.fn()}/></TooltipProvider></MentionShell>)
    await user.click(screen.getByRole('button', { name: 'Accept issue from triage' }))
    const dialog = await screen.findByRole('dialog', { name: 'Accept issue…' })
    const picker = within(dialog).getByLabelText('Change status. Current status is Doing')
    await user.click(picker)
    const names = (await screen.findAllByRole('option', { hidden: true })).map(option => option.textContent ?? '')
    expect(names.some(name => name.includes('Doing'))).toBe(true)
    expect(names.some(name => name.includes('Shipped'))).toBe(true)
    expect(names.some(name => name.includes('In progress'))).toBe(false)
    await user.keyboard('{Escape}')
    await user.click(within(dialog).getByRole('button', { name: 'Accept' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith(issue.id, expect.objectContaining({ stateId: 'parent-doing' })))
  })
})
