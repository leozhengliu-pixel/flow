import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, ChevronRight } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { toast } from 'sonner'
import { useI18n } from '@/i18n/i18n'
import { addSubscription, removeSubscription, setTeamMembership } from '@/lib/api'
import { toggleFavoriteFor } from '@/lib/favorites'
import { refreshResourcePreferences } from '@/lib/resource-preferences'
import { settingsPath, teamArchivePath, teamHomePath } from '@/lib/app-routes'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { SlackIcon } from '@/components/issue/issue-icons'
import { TeamSettingsIcon, TeamArchiveIcon } from './team-menu-icons'
import type { BootstrapData, Team } from '@/types/flow'

const notifications = [
  ['issueAdded', 'An issue is added to the team'],
  ['issueCompleted', 'An issue is marked completed or canceled'],
  ['triage', 'An issue is added to the triage queue'],
  ['pulse', 'A team project update is posted'],
] as const

function teamLeaveDisabledReason(data: BootstrapData, team: Team) {
  const membership = data.teamMembers.find(item => item.teamId === team.id && item.userId === data.viewer.id)
  if (!membership) return 'You are not a member of this team'
  if (membership.managedSource) return 'Managed by SCIM'
  if (membership.role === 'owner' && !data.teamMembers.some(item => item.teamId === team.id && item.userId !== data.viewer.id && item.role === 'owner')) return 'A team needs at least one owner'
  const active = new Set(data.teams.filter(item => !item.retiredAt).map(item => item.id))
  const memberships = data.teamMembers.filter(item => item.userId === data.viewer.id && active.has(item.teamId))
  if (memberships.length <= 1) return 'You should be a member of at least one active team'
  if (data.viewerRole !== 'guest' && memberships.some(item => {
    const seen = new Set<string>()
    for (let parent = data.teamSettings[item.teamId]?.parentTeamId ?? data.teamParents?.[item.teamId]; parent && !seen.has(parent); parent = data.teamSettings[parent]?.parentTeamId ?? data.teamParents?.[parent]) {
      if (parent === team.id) return true
      seen.add(parent)
    }
    return false
  })) return 'Leave the sub-teams before leaving this team'
  return undefined
}

export function SidebarTeamMenu({ data, team, onReload, children }: { data: BootstrapData; team: Team; onReload?: () => Promise<void>; children: ReactNode }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false), [query, setQuery] = useState(''), [pending, setPending] = useState(false), [leaving, setLeaving] = useState(false)
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.innerWidth <= 800)
  useEffect(() => {
    const media = window.matchMedia('(max-width: 800px)')
    const update = () => setNarrow(media.matches)
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])
  const content = useRef<HTMLDivElement>(null), search = useRef<HTMLInputElement>(null), busy = useRef(false)
  const favorite = useMemo(() => open && data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'team' && item.resourceId === team.id), [open, data.favorites, data.viewer.id, team.id])
  const subscription = useMemo(() => open ? data.subscriptions.find(item => item.userId === data.viewer.id && item.resourceType === 'team' && item.resourceId === team.id) : undefined, [open, data.subscriptions, data.viewer.id, team.id])
  const savedEvents = subscription ? subscription.events?.length ? subscription.events.map(event => event === 'updates' ? 'pulse' : event) : ['pulse'] : []
  const savedKey = JSON.stringify(savedEvents)
  const [events, setEvents] = useState<string[]>(savedEvents)
  useEffect(() => { if (!pending) setEvents(JSON.parse(savedKey) as string[]) }, [savedKey, pending])
  const label = favorite ? 'Unfavorite' : 'Favorite'
  const matches = (label: string) => [label, t(label)].some(value => value.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  const groups = [[label], ['Team settings', 'Copy URL', 'Open archive'], ['Subscribe', 'Configure Slack notifications…'], ['Leave team…']].map(group => group.filter(matches)).filter(group => group.length)
  const leaveReason = useMemo(() => open ? teamLeaveDisabledReason(data, team) : undefined, [open, data, team])
  const updateEvent = async (event: string, checked: boolean) => {
    if (busy.current) return
    busy.current = true
    setPending(true)
    const previous = events, next = checked ? [...new Set([...events, event])] : events.filter(value => value !== event)
    setEvents(next)
    try {
      if (next.length) await addSubscription('team', team.id, next)
      else await removeSubscription('team', team.id)
      await refreshResourcePreferences(data.workspace.urlKey)
    } catch (error) {
      setEvents(previous)
      toast.error(t('Could not update team notifications'), { description: error instanceof Error ? error.message : undefined })
    } finally { busy.current = false; setPending(false) }
  }
  const leave = async () => {
    if (leaving || leaveReason) return
    const confirmed = await confirmAction(t('Leave this team?'), { description: `${team.name}. ${t(team.private ? 'You will need an invitation to rejoin this private team.' : 'You can rejoin from the Teams page.')}`, confirmLabel: t('Leave team'), danger: true })
    if (!confirmed) return
    setLeaving(true)
    try { await setTeamMembership(data.workspace.urlKey, team.id, data.viewer.id, false); await onReload?.(); toast.success(t('You left the team')) }
    catch (error) { toast.error(t('Could not update team membership'), { description: error instanceof Error ? error.message : undefined }) }
    finally { setLeaving(false) }
  }
  const select = (label: string) => {
    if (label === 'Favorite' || label === 'Unfavorite') void toggleFavoriteFor(data, 'team', team.id).catch(() => undefined)
    else if (label === 'Leave team…') void leave()
    else if (label === 'Copy URL') void navigator.clipboard.writeText(`${location.origin}${teamHomePath(data.workspace.urlKey, team.key)}`).then(() => toast.success(t('Team URL copied to clipboard'))).catch(error => toast.error(t('Could not copy URL'), { description: String(error) }))
  }
  const icon = (label: string) => label === 'Team settings' ? <TeamSettingsIcon/> : label === 'Open archive' ? <TeamArchiveIcon/> : label === 'Configure Slack notifications…' ? <SlackIcon/> : label === 'Leave team…' ? <span className="sidebar-team-menu-icon-spacer"/> : <ViewGlyph color="currentColor" icon={label === 'Copy URL' ? 'Link' : label === 'Subscribe' ? 'Subscribe' : 'Favorite'}/>
  return <DropdownMenu.Root open={open} onOpenChange={value => { setOpen(value); if (!value) setQuery('') }}>
    <DropdownMenu.Trigger asChild onPointerDown={event => {
      // Opening on pointerdown can put a menu item under the same pointerup.
      // Wait for the completed click; retain Radix keyboard activation.
      if (event.button === 0) event.preventDefault()
    }} onClick={() => setOpen(value => !value)}>{children}</DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" ref={content} className="sidebar-popover sidebar-team-menu" side={narrow ? 'bottom' : 'right'} align={narrow ? 'end' : 'start'} sideOffset={narrow ? 4 : -24} collisionPadding={8} onKeyDown={event => {
      if (event.defaultPrevented || !content.current?.contains(event.target as Node) || event.target === search.current || event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return
      event.preventDefault(); event.stopPropagation(); setQuery(value => value + event.key); search.current?.focus()
    }}>
      <div className="sidebar-team-menu-search" data-searching={Boolean(query)}><input ref={search} autoFocus aria-label={t('Filter…')} placeholder={t('Filter…')} value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => {
        if (event.key === 'Escape' || event.key === 'Tab') return
        event.stopPropagation()
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Enter') {
          event.preventDefault()
          const items = content.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([data-disabled])')
          const item = event.key === 'ArrowUp' ? items?.[items.length - 1] : items?.[0]
          if (event.key === 'Enter') item?.click(); else item?.focus()
        }
      }}/></div>
      {!groups.length && <div className="sidebar-team-menu-empty" role="status">{t('No results')}</div>}
      {groups.map((group, index) => <div className="sidebar-team-menu-group" key={index}>{index > 0 && <DropdownMenu.Separator/>}{group.map(label => {
        if (label === 'Subscribe') return <DropdownMenu.Sub key={label}><DropdownMenu.SubTrigger>{icon(label)}<span>{t(label)}</span><ChevronRight className="menu-chevron"/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className="sidebar-popover sidebar-team-subscribe-menu" sideOffset={narrow ? -247 : -2} collisionPadding={8}>
          {notifications.map(([event, label], index) => <div className="sidebar-team-subscribe-group" key={event}>{index === 0 && <DropdownMenu.Label>{t('Inbox notifications')}</DropdownMenu.Label>}{index === 3 && <DropdownMenu.Label>{t('Pulse updates')}</DropdownMenu.Label>}<DropdownMenu.CheckboxItem disabled={pending} checked={events.includes(event)} onCheckedChange={checked => void updateEvent(event, checked)} onSelect={event => event.preventDefault()}><span className="sidebar-team-subscribe-check"><DropdownMenu.ItemIndicator><Check size={10}/></DropdownMenu.ItemIndicator></span><span>{t(label)}</span></DropdownMenu.CheckboxItem></div>)}
        </DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
        const href = label === 'Team settings' ? settingsPath(data.workspace.urlKey, 'team', team.key) : label === 'Open archive' ? teamArchivePath(data.workspace.urlKey, team.key) : label === 'Configure Slack notifications…' ? settingsPath(data.workspace.urlKey, 'team', team.key, 'notifications') : undefined
        const body = <>{icon(label)}<span>{t(label)}</span>{(label === 'Favorite' || label === 'Unfavorite') && <kbd>⌥ F</kbd>}{label === 'Copy URL' && <kbd>⌘ ⇧ ,</kbd>}</>
        return href ? <DropdownMenu.Item key={label} asChild><NavLink to={href}>{body}</NavLink></DropdownMenu.Item> : <DropdownMenu.Item key={label} disabled={label === 'Leave team…' && (Boolean(leaveReason) || leaving)} title={label === 'Leave team…' && leaveReason ? t(leaveReason) : undefined} onSelect={() => select(label)}>{body}</DropdownMenu.Item>
      })}</div>)}
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}
