import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { defaultTeamTimezone, timezoneLabel, timezoneOptions } from '@/lib/team-timezones'
import { makeBootstrap, viewer } from '@/test/fixtures'

import { suggestTeamKey } from '@/lib/team-key'

import { TeamCreateSettings, type TeamCreateInput } from './team-create-settings'

const teams = [
  { id: 'eng', key: 'ENG', name: 'Engineering', color: '#111' },
  { id: 'des', key: 'DES', name: 'Design', color: '#222' },
]

function renderForm(props: Partial<Parameters<typeof TeamCreateSettings>[0]> = {}) {
  const onCreate = vi.fn<(input: TeamCreateInput) => Promise<void>>().mockResolvedValue(undefined)
  const onCreated = vi.fn()
  const onBack = vi.fn()
  const data = makeBootstrap({
    teams,
    teamSettings: {
      eng: { teamId: 'eng', timezone: 'America/Los_Angeles' } as never,
      des: { teamId: 'des', timezone: 'America/Los_Angeles' } as never,
    },
    viewer,
    viewerRole: 'admin',
  })
  render(
    <I18nProvider>
      <TeamCreateSettings data={data} onBack={onBack} onCreate={onCreate} onCreated={onCreated} {...props} />
    </I18nProvider>,
  )
  return { onCreate, onCreated, onBack }
}

describe('TeamCreateSettings', () => {
  it('renders the reference fields in order with their copy and defaults', () => {
    renderForm()
    expect(screen.getByRole('heading', { name: 'Create a new team' })).toBeInTheDocument()
    expect(screen.getByText('Create a new team to manage separate cycles, workflows, and notifications')).toBeInTheDocument()
    const labels = ['Icon & Name', 'Identifier', 'Parent team', 'Team access', 'Timezone', 'Copy from team']
    const nodes = labels.map(label => screen.getAllByText(label)[0])
    nodes.slice(1).forEach((node, index) => expect(nodes[index].compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy())
    expect(screen.getByText('Used as a prefix for team issues (e.g. ENG-123)')).toBeInTheDocument()
    expect(screen.getByText('Settings and workflows will be inherited from the parent team')).toBeInTheDocument()
    expect(screen.getByText('Copy workflows, cycle, and team settings from another team. Team members and Slack notification settings won\'t be copied.')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Icon & Name' })).toHaveAttribute('placeholder', 'e.g. Engineering')
    expect(screen.getByRole('textbox', { name: 'Icon & Name' })).toHaveFocus()
    expect(screen.getByRole('textbox', { name: 'Identifier' })).toHaveAttribute('placeholder', 'e.g. ENG')
    expect(screen.getByRole('combobox', { name: 'Parent team' })).toHaveTextContent('No parent team')
    expect(screen.getByRole('combobox', { name: 'Copy from team' })).toHaveTextContent('Don’t copy')
    // The default timezone is the one the workspace's teams use.
    expect(screen.getByRole('combobox', { name: 'Timezone' })).toHaveTextContent(timezoneLabel('America/Los_Angeles'))
    expect(screen.queryByText(/upgrade|Business|plan/i)).not.toBeInTheDocument()
  })

  it('suggests the identifier from the name until it is edited, then submits the form', async () => {
    const user = userEvent.setup()
    const { onCreate, onCreated } = renderForm()
    await user.type(screen.getByRole('textbox', { name: 'Icon & Name' }), 'Growth ops')
    const identifier = screen.getByRole('textbox', { name: 'Identifier' })
    expect(identifier).toHaveValue('GRO')
    expect(screen.getByText('Used as a prefix for team issues (e.g. GRO-123)')).toBeInTheDocument()
    await user.clear(identifier)
    await user.type(identifier, 'gw')
    expect(identifier).toHaveValue('GW')
    await user.type(screen.getByRole('textbox', { name: 'Icon & Name' }), ' two')
    expect(identifier).toHaveValue('GW')
    await user.click(screen.getByRole('button', { name: 'Create team' }))
    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: 'Growth ops two', key: 'GW', private: false, parentTeamId: '', copyFromTeamId: '', timezone: 'America/Los_Angeles' }))
    expect(onCreated).toHaveBeenCalledWith('GW')
  })

  it('explains missing, duplicate and taken values instead of submitting', async () => {
    const user = userEvent.setup()
    const { onCreate } = renderForm()
    await user.click(screen.getByRole('button', { name: 'Create team' }))
    expect(screen.getByText('Name must be at least 1 character long')).toBeInTheDocument()
    expect(screen.getByText('Identifier must be at least 1 character long')).toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: 'Icon & Name' }), 'engineering')
    await user.click(screen.getByRole('button', { name: 'Create team' }))
    expect(screen.getByText('Team with this name already exists')).toBeInTheDocument()
    await user.clear(screen.getByRole('textbox', { name: 'Icon & Name' }))
    await user.type(screen.getByRole('textbox', { name: 'Icon & Name' }), 'Other')
    await user.clear(screen.getByRole('textbox', { name: 'Identifier' }))
    await user.type(screen.getByRole('textbox', { name: 'Identifier' }), 'eng')
    await user.click(screen.getByRole('button', { name: 'Create team' }))
    expect(screen.getByText('The team Engineering is already using this identifier')).toBeInTheDocument()
    expect(onCreate).not.toHaveBeenCalled()
  })

  it('preselects the parent team, which fixes the copy source and is sent with the team', async () => {
    const user = userEvent.setup()
    const { onCreate } = renderForm({ parentTeamId: 'eng', initialName: 'Platform' })
    expect(screen.getByRole('combobox', { name: 'Parent team' })).toHaveTextContent('Engineering')
    expect(screen.getByRole('combobox', { name: 'Copy from team' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Create team' }))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: 'Platform', key: 'PLA', parentTeamId: 'eng', copyFromTeamId: 'eng' }))
  })

  it('goes back through the Back link and reports a server error', async () => {
    const user = userEvent.setup()
    const { onBack, onCreate, onCreated } = renderForm()
    await user.click(screen.getByRole('button', { name: 'Back' }))
    expect(onBack).toHaveBeenCalled()
    onCreate.mockRejectedValueOnce(new Error('Team limit reached'))
    await user.type(screen.getByRole('textbox', { name: 'Icon & Name' }), 'Support')
    await user.click(screen.getByRole('button', { name: 'Create team' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Team limit reached')
    expect(onCreated).not.toHaveBeenCalled()
  })
})

describe('suggestTeamKey', () => {
  it('uses the first three letters or digits and numbers a taken identifier', () => {
    expect(suggestTeamKey('Growth ops', [])).toBe('GRO')
    expect(suggestTeamKey('  a-b c ', [])).toBe('ABC')
    expect(suggestTeamKey('Engineering', ['ENG'])).toBe('ENG2')
    expect(suggestTeamKey('Engineering', ['eng', 'ENG2'])).toBe('ENG3')
    expect(suggestTeamKey('!!!', [])).toBe('')
  })
})

describe('team timezones', () => {
  it('labels zones like the reference and orders them by offset', () => {
    expect(timezoneLabel('Etc/UTC')).toBe('GMT+0:00 – Coordinated Universal Time')
    expect(timezoneLabel('America/Los_Angeles')).toMatch(/^GMT-[78]:00 – Pacific (Time|Standard Time|Daylight Time) - Los Angeles$/)
    const options = timezoneOptions()
    expect(options.length).toBeGreaterThan(100)
    expect(options.findIndex(option => option.value === 'Pacific/Honolulu')).toBeLessThan(options.findIndex(option => option.value === 'Etc/UTC'))
    expect(options.findIndex(option => option.value === 'Etc/UTC')).toBeLessThan(options.findIndex(option => option.value === 'Asia/Tokyo'))
  })

  it('defaults to the most used team timezone, or the browser zone when it is used or no team exists', () => {
    expect(defaultTeamTimezone([], 'Asia/Tokyo')).toBe('Asia/Tokyo')
    expect(defaultTeamTimezone(['America/Los_Angeles', 'America/Los_Angeles', 'Europe/London'], 'Asia/Tokyo')).toBe('America/Los_Angeles')
    expect(defaultTeamTimezone(['America/Los_Angeles', 'Europe/London'], 'Europe/London')).toBe('Europe/London')
    expect(defaultTeamTimezone(['Nowhere/Land'], 'Asia/Tokyo')).toBe('Etc/UTC')
  })
})
