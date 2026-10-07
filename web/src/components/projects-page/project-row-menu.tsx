import { useRef, useState, type MouseEvent, type ReactElement } from 'react'
import { createPortal } from 'react-dom'
import { toast } from 'sonner'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { NoAssigneeIcon, PriorityIcon, SlackIcon } from '@/components/issue/issue-icons'
import { ProjectLabelMenuContent } from '@/components/property/project-label-menu-content'
import { LinearContextMenuPortal, LinearContextMenuRoot, LinearContextMenuTrigger, LinearMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSeparator, LinearSubmenu, type LinearMenuOption } from '@/components/ui/row-context-menu'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearReminderOptions } from '@/components/ui/reminder-options'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { useI18n } from '@/i18n/i18n'
import { toggleGroupedLabelIds } from '@/lib/labels'
import { personMatchesQuery } from '@/lib/people'
import { usePeopleDirectory } from '@/components/property/people-context'
import { directoryPerson } from '@/lib/people'
import { PULSE_EVENT, sessionPulseChoice, usePulseSubscription } from '@/lib/pulse-subscriptions'
import { ProjectDatePicker } from './project-target-date-picker'
import { ProjectStatusGlyph, type ProjectPropertyOption } from './project-property-picker'
import type { ProjectAction, ProjectMenuIntegration, ProjectPageItem, ProjectProperty } from './projects-data-view'


const SUBSCRIPTION_OPTIONS: Array<[string, string]> = [
  ['issueAdded', 'An issue is added to the project'],
  ['issueCompleted', 'An issue is marked completed or canceled'],
  ['descriptionChanged', 'Comments and changes to project description'],
  ['customerRequest', 'A customer request is added'],
  ['updatePosted', 'New project update is posted'],
]

const PRIORITY_NUMBER: Record<string, number> = { none: 0, urgent: 1, high: 2, medium: 3, low: 4 }

type DatePick = { kind: 'startDate' | 'targetDate' | 'reminder'; x: number; y: number }

export type ProjectRowMenuProps = {
  children: ReactElement
  project: ProjectPageItem
  options: Record<ProjectProperty, ProjectPropertyOption[]>
  integration?: ProjectMenuIntegration
  manualOrdering?: boolean
  onAction: (action: ProjectAction) => void
  onPropertyChange: (property: ProjectProperty, value: string) => void
}

/** Right-click menu for a project row or board card (Linear's project contextual menu). */
export function ProjectRowMenu({ children, project, options, integration, manualOrdering = false, onAction, onPropertyChange }: ProjectRowMenuProps) {
  const { t } = useI18n()
  const point = useRef({ x: 0, y: 0 })
  const [date, setDate] = useState<DatePick | null>(null)
  // The picker opens once the menu has closed, so the menu's focus return can't dismiss it.
  const pendingDate = useRef<DatePick | null>(null)
  const openPendingDate = (event: Event) => { if (!pendingDate.current) return; event.preventDefault(); setDate(pendingDate.current); pendingDate.current = null }
  const anchor = useRef<HTMLSpanElement>(null)
  return <>
    <LinearContextMenuRoot>
      <LinearContextMenuTrigger asChild onContextMenu={(event: MouseEvent) => { point.current = { x: event.clientX, y: event.clientY } }}>{children}</LinearContextMenuTrigger>
      <LinearContextMenuPortal>
        <ProjectRowMenuContent project={project} options={options} integration={integration} manualOrdering={manualOrdering} onAction={onAction} onPropertyChange={onPropertyChange} onPickDate={kind => { pendingDate.current = { kind, ...point.current } }} onCloseAutoFocus={openPendingDate}/>
      </LinearContextMenuPortal>
    </LinearContextMenuRoot>
    {date && createPortal(<span ref={anchor} aria-hidden="true" style={{ position: 'fixed', left: date.x, top: date.y, width: 1, height: 1, pointerEvents: 'none' }}/>, document.body)}
    {date && <ProjectDatePicker
      externalAnchor={anchor}
      label={date.kind === 'startDate' ? 'Start date' : 'Target date'}
      align="start"
      side="bottom"
      open
      onOpenChange={open => { if (!open) setDate(null) }}
      value={date.kind === 'startDate' ? project.rawStartDate : date.kind === 'targetDate' ? project.rawTargetDate : undefined}
      onChange={value => {
        if (date.kind === 'reminder') { if (value) void remind(integration, project.id, atNine(value), t) }
        else onPropertyChange(date.kind, value)
        setDate(null)
      }}
    >{null}</ProjectDatePicker>}
  </>
}


function ProjectRowMenuContent({ project, options, integration, manualOrdering, onAction, onPropertyChange, onPickDate, onCloseAutoFocus }: Omit<ProjectRowMenuProps, 'children'> & { onPickDate: (kind: DatePick['kind']) => void; onCloseAutoFocus: (event: Event) => void }) {
  const { t } = useI18n()
  const directory = usePeopleDirectory()
  const mac = isMacPlatform()
  const favorite = integration?.isFavorite(project.id) ?? false
  const events = new Set(integration?.subscriptionEvents(project.id) ?? [])
  const [subscribeOpen, setSubscribeOpen] = useState(false)
  const pulse = usePulseSubscription('project', project.id, events.has(PULSE_EVENT), subscribeOpen)
  const subscriptions = new Set([...events].filter(event => event !== PULSE_EVENT))
  if (pulse.subscribed) subscriptions.add(PULSE_EVENT)
  const changeSubscription = (id: string) => {
    if (id === PULSE_EVENT) {
      if (!pulse.saving) void pulse.toggle(!pulse.subscribed).catch(error => toast.error(t('Could not update Pulse subscription'), { description: error instanceof Error ? error.message : undefined }))
      return
    }
    const next = new Set([...events].filter(event => event !== PULSE_EVENT))
    if (next.has(id)) next.delete(id); else next.add(id)
    // Inbox events keep the record's explicit Pulse subscribe so writing them never undoes it.
    const keepPulse = (sessionPulseChoice('project', project.id) ?? events.has(PULSE_EVENT)) === true
    void integration?.onSubscriptionEventsChange(project.id, keepPulse ? [...next, PULSE_EVENT] : [...next])
  }
  const url = project.href ? new URL(project.href, window.location.origin).href : window.location.href
  const copy = (value: string) => void navigator.clipboard?.writeText(value).then(() => toast.success(t('Copied to clipboard')), () => toast.error(t('Could not copy to clipboard')))
  const people = (property: 'lead' | 'members'): LinearMenuOption[] => options[property].map(option => ({
    id: option.value,
    label: option.label,
    translate: !option.value,
    keywords: option.keywords,
    icon: option.value ? <UserAvatar className="linear-menu__avatar" avatarUrl={option.avatarUrl} color={option.color} name={option.label}/> : <NoAssigneeIcon size={16}/>,
  }))
  const matchesPerson = (option: LinearMenuOption, query: string) => {
    const person = option.id ? directoryPerson(directory.users, option.id) : undefined
    return person ? personMatchesQuery(person, query) : `${option.label} ${t(option.label)} ${option.keywords ?? ''}`.toLocaleLowerCase().includes(query)
  }
  const labels = options.labels.map(option => ({ id: option.value, label: option.label, color: option.color, groupId: option.groupId, groupLabel: option.group, groupColor: option.groupColor }))
  return <LinearMenuContent label={t('Project actions')} onCloseAutoFocus={onCloseAutoFocus}>
    <LinearSubmenu icon={<LinearGlyph name="projectStatus" size={16}/>} label="Status" shortcut="P then S" search>
      <LinearMenuOptions placeholder="Change status…" selected={new Set([project.status])} onChoose={value => onPropertyChange('status', value)}
        options={options.status.map((option, index) => ({ id: option.value, label: option.label, keywords: option.keywords, shortcut: option.shortcut ?? (index < 9 ? String(index + 1) : undefined), icon: <ProjectStatusGlyph color={option.color} name={option.label} type={option.statusType}/> }))}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="priority"/>} label="Priority" shortcut="P then P" search>
      <LinearMenuOptions placeholder="Change priority…" selected={new Set([project.priority])} onChoose={value => onPropertyChange('priority', value)}
        options={options.priority.map(option => ({ id: option.value, label: option.label, shortcut: option.shortcut, icon: <PriorityIcon priority={PRIORITY_NUMBER[option.value] ?? 0} size={16}/> }))}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="projectLead"/>} label="Project lead" shortcut="P then A" search>
      <LinearMenuOptions placeholder="Set lead…" selected={new Set([project.lead?.id ?? ''])} matches={matchesPerson} onChoose={value => onPropertyChange('lead', value)} options={people('lead')}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="members"/>} label="Members" shortcut="P then M" search>
      <LinearMenuOptions multiple placeholder="Change members…" selected={new Set(project.memberIds ?? [])} matches={matchesPerson} options={people('members')}
        onChoose={value => { const next = new Set(project.memberIds ?? []); if (next.has(value)) next.delete(value); else next.add(value); onPropertyChange('members', [...next].join(',')) }}/>
    </LinearSubmenu>
    <LinearMenuItem icon={<LinearGlyph name="dateAdd"/>} label="Start date…" detail={project.startDate} shortcut="Ctrl ⌥ S" onSelect={() => onPickDate('startDate')}/>
    <LinearMenuItem icon={<LinearGlyph name="dateAdd"/>} label="Target date…" detail={project.targetDate} shortcut="Ctrl ⌥ D" onSelect={() => onPickDate('targetDate')}/>
    <LinearSubmenu icon={<LinearGlyph name="labels"/>} label="Labels" shortcut="P then L" search>
      {({ close, container }) => <ProjectLabelMenuContent options={labels} selectedIds={project.labelIds ?? []} onChoose={id => onPropertyChange('labels', toggleGroupedLabelIds(project.labelIds ?? [], id, labels).join(','))} onClose={close} submenuPortalContainer={container} searchShortcut={false}/>}
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="projectProperties"/>} label="More properties">
      <LinearMenuItem icon={<ViewGlyph icon="Initiative" color="currentColor"/>} label="Initiatives…" shortcut="P then N" onSelect={() => onAction('initiatives')}/>
      <LinearMenuItem icon={<LinearGlyph name="dependencies"/>} label="Dependencies" onSelect={() => onAction('dependencies')}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Add customer request…" fallback={null}/>} label="Add customer request…" shortcut={mac ? 'Ctrl R' : 'Ctrl ⌥ R'} onSelect={() => onAction('customerRequest')}/>
      <LinearMenuItem icon={<ViewGlyph icon="ClockOutline" color="currentColor"/>} label="Change update schedule…" onSelect={() => onAction('schedule')}/>
      <LinearMenuItem icon={<SlackIcon size={16}/>} label="Configure Slack notifications…" href={slackSettingsPath()}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Rename…" fallback={null}/>} label="Rename…" shortcut="⇧ R" onSelect={() => onAction('rename')}/>
    </LinearSubmenu>
    <LinearMenuSeparator/>
    <LinearSubmenu icon={<LinearGlyph name="copy"/>} label="Copy">
      <LinearMenuItem icon={<IssueActionGlyph label="Copy ID" fallback={null}/>} label="Copy ID" shortcut="⌘ ." onSelect={() => copy(project.slugId ?? project.id)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy URL" fallback={null}/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => copy(url)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy title" fallback={null}/>} label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => copy(project.name)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy title as link" fallback={null}/>} label="Copy title as link" shortcut="⌘ C" onSelect={() => copy(`[${project.name}](${url})`)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy content as Markdown" fallback={null}/>} label="Copy overview as Markdown" shortcut="⌘ ⌥ C" onSelect={() => copy(`# ${project.name}\n\n${project.summary ?? ''}`.trim())}/>
    </LinearSubmenu>
    {manualOrdering && <LinearSubmenu icon={<LinearGlyph name="moveTo"/>} label="Move">
      <LinearMenuItem icon={<LinearGlyph name="moveTop"/>} label="Move to top" shortcut="⌥ ⇧ ↑" onSelect={() => onAction('moveTop')}/>
      <LinearMenuItem icon={<LinearGlyph name="moveUp"/>} label="Move up" shortcut="⌥ ↑" onSelect={() => onAction('moveUp')}/>
      <LinearMenuItem icon={<LinearGlyph name="moveDown"/>} label="Move down" shortcut="⌥ ↓" onSelect={() => onAction('moveDown')}/>
      <LinearMenuItem icon={<LinearGlyph name="moveBottom"/>} label="Move to bottom" shortcut="⌥ ⇧ ↓" onSelect={() => onAction('moveBottom')}/>
    </LinearSubmenu>}
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={favorite ? 'Unfavorite' : 'Favorite'} shortcut="⌥ F" onSelect={() => void integration?.onFavoriteChange(project.id, !favorite)}/>
    <LinearSubmenu icon={<LinearGlyph name="subscribe"/>} label="Subscribe" onOpenChange={setSubscribeOpen}>
      <LinearMenuOptions multiple selected={subscriptions} onChoose={changeSubscription}
        options={[...SUBSCRIPTION_OPTIONS.map(([id, label]) => ({ id, label })), { id: PULSE_EVENT, label: 'Subscribe to project updates', group: 'Pulse updates', disabled: pulse.saving }]}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<ViewGlyph icon="Alarm" color="currentColor"/>} label="Remind me" shortcut="⇧ H" search>
      {({ close }) => <LinearReminderOptions onChoose={date => { close(); void remind(integration, project.id, date, t) }} onCustom={() => onPickDate('reminder')}/>}
    </LinearSubmenu>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="newComment"/>} label="New comment…" shortcut="N then C" onSelect={() => onAction('comment')}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={() => onAction('delete')}/>
  </LinearMenuContent>
}

async function remind(integration: ProjectMenuIntegration | undefined, projectId: string, date: Date, t: (key: string) => string) {
  if (!integration) return
  try { await integration.onCreateReminder(projectId, date.toISOString()); toast.success(t('Reminder set')) }
  catch (error) { toast.error(error instanceof Error ? error.message : t('Could not set reminder')) }
}

function atNine(value: string) {
  const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T09:00:00` : value)
  return Number.isFinite(date.getTime()) ? date : new Date(Date.now() + 86_400_000)
}

/** Flow's Slack integration settings (where project channels are connected), as the saved-view menu links. */
function slackSettingsPath() { return `/${location.pathname.split('/').filter(Boolean)[0] ?? ''}/settings/integrations/slack` }
