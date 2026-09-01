import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, CalendarClock, Check, ChevronRight, Clipboard, Clock3, Download, FileClock, Link2, Plus, Search, Star, Trash2, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { Initiative, InitiativeMutationInput, InitiativeUpdateSchedule, Project } from '@/types/flow'
import { NotificationCheckbox, NotificationOptionSection } from '@/components/ui/notification-controls'
import { SelectControl } from '@/components/ui/select-control'
import { DateTimeControl } from '@/components/ui/date-time-control'
import './initiative-controls.css'

const DEFAULT_RULES = { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }
const DEFAULT_SCHEDULE: InitiativeUpdateSchedule = { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' }
type Update = (input: InitiativeMutationInput) => void | Promise<unknown>

export function InitiativeNotificationMenu({ initiative, onUpdate }: { initiative: Initiative; onUpdate: Update }) {
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const rules = initiative.notificationRules ?? DEFAULT_RULES
  const changeRule = (field: keyof typeof rules, value: boolean) => void onUpdate({ notificationRules: { ...rules, [field]: value } })
  return <>
    <Popover.Root><Popover.Trigger asChild><button aria-label="Setup initiative notifications" className={initiative.subscribed ? 'is-active' : ''} type="button"><Bell size={14}/></button></Popover.Trigger><Popover.Portal><Popover.Content align="end" alignOffset={-2} className="li-notifications" collisionPadding={10} sideOffset={4}>
      <NotificationOptionSection className="li-notifications__section" title="Send inbox notifications for"><NotificationCheckbox checked={rules.descriptionChanges} label="Comments and changes to initiative description" onChange={value => changeRule('descriptionChanges', value)}/><NotificationCheckbox checked={rules.newUpdate} label="New initiative update is posted" onChange={value => changeRule('newUpdate', value)}/></NotificationOptionSection>
      <section className="li-notifications__schedule"><div><strong>Update schedule</strong><span>{scheduleLabel(initiative.updateSchedule ?? DEFAULT_SCHEDULE)}</span></div><button onClick={() => setScheduleOpen(true)} type="button">Change</button></section>
      <section className="li-notifications__slack"><ViewGlyph color="currentColor" icon="Slack"/><strong>Slack notifications</strong><button disabled title="Connect Slack from workspace integrations first" type="button">Connect</button></section>
    </Popover.Content></Popover.Portal></Popover.Root>
    <UpdateScheduleDialog initiative={initiative} onOpenChange={setScheduleOpen} onUpdate={onUpdate} open={scheduleOpen}/>
  </>
}

export function InitiativeActionsMenu({ initiative, onCreateReminder, onDelete, onNewUpdate, onShowActivity, onUpdate }: { initiative: Initiative; onCreateReminder: (remindAt: string) => Promise<unknown>; onDelete: () => void; onNewUpdate: () => void; onShowActivity: () => void; onUpdate: Update }) {
  const [historyOpen, setHistoryOpen] = useState(false)
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const [reminderOpen, setReminderOpen] = useState(false)
  const rules = initiative.notificationRules ?? DEFAULT_RULES
  const remind = async (date: Date) => { await onCreateReminder(date.toISOString()); toast.success('Reminder created') }
  return <>
    <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Initiative actions" type="button"><span className="li-ellipsis">•••</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="li-menu li-actions-menu" sideOffset={4}>
      <DropdownMenu.Sub><DropdownMenu.SubTrigger><Clipboard size={14}/>Copy<ChevronRight className="li-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu" sideOffset={5}><DropdownMenu.Item onSelect={() => copyText(location.href, 'Initiative URL copied')}><Link2 size={14}/>Copy URL</DropdownMenu.Item><DropdownMenu.Item onSelect={() => copyText(initiative.name, 'Initiative title copied')}><Clipboard size={14}/>Copy title</DropdownMenu.Item><DropdownMenu.Item onSelect={() => copyText(`[${initiative.name}](${location.href})`, 'Linked title copied')}><Link2 size={14}/>Copy title as link</DropdownMenu.Item><DropdownMenu.Item onSelect={() => copyText(overviewMarkdown(initiative), 'Overview copied as Markdown')}><Clipboard size={14}/>Copy overview as Markdown</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Separator/>
      <DropdownMenu.Item onSelect={() => onUpdate({ favorite: !initiative.favorite })}><Star fill={initiative.favorite ? 'currentColor' : 'none'} size={14}/>{initiative.favorite ? 'Unfavorite' : 'Favorite'}<kbd>⌥ F</kbd></DropdownMenu.Item>
      <DropdownMenu.Sub><DropdownMenu.SubTrigger><Bell size={14}/>Subscribe<ChevronRight className="li-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu li-subscription-menu" sideOffset={5}><DropdownMenu.CheckboxItem checked={rules.descriptionChanges} onCheckedChange={value => onUpdate({ notificationRules: { ...rules, descriptionChanges: value === true } })}>{rules.descriptionChanges && <Check size={12}/>}Comments and description changes</DropdownMenu.CheckboxItem><DropdownMenu.CheckboxItem checked={rules.newUpdate} onCheckedChange={value => onUpdate({ notificationRules: { ...rules, newUpdate: value === true }, subscribed: value === true })}>{rules.newUpdate && <Check size={12}/>}New initiative updates</DropdownMenu.CheckboxItem></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Sub><DropdownMenu.SubTrigger><Clock3 size={14}/>Remind me<kbd>⇧ H</kbd><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu" sideOffset={5}><DropdownMenu.Item onSelect={() => void remind(addHours(new Date(), 1))}>In one hour</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void remind(atMorning(addDays(new Date(), 1)))}>Tomorrow</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void remind(atMorning(addDays(new Date(), 7)))}>Next week</DropdownMenu.Item><DropdownMenu.Item onSelect={() => { const date = new Date(); date.setMonth(date.getMonth() + 1); void remind(atMorning(date)) }}>Next month</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item onSelect={() => setReminderOpen(true)}><CalendarClock size={14}/>Custom…</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Separator/>
      <DropdownMenu.Item onSelect={onNewUpdate}><InitiativeUpdateGlyph/>New initiative update<kbd>N then U</kbd></DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => setScheduleOpen(true)}><Clock3 size={14}/>Change update schedule…</DropdownMenu.Item>
      <DropdownMenu.Item disabled title="Connect Slack from workspace integrations first"><ViewGlyph color="currentColor" icon="Slack"/>Configure Slack notifications…</DropdownMenu.Item>
      <DropdownMenu.Separator/>
      <DropdownMenu.Item onSelect={() => setHistoryOpen(true)}><FileClock size={14}/>Show description history</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={onShowActivity}><InitiativeUpdateGlyph/>Show updates and activity<kbd>⌘ U</kbd></DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => downloadProjectsCSV(initiative)}><Download size={14}/>Export projects as CSV…</DropdownMenu.Item>
      <DropdownMenu.Separator/>
      <DropdownMenu.Item className="danger" onSelect={onDelete}><Trash2 size={14}/>Delete</DropdownMenu.Item>
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    <InitiativeDescriptionHistoryDialog initiative={initiative} onOpenChange={setHistoryOpen} onUpdate={onUpdate} open={historyOpen}/>
    <UpdateScheduleDialog initiative={initiative} onOpenChange={setScheduleOpen} onUpdate={onUpdate} open={scheduleOpen}/>
    <ReminderDialog onCreate={onCreateReminder} onOpenChange={setReminderOpen} open={reminderOpen}/>
  </>
}

export function AddProjectMenu({ initiative, projects, onCreateNew, onUpdate }: { initiative: Initiative; projects: Project[]; onCreateNew: () => void; onUpdate: Update }) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'actions' | 'existing'>('actions')
  const toggle = (projectId: string) => void onUpdate({ projectIds: initiative.projectIds.includes(projectId) ? initiative.projectIds.filter(id => id !== projectId) : [...initiative.projectIds, projectId] })
  const options=projects.map(project=>({id:project.id,label:project.name})),command=usePropertyCommand({closeOnSelect:false,open:open&&mode==='existing',options,selectedIds:initiative.projectIds,onOpenChange:setOpen,onSelect:option=>toggle(option.id)})
  return <Popover.Root open={open} onOpenChange={nextOpen=>{setOpen(nextOpen);if(!nextOpen)setMode('actions')}}><Popover.Trigger asChild><button aria-label="Add project" type="button"><Plus size={14}/></button></Popover.Trigger><Popover.Portal><Popover.Content align="end" className="li-add-project" sideOffset={4} onOpenAutoFocus={event => event.preventDefault()}>
    {mode === 'actions' ? <><label><Search size={14}/><input autoFocus aria-label="Add…" placeholder="Add…"/></label><button onClick={() => { setOpen(false); onCreateNew() }} type="button"><Plus size={14}/>Create new project…<kbd>N then P</kbd></button><button onClick={() => setMode('existing')} type="button"><Link2 size={14}/>Add existing projects…</button></> : <><header><button aria-label="Back" onClick={() => setMode('actions')} type="button">‹</button><span>Initiative · <b data-i18n-ignore>{initiative.name}</b></span><button aria-label="Close" onClick={() => setOpen(false)} type="button"><X size={13}/></button></header><label><Search size={14}/><input ref={command.inputRef} autoFocus aria-label="Command menu" placeholder="Search projects…" value={command.query} onChange={event=>command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown}/></label><div role="listbox" aria-multiselectable="true" onKeyDown={command.onKeyDown}>{command.filteredOptions.map(option=>{const project=projects.find(item=>item.id===option.id)!;return <button aria-checked={command.isSelected(option.id)} aria-selected={command.activeId===option.id} key={option.id} onPointerMove={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)} role="option" type="button"><span className="li-picker-checkbox">{command.isSelected(option.id)&&<Check size={11}/>}</span><ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)}/><span data-i18n-ignore>{project.name}</span><small>{project.progress}%</small></button>})}</div><footer><kbd>Enter ↵</kbd> Select <span/><kbd>⌥ ↵</kbd> More actions</footer></>}
  </Popover.Content></Popover.Portal></Popover.Root>
}

function UpdateScheduleDialog({ initiative, open, onOpenChange, onUpdate }: { initiative: Initiative; open: boolean; onOpenChange: (open: boolean) => void; onUpdate: Update }) {
  const [draft, setDraft] = useState<InitiativeUpdateSchedule>(initiative.updateSchedule ?? DEFAULT_SCHEDULE)
  const [saving, setSaving] = useState(false)
  useEffect(() => { if (open) setDraft(initiative.updateSchedule ?? DEFAULT_SCHEDULE) }, [initiative.updateSchedule, open])
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content className="li-schedule-dialog"><Dialog.Title>Change update schedule</Dialog.Title><Dialog.Description>Choose when updates are expected for <strong data-i18n-ignore>{initiative.name}</strong>.</Dialog.Description><div className="li-schedule-options">{(['none', 'weekly', 'biweekly', 'monthly', 'custom', 'never'] as const).map(cadence => <label key={cadence}><input checked={draft.cadence === cadence} name="cadence" onChange={() => setDraft({ ...draft, cadence })} type="radio"/><span>{scheduleCadenceLabel(cadence)}</span></label>)}</div>{draft.cadence !== 'none' && draft.cadence !== 'never' && <div className="li-schedule-custom"><label>Weekday<SelectControl label="Weekday" value={String(draft.weekday)} onChange={value => setDraft({ ...draft, weekday: Number(value) })} options={['Monday','Tuesday','Wednesday','Thursday','Friday','Saturday','Sunday'].map((day,index)=>({value:String(index),label:day}))}/></label><label>Time range<SelectControl label="Time range" value={draft.timeRange} onChange={timeRange => setDraft({ ...draft, timeRange })} options={[{value:'09:00-12:00',label:'09:00–12:00'},{value:'12:00-15:00',label:'12:00–15:00'},{value:'15:00-18:00',label:'15:00–18:00'}]}/></label></div>}<footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button disabled={saving} onClick={() => { setSaving(true); Promise.resolve(onUpdate({ updateSchedule: draft })).then(() => onOpenChange(false)).finally(() => setSaving(false)) }} type="button">Save</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function InitiativeDescriptionHistoryDialog({ initiative, open, onOpenChange, onUpdate }: { initiative: Initiative; open: boolean; onOpenChange: (open: boolean) => void; onUpdate: Update }) {
  const revisions = initiative.descriptionHistory ?? []
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content className="li-history-dialog"><Dialog.Title>Restore version for <span data-i18n-ignore>{initiative.name}</span> initiative</Dialog.Title><Dialog.Description>Previous description versions are kept when the overview is edited.</Dialog.Description>{revisions.length ? <div className="li-history-list">{revisions.map(revision => <article key={revision.id}><header><strong data-i18n-ignore>{revision.editor.displayName || revision.editor.name}</strong><time>{new Date(revision.editedAt).toLocaleString()}</time><button onClick={() => { void onUpdate({ description: revision.description }); onOpenChange(false) }} type="button">Restore</button></header><p data-i18n-ignore>{revision.description || 'Empty description'}</p></article>)}</div> : <div className="li-history-empty">There is no history yet.</div>}<footer><Dialog.Close asChild><button type="button">Close</button></Dialog.Close></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function ReminderDialog({ open, onOpenChange, onCreate }: { open: boolean; onOpenChange: (open: boolean) => void; onCreate: (remindAt: string) => Promise<unknown> }) {
  const [value, setValue] = useState(() => toLocalInput(addHours(new Date(), 1)))
  const [saving, setSaving] = useState(false)
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content className="li-reminder-dialog"><Dialog.Title>Set reminder</Dialog.Title><Dialog.Description>Choose a date and time in your local timezone.</Dialog.Description><label>Remind me at<DateTimeControl label="Remind me at" min={toLocalInput(new Date())} mode="datetime" value={value} onChange={setValue}/></label><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button disabled={saving || !value || new Date(value) <= new Date()} onClick={() => { setSaving(true); onCreate(new Date(value).toISOString()).then(() => { toast.success('Reminder created'); onOpenChange(false) }).finally(() => setSaving(false)) }} type="button">Create reminder</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function InitiativeUpdateGlyph() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M12.1 6.45a.75.75 0 0 0-1.2-.9L8.917 8.193 7.6 6.882a1.1 1.1 0 0 0-1.898.158L3.891 9.563a.75.75 0 1 0 1.218.874l1.64-2.284 1.315 1.307a1.1 1.1 0 0 0 1.881-.137L12.1 6.45Z"/><path fillRule="evenodd" d="M1 7.4c0-2.24 0-3.36.436-4.216a4 4 0 0 1 1.748-1.748C4.04 1 5.16 1 7.4 1h1.2c2.24 0 3.36 0 4.216.436a4 4 0 0 1 1.748 1.748C15 4.04 15 5.16 15 7.4v1.2c0 2.24 0 3.36-.436 4.216a4 4 0 0 1-1.748 1.748C11.96 15 10.84 15 8.6 15H7.4c-2.24 0-3.36 0-4.216-.436a4 4 0 0 1-1.748-1.748C1 11.96 1 10.84 1 8.6V7.4Zm6.4-4.9h1.2c1.145 0 1.913.001 2.505.05.574.046.848.13 1.03.222.47.24.852.622 1.092 1.093.092.181.176.456.223 1.03.048.592.05 1.36.05 2.505v1.2c0 1.145-.002 1.913-.05 2.505-.047.574-.131.849-.223 1.03a2.5 2.5 0 0 1-1.092 1.092c-.182.093-.456.176-1.03.223-.592.048-1.36.05-2.505.05H7.4c-1.145 0-1.913-.002-2.505-.05-.574-.047-.849-.13-1.03-.223a2.5 2.5 0 0 1-1.092-1.092c-.093-.181-.176-.456-.223-1.03-.048-.592-.05-1.36-.05-2.505V7.4c0-1.145.002-1.913.05-2.505.047-.574.13-.849.223-1.03A2.5 2.5 0 0 1 3.865 2.77c.181-.092.456-.176 1.03-.222.592-.049 1.36-.05 2.505-.05Z" clipRule="evenodd"/></svg> }
function scheduleLabel(schedule: InitiativeUpdateSchedule) { return schedule.cadence === 'custom' ? `Custom · ${['Mon','Tue','Wed','Thu','Fri','Sat','Sun'][schedule.weekday]} ${schedule.timeRange}` : scheduleCadenceLabel(schedule.cadence) }
function scheduleCadenceLabel(cadence: InitiativeUpdateSchedule['cadence']) { return ({ none: 'No expectation for updates', weekly: 'Weekly', biweekly: 'Every two weeks', monthly: 'Monthly', custom: 'Custom schedule', never: 'Never' })[cadence] }
function downloadProjectsCSV(initiative: Initiative) { const blob = new Blob([`initiative,projectId\n${initiative.projectIds.map(id => `"${initiative.name.replaceAll('"', '""')}",${id}`).join('\n')}`], { type: 'text/csv' }); const url = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${initiative.slugId}-projects.csv`; anchor.click(); URL.revokeObjectURL(url) }
function overviewMarkdown(initiative: Initiative) { return `# ${initiative.name}\n\n${initiative.summary ? `${initiative.summary}\n\n` : ''}${initiative.description}` }
function copyText(value: string, message: string) { void navigator.clipboard.writeText(value).then(() => toast.success(message)) }
function addHours(date: Date, hours: number) { const result = new Date(date); result.setHours(result.getHours() + hours); return result }
function addDays(date: Date, days: number) { const result = new Date(date); result.setDate(result.getDate() + days); return result }
function atMorning(date: Date) { const result = new Date(date); result.setHours(9, 0, 0, 0); return result }
function toLocalInput(date: Date) { const offset = date.getTimezoneOffset() * 60000; return new Date(date.getTime() - offset).toISOString().slice(0, 16) }
