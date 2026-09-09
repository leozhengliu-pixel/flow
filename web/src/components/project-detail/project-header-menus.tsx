import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Clipboard, Clock3, Copy, History, Link2, MessageSquareText, Trash2 } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import type { Project, ProjectUpdateSchedule, Subscription } from '@/types/flow'
import { NotificationCheckbox, NotificationOptionSection } from '@/components/ui/notification-controls'
import { FlowFavoriteIcon, FlowOptionsIcon } from '@/components/issue/flow-header-icons'
import { ReminderChoices } from '@/components/issue/issue-options-menu'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { ProjectMenuItem, ProjectSubmenu } from './project-menu-primitives'
import { ProjectUpdateScheduleDialog } from './project-update-schedule-dialog'
import { projectScheduleLabel } from './project-schedule'
import { SubscriptionIcon } from '@/components/ui/view-action-icons'
import { SlackIcon } from '@/components/issue/issue-icons'

const EVENT_OPTIONS = [
  ['issueAdded', 'An issue is added to the project'],
  ['issueCompleted', 'An issue is marked completed or canceled'],
  ['projectChanges', 'Comments and changes to project description'],
  ['customerRequest', 'A customer request is added'],
  ['projectUpdate', 'New project update is posted'],
] as const

export function ProjectNotificationMenu({ onOpenChange, open, project, subscription, onSetEvents, onUpdate, onShowSlack }: { open: boolean; onOpenChange: (open: boolean) => void; project: Project; subscription?: Subscription; onSetEvents: (events: string[]) => Promise<void>; onShowSlack?: () => void; onUpdate: (input: { updateSchedule?: ProjectUpdateSchedule }) => Promise<void> }) {
  const { t } = useI18n()
  const [scheduleOpen,setScheduleOpen] = useState(false)
  const [events,setEvents] = useState(subscription?.events ?? [])
  const [saving,setSaving] = useState(false)
  const persisted = JSON.stringify(subscription?.events ?? [])
  useEffect(() => setEvents(JSON.parse(persisted) as string[]), [persisted])
  const changeEvent = async (eventName: string, checked: boolean) => {
    if (saving) return
    const before = events
    const next = checked ? [...new Set([...events, eventName])] : events.filter(item => item !== eventName)
    setEvents(next); setSaving(true)
    try { await onSetEvents(next) }
    catch (error) { setEvents(before); toast.error(error instanceof Error ? error.message : t('Could not update project notifications')) }
    finally { setSaving(false) }
  }
  return <>
    <Popover.Root onOpenChange={onOpenChange} open={open}>
      <Popover.Trigger asChild><button aria-label={t('Setup project notifications')} className="project-detail-page__header-action" data-active={Boolean(subscription)} type="button"><SubscriptionIcon/></button></Popover.Trigger>
      <Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className="project-notifications" collisionPadding={16} sideOffset={4}>
        <NotificationOptionSection className="project-notifications__section" title={<><span>{t('Send inbox notifications for')}</span> <span data-i18n-ignore>{project.name}</span></>}>{EVENT_OPTIONS.map(([eventName,label]) => <NotificationCheckbox disabled={saving} checked={events.includes(eventName)} key={eventName} label={t(label)} onChange={checked => void changeEvent(eventName,checked)}/>)}</NotificationOptionSection>
        <NotificationOptionSection className="project-notifications__section" title={t('Pulse updates')}><NotificationCheckbox disabled={saving} checked={events.includes('pulse')} label={t('Subscribe to project updates')} onChange={checked => void changeEvent('pulse',checked)}/></NotificationOptionSection>
        <section className="project-notifications__schedule"><div><strong>{t('Update schedule')}</strong><span>{t(projectScheduleLabel(project))}</span></div><button type="button" onClick={() => { onOpenChange(false); setScheduleOpen(true) }}>{t('Change')}</button></section>
        {onShowSlack && <section className="project-notifications__slack"><strong>{t('Slack notifications')}</strong><button type="button" onClick={onShowSlack}>{t(project.slackChannelId ? 'Change' : 'Connect')}</button></section>}
      </Popover.Content></Popover.Portal>
    </Popover.Root>
    <ProjectUpdateScheduleDialog open={scheduleOpen} onOpenChange={setScheduleOpen} project={project} onSave={updateSchedule => onUpdate({updateSchedule})}/>
  </>
}

export function ProjectActionsMenu({ project, favorited, onDelete, onFavorite, onRemind, onShowActivity, onShowHistory, onShowNotifications, onSetEvents, subscription, onUpdateSchedule, onShowSlack }: { project: Project; favorited: boolean; onDelete: () => void; onFavorite: () => void; onRemind: (remindAt: string) => Promise<void>; onShowActivity: () => void; onShowHistory: () => void; onShowNotifications: () => void; onSetEvents: (events: string[]) => Promise<void>; subscription?: Subscription; onUpdateSchedule?: (schedule: ProjectUpdateSchedule) => Promise<void>; onShowSlack?: () => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [customReminder, setCustomReminder] = useState(false)
  const [reminderAt, setReminderAt] = useState('')
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [events, setEvents] = useState(subscription?.events ?? [])
  const persistedEvents = JSON.stringify(subscription?.events ?? [])
  useEffect(() => setEvents(JSON.parse(persistedEvents) as string[]), [persistedEvents])
  const visible = (label: string) => !query || `${label} ${t(label)}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())
  const markdown = `# ${project.name}\n\n${project.summary}${project.description ? `\n\n${project.description}` : ''}`
  const copy = (value: string) => void navigator.clipboard.writeText(value).then(() => toast.success(t('Copied to clipboard'))).catch(() => toast.error(t('Could not copy to clipboard')))
  const remind = async (value: string) => {
    setSaving(true)
    try { await onRemind(value); setCustomReminder(false); toast.success(t('Reminder set')) }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not set reminder')) }
    finally { setSaving(false) }
  }
  const toggleEvent = async (eventName: string) => {
    if (saving) return
    const previous = events
    const next = events.includes(eventName) ? events.filter(item => item !== eventName) : [...events,eventName]
    setEvents(next); setSaving(true)
    try { await onSetEvents(next) }
    catch (error) { setEvents(previous); toast.error(error instanceof Error ? error.message : t('Could not update project notifications')) }
    finally { setSaving(false) }
  }
  const favoriteLabel = favorited ? 'Unfavorite' : 'Favorite'
  const groupVisible = (labels: string[]) => labels.some(visible)
  const groups = [
    ['Copy'], [favoriteLabel,'Subscribe','Remind me'], ['Change update schedule…',...(onShowSlack ? ['Configure Slack notifications…'] : [])],
    ['Show description history','Show updates and activity'], ['Delete'],
  ]
  const separator = (index: number) => groupVisible(groups[index]) && groups.slice(0,index).some(groupVisible) ? <DropdownMenu.Separator/> : null
  return <>
    <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}>
      <DropdownMenu.Trigger asChild><button aria-label={t('Project actions')} className="project-detail-page__header-action" type="button"><FlowOptionsIcon/></button></DropdownMenu.Trigger>
      <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" aria-label={t('Project actions')} align="start" className="project-action-menu project-action-menu--header" sideOffset={4} collisionPadding={16}>
        <div className={`project-action-menu__search${query ? '' : ' is-hidden'}`}><input aria-label={t('Filter project actions')} autoFocus onChange={event => setQuery(event.target.value)} onKeyDown={event => {
          event.stopPropagation()
          if (event.key !== 'ArrowDown' && event.key !== 'Enter') return
          const firstItem = event.currentTarget.closest('[role="menu"]')?.querySelector<HTMLElement>('[role="menuitem"]:not([data-disabled])')
          if (!firstItem) return
          event.preventDefault()
          if (event.key === 'Enter') firstItem.click()
          else firstItem.focus()
        }} placeholder={t('Filter…')} value={query}/></div>
        {visible('Copy') && <ProjectSubmenu label="Copy" icon={<Copy size={16}/>}>
          <ProjectMenuItem icon={<Link2/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => copy(location.href)}/>
          <ProjectMenuItem icon={<Clipboard/>} label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => copy(project.name)}/>
          <ProjectMenuItem icon={<Link2/>} label="Copy title as link" shortcut="⌘ C" onSelect={() => copy(`[${project.name}](${location.href})`)}/>
          <ProjectMenuItem icon={<Copy/>} label="Copy overview as Markdown" shortcut="⌘ ⌥ C" onSelect={() => copy(markdown)}/>
        </ProjectSubmenu>}
        {separator(1)}
        {visible(favoriteLabel) && <ProjectMenuItem label={favoriteLabel} icon={<FlowFavoriteIcon/>} shortcut="⌥ F" onSelect={onFavorite}/>}
        {visible('Subscribe') && <ProjectSubmenu label="Subscribe" icon={<SubscriptionIcon/>}>
          {EVENT_OPTIONS.map(([eventName,label]) => <DropdownMenu.CheckboxItem key={eventName} checked={events.includes(eventName)} disabled={saving} onSelect={event => event.preventDefault()} onCheckedChange={() => void toggleEvent(eventName)}><span className="project-menu-checkbox">{events.includes(eventName) && <CheckboxMark/>}</span><span className="project-menu-label">{t(label)}</span></DropdownMenu.CheckboxItem>)}
          <DropdownMenu.CheckboxItem checked={events.includes('pulse')} disabled={saving} onSelect={event => event.preventDefault()} onCheckedChange={() => void toggleEvent('pulse')}><span className="project-menu-checkbox">{events.includes('pulse') && <CheckboxMark/>}</span><span className="project-menu-label">{t('Subscribe to project updates in Pulse')}</span></DropdownMenu.CheckboxItem>
        </ProjectSubmenu>}
        {visible('Remind me') && <ProjectSubmenu label="Remind me" icon={<Clock3 size={16}/>} shortcut="⇧ H" searchable>{close => <ReminderChoices onChoose={date => { close(); void remind(date.toISOString()) }} onCustom={() => { close(); setCustomReminder(true) }}/>}</ProjectSubmenu>}
        {separator(2)}
        {visible('Change update schedule…') && <ProjectMenuItem label="Change update schedule…" icon={<ViewGlyph icon="ClockOutline" color="currentColor"/>} onSelect={() => { if (!onUpdateSchedule) { onShowNotifications(); return }; setScheduleOpen(true) }}/>}
        {onShowSlack && visible('Configure Slack notifications…') && <ProjectMenuItem label="Configure Slack notifications…" icon={<SlackIcon size={16}/>} onSelect={onShowSlack}/>}
        {separator(3)}
        {visible('Show description history') && <ProjectMenuItem label="Show description history" icon={<History size={16}/>} onSelect={onShowHistory}/>}
        {visible('Show updates and activity') && <ProjectMenuItem label="Show updates and activity" icon={<MessageSquareText size={16}/>} shortcut="⌘ U" onSelect={onShowActivity}/>}
        {separator(4)}
        {visible('Delete') && <ProjectMenuItem label="Delete" icon={<Trash2 size={16}/>} onSelect={onDelete}/>}
        {!groups.some(groupVisible) && <div className="project-action-menu__empty">{t('No results')}</div>}
      </DropdownMenu.Content></DropdownMenu.Portal>
    </DropdownMenu.Root>
    <Dialog.Root open={customReminder} onOpenChange={setCustomReminder}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="project-detail-page__form-dialog" aria-describedby={undefined}><Dialog.Title>{t('Remind me')}</Dialog.Title><DateTimeControl label={t('Reminder time')} mode="datetime" value={reminderAt} onChange={setReminderAt}/><footer><Dialog.Close asChild><button type="button">{t('Cancel')}</button></Dialog.Close><button className="is-primary" type="button" disabled={saving || !reminderAt || new Date(reminderAt).getTime() <= Date.now()} onClick={() => void remind(new Date(reminderAt).toISOString())}>{t('Set reminder')}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
    {onUpdateSchedule && <ProjectUpdateScheduleDialog open={scheduleOpen} onOpenChange={setScheduleOpen} project={project} onSave={onUpdateSchedule}/>}
  </>
}

export function ProjectDescriptionHistoryDialog({ onOpenChange, open, project }: { onOpenChange: (open: boolean) => void; open: boolean; project: Project }) {
  const revisions = project.descriptionRevisions ?? []
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="project-description-history"><header><Dialog.Title>Description history</Dialog.Title><Dialog.Close aria-label="Close description history">×</Dialog.Close></header><div className="project-description-history__list">{revisions.length ? revisions.map(revision => <article key={revision.id}><header><strong data-i18n-ignore>{revision.author.displayName}</strong><time>{formatDistanceToNowStrict(new Date(revision.createdAt), { addSuffix: true })}</time></header><p data-i18n-ignore>{revision.description || 'No description'}</p><small>{format(new Date(revision.createdAt), 'PPpp')}</small></article>) : <div className="project-description-history__empty"><History size={20}/><strong>No earlier description versions</strong><span>Previous descriptions will appear here after an edit.</span></div>}</div></Dialog.Content></Dialog.Portal></Dialog.Root>
}
