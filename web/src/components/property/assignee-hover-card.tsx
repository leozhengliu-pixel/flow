import * as Tooltip from '@radix-ui/react-tooltip'
import { CalendarOff, Clock3, ContactRound } from 'lucide-react'
import type { ReactNode } from 'react'
import { Avatar } from '@/components/issue/issue-row'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { useI18n } from '@/i18n/i18n'
import { memberProfileHandle, memberProfilePath, teamHomePath } from '@/lib/app-routes'
import { personDisplayName, personIdentifier, personUsername } from '@/lib/people'
import { useUserPreferences } from '@/lib/runtime-preferences'
import type { ProjectSummary, User, WorkspaceMember } from '@/types/flow'
import { usePeopleDirectory } from './people-context'
import './assignee-hover-card.css'

export function AssigneeHoverPreview({ user: suppliedUser, member: suppliedMember, online }: { user: User; member?: WorkspaceMember; online?: boolean; workspaceName?: string; project?: ProjectSummary }) {
  const { t, locale } = useI18n()
  const directory = usePeopleDirectory()
  const user = { ...suppliedUser, ...directory.users.get(suppliedUser.id) }
  const now = useCurrentTimeMs()
  const preferences = useUserPreferences()
  const member = suppliedMember ?? directory.members.get(user.id)
  const name = personDisplayName(user) || t('Unknown user')
  const username = personUsername(user) || personIdentifier(user)
  const inactive = user.active === false || member?.status === 'suspended'
  const profile = directory.workspaceKey && directory.users.has(user.id) && !inactive ? memberProfilePath(directory.workspaceKey, memberProfileHandle(user)) : undefined
  const teams = (directory.teamIds.get(user.id) ?? []).flatMap(id => {
    const team = directory.teamDirectory.get(id)
    return team?.key && team.name ? [{ key: team.key, name: team.name }] : []
  })
  const away = user.outOfOfficeUntil && new Date(user.outOfOfficeUntil).getTime() > now ? new Date(user.outOfOfficeUntil) : undefined
  const liveOnline = online ?? (directory.onlineUserIds ? directory.onlineUserIds.has(user.id) : undefined)
  const status = member?.status === 'suspended' ? 'Suspended' : inactive ? 'Inactive' : liveOnline !== undefined ? liveOnline ? 'Online' : 'Offline' : undefined
  let localTime: string | undefined
  if (!inactive && user.id === directory.viewerId && preferences.timezone) {
    try { localTime = new Intl.DateTimeFormat(locale, { hour: 'numeric', minute: '2-digit', timeZone: preferences.timezone }).format(now) } catch { /* Unknown zones do not imply the viewer's local zone. */ }
  }
  return <div className="assignee-person-card">
    <header><Avatar id={user.id} name={name} avatarUrl={user.avatarUrl}/><div><strong data-i18n-ignore>{profile ? <a href={profile}>{name}</a> : name}</strong>{username && <span data-i18n-ignore>{username}</span>}</div></header>
    {(status || away || localTime || teams.length > 0) && <div className="assignee-person-card__details">
      {status && <div><i className="assignee-person-card__presence" data-online={status === 'Online' || undefined}/><span>{t(status)}</span></div>}
      {!inactive && away && <div><CalendarOff/><span>{t('Out of office')}<small>{away.toLocaleDateString(locale, { month: 'short', day: 'numeric' })}</small></span></div>}
      {localTime && <div><Clock3/><span>{localTime}<small>{t('local time')}</small></span></div>}
      {teams.length > 0 && <div><ContactRound/><span data-i18n-ignore>{teams.map((team, index) => <span key={team.key}>{index > 0 && ', '}{directory.workspaceKey && !inactive ? <a href={teamHomePath(directory.workspaceKey, team.key)}>{team.name}</a> : team.name}</span>)}</span></div>}
    </div>}
  </div>
}

/**
 * Opens the person card from any avatar-sized trigger: above it, 8px away and aligned with its left edge, the way the reference app's
 * person card opens from an activity avatar. Without a user the children render as they are.
 */
export function PersonCardHover({ user, children }: { user?: User; children: ReactNode }) {
  if (!user) return <>{children}</>
  return <Tooltip.Provider delayDuration={350} skipDelayDuration={150}><Tooltip.Root>
    <Tooltip.Trigger asChild><span className="person-card-trigger">{children}</span></Tooltip.Trigger>
    <Tooltip.Portal><Tooltip.Content data-flow-motion="tooltip" className="assignee-person-surface" side="top" align="start" sideOffset={8} collisionPadding={8}><AssigneeHoverPreview user={user}/></Tooltip.Content></Tooltip.Portal>
  </Tooltip.Root></Tooltip.Provider>
}
