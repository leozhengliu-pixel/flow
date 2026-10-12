import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { ApplicationTeamAccess } from './application-team-access'

it('distinguishes dynamic public access from explicit private grants', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup()
  const teams = makeBootstrap().teams
  const privateTeam = { ...teams[0], id: 'private', name: 'Private team', private: true }
  const onChange = vi.fn()
  render(<I18nProvider><ApplicationTeamAccess teams={[...teams, privateTeam]} value={{ allPublicTeams: false, teamIds: [] }} onChange={onChange}/></I18nProvider>)
  await user.click(screen.getByRole('radio', { name: 'All public teams' }))
  expect(onChange).toHaveBeenLastCalledWith({ allPublicTeams: true, teamIds: [] })
  expect(screen.queryByRole('checkbox', { name: 'Private team' })).toBeNull()
  await user.click(screen.getByRole('radio', { name: 'All public teams and select other teams…' }))
  expect(screen.queryByRole('checkbox', { name: 'Test team' })).toBeNull()
  await user.click(screen.getByRole('checkbox', { name: 'Private team' }))
  expect(onChange).toHaveBeenLastCalledWith({ allPublicTeams: true, teamIds: ['private'] })
})
