import { useEffect, useRef } from 'react'

import type { BootstrapData, Team, User } from '@/types/flow'

/** Linear "Agent" / "Application" badge for app users. */
export function memberAppKind(user: User): 'Agent' | 'Application' | undefined {
  if (!user.app) return undefined
  return user.appScopes?.some(scope => scope === 'app:mentionable' || scope === 'app:assignable') ? 'Agent' : 'Application'
}

/** Members first (alphabetical), then apps; invited/deactivated people are left out. */
export function memberSwitcherUsers(users: User[]) {
  const byName = (a: User, b: User) => a.displayName.localeCompare(b.displayName)
  return [...users.filter(user => !user.app && user.active).sort(byName), ...users.filter(user => user.app).sort(byName)]
}

/** How long "O" stays armed for the "O then U" sequence (matches the app-wide G/N sequences). */
const SEQUENCE_TIMEOUT = 1100
const EDITABLE = 'input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"]'

function hasOpenOverlay() {
  if (document.querySelector('[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]')) return true
  return [...document.querySelectorAll('[data-radix-popper-content-wrapper]')].some(wrapper => !wrapper.querySelector('[role="tooltip"]'))
}

/** Opens the switcher on "O then U" while no field, menu or dialog has the keyboard. */
export function useOpenUserShortcut(onOpen: () => void) {
  const armedAt = useRef(0)
  const open = useRef(onOpen)
  useEffect(() => { open.current = onOpen })
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.repeat) return
      const target = event.target instanceof Element ? event.target : null
      if (target?.closest(EDITABLE) || (target instanceof HTMLElement && target.isContentEditable) || hasOpenOverlay()) return
      const key = event.key.toLowerCase()
      const armed = Date.now() - armedAt.current < SEQUENCE_TIMEOUT
      armedAt.current = 0
      if (armed && key === 'u') { event.preventDefault(); open.current(); return }
      if (key === 'o') armedAt.current = Date.now()
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [])
}

/** A member counts as online when seen in the last five minutes (the viewer always is). */
const ONLINE_WINDOW = 5 * 60_000

export function memberJoinedAt(data: BootstrapData, user: User) {
  return data.members?.find(member => member.user.id === user.id)?.joinedAt
}

export function memberTeams(data: BootstrapData, user: User): Team[] {
  const ids = new Set([...(data.teamMembers ?? []).filter(member => member.userId === user.id).map(member => member.teamId), ...(user.app ? user.appTeamIds ?? [] : [])])
  return data.teams.filter(team => ids.has(team.id) && !team.archivedAt)
}

export function memberIsOnline(data: BootstrapData, user: User, now = Date.now()) {
  if (user.id === data.viewer.id) return true
  const lastSeen = data.members?.find(member => member.user.id === user.id)?.lastSeenAt
  return Boolean(lastSeen && now - new Date(lastSeen).getTime() < ONLINE_WINDOW)
}

/** "5 hours ago", like Linear's relative dates. */
export function relativeTimeAgo(value: string, now = Date.now()) {
  const seconds = Math.round((new Date(value).getTime() - now) / 1000)
  const units: [Intl.RelativeTimeFormatUnit, number][] = [['year', 31_536_000], ['month', 2_592_000], ['week', 604_800], ['day', 86_400], ['hour', 3_600], ['minute', 60]]
  const format = new Intl.RelativeTimeFormat('en-US', { numeric: 'always' })
  for (const [unit, size] of units) if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit)
  return 'just now'
}
