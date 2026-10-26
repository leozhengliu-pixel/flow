import { useState } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { ProjectTeamsPicker } from './project-teams-picker'
import { projectTeamsLabel, setProjectLeadTeam, toggleProjectTeam, type ProjectTeamChoice } from './project-teams'

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
