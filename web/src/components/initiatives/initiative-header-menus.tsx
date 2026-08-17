import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, Check, ChevronRight, Clipboard, Clock3, Download, FileClock, Link2, Plus, Search, Star, Trash2, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import type { Initiative, InitiativeMutationInput, Project } from '@/types/flow'

type Rules = {
  descriptionChanges: boolean
  newUpdate: boolean
  pulseUpdates: boolean
  allProjectUpdates: boolean
  schedule: 'none' | 'weekly' | 'biweekly' | 'monthly'
}

const DEFAULT_RULES: Rules = { descriptionChanges: true, newUpdate: true, pulseUpdates: true, allProjectUpdates: false, schedule: 'none' }

export function InitiativeNotificationMenu({ initiative, onUpdate }: { initiative: Initiative; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown> }) {
  const key = `flow:initiative:${initiative.id}:notification-rules`
  const [rules, setRulesState] = useState<Rules>(() => { try { return { ...DEFAULT_RULES, ...JSON.parse(localStorage.getItem(key) ?? '{}') } } catch { return DEFAULT_RULES } })
  const change = <K extends keyof Rules>(field: K, value: Rules[K]) => {
    const next = { ...rules, [field]: value }
    setRulesState(next); localStorage.setItem(key, JSON.stringify(next))
    if (field === 'pulseUpdates') void onUpdate({ subscribed: Boolean(value) })
  }
  return <Popover.Root><Popover.Trigger asChild><button aria-label="Setup initiative notifications" className={initiative.subscribed ? 'is-active' : ''} type="button"><Bell size={14}/></button></Popover.Trigger><Popover.Portal><Popover.Content align="end" alignOffset={-2} className="li-notifications" collisionPadding={10} sideOffset={4}>
    <NotificationSection title="Send inbox notifications for"><NotificationCheck checked={rules.descriptionChanges} label="Comments and changes to initiative description" onChange={value => change('descriptionChanges', value)}/><NotificationCheck checked={rules.newUpdate} label="New initiative update is posted" onChange={value => change('newUpdate', value)}/></NotificationSection>
    <NotificationSection title="Pulse updates"><NotificationCheck checked={rules.pulseUpdates} label="Subscribe to initiative updates" onChange={value => change('pulseUpdates', value)}/><NotificationCheck checked={rules.allProjectUpdates} label="Subscribe to all project updates" onChange={value => change('allProjectUpdates', value)}/></NotificationSection>
    <section className="li-notifications__schedule"><div><strong>Update schedule</strong><span>{scheduleLabel(rules.schedule)}</span></div><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button">Change</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu" sideOffset={5}>{(['none', 'weekly', 'biweekly', 'monthly'] as const).map(schedule => <DropdownMenu.Item key={schedule} onSelect={() => change('schedule', schedule)}><Clock3 size={13}/>{scheduleLabel(schedule)}{rules.schedule === schedule && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></section>
    <section className="li-notifications__slack"><ViewGlyph color="currentColor" icon="Slack"/><strong>Slack notifications</strong><button onClick={() => toast.info('Slack integration is not connected in this workspace.')} type="button">Connect</button></section>
  </Popover.Content></Popover.Portal></Popover.Root>
}

export function InitiativeActionsMenu({ initiative, onDelete, onNewUpdate, onShowActivity, onUpdate }: { initiative: Initiative; onDelete: () => void; onNewUpdate: () => void; onShowActivity: () => void; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown> }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Initiative actions" type="button"><span className="li-ellipsis">•••</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="li-menu li-actions-menu" sideOffset={4}>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><Clipboard size={14}/>Copy<ChevronRight className="li-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu" sideOffset={5}><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(location.href).then(() => toast.success('Initiative URL copied'))}><Clipboard size={14}/>Copy page URL</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(location.pathname).then(() => toast.success('Initiative path copied'))}><Link2 size={14}/>Copy initiative path</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Separator/>
    <DropdownMenu.Item onSelect={() => onUpdate({ favorite: !initiative.favorite })}><Star fill={initiative.favorite ? 'currentColor' : 'none'} size={14}/>{initiative.favorite ? 'Unfavorite' : 'Favorite'}<kbd>⌥ F</kbd></DropdownMenu.Item>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><Bell size={14}/>Subscribe<ChevronRight className="li-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu" sideOffset={5}><DropdownMenu.Item onSelect={() => onUpdate({ subscribed: true })}>Subscribe to updates{initiative.subscribed && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item><DropdownMenu.Item onSelect={() => onUpdate({ subscribed: false })}>Unsubscribe{!initiative.subscribed && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><Clock3 size={14}/>Remind me<kbd>⇧ H</kbd><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu" sideOffset={5}>{['Later today', 'Tomorrow', 'Next week'].map(label => <DropdownMenu.Item key={label} onSelect={() => toast.success(`Reminder set: ${label.toLowerCase()}`)}>{label}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Separator/>
    <DropdownMenu.Item onSelect={onNewUpdate}><InitiativeUpdateGlyph/>New initiative update<kbd>N then U</kbd></DropdownMenu.Item>
    <DropdownMenu.Item onSelect={() => document.querySelector<HTMLElement>('[aria-label="Setup initiative notifications"]')?.click()}><Clock3 size={14}/>Change update schedule…</DropdownMenu.Item>
    <DropdownMenu.Item onSelect={() => toast.info('Slack integration is not connected in this workspace.')}><ViewGlyph color="currentColor" icon="Slack"/>Configure Slack notifications…</DropdownMenu.Item>
    <DropdownMenu.Separator/>
    <DropdownMenu.Item onSelect={() => toast.info('No earlier description versions.')}><FileClock size={14}/>Show description history</DropdownMenu.Item>
    <DropdownMenu.Item onSelect={onShowActivity}><InitiativeUpdateGlyph/>Show updates and activity<kbd>⌘ U</kbd></DropdownMenu.Item>
    <DropdownMenu.Item onSelect={() => downloadProjectsCSV(initiative)}><Download size={14}/>Export projects as CSV…</DropdownMenu.Item>
    <DropdownMenu.Separator/>
    <DropdownMenu.Item className="danger" onSelect={onDelete}><Trash2 size={14}/>Delete</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

export function AddProjectMenu({ initiative, projects, onCreateNew, onUpdate }: { initiative: Initiative; projects: Project[]; onCreateNew: () => void; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown> }) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'actions' | 'existing'>('actions')
  const [query, setQuery] = useState('')
  const filtered = projects.filter(project => project.name.toLowerCase().includes(query.toLowerCase()))
  const toggle = (projectId: string) => void onUpdate({ projectIds: initiative.projectIds.includes(projectId) ? initiative.projectIds.filter(id => id !== projectId) : [...initiative.projectIds, projectId] })
  return <Popover.Root open={open} onOpenChange={nextOpen => { setOpen(nextOpen); if (!nextOpen) { setMode('actions'); setQuery('') } }}><Popover.Trigger asChild><button aria-label="Add project" type="button"><Plus size={14}/></button></Popover.Trigger><Popover.Portal><Popover.Content align="end" className="li-add-project" sideOffset={4} onOpenAutoFocus={event => event.preventDefault()}>
    {mode === 'actions' ? <><label><Search size={14}/><input autoFocus aria-label="Add…" placeholder="Add…"/></label><button onClick={() => { setOpen(false); onCreateNew() }} type="button"><Plus size={14}/>Create new project…<kbd>N then P</kbd></button><button onClick={() => setMode('existing')} type="button"><Link2 size={14}/>Add existing projects…</button></> : <><header><button aria-label="Back" onClick={() => setMode('actions')} type="button">‹</button><span>Initiative · {initiative.name}</span><button aria-label="Close" onClick={() => setOpen(false)} type="button"><X size={13}/></button></header><label><Search size={14}/><input autoFocus aria-label="Command menu" placeholder="Search projects…" value={query} onChange={event => setQuery(event.target.value)}/></label><div role="listbox" aria-multiselectable="true">{filtered.map(project => <button aria-checked={initiative.projectIds.includes(project.id)} key={project.id} onClick={() => toggle(project.id)} role="option" type="button"><span className="li-picker-checkbox">{initiative.projectIds.includes(project.id) && <Check size={11}/>}</span><ViewGlyph color={project.color} icon={project.icon || 'Project'}/><span>{project.name}</span><small>{project.progress}%</small></button>)}</div><footer><kbd>Enter ↵</kbd> Select <span/><kbd>⌥ ↵</kbd> More actions</footer></>}
  </Popover.Content></Popover.Portal></Popover.Root>
}

function NotificationSection({ children, title }: { children: ReactNode; title: string }) { return <section className="li-notifications__section"><h2>{title}</h2>{children}</section> }
function NotificationCheck({ checked, label, onChange }: { checked: boolean; label: string; onChange: (checked: boolean) => void }) { return <label><span>{label}</span><button aria-checked={checked} aria-label={label} data-checked={checked} onClick={() => onChange(!checked)} role="checkbox" type="button">{checked && <Check size={11}/>}</button></label> }
function InitiativeUpdateGlyph() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M12.1 6.45a.75.75 0 0 0-1.2-.9L8.917 8.193 7.6 6.882a1.1 1.1 0 0 0-1.898.158L3.891 9.563a.75.75 0 1 0 1.218.874l1.64-2.284 1.315 1.307a1.1 1.1 0 0 0 1.881-.137L12.1 6.45Z"/><path fillRule="evenodd" d="M1 7.4c0-2.24 0-3.36.436-4.216a4 4 0 0 1 1.748-1.748C4.04 1 5.16 1 7.4 1h1.2c2.24 0 3.36 0 4.216.436a4 4 0 0 1 1.748 1.748C15 4.04 15 5.16 15 7.4v1.2c0 2.24 0 3.36-.436 4.216a4 4 0 0 1-1.748 1.748C11.96 15 10.84 15 8.6 15H7.4c-2.24 0-3.36 0-4.216-.436a4 4 0 0 1-1.748-1.748C1 11.96 1 10.84 1 8.6V7.4Zm6.4-4.9h1.2c1.145 0 1.913.001 2.505.05.574.046.848.13 1.03.222.47.24.852.622 1.092 1.093.092.181.176.456.223 1.03.048.592.05 1.36.05 2.505v1.2c0 1.145-.002 1.913-.05 2.505-.047.574-.131.849-.223 1.03a2.5 2.5 0 0 1-1.092 1.092c-.182.093-.456.176-1.03.223-.592.048-1.36.05-2.505.05H7.4c-1.145 0-1.913-.002-2.505-.05-.574-.047-.849-.13-1.03-.223a2.5 2.5 0 0 1-1.092-1.092c-.093-.181-.176-.456-.223-1.03-.048-.592-.05-1.36-.05-2.505V7.4c0-1.145.002-1.913.05-2.505.047-.574.13-.849.223-1.03A2.5 2.5 0 0 1 3.865 2.77c.181-.092.456-.176 1.03-.222.592-.049 1.36-.05 2.505-.05Z" clipRule="evenodd"/></svg> }
function scheduleLabel(schedule: Rules['schedule']) { return ({ none: 'No expectation for updates', weekly: 'Weekly', biweekly: 'Every two weeks', monthly: 'Monthly' })[schedule] }
function downloadProjectsCSV(initiative: Initiative) { const blob = new Blob([`initiative,projectId\n${initiative.projectIds.map(id => `"${initiative.name.replaceAll('"', '""')}",${id}`).join('\n')}`], { type: 'text/csv' }); const url = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${initiative.slugId}-projects.csv`; anchor.click(); URL.revokeObjectURL(url) }
