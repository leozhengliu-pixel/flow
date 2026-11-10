import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'
import { parseAppRoute, settingsPath } from '@/lib/app-routes'
import { TeamWorkflowSettings } from './team-workflow-settings'

beforeEach(() => { localStorage.clear(); localStorage.setItem('flow:locale', 'en-US') })

function data(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({ teamMembers: [], teamSettings: {}, cycleSettings: {}, documentTemplates: [], projectTemplates: [], ...overrides })
}

function mount(value = data()) {
  const onNavigate = vi.fn()
  const result = render(<MemoryRouter><I18nProvider><TeamWorkflowSettings data={value} team={value.teams[0]} section="overview" onNavigate={onNavigate} onReload={vi.fn()}/></I18nProvider></MemoryRouter>)
  return { ...result, onNavigate }
}

it('groups the same working destinations in the current hub order', async () => {
  const { container, onNavigate } = mount()
  expect([...container.querySelectorAll('.team-overview-page .settings-section > h3')].map(node => node.textContent)).toEqual(['Issues', 'Projects', 'Workflows', 'AI & agents', 'Danger zone'])
  const groups = [...container.querySelectorAll('[data-team-overview-group]')]
  expect(groups.map(node => node.getAttribute('data-team-overview-group'))).toEqual([
    'general-members-hierarchy', 'issue-labels-statuses-estimates-recurring-issues',
    'project-labels-project-statuses', 'templates-triage-cycles-workflow-notifications',
    'agents-agent-skills-agent-connectors-loops-ai-updates-ai-summaries',
  ])
  expect(within(groups[3] as HTMLElement).getByRole('link', { name: /^Templates/ })).toHaveTextContent('None')
  for (const id of ['estimates', 'hierarchy'] as const) {
    const link = container.querySelector(`[data-team-overview-row="${id}"]`) as HTMLAnchorElement
    expect(parseAppRoute(link.pathname)).toMatchObject({ kind: 'settings', teamSection: id })
    await userEvent.click(link)
    expect(onNavigate).toHaveBeenLastCalledWith(id)
  }
  expect(container.querySelector('[data-team-overview-row=loops]')).toHaveAttribute('href', '/workspace/team/TST/loops')
})

it('counts only this team’s templates and published loops using existing data', () => {
  const value = data({
    issueTemplates: [{ id: 'owned', teamId: 'team-1', name: 'Owned' }, { id: 'other', teamId: 'team-2' }, { id: 'workspace', scope: 'workspace' }] as BootstrapData['issueTemplates'],
    documentTemplates: [{ id: 'doc', teamId: 'team-1' }, { id: 'other-doc', teamId: 'team-2' }] as BootstrapData['documentTemplates'],
    loops: [{ id: 'published', teamId: 'team-1', status: 'published' }, { id: 'draft', teamId: 'team-1', status: 'draft' }, { id: 'other', teamId: 'team-2' }] as BootstrapData['loops'],
  })
  const { container, rerender } = mount(value)
  expect(container.querySelector('[data-team-overview-row=templates]')).toHaveTextContent('2 templates')
  expect(container.querySelector('[data-team-overview-row=loops]')).toHaveTextContent('1 loop')
  rerender(<MemoryRouter><I18nProvider><TeamWorkflowSettings data={{ ...value, issueTemplates: [] }} team={value.teams[0]} section="overview" onNavigate={vi.fn()} onReload={vi.fn()}/></I18nProvider></MemoryRouter>)
  expect(container.querySelector('[data-team-overview-row=templates]')).toHaveTextContent('1 template')
})

it.each(['owner', 'admin', 'member'] as const)('shows access management only to team managers (%s)', role => {
  const value = data({ viewerRole: role })
  const { container } = mount(value)
  expect(Boolean(container.querySelector('[data-team-overview-row=security]'))).toBe(role !== 'member')
})

it('keeps the new destinations routable and translated', () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  mount()
  expect(screen.getByText('体现团队结构并共享工作流和设置')).toBeVisible()
  for (const section of ['hierarchy', 'estimates'] as const) {
    expect(parseAppRoute(settingsPath('workspace', 'team', 'TST', section))).toMatchObject({ teamSection: section })
  }
})

it('shows the current value of triage and cycles, nothing for a team without loops, and lists access management with the first group', () => {
  const base = data({ viewerRole: 'admin' })
  const team = base.teams[0]
  const value = data({
    viewerRole: 'admin',
    teamSettings: { [team.id]: { teamId: team.id, triageEnabled: true } as BootstrapData['teamSettings'][string] },
    cycleSettings: { [team.id]: { enabled: true, durationWeeks: 2, cooldownWeeks: 2 } as BootstrapData['cycleSettings'][string] },
  })
  const { container } = mount(value)
  expect(container.querySelector('[data-team-overview-row=triage] .team-overview-summary')).toHaveTextContent('Enabled')
  expect(container.querySelector('[data-team-overview-row=cycles] .team-overview-summary')).toHaveTextContent('Every 2 weeks, 2 week cooldown')
  expect(container.querySelector('[data-team-overview-row=loops] .team-overview-summary')).toBeEmptyDOMElement()
  const first = container.querySelector('[data-team-overview-group=general-members-security-hierarchy]')
  expect(first?.querySelector('[data-team-overview-row=security]')).not.toBeNull()
  const rows = [...(first?.querySelectorAll('[data-team-overview-row]') ?? [])].map(node => node.getAttribute('data-team-overview-row'))
  expect(rows).toEqual(['general', 'members', 'security', 'hierarchy'])
})
