import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { ProjectIssueFilterMenu, ProjectIssues } from './project-issues'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

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

it('reuses the issue explorer filter fields and swaps project fields for milestone', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const data = makeBootstrap()
  render(<I18nProvider><ProjectIssueFilterMenu issueData={data} issues={data.issues} filters={[]} onChange={onChange}/></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Add filter' }))
  expect(screen.getByRole('option', { name: 'Creator' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Project milestone' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Cycle' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Customers' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Dates' })).toBeInTheDocument()
  fireEvent.mouseMove(screen.getByRole('option', { name: 'Customers' }))
  expect(await screen.findByRole('option', { name: 'Customer name' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Customer status' })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'Project' })).not.toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'Project properties' })).not.toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'Initiative' })).not.toBeInTheDocument()
  fireEvent.mouseMove(screen.getByRole('option', { name: 'Creator' }))
  await user.click(await screen.findByRole('option', { name: /^Viewer/ }))
  expect(onChange).toHaveBeenCalledWith(expect.arrayContaining([expect.objectContaining({ field: 'creator', value: 'user-1' })]))
})
