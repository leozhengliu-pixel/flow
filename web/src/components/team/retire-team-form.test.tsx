import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, started, viewer } from '@/test/fixtures'

import { RetireTeamForm } from './retire-team-form'

describe('RetireTeamForm', () => {
  it('requires move or cancel for open issues and shows progress controls', async () => {
    const user = userEvent.setup()
    const team = { id: 'team-1', key: 'TST', name: 'Test team', color: '#5e6ad2' }
    const data = makeBootstrap({
      teams: [
        team,
        { id: 'team-2', key: 'OTH', name: 'Other', color: '#222' },
      ],
      teamMembers: [
        { teamId: 'team-1', userId: viewer.id, role: 'owner', joinedAt: '' },
        { teamId: 'team-2', userId: viewer.id, role: 'member', joinedAt: '' },
      ],
      issues: [makeIssue({ state: started })],
      states: [
        started,
        { id: 'state-canceled', name: 'Canceled', color: '#999', type: 'canceled', position: 3 },
      ],
    })
    render(
      <I18nProvider>
        <RetireTeamForm
          open
          onOpenChange={vi.fn()}
          data={data}
          team={team}
          onReload={vi.fn()}
        />
      </I18nProvider>,
    )
    expect(screen.getByRole('heading', { name: 'Retire team' })).toBeInTheDocument()
    expect(screen.getByText('Move to team')).toBeInTheDocument()
    expect(screen.getByText('Cancel issues')).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: 'Cancel issues' }))
    expect(screen.getByText(/will be canceled/i)).toBeInTheDocument()
  })
})
