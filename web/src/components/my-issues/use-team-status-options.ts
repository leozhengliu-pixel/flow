import { useContext, useMemo } from 'react'
import { workflowStatusOptions } from '@/lib/team-workflow'
import { ApplicationStoreContext } from '@/store/application-store-contexts'

/**
 * The status options a row's team can move to: its effective workflow (own, inherited or workspace statuses),
 * so a list or board showing several teams never offers another team's status. Without workspace data it
 * falls back to the options' own team tags.
 */
export function useTeamStatusOptions<T extends { id: string; teamId?: string }>(options: T[], teamId: string | undefined): T[] {
  const data = useContext(ApplicationStoreContext)?.data
  const states = data?.states
  const teamSettings = data?.teamSettings
  return useMemo(() => {
    if (states?.length) return workflowStatusOptions({ states, teamSettings }, [teamId], options)
    return teamId ? options.filter(option => !option.teamId || option.teamId === teamId) : options
  }, [options, states, teamId, teamSettings])
}
