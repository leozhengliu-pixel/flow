import { useState, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, Check, ChevronRight, Clipboard, Clock3, Copy, FileClock, History, MessageSquare as Slack, MessageSquareText, Star, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

type NotificationRules = {
  issueAdded: boolean
  issueCompleted: boolean
  projectChanges: boolean
  customerRequest: boolean
  projectUpdate: boolean
  pulseUpdates: boolean
  schedule: 'none' | 'weekly' | 'biweekly' | 'monthly'
}

const DEFAULT_RULES: NotificationRules = {
  issueAdded: false,
  issueCompleted: false,
  projectChanges: true,
  customerRequest: true,
  projectUpdate: true,
  pulseUpdates: true,
  schedule: 'none',
}

export function ProjectNotificationMenu({ projectId, subscribed, onSubscribedChange }: { projectId: string; subscribed: boolean; onSubscribedChange: (subscribed: boolean) => void }) {
  const [rules, setRules] = useStoredRules(`flow:project:${projectId}:notification-rules`)
  const change = <K extends keyof NotificationRules>(key: K, value: NotificationRules[K]) => {
    const next = { ...rules, [key]: value }
    setRules(next)
    if (key === 'pulseUpdates') onSubscribedChange(Boolean(value))
  }
  return <Popover.Root>
    <Popover.Trigger asChild><button aria-label="Setup project notifications" className="project-detail-page__header-action" data-active={subscribed} title="Setup project notifications" type="button"><Bell size={14}/></button></Popover.Trigger>
    <Popover.Portal><Popover.Content align="end" className="project-notifications" collisionPadding={10} sideOffset={5}>
      <NotificationSection title="Send inbox notifications for">
        <NotificationCheck checked={rules.issueAdded} label="An issue is added to the project" onChange={value => change('issueAdded', value)}/>
        <NotificationCheck checked={rules.issueCompleted} label="An issue is marked completed or canceled" onChange={value => change('issueCompleted', value)}/>
        <NotificationCheck checked={rules.projectChanges} label="Comments and changes to project description" onChange={value => change('projectChanges', value)}/>
        <NotificationCheck checked={rules.customerRequest} label="A customer request is added" onChange={value => change('customerRequest', value)}/>
        <NotificationCheck checked={rules.projectUpdate} label="New project update is posted" onChange={value => change('projectUpdate', value)}/>
      </NotificationSection>
      <NotificationSection title="Pulse updates"><NotificationCheck checked={rules.pulseUpdates} label="Subscribe to project updates" onChange={value => change('pulseUpdates', value)}/></NotificationSection>
      <section className="project-notifications__schedule"><div><strong>Update schedule</strong><span>{scheduleLabel(rules.schedule)}</span></div><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button">Change</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={5}>{(['none','weekly','biweekly','monthly'] as const).map(schedule => <DropdownMenu.Item key={schedule} onSelect={() => change('schedule', schedule)}><Clock3 size={13}/><span>{scheduleLabel(schedule)}</span>{rules.schedule === schedule && <Check size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></section>
      <section className="project-notifications__slack"><Slack size={15}/><strong>Slack notifications</strong><button onClick={() => toast.info('Slack integration is not connected in this workspace.')} type="button">Connect</button></section>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

export function ProjectActionsMenu({ favorited, onDelete, onFavorite, onShowActivity, onSubscribe, subscribed }: { favorited: boolean; onDelete: () => void; onFavorite: () => void; onShowActivity: () => void; onSubscribe: (subscribed: boolean) => void; subscribed: boolean }) {
  const [query, setQuery] = useState('')
  const visible = (label: string) => !query || label.toLowerCase().includes(query.toLowerCase())
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}><DropdownMenu.Trigger asChild><button aria-label="Project actions" className="project-detail-page__header-action" type="button"><span className="project-detail-page__ellipsis">•••</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu project-detail-page__actions-menu" sideOffset={5}>
    <div className="project-detail-page__menu-search"><input aria-label="Filter project actions" autoFocus onChange={event => setQuery(event.target.value)} placeholder="Filter…" value={query}/></div>
    {visible('Copy') && <DropdownMenu.Sub><DropdownMenu.SubTrigger><Copy size={14}/><span>Copy</span><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu" sideOffset={6}><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(location.href).then(() => toast.success('Project URL copied'))}><Clipboard size={14}/><span>Copy page URL</span></DropdownMenu.Item><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(location.pathname).then(() => toast.success('Project path copied'))}><Copy size={14}/><span>Copy project path</span></DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    <DropdownMenu.Separator/>
    {visible('Favorite') && <DropdownMenu.Item onSelect={onFavorite}><Star fill={favorited ? 'currentColor' : 'none'} size={14}/><span>{favorited ? 'Unfavorite' : 'Favorite'}</span><kbd>⌥ F</kbd></DropdownMenu.Item>}
    {visible('Subscribe') && <DropdownMenu.Sub><DropdownMenu.SubTrigger><Bell size={14}/><span>Subscribe</span><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu" sideOffset={6}><DropdownMenu.Item onSelect={() => onSubscribe(true)}><span>Subscribe to updates</span>{subscribed && <Check size={13}/>}</DropdownMenu.Item><DropdownMenu.Item onSelect={() => onSubscribe(false)}><span>Unsubscribe</span>{!subscribed && <Check size={13}/>}</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    {visible('Remind me') && <DropdownMenu.Sub><DropdownMenu.SubTrigger><Clock3 size={14}/><span>Remind me</span><kbd>⇧ H</kbd><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu" sideOffset={6}>{['Later today','Tomorrow','Next week'].map(label => <DropdownMenu.Item key={label} onSelect={() => toast.success(`Reminder set: ${label.toLowerCase()}`)}><span>{label}</span></DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    <DropdownMenu.Separator/>
    {visible('Change update schedule') && <DropdownMenu.Item onSelect={() => document.querySelector<HTMLElement>('[aria-label="Setup project notifications"]')?.click()}><FileClock size={14}/><span>Change update schedule…</span></DropdownMenu.Item>}
    {visible('Configure Slack notifications') && <DropdownMenu.Item onSelect={() => toast.info('Slack integration is not connected in this workspace.')}><Slack size={14}/><span>Configure Slack notifications…</span></DropdownMenu.Item>}
    <DropdownMenu.Separator/>
    {visible('Show description history') && <DropdownMenu.Item onSelect={() => toast.info('No earlier description versions.')}><History size={14}/><span>Show description history</span></DropdownMenu.Item>}
    {visible('Show updates and activity') && <DropdownMenu.Item onSelect={onShowActivity}><MessageSquareText size={14}/><span>Show updates and activity</span><kbd>⌘ U</kbd></DropdownMenu.Item>}
    <DropdownMenu.Separator/>
    {visible('Delete') && <DropdownMenu.Item className="is-danger" onSelect={onDelete}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function NotificationSection({ children, title }: { children: ReactNode; title: string }) { return <section className="project-notifications__section"><h2>{title}</h2>{children}</section> }
function NotificationCheck({ checked, label, onChange }: { checked: boolean; label: string; onChange: (checked: boolean) => void }) { return <label><span>{label}</span><button aria-checked={checked} aria-label={label} data-checked={checked} onClick={() => onChange(!checked)} role="checkbox" type="button">{checked && <Check size={11}/>}</button></label> }
function scheduleLabel(schedule: NotificationRules['schedule']) { return ({ none: 'No expectation for updates', weekly: 'Weekly', biweekly: 'Every two weeks', monthly: 'Monthly' })[schedule] }
function useStoredRules(key: string) {
  const [rules, setRulesState] = useState<NotificationRules>(() => { try { return { ...DEFAULT_RULES, ...JSON.parse(localStorage.getItem(key) ?? '{}') } } catch { return DEFAULT_RULES } })
  const setRules = (next: NotificationRules) => { localStorage.setItem(key, JSON.stringify(next)); setRulesState(next) }
  return [rules, setRules] as const
}
