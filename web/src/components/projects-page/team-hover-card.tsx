import { FileText } from 'lucide-react'
import { ProjectIcon, TeamIcon } from '@/components/issue/issue-icons'
import { avatarColor } from '@/components/issue/avatar-color'
import { usePeopleDirectory } from '@/components/property/people-context'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { teamGlyph, type ProjectTeamChoice } from './project-teams'
import './team-hover-card.css'

/** Class of the surface a team row opens in the project teams picker (`PropertyOption.hoverClassName`). */
export const TEAM_HOVER_CLASS = 'team-hover-surface'
const MAX_AVATARS = 4

function countText(count: number, noun: 'project' | 'document') {
  return count === 0 ? `No ${noun}s` : `${count} ${noun}${count === 1 ? '' : 's'}`
}

/**
 * The hover card of a team row in the project teams picker: the team's icon and name with its members' avatars,
 * then how many projects and documents the team has.
 */
export function TeamHoverCard({ team }: { team: ProjectTeamChoice }) {
  const { t } = useI18n()
  const directory = usePeopleDirectory()
  const info = directory.teamDirectory.get(team.id)
  const members = (info?.memberIds ?? []).flatMap(id => { const user = directory.users.get(id); return user ? [user] : [] })
  return <div className="team-hover-card" data-team-hover-card="">
    <div className="team-hover-card__head">
      <TeamIcon className="team-hover-card__icon" size={16} team={teamGlyph(team)}/>
      <strong className="team-hover-card__name" data-i18n-ignore>{team.name}</strong>
      {members.length > 0 && <span aria-hidden="true" className="team-hover-card__avatars">{members.slice(0, MAX_AVATARS).map(user => <UserAvatar avatarUrl={user.avatarUrl} color={avatarColor(user.id)} key={user.id} name={user.displayName || user.name || user.id}/>)}</span>}
    </div>
    <div className="team-hover-card__row" data-team-hover-projects=""><ProjectIcon size={16}/><span>{t(countText(info?.projectCount ?? 0, 'project'))}</span></div>
    <div className="team-hover-card__row" data-team-hover-documents=""><FileText size={16} strokeWidth={1.5}/><span>{t(countText(info?.documentCount ?? 0, 'document'))}</span></div>
  </div>
}
