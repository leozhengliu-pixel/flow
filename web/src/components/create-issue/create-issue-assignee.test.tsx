import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, Issue, User } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueSuggestionPreview: vi.fn(), createDraft: vi.fn(), updateDraft: vi.fn(), deleteDraft: vi.fn(), listIssueRecords: vi.fn() }))
const layout = vi.hoisted(() => ({ phone: false }))
vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => layout.phone }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'create-issue-assignee-test' }))

import { Shell } from '@/components/settings/mention-field-shell'
import { stubEditorDom } from '@/components/settings/mention-field-test-kit'
import { BUILTIN_AGENT_AVATAR_URL } from '@/components/ui/agent-avatar-url'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import { CreateIssueDialog } from './create-issue-dialog'

const flow = { id: 'app_flow', name: 'Flow', displayName: 'Flow', avatarUrl: BUILTIN_AGENT_AVATAR_URL, email: '', active: true, app: true, builtinAgent: true, appScopes: ['read', 'app:assignable'], appTeamIds: ['team-1'] } as User
const settle = async () => {
  await waitFor(() => expect(screen.getByLabelText('Issue title')).toHaveFocus())
  await act(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

beforeEach(() => {
  layout.phone = false
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  for (const mock of Object.values(api)) mock.mockReset()
  api.fetchIssueSuggestionPreview.mockResolvedValue({ suggestions: [] })
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  stubEditorDom()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('new issue: assignee picker', () => {
  it('groups phone candidates from the viewer, selected assignee and available agents, and filters real people', async () => {
    layout.phone = true
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const helper = { ...flow, id: 'app_helper', displayName: 'Helper', builtinAgent: false }
    const data = makeBootstrap({ users: [viewer, teammate, flow, helper], teamSettings: {}, cycleSettings: {}, teamMembers: [], issueTemplates: [] } as unknown as Partial<BootstrapData>)
    render(<Shell data={data}><CreateIssueDialog open data={data} initialTeamId="team-1" initialContext={{ assigneeId: viewer.id }} onOpenChange={vi.fn()} onCreate={vi.fn()}/></Shell>)
    await settle()
    await user.click(screen.getByRole('combobox', { name: /Assignee/ }))
    const list = await screen.findByRole('listbox', { name: 'Assignee' })
    expect(within(list).getAllByRole('option').map(row => row.querySelector('.property-command-label')?.textContent)).toEqual(['No assignee', viewer.displayName, 'Flow', 'Helper', teammate.displayName])
    expect(within(list).getByText('Other agents')).toBeInTheDocument()
    expect(within(list).getByText('Team members')).toBeInTheDocument()
    expect(within(list).getByRole('option', { name: new RegExp(viewer.displayName) })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText('Assigned users')).toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: 'Assign to…' }), 'Helper')
    expect(within(list).getAllByRole('option')).toHaveLength(1)
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('listbox', { name: 'Assignee' })).not.toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'New issue' })).toBeVisible()
  })
  it('lists No assignee, people, then agents with number keys and 16px avatars, with no Agents header, pill or invite row', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const data = makeBootstrap({ users: [viewer, teammate, flow], teamSettings: {}, cycleSettings: {}, teamMembers: [], issueTemplates: [] } as unknown as Partial<BootstrapData>)
    const onCreate = vi.fn().mockResolvedValue(makeIssue() as Issue)
    render(<Shell data={data}><CreateIssueDialog open data={data} initialTeamId="team-1" onOpenChange={vi.fn()} onCreate={onCreate}/></Shell>)
    await settle()
    await user.click(screen.getByRole('combobox', { name: /Assignee/ }))
    const list = await screen.findByRole('listbox', { name: 'Assignee' })
    const rows = within(list).getAllByRole('option')
    expect(rows.map(row => row.querySelector('.property-command-label')?.textContent)).toEqual(['No assignee', viewer.displayName, teammate.displayName, 'Flow'])
    expect(rows.map(row => row.querySelector('kbd')?.textContent)).toEqual(['0', '1', '2', '3'])
    expect(screen.getByRole('textbox', { name: 'Assign to…' })).toHaveAttribute('placeholder', 'Assign to…')
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(screen.queryByText('New user')).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Invite and assign/ })).not.toBeInTheDocument()
    expect(list.querySelector('[data-agent-badge]')).toBeNull()
    expect(rows[1].querySelector('.core-person-picker-avatar')).not.toBeNull()
    expect(rows[3].querySelector('[data-agent-avatar]')).not.toBeNull()
  })
})
