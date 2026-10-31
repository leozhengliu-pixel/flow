import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { WorkspaceTeamsSettings } from './workspace-teams-settings'
import { TeamWorkflowSettings } from './team-workflow-settings'
import { WorkspaceAnnouncements } from './workspace-announcements'

vi.mock('@/hooks/use-desktop-settings', () => ({ useDesktopSettings: () => true }))
const api = vi.hoisted(() => ({ updateWorkspaceSettings: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))

beforeEach(() => { localStorage.clear(); localStorage.setItem('flow:locale', 'en-US'); api.updateWorkspaceSettings.mockReset() })
const wrap = (content: React.ReactNode) => <MemoryRouter><I18nProvider>{content}</I18nProvider></MemoryRouter>

it('sorts every team column in both directions using the displayed data', async () => {
  const a = { id: 'a', name: 'Alpha', key: 'ALP', color: 'currentColor', createdAt: '2026-01-01' }
  const z = { id: 'z', name: 'Zulu', key: 'ZUL', color: 'currentColor', private: true, createdAt: '2026-02-01' }
  const data = makeBootstrap({ teams: [z, a], viewerRole: 'admin', teamSettings: { a: { description: 'Zebra' }, z: { description: 'Apple' } } as never, teamMembers: [{ teamId: 'a', userId: viewer.id }] as never, issues: [{ team: z }] as never })
  const { container } = render(wrap(<WorkspaceTeamsSettings data={data} onCreate={vi.fn()} onOpen={vi.fn()} onReload={vi.fn()}/>))
  const first = () => container.querySelector('.settings-teams-row strong')?.textContent
  expect(first()).toBe('Alpha')
  for (const [column, expected] of [['Description', 'Zulu'], ['Visibility', 'Zulu'], ['Members', 'Alpha'], ['Issues', 'Zulu'], ['Created', 'Alpha'], ['Name', 'Alpha']]) {
    await userEvent.click(screen.getByRole('button', { name: `Order by ${column}` }))
    expect(first()).toBe(expected)
    await userEvent.click(screen.getByRole('button', { name: `Order by ${column}` }))
    expect(first()).not.toBe(expected)
  }
  expect(screen.getAllByRole('link', { name: '-' }).length).toBeGreaterThan(0)
})

it('shows team visibility, working section links, actual members and workflow status counts', () => {
  const team = makeBootstrap().teams[0]
  const data = makeBootstrap({ teamMembers: [{ teamId: team.id, userId: viewer.id } as never], teamSettings: {}, cycleSettings: {}, documentTemplates: [], projectTemplates: [] })
  render(wrap(<TeamWorkflowSettings data={data} team={team} section="overview" onNavigate={vi.fn()} onReload={vi.fn()} onOpenTeams={vi.fn()}/>))
  expect(screen.getByText('Accessible to all workspace members')).toBeVisible()
  expect(screen.getByRole('link', { name: 'Team overview' })).toHaveAttribute('href', '/workspace/team/TST/overview')
  expect(screen.getByText('1 member')).toBeVisible()
  expect(screen.getByRole('link', { name: /Project labels/ })).toHaveAttribute('href', '/workspace/settings/teams/TST/project-labels')
  expect(screen.getByRole('link', { name: /Issue statuses/ })).toHaveTextContent('3 statuses')
  expect(screen.getByRole('link', { name: /Project statuses/ })).toBeVisible()
})

it('validates announcements, preserves unrelated settings, saves, and reopens persisted content', async () => {
  api.updateWorkspaceSettings.mockResolvedValue({})
  const data = makeBootstrap({ settings: { existing: { value: true } } })
  const reload = vi.fn().mockResolvedValue(undefined)
  render(wrap(<WorkspaceAnnouncements data={data} onReload={reload}/>))
  await userEvent.click(screen.getByRole('button', { name: 'Configure announcements' }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Enable announcements' }))
  expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
  await userEvent.type(within(dialog).getByRole('textbox', { name: 'Announcement title' }), 'Team news')
  await userEvent.type(within(dialog).getByRole('textbox', { name: 'Announcement message' }), 'Read the handbook')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  expect(api.updateWorkspaceSettings).toHaveBeenCalledWith({ existing: { value: true }, onboardingAnnouncement: { enabled: true, title: 'Team news', message: 'Read the handbook' } })
  expect(reload).toHaveBeenCalledOnce()
})
