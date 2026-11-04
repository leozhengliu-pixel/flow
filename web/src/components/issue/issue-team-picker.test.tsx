import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { issueMoveTeams } from '@/lib/issue-team-move'
import { IssueTeamPicker } from './issue-team-picker'

describe('issue team property', () => {
  it('shows the current tick, searches sub-team keys and moves with the selected team', async () => {
    const issue = makeIssue()
    const child = { ...issue.team, id: 'child', name: 'Child', key: 'CHD' }
    const data = makeBootstrap({ teams: [issue.team, child], teamSettings: { child: { parentTeamId: issue.team.id } } as never })
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><IssueTeamPicker issue={issue} data={data} onUpdate={onUpdate}/></I18nProvider>)
    await userEvent.click(screen.getByRole('combobox', { name: 'Change team' }))
    expect(screen.getByRole('heading', { name: 'Showing all items' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: `${issue.team.name} ${issue.team.key}` })).toHaveAttribute('aria-checked', 'true')
    const search = screen.getByPlaceholderText('Move to team…')
    fireEvent.change(search, { target: { value: 'CHD' } })
    expect(screen.getAllByRole('option')).toHaveLength(1)
    fireEvent.keyDown(search, { key: 'ArrowDown' })
    fireEvent.keyDown(search, { key: 'Enter' })
    await waitFor(() => expect(onUpdate).toHaveBeenCalledExactlyOnceWith({ teamId: child.id }))
  })

  it.each(['guest', 'member'] as const)('prevents forbidden destinations for %s', async role => {
    const issue = makeIssue()
    const privateTeam = { ...issue.team, id: 'private', name: 'Private', private: true }
    const archived = { ...issue.team, id: 'archived', archivedAt: '2026-11-01' }
    const retired = { ...issue.team, id: 'retired', retiredAt: '2026-11-01' }
    const data = makeBootstrap({ viewerRole: role, teams: [issue.team, privateTeam, archived, retired] })
    const onUpdate = vi.fn()
    render(<I18nProvider><IssueTeamPicker issue={issue} data={data} onUpdate={onUpdate}/></I18nProvider>)
    const trigger = screen.getByRole('combobox', { name: 'Change team' })
    if (role === 'guest') expect(trigger).toBeDisabled()
    else {
      await userEvent.click(trigger)
      expect(screen.getAllByRole('option')).toHaveLength(1)
      expect(screen.getByRole('option')).toHaveTextContent(issue.team.name)
    }
    expect(onUpdate).not.toHaveBeenCalled()
  })

  it('allows a joined private team and hides a child behind an unjoined private boundary', () => {
    const issue = makeIssue()
    const parent = { ...issue.team, id: 'parent', private: true }
    const child = { ...issue.team, id: 'child' }
    const data = makeBootstrap({ teams: [issue.team, parent, child], teamMembers: [], teamSettings: { child: { parentTeamId: parent.id } } as never })
    expect(issueMoveTeams(data).map(item => item.team.id)).toEqual([issue.team.id])
    data.teamMembers = [{ teamId: parent.id, userId: data.viewer.id, role: 'member', joinedAt: '' }]
    expect(issueMoveTeams(data).map(item => item.team.id)).toEqual([issue.team.id, parent.id, child.id])
  })
})
