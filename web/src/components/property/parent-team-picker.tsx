import { useMemo } from 'react'
import { PropertyMenu } from './property-menu'
import { TeamIcon } from '@/components/issue/issue-icons'
import { teamHierarchy, type TeamHierarchySettings } from '@/lib/team-hierarchy'
import type { Team } from '@/types/flow'
import { confirmAction } from '@/components/ui/action-dialog-service'
import './parent-team-picker.css'

export function ParentTeamPicker({ teams, settings, teamId = '', value = '', onChange }: {
  teams: Team[]; settings?: TeamHierarchySettings; teamId?: string; value?: string; onChange: (id: string) => void
}) {
  const hierarchy = useMemo(() => teamHierarchy(teams, settings), [teams, settings])
  const options = useMemo(() => [
    {id: '', label: 'No parent team'},
    ...hierarchy.rows(teams.filter(team => !team.retiredAt)).map(({team}) => ({
      id: team.id, label: hierarchy.path(team.id), keywords: `${team.key} ${hierarchy.path(team.id)}`,
      labelContent: <>{Boolean(hierarchy.ancestors.get(team.id)?.length) && <span className="parent-team-ancestors">{hierarchy.ancestors.get(team.id)!.map(item => item.name).join(' › ')} ›</span>}<span className="parent-team-name" title={hierarchy.parentError(teamId, team.id) ?? hierarchy.path(team.id)}>{team.name}</span></>,
      icon: <TeamIcon team={team} size={16}/>,
      disabled: Boolean(hierarchy.parentError(teamId, team.id)),
      description: hierarchy.parentError(teamId, team.id), i18nIgnore: true,
    })),
  ], [hierarchy, teams, teamId])
  const changeParent = async (id: string) => {
    if (id === value) return
    if (teamId && !await confirmAction(id ? 'Change parent team' : 'Remove parent team', {
      danger: false, confirmLabel: id ? 'Change parent team' : 'Remove parent team',
      description: id ? 'This moves the team and its sub-teams. Members, except guests, will be added to the new parent teams. Access to non-private sub-teams follows the new parent.' : 'This detaches the team and its sub-teams. Existing members and workflows are preserved. Access to non-private teams will no longer be restricted by the former parent.',
    })) return
    onChange(id)
  }
  const selected = hierarchy.byId.get(value)
  return <PropertyMenu compact label="Parent team" ariaLabel="Parent team" selectedId={value} options={options} onChange={changeParent} searchPlaceholder="Set parent team…" value={selected?.name ?? 'No parent team'} valueIsEntityName={Boolean(value)} trigger={selected ? <><TeamIcon team={selected} size={16}/><span title={hierarchy.path(value)}>{selected.name}</span></> : undefined} triggerClassName="mini-property-trigger parent-team-picker" surfaceClassName="parent-team-picker-menu"/>
}
