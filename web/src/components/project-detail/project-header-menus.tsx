import { useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, Check, ChevronRight, Clipboard, Clock3, Copy, FileClock, History, Link2, MessageSquareText, Star, Trash2 } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import type { Project, Subscription } from '@/types/flow'
import { NotificationCheckbox, NotificationOptionSection } from '@/components/ui/notification-controls'

const EVENT_OPTIONS = [
  ['issueAdded', 'An issue is added to the project'],
  ['issueCompleted', 'An issue is marked completed or canceled'],
  ['projectChanges', 'Comments and changes to project description'],
  ['projectUpdate', 'New project update is posted'],
] as const

export function ProjectNotificationMenu({ onOpenChange, open, project, subscription, onSetEvents, onUpdate }: { open: boolean; onOpenChange: (open: boolean) => void; project: Project; subscription?: Subscription; onSetEvents: (events: string[]) => Promise<void>; onUpdate: (input: { updateCadence?: Project['updateCadence'] }) => Promise<void> }) {
  const events = subscription?.events ?? []
  const changeEvent = async (eventName: string, checked: boolean) => {
    const next = checked ? [...new Set([...events, eventName])] : events.filter(item => item !== eventName)
    await onSetEvents(next)
  }
  return <Popover.Root onOpenChange={onOpenChange} open={open}>
    <Popover.Trigger asChild><button aria-label="Setup project notifications" className="project-detail-page__header-action" data-active={Boolean(subscription)} title="Setup project notifications" type="button"><Bell size={14}/></button></Popover.Trigger>
    <Popover.Portal><Popover.Content align="end" className="project-notifications" collisionPadding={10} sideOffset={4}>
      <NotificationOptionSection className="project-notifications__section" title={<><span>Send inbox notifications for</span> <span data-i18n-ignore>{project.name}</span></>}>{EVENT_OPTIONS.map(([eventName, label]) => <NotificationCheckbox checked={events.includes(eventName)} key={eventName} label={label} onChange={checked => void changeEvent(eventName, checked)}/>)}</NotificationOptionSection>
      <section className="project-notifications__schedule"><div><strong>Update schedule</strong><span>{scheduleLabel(project.updateCadence || 'none')}</span></div><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button">Change</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={5}>{(['none','weekly','biweekly','monthly'] as const).map(schedule => <DropdownMenu.Item key={schedule} onSelect={() => void onUpdate({ updateCadence: schedule })}><Clock3 size={13}/><span>{scheduleLabel(schedule)}</span>{project.updateCadence === schedule && <Check size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></section>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

export function ProjectActionsMenu({ project, favorited, onDelete, onFavorite, onRemind, onShowActivity, onShowHistory, onShowNotifications, onSetEvents, subscription }: { project: Project; favorited: boolean; onDelete: () => void; onFavorite: () => void; onRemind: (remindAt: string) => Promise<void>; onShowActivity: () => void; onShowHistory: () => void; onShowNotifications: () => void; onSetEvents: (events: string[]) => Promise<void>; subscription?: Subscription }) {
  const [query, setQuery] = useState('')
  const visible = (label: string) => !query || label.toLowerCase().includes(query.toLowerCase())
  const subscribed = Boolean(subscription)
  const markdown = `# ${project.name}\n\n${project.summary}${project.description ? `\n\n${project.description}` : ''}`
  const reminders = [['Later today', nextReminder(5)], ['Tomorrow', nextReminder(24)], ['Next week', nextReminder(24 * 7)]] as const
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}><DropdownMenu.Trigger asChild><button aria-label="Project actions" className="project-detail-page__header-action" type="button"><span className="project-detail-page__ellipsis">•••</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu project-detail-page__actions-menu" sideOffset={5}>
    <div className="project-detail-page__menu-search"><input aria-label="Filter project actions" autoFocus onChange={event => setQuery(event.target.value)} onKeyDown={event => {
      if (event.key !== 'ArrowDown' && event.key !== 'Enter') return
      const firstItem = event.currentTarget.closest('[role="menu"]')?.querySelector<HTMLElement>('[role="menuitem"]:not([data-disabled])')
      if (!firstItem) return
      event.preventDefault()
      if (event.key === 'Enter') firstItem.click()
      else firstItem.focus()
    }} placeholder="Filter…" value={query}/></div>
    {visible('Copy') && <DropdownMenu.Sub><DropdownMenu.SubTrigger><Copy size={14}/><span>Copy</span><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu project-detail-page__copy-menu" sideOffset={6}><CopyItem icon={<Link2 size={14}/>} label="Copy URL" value={location.href}/><CopyItem icon={<Clipboard size={14}/>} label="Copy title" value={project.name}/><CopyItem icon={<Link2 size={14}/>} label="Copy title as link" value={`[${project.name}](${location.href})`}/><CopyItem icon={<Copy size={14}/>} label="Copy overview as Markdown" value={markdown}/></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    <DropdownMenu.Separator/>
    {visible('Favorite') && <DropdownMenu.Item onSelect={onFavorite}><Star fill={favorited ? 'currentColor' : 'none'} size={14}/><span>{favorited ? 'Unfavorite' : 'Favorite'}</span><kbd>⌥ F</kbd></DropdownMenu.Item>}
    {visible('Subscribe') && <DropdownMenu.Sub><DropdownMenu.SubTrigger><Bell size={14}/><span>Subscribe</span><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu" sideOffset={6}><DropdownMenu.Item onSelect={() => void onSetEvents(EVENT_OPTIONS.map(([eventName]) => eventName))}><span>Subscribe to all project events</span>{subscribed && <Check size={13}/>}</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void onSetEvents([])}><span>Unsubscribe</span>{!subscribed && <Check size={13}/>}</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    {visible('Remind me') && <DropdownMenu.Sub><DropdownMenu.SubTrigger><Clock3 size={14}/><span>Remind me</span><kbd>⇧ H</kbd><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu" sideOffset={6}>{reminders.map(([label, remindAt]) => <DropdownMenu.Item key={label} onSelect={() => void onRemind(remindAt).then(() => toast.success(`Reminder set for ${label.toLowerCase()}`))}><span>{label}</span></DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    <DropdownMenu.Separator/>
    {visible('Change update schedule') && <DropdownMenu.Item onSelect={onShowNotifications}><FileClock size={14}/><span>Change update schedule…</span></DropdownMenu.Item>}
    <DropdownMenu.Separator/>
    {visible('Show description history') && <DropdownMenu.Item onSelect={onShowHistory}><History size={14}/><span>Show description history</span></DropdownMenu.Item>}
    {visible('Show updates and activity') && <DropdownMenu.Item onSelect={onShowActivity}><MessageSquareText size={14}/><span>Show updates and activity</span><kbd>⌘ U</kbd></DropdownMenu.Item>}
    <DropdownMenu.Separator/>
    {visible('Delete') && <DropdownMenu.Item className="is-danger" onSelect={onDelete}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

export function ProjectDescriptionHistoryDialog({ onOpenChange, open, project }: { onOpenChange: (open: boolean) => void; open: boolean; project: Project }) {
  const revisions = project.descriptionRevisions ?? []
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-description-history"><header><Dialog.Title>Description history</Dialog.Title><Dialog.Close aria-label="Close description history">×</Dialog.Close></header><div className="project-description-history__list">{revisions.length ? revisions.map(revision => <article key={revision.id}><header><strong data-i18n-ignore>{revision.author.displayName}</strong><time>{formatDistanceToNowStrict(new Date(revision.createdAt), { addSuffix: true })}</time></header><p data-i18n-ignore>{revision.description || 'No description'}</p><small>{format(new Date(revision.createdAt), 'PPpp')}</small></article>) : <div className="project-description-history__empty"><History size={20}/><strong>No earlier description versions</strong><span>Previous descriptions will appear here after an edit.</span></div>}</div></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function CopyItem({ icon, label, value }: { icon: ReactNode; label: string; value: string }) { return <DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(value).then(() => toast.success(`${label} copied`))}>{icon}<span>{label}</span></DropdownMenu.Item> }
function scheduleLabel(schedule: Project['updateCadence']) { return ({ none: 'No expectation for updates', weekly: 'Weekly', biweekly: 'Every two weeks', monthly: 'Monthly' })[schedule] }
function nextReminder(hours: number) { return new Date(Date.now() + hours * 60 * 60 * 1000).toISOString() }
