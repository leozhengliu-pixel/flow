import { useRef, useState, type MouseEvent, type ReactElement } from 'react'
import { createPortal } from 'react-dom'
import { toast } from 'sonner'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { NoAssigneeIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import { LinearContextMenuPortal, LinearContextMenuRoot, LinearContextMenuTrigger, LinearMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSeparator, LinearSubmenu, type LinearMenuOption } from '@/components/ui/row-context-menu'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearReminderOptions } from '@/components/ui/reminder-options'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { personMatchesQuery, personSearchText } from '@/lib/people'
import { usePulseSubscription } from '@/lib/pulse-subscriptions'
import type { Initiative, InitiativeMutationInput, InitiativeStatus, IssueLabel, Team, User } from '@/types/flow'
import { formatTarget } from './initiative-model'
import { InitiativeStatusIcon } from './initiative-shared'
import { initiativeGraph } from './initiative-hierarchy'
import { showNestingLimitError } from './initiative-hierarchy-actions'


const STATUSES: Array<[InitiativeStatus, string]> = [['proposed', 'Proposed'], ['planned', 'Planned'], ['active', 'Active'], ['completed', 'Completed'], ['canceled', 'Canceled']]
const PRIORITIES = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
const DEFAULT_RULES = { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }

export type InitiativeRowMenuProps = {
  children: ReactElement
  initiative: Initiative
  /** The initiative's page, for the Copy URL / link items. */
  href: string
  initiatives: Initiative[]
  canParent: (child: string, parent: string) => boolean
  users: User[]
  teams: Team[]
  labels: IssueLabel[]
  onCreateLabel: (name: string) => Promise<IssueLabel>
  onCreateReminder: (remindAt: string) => Promise<unknown>
  onCreateSubInitiative: () => void
  onDelete: () => void
  onEdit: () => void
  onNewComment: () => void
  onNewUpdate: () => void
  onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
  onUpdateInitiative: (id: string, input: InitiativeMutationInput) => void | Promise<unknown>
}

type DatePick = { kind: 'target' | 'reminder'; x: number; y: number }

/** Right-click / "…" menu for an initiative row. */
export function InitiativeRowMenu({ children, ...props }: InitiativeRowMenuProps) {
  const { t } = useI18n()
  const point = useRef({ x: 0, y: 0 })
  const anchor = useRef<HTMLSpanElement>(null)
  const [date, setDate] = useState<DatePick | null>(null)
  // The picker opens once the menu has closed, so the menu's focus return can't dismiss it.
  const pendingDate = useRef<DatePick | null>(null)
  const openPendingDate = (event: Event) => { if (!pendingDate.current) return; event.preventDefault(); setDate(pendingDate.current); pendingDate.current = null }
  const { initiative } = props
  return <>
    <LinearContextMenuRoot>
      <LinearContextMenuTrigger asChild onContextMenu={(event: MouseEvent) => { point.current = { x: event.clientX, y: event.clientY } }}>{children}</LinearContextMenuTrigger>
      <LinearContextMenuPortal><InitiativeRowMenuContent {...props} onPickDate={kind => { pendingDate.current = { kind, ...point.current } }} onCloseAutoFocus={openPendingDate}/></LinearContextMenuPortal>
    </LinearContextMenuRoot>
    {date && createPortal(<span ref={anchor} aria-hidden="true" style={{ position: 'fixed', left: date.x, top: date.y, width: 1, height: 1, pointerEvents: 'none' }}/>, document.body)}
    {date && <ProjectDatePicker
      externalAnchor={anchor}
      label="Target date"
      align="start"
      side="bottom"
      open
      compactPeriods={date.kind === 'target'}
      defaultMode={date.kind === 'target' ? 'quarter' : 'day'}
      resolution={date.kind === 'target' ? initiative.targetDateResolution : undefined}
      value={date.kind === 'target' ? initiative.targetDate : undefined}
      onOpenChange={open => { if (!open) setDate(null) }}
      onChange={(value, resolution) => {
        if (date.kind === 'target') void props.onUpdate({ targetDate: value, targetDateResolution: resolution ?? '' })
        else if (value) void props.onCreateReminder(new Date(`${value}T09:00:00`).toISOString()).then(() => toast.success(t('Reminder set')))
        setDate(null)
      }}
    >{null}</ProjectDatePicker>}
  </>
}

function InitiativeRowMenuContent({ initiative, href, initiatives, canParent, users, teams, labels, onCreateLabel, onCreateReminder, onCreateSubInitiative, onDelete, onEdit, onNewComment, onNewUpdate, onUpdate, onUpdateInitiative, onPickDate, onCloseAutoFocus }: Omit<InitiativeRowMenuProps, 'children'> & { onPickDate: (kind: DatePick['kind']) => void; onCloseAutoFocus: (event: Event) => void }) {
  const { t } = useI18n()
  const people: LinearMenuOption[] = [{ id: '', label: 'No owner', icon: <NoAssigneeIcon size={16}/> }, ...users.map(user => ({ id: user.id, label: user.displayName || user.name, translate: false, keywords: personSearchText(user), icon: <UserAvatar className="linear-menu__avatar" avatarUrl={user.avatarUrl} name={user.displayName || user.name}/> }))]
  const matchesPerson = (option: LinearMenuOption, query: string) => {
    const user = users.find(item => item.id === option.id)
    return user ? personMatchesQuery(user, query) : `${option.label} ${t(option.label)}`.toLocaleLowerCase().includes(query)
  }
  const toggleLabel = (id: string) => void onUpdate({ labelIds: initiative.labelIds.includes(id) ? initiative.labelIds.filter(item => item !== id) : [...initiative.labelIds, id] })
  return <LinearMenuContent label={t('Initiative actions')} onCloseAutoFocus={onCloseAutoFocus}>
    <LinearMenuItem icon={<LinearGlyph name="edit"/>} label="Edit" onSelect={onEdit}/>
    <LinearSubmenu icon={<span className="linear-menu__mono"><InitiativeStatusIcon status={initiative.status}/></span>} label="Status" shortcut="S" search>
      <LinearMenuOptions placeholder="Change status…" selected={new Set([initiative.status])} onChoose={status => void onUpdate({ status: status as InitiativeStatus })}
        options={STATUSES.map(([id, label], index) => ({ id, label, shortcut: String(index + 1), icon: <InitiativeStatusIcon status={id}/> }))}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="priority"/>} label="Priority" search>
      <LinearMenuOptions placeholder="Change priority…" selected={new Set([String(initiative.priority)])} onChoose={priority => void onUpdate({ priority: Number(priority) })}
        options={PRIORITIES.map((label, priority) => ({ id: String(priority), label, shortcut: String(priority), icon: <PriorityIcon priority={priority} size={16}/> }))}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="owner"/>} label="Owner" shortcut="N then O" search>
      <LinearMenuOptions placeholder="Set owner…" selected={new Set([initiative.owner?.id ?? ''])} matches={matchesPerson} options={people} onChoose={ownerId => void onUpdate({ ownerId })}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="leadTeam"/>} label="Lead team" search>
      <LinearMenuOptions placeholder="Set lead team…" selected={new Set([initiative.leadTeamId ?? ''])} onChoose={leadTeamId => void onUpdate({ leadTeamId })}
        options={[{ id: '', label: 'No lead team', icon: <LinearGlyph name="leadTeam"/> }, ...teams.map(team => ({ id: team.id, label: team.name, translate: false, keywords: team.key, icon: <ViewGlyph color={team.color} icon={team.icon || 'Team'}/> }))]}/>
    </LinearSubmenu>
    <LinearMenuItem icon={<LinearGlyph name="dateAdd"/>} label="Target date…" detail={formatTarget(initiative.targetDate, initiative.targetDateResolution) || undefined} shortcut="Ctrl ⌥ D" onSelect={() => onPickDate('target')}/>
    <InitiativeHierarchySubmenus initiative={initiative} initiatives={initiatives} canParent={canParent} onCreateSubInitiative={onCreateSubInitiative} onUpdate={onUpdate} onUpdateInitiative={onUpdateInitiative}/>
    <LinearSubmenu icon={<LinearGlyph name="labels"/>} label="Labels" shortcut="N then L" search>
      {({ close }) => <InitiativeLabelOptions labels={labels} selected={initiative.labelIds} onToggle={toggleLabel} onCreate={async name => { const label = await onCreateLabel(name); await onUpdate({ labelIds: [...initiative.labelIds, label.id] }); close() }}/>}
    </LinearSubmenu>
    <LinearMenuSeparator/>
    <InitiativeCopySubmenu initiative={initiative} url={new URL(href, location.origin).href}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={initiative.favorite ? 'Unfavorite' : 'Favorite'} shortcut="⌥ F" onSelect={() => void onUpdate({ favorite: !initiative.favorite })}/>
    <InitiativeSubscribeSubmenu initiative={initiative} onUpdate={onUpdate}/>
    <InitiativeRemindSubmenu onCreateReminder={onCreateReminder} onCustom={() => onPickDate('reminder')}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="initiativeUpdate"/>} label="New initiative update" shortcut="N then U" onSelect={onNewUpdate}/>
    <LinearMenuItem icon={<LinearGlyph name="newComment"/>} label="New comment…" shortcut="N then C" onSelect={onNewComment}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={onDelete}/>
  </LinearMenuContent>
}

function InitiativeLabelOptions({ labels, selected, onToggle, onCreate }: { labels: IssueLabel[]; selected: string[]; onToggle: (id: string) => void; onCreate: (name: string) => Promise<void> }) {
  const { t } = useI18n()
  const chosen = new Set(selected)
  // Linear lists applied labels first.
  const options = [...labels].sort((left, right) => Number(chosen.has(right.id)) - Number(chosen.has(left.id))).map(label => ({ id: label.id, label: label.name, translate: false, icon: <span className="linear-menu__swatch" style={{ background: label.color }}/> }))
  return <LinearMenuOptions multiple placeholder={selected.length ? 'Change or add labels…' : 'Add labels…'} selected={chosen} options={options} onChoose={onToggle} emptyLabel="Start typing to create a new label"
    footer={query => query.length >= 2 && !labels.some(label => label.name.toLocaleLowerCase() === query.toLocaleLowerCase()) ? <LinearMenuItem icon={<LinearGlyph name="labels"/>} label={`${t('Create new label')}: "${query}"`} translate={false} onSelect={() => void onCreate(query)}/> : null}/>
}

const initiativeOption = (item: Initiative): LinearMenuOption => ({ id: item.id, label: item.name, translate: false, icon: <ViewGlyph color={item.color} icon={item.icon || 'Initiative'}/> })

/** Parent initiatives (⌘ ⇧ P) and Sub-initiatives ▸ Create new… / Add existing…, shared by the row and header menus. */
export function InitiativeHierarchySubmenus({ initiative, initiatives, canParent, onCreateSubInitiative, onUpdate, onUpdateInitiative }: { initiative: Initiative; initiatives: Initiative[]; canParent: (child: string, parent: string) => boolean; onCreateSubInitiative: () => void; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>; onUpdateInitiative: (id: string, input: InitiativeMutationInput) => void | Promise<unknown> }) {
  const { t } = useI18n()
  const parents = new Set(initiative.parentInitiativeIds ?? [])
  const current = initiatives.filter(item => item.parentInitiativeIds?.includes(initiative.id))
  const candidates = initiatives.filter(item => item.id !== initiative.id && !item.parentInitiativeIds?.includes(initiative.id) && canParent(item.id, initiative.id))
  return <>
    <LinearSubmenu icon={<LinearGlyph name="parentInitiatives"/>} label="Parent initiatives" shortcut="⌘ ⇧ P" search>
      <LinearMenuOptions multiple placeholder="Change parent initiatives…" emptyLabel="No matching initiatives" selected={parents}
        options={initiatives.filter(item => parents.has(item.id) || canParent(initiative.id, item.id)).map(initiativeOption)}
        onChoose={id => { const next = new Set(parents); if (next.has(id)) next.delete(id); else next.add(id); void onUpdate({ parentInitiativeIds: [...next] }) }}/>
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="subInitiatives"/>} label="Sub-initiatives">
      <LinearMenuItem icon={<LinearGlyph name="subInitiatives"/>} label="Create new…" onSelect={() => initiativeGraph(initiatives).canCreateChild(initiative.id) ? onCreateSubInitiative() : showNestingLimitError(t)}/>
      <LinearSubmenu icon={<ViewGlyph icon="Initiative" color="currentColor"/>} label="Add existing…" search>
        <LinearMenuOptions multiple placeholder="Add existing sub-initiative…" emptyLabel="No matching initiatives" selected={new Set(current.map(item => item.id))}
          options={[...current, ...candidates].map(initiativeOption)}
          onChoose={id => { const child = initiatives.find(item => item.id === id); if (!child) return; const ids = child.parentInitiativeIds ?? []; void onUpdateInitiative(id, { parentInitiativeIds: ids.includes(initiative.id) ? ids.filter(item => item !== initiative.id) : [...ids, initiative.id] }) }}/>
      </LinearSubmenu>
    </LinearSubmenu>
  </>
}

function initiativeOverviewMarkdown(initiative: Initiative) {
  return `# ${initiative.name}\n\n${initiative.summary ? `${initiative.summary}\n\n` : ''}${initiative.description ?? ''}`.trim()
}

/** Copy ▸ (Linear's initiative copy actions), shared by the row and header menus. */
export function InitiativeCopySubmenu({ initiative, url }: { initiative: Initiative; url: string }) {
  const copy = useCopy()
  return <LinearSubmenu icon={<LinearGlyph name="copy"/>} label="Copy">
    <LinearMenuItem icon={<IssueActionGlyph label="Copy ID" fallback={null}/>} label="Copy ID" shortcut="⌘ ." onSelect={() => copy(initiative.slugId || initiative.id)}/>
    <LinearMenuItem icon={<IssueActionGlyph label="Copy URL" fallback={null}/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => copy(url)}/>
    <LinearMenuItem icon={<IssueActionGlyph label="Copy title" fallback={null}/>} label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => copy(initiative.name)}/>
    <LinearMenuItem icon={<IssueActionGlyph label="Copy title as link" fallback={null}/>} label="Copy title as link" shortcut="⌘ C" onSelect={() => copy(`[${initiative.name}](${url})`)}/>
    <LinearMenuItem icon={<IssueActionGlyph label="Copy content as Markdown" fallback={null}/>} label="Copy overview as Markdown" shortcut="⌘ ⌥ C" onSelect={() => copy(initiativeOverviewMarkdown(initiative))}/>
  </LinearSubmenu>
}

function useCopy() {
  const { t } = useI18n()
  return (value: string) => void navigator.clipboard?.writeText(value).then(() => toast.success(t('Copied to clipboard')), () => toast.error(t('Could not copy to clipboard')))
}

/** Subscribe ▸ inbox rules plus the Pulse subscription, shared by the row and header menus. */
export function InitiativeSubscribeSubmenu({ initiative, pulseSubscribed = false, onUpdate }: { initiative: Initiative; pulseSubscribed?: boolean; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown> }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const pulse = usePulseSubscription('initiative', initiative.id, pulseSubscribed, open)
  const rules = initiative.notificationRules ?? DEFAULT_RULES
  return <LinearSubmenu icon={<LinearGlyph name="subscribe"/>} label="Subscribe" onOpenChange={setOpen}>
    <LinearMenuOptions multiple selected={new Set([...(rules.descriptionChanges ? ['descriptionChanges'] : []), ...(rules.newUpdate ? ['newUpdate'] : []), ...(pulse.subscribed ? ['pulse'] : [])])}
      options={[{ id: 'descriptionChanges', label: 'Comments and changes to initiative description' }, { id: 'newUpdate', label: 'New initiative update is posted' }, { id: 'pulse', label: 'Subscribe to initiative updates', group: 'Pulse updates', disabled: pulse.saving }]}
      onChoose={id => {
        if (id === 'pulse') { void pulse.toggle(!pulse.subscribed).catch(error => toast.error(t('Could not update Pulse subscription'), { description: error instanceof Error ? error.message : undefined })); return }
        if (id === 'newUpdate') void onUpdate({ notificationRules: { ...rules, newUpdate: !rules.newUpdate }, subscribed: !rules.newUpdate })
        else void onUpdate({ notificationRules: { ...rules, descriptionChanges: !rules.descriptionChanges } })
      }}/>
  </LinearSubmenu>
}

/** Remind me ▸ (⇧ H), shared by the row and header menus. */
export function InitiativeRemindSubmenu({ onCreateReminder, onCustom }: { onCreateReminder: (remindAt: string) => Promise<unknown>; onCustom: () => void }) {
  const { t } = useI18n()
  const remind = (value: Date) => void onCreateReminder(value.toISOString()).then(() => toast.success(t('Reminder set')), error => toast.error(error instanceof Error ? error.message : t('Could not set reminder')))
  return <LinearSubmenu icon={<ViewGlyph icon="Alarm" color="currentColor"/>} label="Remind me" shortcut="⇧ H" search>
    {({ close }) => <LinearReminderOptions onChoose={date => { close(); remind(date) }} onCustom={onCustom}/>}
  </LinearSubmenu>
}
