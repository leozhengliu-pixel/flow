import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import { teamUsesEstimates } from '@/lib/estimates'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import { slaFeatureEnabled } from '@/components/settings/sla-model'
import type { BootstrapData } from '@/types/flow'
import type { MyIssuesGrouping, MyIssuesProperty } from './my-issues-surface'

/**
 * Display properties the issues in scope cannot have, so the display menu leaves them out: Cycle without cycles,
 * Estimate without estimates, SLA without SLAs, Release without the releases feature, Customers and Customer revenue without customers, and "My activity date" outside
 * the activity tab. `teamIds` are the teams the list covers; without them every team counts.
 */
export function unavailableDisplayProperties(data: BootstrapData, teamIds?: readonly string[], options: { activity?: boolean } = {}): MyIssuesProperty[] {
  const scope = teamIds?.length ? teamIds : (data.teams ?? []).map(team => team.id)
  const hidden: MyIssuesProperty[] = []
  if (!scope.some(teamId => data.cycleSettings?.[teamId]?.enabled)) hidden.push('cycle')
  if (!scope.some(teamId => teamUsesEstimates(resolvedTeamSettings(data.teamSettings ?? {}, teamId)))) hidden.push('estimate')
  if (!slaFeatureEnabled(data)) hidden.push('sla')
  if (!workspaceFeatureEnabled(data.workspaceSettings?.featureFlags, 'releases')) hidden.push('release')
  if (!data.customers?.length) hidden.push('customers', 'customerRevenue')
  if (!options.activity) hidden.push('myActivity')
  return hidden
}

/**
 * The groupings the design reference offers on a team's issues and on My issues, in its menu order. Cycle needs a
 * team with cycles, Customer the customer requests feature, Release the releases feature; a team list offers Team only when it
 * covers sub-teams.
 */
export function availableIssueGroupings(data: BootstrapData, surface: 'team' | 'myIssues', teamIds?: readonly string[]): MyIssuesGrouping[] {
  const scope = teamIds?.length ? teamIds : (data.teams ?? []).map(team => team.id)
  const cycles: MyIssuesGrouping[] = scope.some(teamId => data.cycleSettings?.[teamId]?.enabled) ? ['cycle'] : []
  const customers: MyIssuesGrouping[] = workspaceFeatureEnabled(data.workspaceSettings?.featureFlags, 'customer-requests') ? ['customer'] : []
  if (surface === 'myIssues') return ['none', 'focus', 'status', 'statusType', 'agent', 'project', 'priority', ...cycles, 'label', 'labelGroup', 'team', ...customers]
  const teams: MyIssuesGrouping[] = scope.length > 1 ? ['team'] : []
  const releases: MyIssuesGrouping[] = workspaceFeatureEnabled(data.workspaceSettings?.featureFlags, 'releases') ? ['release'] : []
  return ['none', 'status', 'statusType', 'assignee', 'agent', 'project', 'priority', ...cycles, 'label', 'labelGroup', ...teams, ...customers, ...releases]
}
