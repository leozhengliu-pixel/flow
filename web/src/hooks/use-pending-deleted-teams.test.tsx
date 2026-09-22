import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { ClientStorage } from '@/lib/client-storage'
import {
  PendingDeletedTeamsProvider,
  TEAM_DELETE_GRACE_MS,
  usePendingDeletedTeams,
} from './use-pending-deleted-teams'

vi.mock('@/lib/api', () => ({
  deleteTeam: vi.fn().mockResolvedValue(undefined),
}))

import { deleteTeam } from '@/lib/api'

function wrapper({ children }: { children: React.ReactNode }) {
  return <PendingDeletedTeamsProvider>{children}</PendingDeletedTeamsProvider>
}

describe('usePendingDeletedTeams (LS-0758)', () => {
  beforeEach(() => {
    ClientStorage.clear('local')
    vi.mocked(deleteTeam).mockClear()
  })

  afterEach(() => {
    ClientStorage.clear('local')
  })

  it('requires a provider', () => {
    expect(() => renderHook(() => usePendingDeletedTeams())).toThrow(
      /PendingDeletedTeamsProvider/,
    )
  })

  it('schedules delete, reports pending, and allows cancel', () => {
    const { result } = renderHook(() => usePendingDeletedTeams(), { wrapper })
    act(() => {
      result.current.scheduleDelete({
        workspaceKey: 'acme',
        teamId: 'team-1',
        name: 'Engineering',
        graceMs: TEAM_DELETE_GRACE_MS,
      })
    })
    expect(result.current.isDeletePending('team-1')).toBe(true)
    expect(result.current.teamsPendingDelete.get('team-1')).toBe(true)
    act(() => {
      result.current.cancelPendingDelete('team-1')
    })
    expect(result.current.isDeletePending('team-1')).toBe(false)
    expect(deleteTeam).not.toHaveBeenCalled()
  })

  it('purges immediately via hard DELETE', async () => {
    const { result } = renderHook(() => usePendingDeletedTeams(), { wrapper })
    act(() => {
      result.current.scheduleDelete({
        workspaceKey: 'acme',
        teamId: 'team-2',
        graceMs: 60_000,
      })
    })
    await act(async () => {
      await result.current.purgePendingDelete('team-2')
    })
    expect(deleteTeam).toHaveBeenCalledWith('acme', 'team-2')
    await waitFor(() => expect(result.current.isDeletePending('team-2')).toBe(false))
  })
})
