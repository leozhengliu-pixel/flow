import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { ProjectIssues } from './project-issues'

it('offers unused workflow states and persists cycle changes from a project card', async () => {
  const user = userEvent.setup()
  const issue = makeIssue({ cycleId: 'cycle-1' })
  const data = makeBootstrap({ issues: [issue] })
  const onUpdateIssue = vi.fn().mockResolvedValue(undefined), onOpenIssue = vi.fn()
  const props = {
    ...data, project: { ...project, teamIds: [issue.team.id] }, projectIssues: [issue],
    workflowStates: [issue.state, { ...issue.state, id: 'unused-state', name: 'Unused status', position: 5 }],
    cycles: [{ id: 'cycle-1', name: 'Iteration 1', teamId: issue.team.id }],
    display: createIssueDisplayOptions({ layout: 'board', grouping: 'status', properties: new Set(['id', 'status', 'cycle']) }),
    filters: [], onUpdateIssue, onOpenIssue, onFiltersChange: vi.fn(),
  } as unknown as ComponentProps<typeof ProjectIssues>
  render(<I18nProvider><ProjectIssues {...props}/></I18nProvider>)
  await user.click(screen.getByRole('combobox', { name: /Change status/ }))
  await user.click(screen.getByRole('option', { name: /Unused status/ }))
  await waitFor(() => expect(onUpdateIssue).toHaveBeenCalledWith(issue.id, { stateId: 'unused-state' }))
  await user.click(screen.getByRole('combobox', { name: /Change cycle/ }))
  await user.click(screen.getByRole('option', { name: 'No cycle' }))
  await waitFor(() => expect(onUpdateIssue).toHaveBeenCalledWith(issue.id, { cycleId: '' }))
  expect(onOpenIssue).not.toHaveBeenCalled()
})
