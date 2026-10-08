import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, Check, Link2, Plus, Search, X } from 'lucide-react'
import { useEffect, useMemo, useState, type ReactElement } from 'react'
import { toast } from 'sonner'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { Initiative, InitiativeMutationInput, InitiativeUpdateSchedule, IssueLabel, Project, Team, User } from '@/types/flow'
import { NotificationCheckbox, NotificationOptionSection } from '@/components/ui/notification-controls'
import { SelectControl } from '@/components/ui/select-control'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { useI18n } from '@/i18n/i18n'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuSeparator } from '@/components/ui/row-context-menu'
import { queueLinearMenuShortcut, useLinearHotkeys } from '@/components/ui/menu-shortcuts'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { SlackIcon } from '@/components/issue/issue-icons'
import { InitiativeCopySubmenu, InitiativeHierarchySubmenus, InitiativeRemindSubmenu, InitiativeSubscribeSubmenu } from './initiative-row-menu'
import { initiativeGraph } from './initiative-hierarchy'
import { childOptions, directChildIds, parentOptions, saveInitiativeRelation, showNestingLimitError, toggleChild, toggleId } from './initiative-hierarchy-actions'
import { InitiativeCreateRow } from './initiatives-page'
import { usePulseSubscription } from '@/lib/pulse-subscriptions'
import './initiative-controls.css'

const DEFAULT_RULES = { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }
const DEFAULT_SCHEDULE: InitiativeUpdateSchedule = { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' }
type Update = (input: InitiativeMutationInput) => void | Promise<unknown>

/** The viewer's Pulse subscription for the initiative (contract item 2), via the Pulse API. */
function useInitiativePulse(initiative: Initiative, pulseSubscribed: boolean | undefined, refresh: boolean) {
  const { t } = useI18n()
  const pulse = usePulseSubscription('initiative', initiative.id, pulseSubscribed ?? false, refresh)
  const change = async (checked: boolean) => {
    if (pulse.saving) return
    try { await pulse.toggle(checked) }
    catch (error) { toast.error(t('Could not update Pulse subscription'), { description: error instanceof Error ? error.message : undefined }) }
  }
  return { ...pulse, change }
}

export function InitiativeNotificationMenu({ initiative, pulseSubscribed, onUpdate }: { initiative: Initiative; /** Derived from loaded data (explicit choice + default rules). */ pulseSubscribed?: boolean; onUpdate: Update }) {
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const [open, setOpen] = useState(false)
  const pulse = useInitiativePulse(initiative, pulseSubscribed, open)
  const { t } = useI18n()
  const rules = initiative.notificationRules ?? DEFAULT_RULES
  const changeRule = (field: keyof typeof rules, value: boolean) => void onUpdate({ notificationRules: { ...rules, [field]: value } })
  return <>
    <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button aria-label="Setup initiative notifications" className={initiative.subscribed ? 'is-active' : ''} type="button"><Bell size={14}/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" alignOffset={-2} className="li-notifications" collisionPadding={10} sideOffset={4}>
      <NotificationOptionSection className="li-notifications__section" title="Send inbox notifications for"><NotificationCheckbox checked={rules.descriptionChanges} label="Comments and changes to initiative description" onChange={value => changeRule('descriptionChanges', value)}/><NotificationCheckbox checked={rules.newUpdate} label="New initiative update is posted" onChange={value => changeRule('newUpdate', value)}/></NotificationOptionSection>
      <NotificationOptionSection className="li-notifications__section" title={t('Pulse updates')}><NotificationCheckbox disabled={pulse.saving} checked={pulse.subscribed} label={t('Subscribe to initiative updates')} onChange={value => void pulse.change(value)}/></NotificationOptionSection>
      <section className="li-notifications__schedule"><div><strong>Update schedule</strong><span>{scheduleLabel(initiative.updateSchedule ?? DEFAULT_SCHEDULE)}</span></div><button onClick={() => setScheduleOpen(true)} type="button">Change</button></section>
      <section className="li-notifications__slack"><ViewGlyph color="currentColor" icon="Slack"/><strong>Slack notifications</strong><button disabled title="Connect Slack from workspace integrations first" type="button">Connect</button></section>
    </Popover.Content></Popover.Portal></Popover.Root>
    <UpdateScheduleDialog initiative={initiative} onOpenChange={setScheduleOpen} onUpdate={onUpdate} open={scheduleOpen}/>
  </>
}

export type InitiativeActionsMenuProps = {
  initiative: Initiative
  initiatives: Initiative[]
  users: User[]
  teams: Team[]
  labels: IssueLabel[]
  viewer: User
  pulseSubscribed?: boolean
  onCreateInitiative: (input: InitiativeMutationInput & { name: string }) => Promise<unknown>
  onCreateLabel: (name: string) => Promise<IssueLabel>
  onCreateReminder: (remindAt: string) => Promise<unknown>
  onDelete: () => void
  onNewUpdate: () => void
  onShowActivity: () => void
  onUpdate: Update
  onUpdateInitiative: (id: string, input: InitiativeMutationInput) => Promise<unknown>
  /** Opens the overview's inline create row instead of the fallback dialog (Linear's create-sub-initiative signal). */
  onCreateSubInitiative?: () => void
}

/**
 * The initiative page's "…" menu (Linear's initiative header menu), built from the same rows and
 * submenus as the initiative list's row menu. Its key hints also work anywhere on the page.
 */
export function InitiativeActionsMenu({ initiative, initiatives, users, teams, labels, viewer, pulseSubscribed, onCreateInitiative, onCreateLabel, onCreateReminder, onCreateSubInitiative, onDelete, onNewUpdate, onShowActivity, onUpdate, onUpdateInitiative }: InitiativeActionsMenuProps) {
  const { t } = useI18n()
  const [menuOpen, setMenuOpen] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const [reminderOpen, setReminderOpen] = useState(false)
  const [creatingChild, setCreatingChild] = useState(false)
  const graph = useMemo(() => initiativeGraph(initiatives), [initiatives])
  const openMenuWith = (shortcut: string) => { queueLinearMenuShortcut(shortcut); setMenuOpen(true) }
  const copy = (value: string) => void navigator.clipboard?.writeText(value).then(() => toast.success(t('Copied to clipboard')), () => toast.error(t('Could not copy to clipboard')))
  useLinearHotkeys({
    '⌘ ⇧ P': () => openMenuWith('⌘ ⇧ P'),
    '⌥ F': () => void onUpdate({ favorite: !initiative.favorite }),
    '⇧ H': () => openMenuWith('⇧ H'),
    'N then U': onNewUpdate,
    '⌘ U': onShowActivity,
    '⌘ .': () => copy(initiative.slugId || initiative.id),
    '⌘ ⇧ ,': () => copy(location.href),
    "⌘ ⇧ '": () => copy(initiative.name),
    '⌘ ⌥ C': () => copy(overviewMarkdown(initiative)),
  })
  return <>
    <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}><DropdownMenu.Trigger asChild><button aria-label={t('Initiative actions')} type="button"><span className="li-ellipsis">•••</span></button></DropdownMenu.Trigger><DropdownMenu.Portal>
      <LinearDropdownMenuContent label={t('Initiative actions')}>
        <InitiativeHierarchySubmenus initiative={initiative} initiatives={initiatives} canParent={graph.canParent} onCreateSubInitiative={onCreateSubInitiative ?? (() => setCreatingChild(true))} onUpdate={onUpdate} onUpdateInitiative={onUpdateInitiative}/>
        <LinearMenuSeparator/>
        <InitiativeCopySubmenu initiative={initiative} url={location.href}/>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={initiative.favorite ? 'Unfavorite' : 'Favorite'} shortcut="⌥ F" onSelect={() => void onUpdate({ favorite: !initiative.favorite })}/>
        <InitiativeSubscribeSubmenu initiative={initiative} pulseSubscribed={pulseSubscribed} onUpdate={onUpdate}/>
        <InitiativeRemindSubmenu onCreateReminder={onCreateReminder} onCustom={() => setReminderOpen(true)}/>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<LinearGlyph name="initiativeUpdate"/>} label="New initiative update" shortcut="N then U" onSelect={onNewUpdate}/>
        <LinearMenuItem icon={<ViewGlyph icon="ClockOutline" color="currentColor"/>} label="Change update schedule…" onSelect={() => setScheduleOpen(true)}/>
        <LinearMenuItem icon={<SlackIcon size={16}/>} label="Configure Slack notifications…" href={`/${location.pathname.split('/').filter(Boolean)[0] ?? ''}/settings/integrations/slack`}/>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<IssueActionGlyph label="Show description history" fallback={null}/>} label="Show description history" onSelect={() => setHistoryOpen(true)}/>
        <LinearMenuItem icon={<LinearGlyph name="initiativeUpdate"/>} label="Show updates and activity" shortcut="⌘ U" onSelect={onShowActivity}/>
        <LinearMenuItem icon={<LinearGlyph name="exportCsv"/>} label="Export projects as CSV…" onSelect={() => downloadProjectsCSV(initiative)}/>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={onDelete}/>
      </LinearDropdownMenuContent>
    </DropdownMenu.Portal></DropdownMenu.Root>
    <InitiativeDescriptionHistoryDialog initiative={initiative} onOpenChange={setHistoryOpen} onUpdate={onUpdate} open={historyOpen}/>
    <UpdateScheduleDialog initiative={initiative} onOpenChange={setScheduleOpen} onUpdate={onUpdate} open={scheduleOpen}/>
    <ReminderDialog onCreate={onCreateReminder} onOpenChange={setReminderOpen} open={reminderOpen}/>
    <Dialog.Root open={creatingChild} onOpenChange={setCreatingChild}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="li-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="li-subinitiative-dialog"><Dialog.Title>{t('New sub-initiative')}</Dialog.Title>
      <InitiativeCreateRow initialLeadTeamId={initiative.leadTeamId} labels={labels} teams={teams} users={users} viewer={viewer} view="planned" onCancel={() => setCreatingChild(false)} onCreateLabel={onCreateLabel} onCreate={async input => { try { await onCreateInitiative({ ...input, parentInitiativeIds: [initiative.id] }); setCreatingChild(false) } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not create initiative')) } }}/>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
  </>
}

type AddMenuMode = 'actions' | 'projects' | 'parents' | 'children'

/** Linear's header "Add…" menu (and the overview's "Add a project" menus). With `hierarchy` it also
 * offers Linear Enterprise's parent / sub-initiative actions after a divider. */
export function AddProjectMenu({ initiative, projects, onCreateNew, onUpdate, trigger, align = 'end', hierarchy }: {
  initiative: Initiative; projects: Project[]; onCreateNew: () => void; onUpdate: Update
  /** Replaces the header's "+" button (the overview's empty-state button, for one). */
  trigger?: ReactElement
  align?: 'start' | 'end'
  hierarchy?: { initiatives: Initiative[]; onCreateSubInitiative: () => void; onUpdateInitiative: (id: string, input: InitiativeMutationInput) => Promise<unknown> }
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<AddMenuMode>('actions')
  const graph = useMemo(() => initiativeGraph(hierarchy?.initiatives ?? []), [hierarchy?.initiatives])
  const parentIds = initiative.parentInitiativeIds ?? []
  const choices = useMemo(() => {
    if (mode === 'parents' && hierarchy) return { options: parentOptions(initiative, hierarchy.initiatives, graph), selected: initiative.parentInitiativeIds ?? [], placeholder: 'Change parent initiatives…' }
    if (mode === 'children' && hierarchy) return { options: childOptions(initiative, hierarchy.initiatives, graph), selected: [...directChildIds(initiative, hierarchy.initiatives)], placeholder: 'Add existing sub-initiative…' }
    return { options: projects.map(project => ({ id: project.id, label: project.name })), selected: initiative.projectIds, placeholder: 'Search projects…' }
  }, [graph, hierarchy, initiative, mode, projects])
  const choose = (id: string) => {
    if (mode === 'parents') return void saveInitiativeRelation(async () => onUpdate({ parentInitiativeIds: toggleId(parentIds, id) }), t)
    if (mode === 'children' && hierarchy) return void saveInitiativeRelation(() => toggleChild(initiative, graph.byId.get(id), hierarchy.onUpdateInitiative), t)
    void onUpdate({ projectIds: toggleId(initiative.projectIds, id) })
  }
  const command = usePropertyCommand({ closeOnSelect: false, open: open && mode !== 'actions', options: choices.options, selectedIds: choices.selected, resetKey: mode, onOpenChange: setOpen, onSelect: option => choose(option.id) })
  const createSubInitiative = () => { setOpen(false); if (graph.canCreateChild(initiative.id)) hierarchy?.onCreateSubInitiative(); else showNestingLimitError(t) }
  return <Popover.Root open={open} onOpenChange={nextOpen=>{setOpen(nextOpen);if(!nextOpen)setMode('actions')}}><Popover.Trigger asChild>{trigger ?? <button aria-label={t(hierarchy ? 'Add' : 'Add project')} type="button"><Plus size={14}/></button>}</Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align={align} className="li-add-project" sideOffset={4} onOpenAutoFocus={event => event.preventDefault()}>
    {mode === 'actions' ? <><label><Search size={14}/><input autoFocus aria-label={t('Add…')} placeholder={t('Add…')}/></label><button onClick={() => { setOpen(false); onCreateNew() }} type="button"><Plus size={14}/>{t('Create new project…')}<kbd>N then P</kbd></button><button onClick={() => setMode('projects')} type="button"><Link2 size={14}/>{t('Add existing projects…')}</button>
      {hierarchy && <><hr/><button onClick={() => setMode('parents')} type="button"><LinearGlyph name="parentInitiatives"/>{t(parentIds.length ? 'Change parent initiatives' : 'Set parent initiatives')}</button><button onClick={createSubInitiative} type="button"><LinearGlyph name="subInitiatives"/>{t('Create sub-initiative')}</button><button onClick={() => setMode('children')} type="button"><ViewGlyph icon="Initiative" color="currentColor"/>{t('Add existing sub-initiative')}</button></>}
    </> : <><header><button aria-label={t('Back')} onClick={() => setMode('actions')} type="button">‹</button><span>{t('Initiative')} · <b data-i18n-ignore>{initiative.name}</b></span><button aria-label={t('Close')} onClick={() => setOpen(false)} type="button"><X size={13}/></button></header><label><Search size={14}/><input ref={command.inputRef} autoFocus aria-label={t('Command menu')} placeholder={t(choices.placeholder)} value={command.query} onChange={event=>command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown}/></label><div role="listbox" aria-multiselectable="true" onKeyDown={command.onKeyDown}>{command.filteredOptions.map(option=>{const project=mode==='projects'?projects.find(item=>item.id===option.id):undefined;const item=mode==='projects'?undefined:graph.byId.get(option.id);return <button aria-checked={command.isSelected(option.id)} aria-selected={command.activeId===option.id} key={option.id} onPointerMove={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)} role="option" type="button"><span className="li-picker-checkbox">{command.isSelected(option.id)&&<Check size={11}/>}</span>{project?<ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)}/>:<ViewGlyph color={item?.color} icon={item?.icon || 'Initiative'}/>}<span data-i18n-ignore>{option.label}</span>{project&&<small>{project.progress}%</small>}</button>})}{!command.filteredOptions.length&&<p className="li-add-project__empty">{t(mode==='projects'?'No results':'No matching initiatives')}</p>}</div><footer><kbd>Enter ↵</kbd> {t('Select')} <span/><kbd>⌥ ↵</kbd> {t('More actions')}</footer></>}
  </Popover.Content></Popover.Portal></Popover.Root>
}

function UpdateScheduleDialog({ initiative, open, onOpenChange, onUpdate }: { initiative: Initiative; open: boolean; onOpenChange: (open: boolean) => void; onUpdate: Update }) {
  const [draft, setDraft] = useState<InitiativeUpdateSchedule>(initiative.updateSchedule ?? DEFAULT_SCHEDULE)
  const [saving, setSaving] = useState(false)
  useEffect(() => { if (open) setDraft(initiative.updateSchedule ?? DEFAULT_SCHEDULE) }, [initiative.updateSchedule, open])
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="li-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="li-schedule-dialog"><Dialog.Title>Change update schedule</Dialog.Title><Dialog.Description>Choose when updates are expected for <strong data-i18n-ignore>{initiative.name}</strong>.</Dialog.Description><div className="li-schedule-options">{(['none', 'weekly', 'biweekly', 'monthly', 'custom', 'never'] as const).map(cadence => <label key={cadence}><input checked={draft.cadence === cadence} name="cadence" onChange={() => setDraft({ ...draft, cadence })} type="radio"/><span>{scheduleCadenceLabel(cadence)}</span></label>)}</div>{draft.cadence !== 'none' && draft.cadence !== 'never' && <div className="li-schedule-custom"><label>Weekday<SelectControl label="Weekday" value={String(draft.weekday)} onChange={value => setDraft({ ...draft, weekday: Number(value) })} options={['Monday','Tuesday','Wednesday','Thursday','Friday','Saturday','Sunday'].map((day,index)=>({value:String(index),label:day}))}/></label><label>Time range<SelectControl label="Time range" value={draft.timeRange} onChange={timeRange => setDraft({ ...draft, timeRange })} options={[{value:'09:00-12:00',label:'09:00–12:00'},{value:'12:00-15:00',label:'12:00–15:00'},{value:'15:00-18:00',label:'15:00–18:00'}]}/></label></div>}<footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button disabled={saving} onClick={() => { setSaving(true); Promise.resolve(onUpdate({ updateSchedule: draft })).then(() => onOpenChange(false)).finally(() => setSaving(false)) }} type="button">Save</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function InitiativeDescriptionHistoryDialog({ initiative, open, onOpenChange, onUpdate }: { initiative: Initiative; open: boolean; onOpenChange: (open: boolean) => void; onUpdate: Update }) {
  const revisions = initiative.descriptionHistory ?? []
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="li-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="li-history-dialog"><Dialog.Title>Restore version for <span data-i18n-ignore>{initiative.name}</span> initiative</Dialog.Title><Dialog.Description>Previous description versions are kept when the overview is edited.</Dialog.Description>{revisions.length ? <div className="li-history-list">{revisions.map(revision => <article key={revision.id}><header><strong data-i18n-ignore>{revision.editor.displayName || revision.editor.name}</strong><time>{new Date(revision.editedAt).toLocaleString()}</time><button onClick={() => { void onUpdate({ description: revision.description }); onOpenChange(false) }} type="button">Restore</button></header><p data-i18n-ignore>{revision.description || 'Empty description'}</p></article>)}</div> : <div className="li-history-empty">There is no history yet.</div>}<footer><Dialog.Close asChild><button type="button">Close</button></Dialog.Close></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function ReminderDialog({ open, onOpenChange, onCreate }: { open: boolean; onOpenChange: (open: boolean) => void; onCreate: (remindAt: string) => Promise<unknown> }) {
  const [value, setValue] = useState(() => toLocalInput(addHours(new Date(), 1)))
  const [saving, setSaving] = useState(false)
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="li-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="li-reminder-dialog"><Dialog.Title>Set reminder</Dialog.Title><Dialog.Description>Choose a date and time in your local timezone.</Dialog.Description><label>Remind me at<DateTimeControl label="Remind me at" min={toLocalInput(new Date())} mode="datetime" value={value} onChange={setValue}/></label><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button disabled={saving || !value || new Date(value) <= new Date()} onClick={() => { setSaving(true); onCreate(new Date(value).toISOString()).then(() => { toast.success('Reminder created'); onOpenChange(false) }).finally(() => setSaving(false)) }} type="button">Create reminder</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function scheduleLabel(schedule: InitiativeUpdateSchedule) { return schedule.cadence === 'custom' ? `Custom · ${['Mon','Tue','Wed','Thu','Fri','Sat','Sun'][schedule.weekday]} ${schedule.timeRange}` : scheduleCadenceLabel(schedule.cadence) }
function scheduleCadenceLabel(cadence: InitiativeUpdateSchedule['cadence']) { return ({ none: 'No expectation for updates', weekly: 'Weekly', biweekly: 'Every two weeks', monthly: 'Monthly', custom: 'Custom schedule', never: 'Never' })[cadence] }
function downloadProjectsCSV(initiative: Initiative) { const blob = new Blob([`initiative,projectId\n${initiative.projectIds.map(id => `"${initiative.name.replaceAll('"', '""')}",${id}`).join('\n')}`], { type: 'text/csv' }); const url = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${initiative.slugId}-projects.csv`; anchor.click(); URL.revokeObjectURL(url) }
function overviewMarkdown(initiative: Initiative) { return `# ${initiative.name}\n\n${initiative.summary ? `${initiative.summary}\n\n` : ''}${initiative.description}` }
function addHours(date: Date, hours: number) { const result = new Date(date); result.setHours(result.getHours() + hours); return result }
function toLocalInput(date: Date) { const offset = date.getTimezoneOffset() * 60000; return new Date(date.getTime() - offset).toISOString().slice(0, 16) }
