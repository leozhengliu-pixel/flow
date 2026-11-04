import { PropertyMenu } from '@/components/property/property-menu'
import { TeamIcon } from './issue-icons'
import { issueMoveTeams } from '@/lib/issue-team-move'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { ChevronRight } from 'lucide-react'
import { teamHomePath } from '@/lib/app-routes'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import './issue-team-picker.css'

export function IssueTeamPicker({ issue, data, onUpdate }: { issue: Issue; data: BootstrapData; onUpdate: (input: IssueUpdateInput) => Promise<void> }) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const joined = new Set((data.teamMembers ?? []).filter(member => member.userId === data.viewer.id).map(member => member.teamId))
  joined.add(issue.team.id)
  const teams = issueMoveTeams(data).sort((a, b) => Number(joined.has(b.team.id)) - Number(joined.has(a.team.id)))
  return <div className="issue-team-property core-property-picker" title={data.viewerRole === 'guest' ? t('Guests cannot move issues to another team') : undefined}><PropertyMenu label="Team" value={issue.team.name} valueIsEntityName selectedId={issue.team.id}
    disabled={Boolean(issue.archivedAt) || data.viewerRole === 'guest'}
    options={teams.map(({ team, path, ancestors }) => ({ id: team.id, label: `${path} ${team.key}`, ariaLabel: `${path} ${team.key}`, groupId: joined.has(team.id) ? 'joined' : 'other', labelContent: <span className="issue-team-path">{ancestors.map(parent => <span className="issue-team-parent" key={parent.id}><span>{parent.name}</span><ChevronRight viewBox="8 5 8 14"/></span>)}<span className="issue-team-name">{team.name}</span><span className="issue-team-key">{team.key}</span></span>, icon: <TeamIcon team={team}/>, i18nIgnore: true }))}
    hideSearch groupHeading="Showing all items" ariaLabel={t('Change team')} searchPlaceholder={t('Move to team…')} searchShortcut="Command Shift M" tooltip="Team" tooltipShortcut="⌘ ⇧ M"
    triggerClassName="core-property-trigger" trigger={<><TeamIcon team={issue.team}/><span data-i18n-ignore>{issue.team.name}</span></>}
    onChange={async teamId => {
      if (teamId === issue.team.id) return
      try {
        await onUpdate({ teamId })
        toast.success(t('Moved to {name}').replace('{name}', () => teams.find(item => item.team.id === teamId)!.team.name))
      } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update issue')) }
    }}/>{!phone && <a className="issue-assignee-profile" href={teamHomePath(data.workspace.urlKey, issue.team.key)} aria-label={t('Open team overview')}><ChevronRight size={15}/></a>}</div>
}
