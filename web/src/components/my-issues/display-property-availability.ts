import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import { teamUsesEstimates } from '@/lib/estimates'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import { slaFeatureEnabled } from '@/components/settings/sla-model'
import type { BootstrapData } from '@/types/flow'
import type { MyIssuesProperty } from './my-issues-surface'

/**
 * Display properties the issues in scope cannot have, so the display menu leaves them out: Cycle without cycles,
 * Estimate without estimates, SLA without SLAs, Release without release pipelines, and "My activity date" outside
 * the activity tab. `teamIds` are the teams the list covers; without them every team counts.
 */
export function unavailableDisplayProperties(data: BootstrapData, teamIds?: readonly string[], options: { activity?: boolean } = {}): MyIssuesProperty[] {
  const scope = teamIds?.length ? teamIds : (data.teams ?? []).map(team => team.id)
  const hidden: MyIssuesProperty[] = []
  if (!scope.some(teamId => data.cycleSettings?.[teamId]?.enabled)) hidden.push('cycle')
  if (!scope.some(teamId => teamUsesEstimates(resolvedTeamSettings(data.teamSettings ?? {}, teamId)))) hidden.push('estimate')
  if (!slaFeatureEnabled(data)) hidden.push('sla')
  if (!workspaceFeatureEnabled(data.workspaceSettings?.featureFlags, 'releases') || !data.releasePipelines?.length) hidden.push('release')
  if (!options.activity) hidden.push('myActivity')
  return hidden
}
