import { useEffect, useMemo, useState } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { MoreHorizontal } from 'lucide-react'

import { TeamIcon } from '@/components/issue/issue-icons'
import { AppLink } from '@/components/ui/app-link'
import { FlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { settingsPath, teamHomePath } from '@/lib/app-routes'
import type { BootstrapData, User } from '@/types/flow'

import { memberIsOnline, memberJoinedAt, memberTeams, relativeTimeAgo } from './member-profile-model'

function fullDate(value: string) {
  return new Intl.DateTimeFormat('en-US', { month: 'long', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(value))
}

function localTime(now: number, timeZone?: string) {
  try { return new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit', timeZone }).format(now) }
  catch { return new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit' }).format(now) }
}

function useMinuteClock() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  return now
}

/**
 * Linear profile aside ("Open details" on a member profile): identity card with the member's
 * presence and an options menu, then Email / Local time / Joined / Teams.
 */
export function MemberProfileAside({ data, user, timeZone }: { data: BootstrapData; user: User; timeZone?: string }) {
  const now = useMinuteClock()
  const self = user.id === data.viewer.id
  const online = memberIsOnline(data, user, now)
  const joinedAt = memberJoinedAt(data, user)
  const teams = useMemo(() => memberTeams(data, user), [data, user])
  const workspaceKey = data.workspace.urlKey
  return <aside className="member-profile-aside" aria-label="Member details">
    <section className="member-profile-aside__card member-profile-aside__identity">
      <UserAvatar className="member-profile-aside__avatar" avatarUrl={user.avatarUrl} name={user.displayName}/>
      <div className="member-profile-aside__identity-text">
        <h2 data-i18n-ignore>{user.displayName}</h2>
        <p><span data-i18n-ignore>{user.name}</span>{online && <> <span aria-hidden>⋅</span> Online <i className="member-profile-aside__online" aria-hidden/></>}</p>
      </div>
      {self && <DropdownMenu.Root modal={false}>
        <DropdownMenu.Trigger asChild>
          <button type="button" className="member-profile-aside__menu-button" aria-label="Open menu"><MoreHorizontal size={16}/></button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content data-flow-motion="floating" className="member-profile-menu" align="end" sideOffset={4} collisionPadding={8}>
            <DropdownMenu.Item asChild><AppLink href={settingsPath(workspaceKey, 'profile')}>Edit profile</AppLink></DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>}
    </section>
    <section className="member-profile-aside__card member-profile-aside__details" aria-label="Profile details">
      <dl>
        {user.email && <div><dt>Email</dt><dd data-i18n-ignore><a className="member-profile-aside__email" href={`mailto:${user.email}`}>{user.email}</a></dd></div>}
        <div><dt>Local time</dt><dd>{localTime(now, timeZone)}</dd></div>
        {joinedAt && <div><dt>Joined</dt><dd><FlowTooltip label={fullDate(joinedAt)}><time dateTime={joinedAt} tabIndex={0}>{relativeTimeAgo(joinedAt, now)}</time></FlowTooltip></dd></div>}
        <div><dt>Teams</dt><dd className="member-profile-aside__teams">
          {teams.length ? teams.map(team => <AppLink key={team.id} className="member-profile-aside__team" href={teamHomePath(workspaceKey, team.key)}>
            <TeamIcon team={team} size={14}/><span data-i18n-ignore>{team.name}</span>
          </AppLink>) : <span className="member-profile-aside__empty">No teams</span>}
        </dd></div>
      </dl>
    </section>
  </aside>
}
