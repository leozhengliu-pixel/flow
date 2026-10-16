import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, Issue, IssueTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueSuggestionPreview: vi.fn(), createDraft: vi.fn(), updateDraft: vi.fn(), deleteDraft: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'create-issue-templates-test' }))

import { Shell } from '@/components/settings/mention-field-shell'
import { stubEditorDom } from '@/components/settings/mention-field-test-kit'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { CreateIssueDialog } from './create-issue-dialog'

const template = (over: Partial<IssueTemplate>) => ({ id: 'tpl', name: 'Template', priority: 0, labelIds: [], creator: viewer, createdAt: '', updatedAt: '', templateType: 'standard', ...over }) as IssueTemplate
const bug = template({ id: 'bug', name: 'Bug report', title: 'Bug: ', priority: 2, scope: 'workspace' })
const teamForm = template({ id: 'form', name: 'Support request', scope: undefined, teamId: 'team-1', templateType: 'customForm', formFields: [
  { id: 'f-steps', type: 'text', label: 'Repro steps', required: true },
  { id: 'f-help', type: 'instructions', label: 'Instructions', description: 'Tell us what you saw.', required: false },
  { id: 'f-platform', type: 'dropdown', label: 'Platform', required: false, options: ['Web', 'iOS'] },
  { id: 'f-title', type: 'title', label: 'Title', required: true },
] })

/** Wait for editor initialization and drain the composer's queued focus frames before opening a menu. */
const settle = async () => {
  await waitFor(() => expect(screen.getByLabelText('Issue title')).toHaveFocus())
  await act(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

function mount(templates: IssueTemplate[]) {
  const data = makeBootstrap({ issueTemplates: templates, teamSettings: {}, cycleSettings: {}, teamMembers: [] } as unknown as Partial<BootstrapData>)
  const onCreate = vi.fn().mockResolvedValue(makeIssue() as Issue)
  render(<Shell data={data}><CreateIssueDialog open data={data} initialTeamId="team-1" onOpenChange={vi.fn()} onCreate={onCreate}/></Shell>)
  return { onCreate }
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  for (const mock of Object.values(api)) mock.mockReset()
  api.fetchIssueSuggestionPreview.mockResolvedValue({ suggestions: [] })
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  stubEditorDom()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('new issue: create from a template', () => {
  it('shows a Template chip next to the team and applies the chosen template', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount([bug])
    await settle()
    await user.click(screen.getByRole('combobox', { name: 'Choose a template' }))
    await user.click(await screen.findByRole('option', { name: /Bug report/, hidden: true }))
    expect(screen.getByRole('combobox', { name: 'Choose a template' })).toHaveTextContent('Bug report')
    await waitFor(() => expect(screen.getByLabelText('Issue title')).toHaveTextContent('Bug:'))
    await user.click(screen.getByLabelText('Issue title'))
    await user.keyboard('crash on save')
    await user.click(screen.getByRole('button', { name: 'Create issue' }))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ templateId: 'bug', priority: 2, teamId: 'team-1' })
    expect(onCreate.mock.calls[0][0].title).toContain('crash on save')
  })

  it('hides the chip when no template applies to the team', () => {
    mount([template({ id: 'other', name: 'Other team', scope: undefined, teamId: 'team-2' })])
    expect(screen.queryByRole('combobox', { name: 'Choose a template' })).not.toBeInTheDocument()
  })

  it('fills a form template: required fields block creation, answers become the description', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount([teamForm])
    await settle()
    await user.click(screen.getByRole('combobox', { name: 'Choose a template' }))
    await user.click(await screen.findByRole('option', { name: /Support request/, hidden: true }))
    expect(await screen.findByLabelText(/Repro steps/)).toBeInTheDocument()
    expect(screen.getByText('Tell us what you saw.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create issue' }))
    expect(onCreate).not.toHaveBeenCalled()
    expect(screen.getByText('Fill in the required fields')).toBeInTheDocument()
    await user.type(screen.getByLabelText(/Repro steps/), 'Open the page')
    await user.selectOptions(screen.getByLabelText('Platform'), 'iOS')
    await user.type(screen.getByLabelText(/^Title/), 'Crash')
    await user.click(screen.getByRole('button', { name: 'Create issue' }))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const input = onCreate.mock.calls[0][0]
    expect(input.templateId).toBe('form')
    expect(input.description).toBe('**Repro steps**\nOpen the page\n\nTell us what you saw.\n\n**Platform**\niOS')
  })
})
