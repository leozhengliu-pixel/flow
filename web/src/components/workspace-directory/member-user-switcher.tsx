import { useMemo } from 'react'
import { ChevronDown } from 'lucide-react'

import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { UserAvatar } from '@/components/ui/user-avatar'
import type { User } from '@/types/flow'

import { memberAppKind, memberSwitcherUsers } from './member-profile-model'

/**
 * Linear profile header "Open user" button: avatar + name + chevron, opening a user switcher
 * (hidden type-to-filter search, members then apps with an Agent badge).
 */
export function MemberUserSwitcher({ user, users, open, onOpenChange, onSelect }: {
  user: User
  users: User[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSelect: (user: User) => void
}) {
  const choices = useMemo(() => memberSwitcherUsers(users), [users])
  const options = useMemo<PropertyOption[]>(() => choices.map(item => ({
    id: item.id,
    label: item.displayName,
    keywords: `${item.name} ${item.email}`,
    icon: <UserAvatar className="people-menu-avatar member-profile-switcher__avatar" avatarUrl={item.avatarUrl} name={item.displayName}/>,
    end: memberAppKind(item),
    i18nIgnore: true,
  })), [choices])
  return <PropertyMenu
    label="Profile"
    ariaLabel="Open user"
    triggerRole="button"
    triggerClassName="member-profile-user-button"
    surfaceClassName="member-profile-switcher"
    trigger={<>
      <UserAvatar className="people-menu-avatar member-profile-user-button__avatar" avatarUrl={user.avatarUrl} name={user.displayName}/>
      <span className="member-profile-user-button__name" data-i18n-ignore>{user.displayName}</span>
      <ChevronDown className="member-profile-user-button__chevron" size={12} aria-hidden/>
    </>}
    options={options}
    hideSearch
    searchPlaceholder="Open user…"
    emptyLabel="No results"
    tooltip="Open user"
    tooltipShortcut="O then U"
    open={open}
    onOpenChange={onOpenChange}
    onChange={id => {
      const next = choices.find(item => item.id === id)
      if (next && next.id !== user.id) onSelect(next)
    }}
  />
}
