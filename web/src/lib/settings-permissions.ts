import type { BootstrapData, TeamSettings } from '@/types/flow'
import type { TeamSettingsSection } from './app-routes'

export function canManageTeamSettings(data: BootstrapData,teamId:string,section:TeamSettingsSection = 'overview') {
  if (data.viewerRole === 'admin' || data.viewerRole === 'owner') return true
  if (data.viewerRole === 'guest') return false
  const seen = new Set<string>()
  for (let id=teamId;id&&!seen.has(id);id=data.teamSettings[id]?.parentTeamId??'') {
    seen.add(id)
    if(data.teamMembers.some(member=>member.teamId===id&&member.userId===data.viewer.id&&member.role==='owner'))return true
  }
  if (section === 'security') return false
  const settings=data.teamSettings[teamId]
  if(!settings)return false
  const field: keyof TeamSettings = section==='members'?'memberPermission':section==='issue-labels'?'labelPermission':section==='templates'?'templatePermission':section==='agent-skills'?'agentSkillPermission':'settingsPermission'
  const member=data.teamMembers.some(item=>item.teamId===teamId&&item.userId===data.viewer.id)
  return settings[field]==='allMembers'?(member||settings.access==='public'):settings[field]==='teamMembers'&&member
}
