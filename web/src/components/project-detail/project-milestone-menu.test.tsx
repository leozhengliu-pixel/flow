import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'
import { ProjectDetailsSidebar } from './project-details-sidebar'

beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => vi.unstubAllGlobals())

function renderSidebar() {
  const data = makeBootstrap()
  const current = { ...project, id: 'project-menu', lead: undefined, memberIds: [], initiatives: [], milestones: [{ id: 'milestone-alpha', projectId: 'project-menu', name: 'Alpha', description: '', createdAt: '2026-09-27T00:00:00.000Z', updatedAt: '2026-09-27T00:00:00.000Z' }], createdAt: '2026-09-27T00:00:00.000Z', updatedAt: '2026-09-27T00:00:00.000Z' } as typeof project
  render(<I18nProvider><ProjectDetailsSidebar
    initiatives={[]} integrationConnections={[]} labelGroups={[]} labels={[]}
    onConvertMilestone={vi.fn()} onCreateMilestone={vi.fn()} onDeleteMilestone={vi.fn()} onMoveMilestone={vi.fn()}
    onOpenIssueFilter={vi.fn()} onOpenMilestoneIssues={vi.fn()} onReorderMilestones={vi.fn()} onTabChange={vi.fn()}
    onUpdate={vi.fn().mockResolvedValue(undefined)} onUpdateProject={vi.fn().mockResolvedValue(current)} onUpdateMilestone={vi.fn()}
    project={current} projectIssues={[]} projectRelations={[]} projects={[current]} projectStatuses={[current.status]} projectUpdates={[]}
    teams={data.teams} users={data.users} viewer={data.viewer}
  /></I18nProvider>)
}

it('matches Linear milestone actions with a filter input', async () => {
  const user = userEvent.setup()
  renderSidebar()
  await user.click(screen.getByRole('button', { name: 'Alpha actions' }))
  const menu = await screen.findByRole('menu')
  const filter = within(menu).getByRole('textbox', { name: 'Filter milestone actions' })
  expect(filter.closest('.project-action-menu__search')).not.toHaveClass('is-hidden')
  const labels = within(menu).getAllByRole('menuitem').map(item => item.textContent?.replace(/⌘ ⌫$/, ''))
  expect(labels).toEqual(['Edit…', 'Set target date…', 'Copy', 'Move milestone to', 'Convert to project', 'Delete'])
  expect(menu).not.toHaveTextContent('No date')
  expect(menu).not.toHaveTextContent('Edit target date')

  await user.type(filter, 'conv')
  expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Convert to project'])
  expect(within(menu).queryByRole('separator')).toBeNull()
  await user.clear(filter)
  await user.type(filter, 'zzz')
  expect(within(menu).queryAllByRole('menuitem')).toHaveLength(0)
  expect(within(menu).getByText('No results')).toBeVisible()
})
