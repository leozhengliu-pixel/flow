import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import {
  ActiveTeamProvider,
  SetActiveTeam,
  useActiveTeam,
} from './active-team'
import { makeBootstrap } from '@/test/fixtures'

function Probe() {
  const { activeTeam } = useActiveTeam()
  return <div data-testid="active">{activeTeam?.key ?? 'none'}</div>
}

describe('SetActiveTeam', () => {
  it('sets and clears the active team with the route team', async () => {
    const data = makeBootstrap()
    const team = data.teams[0]
    const { rerender, unmount } = render(
      <ActiveTeamProvider>
        <SetActiveTeam team={team} />
        <Probe />
      </ActiveTeamProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('active')).toHaveTextContent(team.key))
    rerender(
      <ActiveTeamProvider>
        <SetActiveTeam team={undefined} />
        <Probe />
      </ActiveTeamProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('active')).toHaveTextContent('none'))
    unmount()
  })
})
