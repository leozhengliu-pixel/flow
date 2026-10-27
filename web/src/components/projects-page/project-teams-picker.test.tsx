import { useState } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { ProjectTeamsPicker } from './project-teams-picker'
import { PeopleProvider } from '@/components/property/people-provider'
import { orderTeamTree, projectTeamsLabel, setProjectLeadTeam, toggleProjectTeam, type ProjectTeamChoice } from './project-teams'
import type { FlowDocument, Project, Team, TeamMember, User } from '@/types/flow'

const TEAMS: ProjectTeamChoice[] = [
  { id: 'alpha', name: 'Alpha', key: 'ALP', color: '#4cb782' },
  { id: 'beta', name: 'Beta', key: 'BET', color: '#f2994a' },
  { id: 'gamma', name: 'Gamma', key: 'GAM', color: '#5e6ad2' },
]

function Harness({ initial, onChange }: { initial: string[]; onChange?: (ids: string[]) => void }) {
  const [teamIds, setTeamIds] = useState(initial)
  return <I18nProvider><ProjectTeamsPicker teams={TEAMS} teamIds={teamIds} onChange={ids => { setTeamIds(ids); onChange?.(ids) }} triggerClassName="trigger"/></I18nProvider>
}

describe('project team helpers', () => {
  it('adds a team after the lead team and removes any team except the lead team', () => {
    expect(toggleProjectTeam(['alpha'], 'beta')).toEqual(['alpha', 'beta'])
    expect(toggleProjectTeam(['alpha', 'beta', 'gamma'], 'beta')).toEqual(['alpha', 'gamma'])
    // A project always keeps its lead team (and so at least one team).
    expect(toggleProjectTeam(['alpha'], 'alpha')).toEqual(['alpha'])
    expect(toggleProjectTeam(['alpha', 'beta'], 'alpha')).toEqual(['alpha', 'beta'])
  })

  it('moves a project team to the front to make it the lead team', () => {
    expect(setProjectLeadTeam(['alpha', 'beta', 'gamma'], 'gamma')).toEqual(['gamma', 'alpha', 'beta'])
    expect(setProjectLeadTeam(['alpha', 'beta'], 'alpha')).toEqual(['alpha', 'beta'])
    expect(setProjectLeadTeam(['alpha'], 'beta')).toEqual(['alpha'])
  })

  it('labels the trigger with the lead team name and the other teams\' keys', () => {
    expect(projectTeamsLabel(TEAMS, ['alpha'], 'Team')).toBe('Alpha')
    expect(projectTeamsLabel(TEAMS, ['alpha', 'beta'], 'Team')).toBe('Alpha, BET')
    expect(projectTeamsLabel(TEAMS, ['beta', 'gamma', 'alpha'], 'Team')).toBe('Beta, GAM, ALP')
    expect(projectTeamsLabel(TEAMS, [], 'Team')).toBe('Team')
  })
})

describe('orderTeamTree', () => {
  const parents: Record<string, string> = { b1: 'a', b2: 'a', c1: 'b1' }
  const parentOf = (id: string) => parents[id]
  const ids = (rows: ReturnType<typeof orderTeamTree<{ id: string }>>) => rows.map(row => `${row.team.id}:${row.depth}`)

  it('lists a team\'s sub-teams right after it, one level deeper per ancestor', () => {
    const teams = ['b2', 'a', 'z', 'c1', 'b1'].map(id => ({ id }))
    expect(ids(orderTeamTree(teams, parentOf))).toEqual(['a:0', 'b2:1', 'b1:1', 'c1:2', 'z:0'])
  })

  it('keeps a sub-team top-level when no ancestor is in the list and skips a missing parent', () => {
    expect(ids(orderTeamTree([{ id: 'c1' }, { id: 'z' }], parentOf))).toEqual(['c1:0', 'z:0'])
    // The grandparent is shown: the sub-sub-team nests one level under it.
    expect(ids(orderTeamTree([{ id: 'a' }, { id: 'c1' }], parentOf))).toEqual(['a:0', 'c1:1'])
  })

  it('does not loop on a parent cycle', () => {
    const cycle: Record<string, string> = { x: 'y', y: 'x' }
    expect(ids(orderTeamTree([{ id: 'x' }, { id: 'y' }], id => cycle[id])).sort()).toEqual(['x:0', 'y:0'])
  })
})

describe('ProjectTeamsPicker sub-teams', () => {
  const NESTED: ProjectTeamChoice[] = [
    { id: 'beta', name: 'Beta', key: 'BET' },
    { id: 'alpha', name: 'Alpha', key: 'ALP' },
    { id: 'alpha-ios', name: 'Alpha iOS', key: 'IOS', parentId: 'alpha' },
    { id: 'alpha-ios-ui', name: 'Alpha UI', key: 'UIX', parentId: 'alpha-ios' },
    { id: 'gamma', name: 'Gamma', key: 'GAM' },
  ]
  function Nested({ initial }: { initial: string[] }) {
    const [teamIds, setTeamIds] = useState(initial)
    return <I18nProvider><ProjectTeamsPicker teams={NESTED} teamIds={teamIds} onChange={setTeamIds} triggerClassName="trigger"/></I18nProvider>
  }

  it('lists sub-teams under their parent with an elbow and an indent per level', async () => {
    const user = userEvent.setup()
    render(<Nested initial={['beta']}/>)
    await user.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    const rows = within(screen.getByRole('listbox', { name: 'Teams' })).getAllByRole('option')
    expect(rows.map(row => row.textContent)).toEqual(['BetaBET', 'AlphaALP', 'Alpha iOSIOS', 'Alpha UIUIX', 'GammaGAM'])
    expect(rows.map(row => row.getAttribute('data-indent'))).toEqual([null, null, '1', '2', null])
    expect(rows.map(row => row.querySelector('.property-command-tree-elbow') !== null)).toEqual([false, false, true, true, false])
    expect(rows[2]).toHaveStyle({ '--option-indent': '1' })
    expect(rows[3]).toHaveStyle({ '--option-indent': '2' })
    // The checkbox column is the same for every level.
    expect(rows.every(row => row.querySelector('.property-command-checkbox') !== null)).toBe(true)
  })

  it('orders the selected block as a tree too and indents only under a parent shown in the same block', async () => {
    const user = userEvent.setup()
    render(<Nested initial={['alpha-ios-ui', 'gamma', 'alpha', 'alpha-ios']}/>)
    await user.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    const rows = within(screen.getByRole('listbox', { name: 'Teams' })).getAllByRole('option')
    expect(rows.map(row => row.getAttribute('aria-checked'))).toEqual(['true', 'true', 'true', 'true', 'false'])
    expect(rows.map(row => row.textContent?.replace(/Lead|[A-Z]{3}$/, ''))).toEqual(['Gamma', 'Alpha', 'Alpha iOS', 'Alpha UI', 'Beta'])
    expect(rows.map(row => row.getAttribute('data-indent'))).toEqual([null, null, '1', '2', null])
    // The lead team stays the first selected id, wherever the tree puts its row.
    expect(rows[3]).toHaveTextContent('Lead')
  })

  it('takes a sub-team\'s parent from the team directory when the choice carries none', async () => {
    const user = userEvent.setup()
    const teams = [{ id: 'p', name: 'Parent', key: 'PAR' }, { id: 'c', name: 'Child', key: 'CHD' }]
    const directoryTeams = teams.map(team => ({ id: team.id, name: team.name, key: team.key })) as Team[]
    render(<I18nProvider><PeopleProvider users={[]} teams={directoryTeams} teamSettings={{ c: { parentTeamId: 'p' } }}><ProjectTeamsPicker teams={[teams[1], teams[0]]} teamIds={[]} onChange={vi.fn()} triggerClassName="trigger"/></PeopleProvider></I18nProvider>)
    await user.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    const rows = within(screen.getByRole('listbox', { name: 'Teams' })).getAllByRole('option')
    expect(rows.map(row => row.textContent)).toEqual(['ParentPAR', 'ChildCHD'])
    expect(rows[1]).toHaveAttribute('data-indent', '1')
  })
})

describe('team hover card', () => {
  const user = (id: string, displayName: string) => ({ id, name: displayName, displayName, email: '', active: true }) as User
  const project = (id: string, teamIds: string[], archivedAt?: string) => ({ id, name: id, teamIds, archivedAt }) as unknown as Project
  const doc = (id: string, teamIds: string[], projectIds: string[] = []) => ({ id, teamIds, projectIds }) as unknown as FlowDocument
  const teams = [{ id: 'alpha', name: 'Alpha', key: 'ALP' }, { id: 'beta', name: 'Beta', key: 'BET' }] as Team[]
  const members = [{ teamId: 'alpha', userId: 'u1' }, { teamId: 'alpha', userId: 'u2' }] as TeamMember[]

  function renderWithDirectory() {
    return render(<I18nProvider><PeopleProvider users={[user('u1', 'Ada Lovelace'), user('u2', 'Grace Hopper')]} teams={teams} teamMembers={members} projects={[project('p1', ['alpha']), project('p2', ['alpha', 'beta']), project('p3', ['alpha'], '2026-01-01')]} documents={[doc('d1', ['alpha']), doc('d2', [], ['p2'])]}><ProjectTeamsPicker teams={TEAMS.slice(0, 2)} teamIds={[]} onChange={vi.fn()} triggerClassName="trigger"/></PeopleProvider></I18nProvider>)
  }

  it('opens beside the menu with the team, its members and its project and document counts', async () => {
    const hover = userEvent.setup()
    renderWithDirectory()
    await hover.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    await hover.hover(screen.getByRole('option', { name: /Alpha/ }))
    const card = await screen.findByRole('tooltip')
    expect(card).toHaveClass('team-hover-surface')
    expect(within(card).getByText('Alpha')).toBeInTheDocument()
    // Archived projects do not count; documents count through the team's projects too.
    expect(within(card).getByText('2 projects')).toBeInTheDocument()
    expect(within(card).getByText('2 documents')).toBeInTheDocument()
    expect(card.querySelectorAll('.team-hover-card__avatars > span')).toHaveLength(2)
    await hover.hover(screen.getByRole('option', { name: /Beta/ }))
    await waitFor(() => expect(screen.getByRole('tooltip')).toHaveTextContent('Beta'))
    expect(screen.getByRole('tooltip')).toHaveTextContent('1 project')
    expect(screen.getByRole('tooltip')).toHaveTextContent('1 document')
  })

  it('says "No projects" and "No documents" for a team with none', async () => {
    const hover = userEvent.setup()
    const { unmount } = render(<I18nProvider><ProjectTeamsPicker teams={TEAMS.slice(0, 1)} teamIds={[]} onChange={vi.fn()} triggerClassName="trigger"/></I18nProvider>)
    await hover.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    await hover.hover(screen.getByRole('option', { name: /Alpha/ }))
    const card = await screen.findByRole('tooltip')
    expect(card).toHaveTextContent('No projects')
    expect(card).toHaveTextContent('No documents')
    expect(card.querySelector('.team-hover-card__avatars')).toBeNull()
    unmount()
  })
})

describe('ProjectTeamsPicker', () => {
  it('is a multi-select that closes when a team is toggled', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial={['alpha']} onChange={onChange}/>)
    const trigger = screen.getByRole('combobox', { name: 'Change project teams' })
    expect(trigger).toHaveTextContent('Alpha')
    await user.click(trigger)
    expect(screen.getByRole('listbox', { name: 'Teams' })).toHaveAttribute('aria-multiselectable', 'true')
    await user.click(screen.getByRole('option', { name: /Beta/ }))
    expect(onChange).toHaveBeenLastCalledWith(['alpha', 'beta'])
    await waitFor(() => expect(screen.queryByRole('listbox', { name: 'Teams' })).not.toBeInTheDocument())
    expect(trigger).toHaveTextContent('Alpha, BET')
    // A third team goes to the end; the trigger lists every other team's key.
    await user.click(trigger)
    await user.click(screen.getByRole('option', { name: /Gamma/ }))
    expect(onChange).toHaveBeenLastCalledWith(['alpha', 'beta', 'gamma'])
    expect(trigger).toHaveTextContent('Alpha, BET, GAM')
  })

  it('lists the project\'s teams first and the other teams under "Your teams"', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['gamma', 'alpha']}/>)
    await user.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    const listbox = screen.getByRole('listbox', { name: 'Teams' })
    const rows = within(listbox).getAllByRole('option')
    expect(rows.map(row => row.textContent)).toEqual(['GammaLead', 'AlphaALP', 'BetaBET'])
    expect(rows.map(row => row.getAttribute('aria-checked'))).toEqual(['true', 'true', 'false'])
    const headings = listbox.querySelectorAll('.property-command-group')
    expect(Array.from(headings).map(heading => heading.textContent)).toEqual(['Your teams'])
    expect(rows[2].compareDocumentPosition(headings[0]) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy()
    expect(listbox.querySelector('.property-command-search')).toBeNull()
    expect(screen.getByPlaceholderText('Change teams…')).toBeInTheDocument()
  })

  it('removes a team that is not the lead team and keeps the last team', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial={['alpha', 'beta']} onChange={onChange}/>)
    const trigger = screen.getByRole('combobox', { name: 'Change project teams' })
    await user.click(trigger)
    await user.click(screen.getByRole('option', { name: /Beta/ }))
    expect(onChange).toHaveBeenLastCalledWith(['alpha'])
    expect(trigger).toHaveTextContent('Alpha')
    onChange.mockClear()
    // One team left: it shows its key, no lead badge, and choosing it changes nothing (a project needs a team).
    await user.click(trigger)
    const only = screen.getByRole('option', { name: /Alpha/ })
    expect(only).toHaveTextContent('ALP')
    expect(only).not.toHaveTextContent('Lead')
    expect(only.querySelector('.property-command-group-chevron')).toBeNull()
    await user.click(only)
    expect(trigger).toHaveTextContent('Alpha')
    expect(onChange).not.toHaveBeenCalledWith([])
    await waitFor(() => expect(screen.queryByRole('listbox', { name: 'Teams' })).not.toBeInTheDocument())
  })

  it('changes the lead team from the lead row\'s submenu without unchecking it', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial={['alpha', 'beta']} onChange={onChange}/>)
    const trigger = screen.getByRole('combobox', { name: 'Change project teams' })
    await user.click(trigger)
    const leadRow = screen.getAllByRole('option').find(row => row.textContent === 'AlphaLead')!
    expect(leadRow.querySelector('.property-command-checkbox.is-locked')).not.toBeNull()
    await user.click(leadRow)
    // Choosing the lead row opens "Change lead team…" instead of toggling it.
    expect(onChange).not.toHaveBeenCalled()
    const search = await screen.findByPlaceholderText('Change lead team…')
    const submenu = search.closest('[role=dialog]') as HTMLElement
    expect(within(submenu).getByText('Project teams')).toBeInTheDocument()
    const choices = within(submenu).getAllByRole('option')
    expect(choices.map(choice => choice.textContent)).toEqual(['AlphaALP', 'BetaBET'])
    expect(choices[0]).toHaveAttribute('aria-checked', 'true')
    await user.click(choices[1])
    expect(onChange).toHaveBeenCalledWith(['beta', 'alpha'])
    expect(trigger).toHaveTextContent('Beta, ALP')
    await waitFor(() => expect(screen.queryByRole('listbox', { name: 'Teams' })).not.toBeInTheDocument())
  })

  it('opens the lead team submenu from the keyboard and picks a lead team with Enter', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial={['alpha', 'beta']} onChange={onChange}/>)
    await user.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    // The search takes focus on the next frame; keys typed before that reach the trigger.
    await waitFor(() => expect(screen.getByPlaceholderText('Change teams…')).toHaveFocus())
    // The lead row is highlighted first: ArrowRight opens its submenu and focuses the search.
    await user.keyboard('{ArrowRight}')
    const search = await screen.findByPlaceholderText('Change lead team…')
    await waitFor(() => expect(search).toHaveFocus())
    await user.keyboard('{ArrowDown}{Enter}')
    expect(onChange).toHaveBeenCalledWith(['beta', 'alpha'])
  })

  it('searches teams by name or key', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['alpha']}/>)
    await user.click(screen.getByRole('combobox', { name: 'Change project teams' }))
    await user.type(screen.getByPlaceholderText('Change teams…'), 'GAM')
    expect(screen.getAllByRole('option').map(row => row.textContent)).toEqual(['GammaGAM'])
    await user.clear(screen.getByPlaceholderText('Change teams…'))
    await user.type(screen.getByPlaceholderText('Change teams…'), 'nothing')
    expect(screen.getByText('No results')).toBeInTheDocument()
  })
})
