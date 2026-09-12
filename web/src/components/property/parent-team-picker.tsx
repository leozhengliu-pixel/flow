import { useMemo, useState } from 'react'
import { PropertyMenu } from './property-menu'
import { TeamIcon } from '@/components/issue/issue-icons'
import { teamHierarchy, type TeamHierarchySettings } from '@/lib/team-hierarchy'
import type { BootstrapData, Team } from '@/types/flow'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import './parent-team-picker.css'

export function ParentTeamPicker({ teams, settings, teamId = '', value = '', data, onChange }: {
  teams: Team[]; settings?: TeamHierarchySettings; teamId?: string; value?: string; data?: BootstrapData; onChange: (id: string) => void | Promise<void>
}) {
  const hierarchy = useMemo(() => teamHierarchy(teams, settings), [teams, settings])
  const [pendingParentId, setPendingParentId] = useState<string>()
  const canManageTeam = useMemo(() => (candidate: Team) => {
    if (!data || data.viewerRole === 'admin' || data.viewerRole === 'owner') return true
    const ids = [candidate.id, ...(hierarchy.ancestors.get(candidate.id) ?? []).map(team => team.id)]
    return data.teamMembers.some(member => member.userId === data.viewer.id && member.role === 'owner' && ids.includes(member.teamId))
  }, [data, hierarchy])
  const options = useMemo(() => [
    {id: '', label: 'No parent team'},
    ...hierarchy.rows(teams.filter(team => !team.retiredAt)).map(({team}) => ({
      id: team.id, label: hierarchy.path(team.id), keywords: `${team.key} ${hierarchy.path(team.id)}`,
      labelContent: <>{Boolean(hierarchy.ancestors.get(team.id)?.length) && <span className="parent-team-ancestors">{hierarchy.ancestors.get(team.id)!.map(item => item.name).join(' › ')} ›</span>}<span className="parent-team-name" title={hierarchy.parentError(teamId, team.id) ?? hierarchy.path(team.id)}>{team.name}</span></>,
      icon: <TeamIcon team={team} size={16}/>,
      disabled: Boolean(hierarchy.parentError(teamId, team.id)) || !canManageTeam(team),
      description: hierarchy.parentError(teamId, team.id) ?? (!canManageTeam(team) ? 'You don’t have permission to manage this team' : undefined), i18nIgnore: true,
    })),
  ], [canManageTeam, hierarchy, teams, teamId])
  const changeParent = (id: string) => {
    if (id === value) return
    setPendingParentId(id)
  }
  const pendingParent = hierarchy.byId.get(pendingParentId ?? '')
  const affectedTeamIds = useMemo(() => teamId ? [...hierarchy.subtree(teamId)] : [], [hierarchy, teamId])
  const memberAdds = useMemo(() => {
    if (!data || !pendingParent) return 0
    const parentMembers = new Set(data.teamMembers.filter(member => member.teamId === pendingParent.id).map(member => member.userId))
    const guests = new Set(data.members.filter(member => member.role === 'guest').map(member => member.user.id))
    return new Set(data.teamMembers.filter(member => affectedTeamIds.includes(member.teamId) && !parentMembers.has(member.userId) && !guests.has(member.userId)).map(member => member.userId)).size
  }, [affectedTeamIds, data, pendingParent])
  const labelConflicts = useMemo(() => {
    if (!data || !pendingParent || !teamId) return 0
    const ancestorNames = new Set(data.labels.filter(label => hierarchy.ancestors.get(teamId)?.some(team => team.id === label.scope) || label.scope === pendingParent.id).map(label => label.name.toLowerCase()))
    return data.labels.filter(label => affectedTeamIds.includes(label.scope ?? '') && ancestorNames.has(label.name.toLowerCase())).length
  }, [affectedTeamIds, data, hierarchy, pendingParent, teamId])
  const stateConflicts = useMemo(() => {
    if (!data || !pendingParent || !teamId) return 0
    const parentStates = data.states.filter(state => state.teamId === pendingParent.id)
    return data.states.filter(state => affectedTeamIds.includes(state.teamId ?? '') && !parentStates.some(parent => parent.type === state.type && parent.name.toLowerCase() === state.name.toLowerCase())).length
  }, [affectedTeamIds, data, pendingParent, teamId])
  const selected = hierarchy.byId.get(value)
  return <><PropertyMenu compact label="Parent team" ariaLabel="Parent team" selectedId={value} options={options} onChange={changeParent} searchPlaceholder="Set parent team…" value={selected?.name ?? 'No parent team'} valueIsEntityName={Boolean(value)} trigger={selected ? <><TeamIcon team={selected} size={16}/><span title={hierarchy.path(value)}>{selected.name}</span></> : undefined} triggerClassName="mini-property-trigger parent-team-picker" surfaceClassName="parent-team-picker-menu"/><Dialog open={pendingParentId !== undefined} onOpenChange={open => !open && setPendingParentId(undefined)}><DialogContent data-i18n-ignore className="parent-team-change-dialog"><DialogTitle>{pendingParentId ? `Set ${teams.find(team => team.id === teamId)?.name ?? 'team'} under ${pendingParent?.name ?? 'parent team'}` : 'Remove parent team'}</DialogTitle><p>{pendingParentId ? 'This moves the team and its sub-teams. Members, issues, workflows, labels, templates, and cycles are synchronized with the parent team.' : 'This detaches the team and its sub-teams. Existing members and workflows are preserved, and inherited settings become independent.'}</p>{pendingParentId && <ul><li>{memberAdds} members will be added to the parent team</li><li>{labelConflicts} label names will be renamed to preserve uniqueness</li><li>{stateConflicts} issue statuses will be mapped</li><li>Cycle schedules will follow the parent team</li></ul>}<footer><button type="button" onClick={() => setPendingParentId(undefined)}>Cancel</button><button className="primary" type="button" onClick={() => { const next = pendingParentId ?? ''; setPendingParentId(undefined); void onChange(next) }}>{pendingParentId ? 'Set parent team' : 'Remove parent team'}</button></footer></DialogContent></Dialog></>
}
