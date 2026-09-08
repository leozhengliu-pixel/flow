import * as Tooltip from '@radix-ui/react-tooltip'
import type { ReactElement } from 'react'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { directoryPerson, personIdentifier, type PersonIdentity } from '@/lib/people'
import { usePeopleDirectory } from './people-context'
import './person-info.css'

export function PersonIdentityDetails({ person }: { person: PersonIdentity }) {
  const { t } = useI18n()
  const directory = usePeopleDirectory()
  const user = { ...person, ...directoryPerson(directory.users, person.id) }
  return <dl className="person-identity-details">
    <div><dt>{t('User ID')}</dt><dd data-i18n-ignore>{personIdentifier(user)}</dd></div>
    {user.userId && user.userId !== user.id && <div><dt>{t('Internal ID')}</dt><dd data-i18n-ignore>{user.id}</dd></div>}
    {user.email && <div><dt>{t('Email')}</dt><dd data-i18n-ignore>{user.email}</dd></div>}
  </dl>
}

export function PersonInfo({ person }: { person: PersonIdentity }) {
  const { t } = useI18n()
  const directory = usePeopleDirectory()
  const user = { ...person, ...directoryPerson(directory.users, person.id) }
  const name = user.displayName || user.label || user.name || personIdentifier(user)
  const member = directory.members.get(user.id)
  const badge = member?.status === 'suspended' ? 'Suspended' : user.active === false ? 'Inactive' : member?.role === 'owner' ? 'Owner' : member?.role === 'admin' ? 'Admin' : member?.role === 'guest' ? 'Guest' : undefined
  const teams = directory.teams.get(user.id)
  const projects = directory.projects.get(user.id)
  return <div className="person-info"><header><UserAvatar className="person-info-avatar" avatarUrl={user.avatarUrl} name={name}/><div><strong data-i18n-ignore>{name}</strong>{user.name && user.name !== name && <span data-i18n-ignore>{user.name}</span>}{badge && <small className="person-info-badge">{t(badge)}</small>}</div></header><PersonIdentityDetails person={user}/>{(teams?.length || projects?.length) ? <dl className="person-identity-details">{teams?.length ? <div><dt>{t('Teams')}</dt><dd data-i18n-ignore>{teams.join(', ')}</dd></div> : null}{projects?.length ? <div><dt>{t('Projects')}</dt><dd data-i18n-ignore>{projects.join(', ')}</dd></div> : null}</dl> : null}{directory.workspaceName && <footer data-i18n-ignore>{directory.workspaceName}</footer>}</div>
}

export function PersonHover({ children, person, userId }: { children: ReactElement; person?: PersonIdentity; userId?: string }) {
  const directory = usePeopleDirectory()
  const user = person ?? (userId ? directoryPerson(directory.users, userId) : undefined)
  if (!user) return children
  return <Tooltip.Provider delayDuration={350} skipDelayDuration={150}><Tooltip.Root><Tooltip.Trigger asChild>{children}</Tooltip.Trigger><Tooltip.Portal><Tooltip.Content data-flow-motion="tooltip" className="person-info-surface" side="right" align="start" sideOffset={6} collisionPadding={8}><PersonInfo person={user}/></Tooltip.Content></Tooltip.Portal></Tooltip.Root></Tooltip.Provider>
}
