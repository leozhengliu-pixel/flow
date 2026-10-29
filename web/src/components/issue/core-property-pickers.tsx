import type { ReactNode } from 'react'
import { Building2, Clock3, Layers3 } from 'lucide-react'
import type { ActivityEvent, ProjectSummary, User, WorkspaceMember, WorkflowState } from '@/types/flow'
import { NoAssigneeIcon, PriorityIcon, StatusIcon, WorkflowStatusGlyph } from '@/components/issue/issue-icons'
import { TRIAGE_STATUS } from '@/components/triage/triage-model'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { AssigneeHoverPreview, PropertyShortcutTooltip, StatusHoverPreview } from '@/components/property/issue-property-hover'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { PersonIdentityDetails } from '@/components/property/person-info'
import { personDisplayName, personSearchText } from '@/lib/people'
import { displayUserName, useUserPreferences } from '@/lib/runtime-preferences'
import { AgentHoverCard } from '@/components/agent/agent-hover-card'
import { AGENT_HOVER_CLASS } from '@/components/agent/agent-hover-class'
import { agentHoverProps } from '@/components/agent/agent-hover-props'
import { ASSIGNEE_INVITE_ID, assigneeCandidates, assigneeMenuLayout, assigneeShortcut } from '@/lib/agent-members'
import { usePeopleDirectory } from '@/components/property/people-context'
import { openMemberInvite, viewerCanInvite } from '@/lib/invite-navigation'
import { avatarColor } from '@/components/issue/avatar-color'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { NestingLine } from '@/components/ui/nesting-line'
import { confirmRemoveDelegate } from './confirm-remove-delegate'
import './issue-assignee-menu.css'

/** `triage` shows the reference app's orange "Triage" status for an issue still in the team's triage queue. */
export function StatusPicker({ value, states, onChange, hoverHistory, triage = false }: { value: WorkflowState; states: WorkflowState[]; onChange: (id: string) => void | Promise<void>; hoverHistory?: { activities: ActivityEvent[]; issueCreatedAt: string; triagedAt?: string | null }; triage?: boolean }) {
  const displayName = triage ? TRIAGE_STATUS.name : value.name
  const options = [...states].sort((left,right)=>(left.position??0)-(right.position??0)).map((state, index) => ({ id: state.id, label: state.name, icon: <StatusIcon state={state}/>, shortcut: String(index + 1) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Status"
    value={displayName}
    selectedId={value.id}
    options={options}
    searchPlaceholder="Change status…"
    searchShortcut="S"
    ariaLabel={`Change status. Current status is ${displayName}`}
    triggerClassName="core-property-trigger"
    trigger={triage ? <><WorkflowStatusGlyph state={TRIAGE_STATUS}/><span>{displayName}</span></> : <><StatusIcon state={value}/><span>{value.name}</span></>}
    hoverContent={hoverHistory?<StatusHoverPreview state={value} activities={hoverHistory.activities} issueCreatedAt={hoverHistory.issueCreatedAt} states={states} triagedAt={hoverHistory.triagedAt} inTriage={triage}/>:undefined}
    hoverClassName="property-rich-hover"
    onChange={onChange}
  /></div>
}

export function PriorityPicker({ value, onChange }: { value: number; onChange: (value: number) => void | Promise<void> }) {
  const labels = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
  const options = labels.map((label, priority) => ({ id: String(priority), label, icon: <PriorityIcon priority={priority} size={16}/>, shortcut: String(priority) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Priority"
    value={value ? labels[value] : 'Priority'}
    selectedId={String(value)}
    options={options}
    searchPlaceholder={value ? 'Change priority…' : 'Set priority to…'}
    searchShortcut="P"
    ariaLabel={`Change priority. ${value ? `${labels[value]} is selected` : 'No priority is selected'}`}
    triggerClassName={`core-property-trigger${value === 0 ? ' muted' : ''}`}
    trigger={<><PriorityIcon priority={value}/><span>{value ? labels[value] : 'Set priority'}</span></>}
    hoverContent={<PropertyShortcutTooltip label="Change priority" shortcut="P"/>}
    onChange={id => onChange(Number(id))}
  /></div>
}

function isAwayNow(until?: string) {
  return Boolean(until) && new Date(until as string).getTime() > Date.now()
}

export type PersonPickerOption = {
  searchOnly?: boolean
  /** Agent members open the reference app's agent hover card (the "Agent" pill lives there, never in the row). */
  app?: boolean
  id: string
  userId?: string
  label: string
  email?: string
  name?: string
  avatarUrl?: string
  color?: string
  active?: boolean
  online?: boolean
  invited?: boolean
  disabled?: boolean
  end?: string
  /** Out of office until (set while a calendar or profile out-of-office is active): the row shows "Away". */
  outOfOfficeUntil?: string
  groupId?: string
  groupLabel?: string
  hoverContent?: ReactNode
  hoverClassName?: string
  /** Replaces the row's avatar (e.g. the delegated agent hung under the assignee on a NestingLine). */
  icon?: ReactNode
}

export function PersonHoverPreview({ person, projectName, workspaceName }: { person: PersonPickerOption; projectName?: string; workspaceName: string }) {
  const { t } = useI18n()
  const name = personDisplayName(person) || t('Unknown user')
  const secondary = [person.name, person.email, person.label].find(value => value && value !== person.id)
  const invited = person.invited || person.end === 'Invited'
  const online = !invited && person.active !== false && person.online === true
  return <div className="assignee-hover-preview">
    <header><PersonAvatar person={{...person,label:name}}/><div><strong data-i18n-ignore>{name}</strong>{secondary && <span data-i18n-ignore>{secondary}</span>}</div></header>
    {!invited && <PersonIdentityDetails person={person}/>}
    <div className="assignee-hover-preview__details">
      {invited ? <span>{t('Invited')}</span> : person.active === false ? <span>{t('Inactive')}</span> : person.online !== undefined ? <span><i className={online ? undefined : 'offline'}/>{online ? t('Online') : t('Offline')}</span> : null}
      {!invited && <>{workspaceName && <span><Building2/><span data-i18n-ignore>{workspaceName}</span></span>}{projectName && <span><Layers3/><span data-i18n-ignore>{projectName}</span></span>}</>}
      {invited && <span><Clock3/><time>{t('Invitation pending')}</time></span>}
    </div>
  </div>
}

export function PersonPicker({ ariaLabel, closeOnSelect, emptyOptionLabel, emptyOptionShortcut, emptyOptionEnd, emptyOptionIcon, emptyTriggerLabel, hoverClassName, hoverContent, icon, label, multiple = false, onChange, optionHoverClassName, optionHoverContent, people, searchPlaceholder, searchShortcut, selectedId, selectedIds = [], showUnselectedGroupWhenEmpty = false, surfaceClassName, trigger, triggerClassName, unselectedGroupLabel, value, embedded = false, side, alignOffset, extraOptions = [], hideSearch = false, numberedOptions = false, open, onOpenChange, tooltip, tooltipShortcut }: {
  ariaLabel: string
  closeOnSelect?: boolean
  hideSearch?: boolean
  emptyOptionLabel?: string
  emptyOptionShortcut?: string
  emptyOptionEnd?: string
  emptyOptionIcon?: ReactNode
  emptyTriggerLabel: string
  hoverClassName?: string
  hoverContent?: ReactNode
  icon?: ReactNode
  label: string
  multiple?: boolean
  onChange: (id: string) => void | Promise<void>
  optionHoverClassName?: string
  optionHoverContent?: (person: PersonPickerOption) => ReactNode
  people: PersonPickerOption[]
  searchPlaceholder: string
  searchShortcut?: string
  selectedId?: string
  selectedIds?: string[]
  showUnselectedGroupWhenEmpty?: boolean
  surfaceClassName?: string
  trigger?: ReactNode
  triggerClassName: string
  unselectedGroupLabel?: string
  value?: string
  embedded?: boolean
  side?: 'top'|'right'|'bottom'|'left'
  alignOffset?: number
  extraOptions?: PropertyOption[]
  /** Number keys: people keep their order and get shortcuts 1-9 after the empty option's own (the reference app's assignee list). */
  numberedOptions?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  tooltip?: string
  tooltipShortcut?: string
}) {
  const { t } = useI18n()
  useUserPreferences()
  people = people.map(person=>({...person,label:displayUserName({displayName:personDisplayName(person),name:person.name && person.name!==person.id ? person.name : personDisplayName(person)})}))
  const selectedSet = new Set(multiple || selectedIds.length ? selectedIds : selectedId ? [selectedId] : [])
  const selectedPeople = people.filter(person => selectedSet.has(person.id))
  const selected = selectedPeople[0]
  const orderedPeople = selectedPeople.length && !numberedOptions ? [...selectedPeople, ...people.filter(person => !selectedSet.has(person.id))] : people
  const shouldGroupUnselected = Boolean(unselectedGroupLabel && (showUnselectedGroupWhenEmpty || selectedPeople.length > 0))
  const displayValue = value ?? (multiple
    ? selectedPeople.length ? t(`${selectedPeople.length} ${selectedPeople.length === 1 ? 'member' : 'members'}`) : t(emptyTriggerLabel)
    : selected?.label ?? t(emptyTriggerLabel))
  let position = 0
  const options = [
    ...(emptyOptionLabel ? [{ id: '', label: emptyOptionLabel, icon: emptyOptionIcon ?? <NoAssigneeIcon size={16}/>, shortcut: emptyOptionShortcut, end: emptyOptionEnd }] : []),
    ...orderedPeople.map(person => {
      const grouped = !selectedSet.has(person.id) && shouldGroupUnselected
      return {
        id: person.id,
        label: person.label,
        keywords: personSearchText(person),
        person,
        searchOnly: person.searchOnly,
        icon: person.icon ?? <PersonAvatar person={person}/>,
        end: person.end ?? (person.invited ? 'Invited' : undefined),
        ...(isAwayNow(person.outOfOfficeUntil) ? { labelContent: <>{person.label}<span className="property-command-away">{t('Away')}</span></> } : {}),
        disabled: person.disabled,
        groupId: unselectedGroupLabel ? grouped ? person.groupId ?? `people-${unselectedGroupLabel}` : undefined : person.groupId,
        groupLabel: unselectedGroupLabel ? grouped ? person.groupLabel ?? unselectedGroupLabel : undefined : person.groupLabel,
        ...(person.hoverContent || optionHoverContent?.(person)
          ? { hoverContent: person.hoverContent ?? optionHoverContent?.(person), hoverClassName: person.hoverClassName ?? optionHoverClassName }
          : person.app ? agentHoverProps({ id: person.id, name: person.name ?? person.label, displayName: person.label, avatarUrl: person.avatarUrl }) : {}),
        shortcut: numberedOptions && !person.searchOnly ? assigneeShortcut(++position) : undefined,
        i18nIgnore: true,
      }
    }),
    ...extraOptions,
  ]
  return <PropertyMenu
    open={open}
    onOpenChange={onOpenChange}
    tooltip={tooltip}
    tooltipShortcut={tooltipShortcut}
    embedded={embedded}
    side={side}
    alignOffset={alignOffset}
    hideSearch={hideSearch}
    ariaLabel={t(ariaLabel)}
    closeOnSelect={closeOnSelect}
    compact
    hoverClassName={hoverClassName}
    hoverContent={hoverContent}
    label={t(label)}
    multiple={multiple}
    onChange={onChange}
    options={options}
    searchPlaceholder={t(searchPlaceholder)}
    searchShortcut={searchShortcut}
    selectedId={multiple ? undefined : selectedId ?? ''}
    selectedIds={multiple || selectedIds.length ? selectedIds : undefined}
    surfaceClassName={surfaceClassName}
    trigger={trigger ?? <>{icon ?? (selected ? <PersonAvatar person={selected}/> : <NoAssigneeIcon size={15}/>) }<span data-i18n-ignore={!multiple && selected ? true : undefined}>{displayValue}</span></>}
    triggerClassName={triggerClassName}
    value={displayValue}
    valueIsEntityName={!multiple && Boolean(selected)}
  />
}

function PersonAvatar({ person }: { person: PersonPickerOption }) {
  const invited = person.invited || person.end === 'Invited'
  return <UserAvatar avatarUrl={person.avatarUrl} className={`avatar core-person-picker-avatar${invited ? ' is-invited' : ''}`} color={person.color ?? avatarColor(person.id)} name={person.label}/>
}

/** Row id of the delegated agent hung under the assignee; choosing it opens the agent submenu. */
export const ASSIGNEE_DELEGATE_ROW_ID = '__delegate-row__'

/**
 * One issue's assignee menu, shared by the issue sidebar, compact pickers, list rows and board cards (the reference
 * app's assignee menu):
 *  - not delegated: "No assignee" (key 0), the assignee, the viewer and the built-in agent (keys 1-9), then
 *    "Other agents" and "Team members" without keys, and "New user / Invite and assign…";
 *  - delegated: the same top rows with the delegated agent hung under the assignee (the viewer when nobody is
 *    assigned) on a nesting curve, as "<agent> [Agent] ▶" with no check or key; it opens a submenu of
 *    "No agent", the delegate (checked) and the other agents, which leave the main list (search still finds them).
 * Only the human assignee is checked ("No assignee" when there is none). Picking a person assigns them, picking an
 * agent delegates to it; "No assignee" unassigns and then asks "Remove <agent> too?" (the reference app `bB`).
 */
export function IssueAssigneeMenu({ people, assignee, delegate, viewerId, trigger, triggerClassName, ariaLabel, value, hoverContent, hoverClassName, hoverPlacement, delegateSessionActive = false, onAssign, onDelegate, onRemoveAgent, onUnassignAll, onInvite = openMemberInvite }: {
  /** The humans and agents the issue can take (see `assigneeCandidates`). */
  people: User[]
  assignee?: User
  delegate?: User
  /** Defaults to the signed-in viewer from the people directory. */
  viewerId?: string
  trigger: ReactNode
  triggerClassName?: string
  ariaLabel: string
  value?: string
  hoverContent?: ReactNode
  hoverClassName?: string
  hoverPlacement?: 'left' | 'below'
  delegateSessionActive?: boolean
  /** A person picked, or '' to unassign. */
  onAssign: (id: string) => void | Promise<void>
  /** An agent picked (delegate to it), or '' to take the delegated agent off the issue. */
  onDelegate: (id: string) => void | Promise<void>
  /** "No agent" in the delegate's submenu (defaults to `onDelegate('')`). */
  onRemoveAgent?: () => void | Promise<void>
  /** "No assignee", then "Remove agent": clear both in one update (defaults to `onAssign('')` then `onDelegate('')`). */
  onUnassignAll?: () => void | Promise<void>
  onInvite?: () => void
}) {
  const { t, locale } = useI18n()
  useUserPreferences()
  const directory = usePeopleDirectory()
  // A delegate the list no longer offers (e.g. lost team access) keeps its row so it can be removed.
  const listed = delegate && !people.some(user => user.id === delegate.id) ? [...people, { ...delegate, app: true }] : people
  const layout = assigneeMenuLayout(listed, { assigneeId: assignee?.id, delegateId: delegate?.id, viewerId: viewerId ?? directory.viewerId, locale })
  const agentIds = new Set(listed.filter(user => user.app).map(user => user.id))
  const personRow = (user: User, extra: Partial<PropertyOption> = {}): PropertyOption => {
    const display = personDisplayName({ ...user, label: user.displayName })
    const label = displayUserName({ displayName: display, name: user.name && user.name !== user.id ? user.name : display })
    const person = { ...user, label }
    return {
      id: user.id,
      label,
      keywords: personSearchText(person),
      icon: <PersonAvatar person={person}/>,
      ...(isAwayNow(user.outOfOfficeUntil) ? { labelContent: <>{label}<span className="property-command-away">{t('Away')}</span></> } : {}),
      ...(user.app ? agentHoverProps({ id: user.id, name: user.username || user.name || label, displayName: label, avatarUrl: user.avatarUrl }) : {}),
      i18nIgnore: true,
      ...extra,
    }
  }
  const plainRow = (user: User) => personRow(user, { hoverContent: undefined, hoverClassName: undefined })
  const delegateLabel = delegate ? delegate.displayName || delegate.name : ''
  const delegateRow: PropertyOption | undefined = delegate ? {
    id: ASSIGNEE_DELEGATE_ROW_ID,
    label: delegateLabel,
    keywords: personSearchText({ ...delegate, label: delegateLabel }),
    icon: layout.delegateAnchorId ? <NestedDelegateAvatar user={delegate}/> : <PersonAvatar person={{ ...delegate, label: delegateLabel }}/>,
    labelContent: <span className="core-delegate-row-label"><span className="core-delegate-row-name" data-i18n-ignore>{delegateLabel}</span><span className="core-delegate-row-pill">{t('Agent')}</span></span>,
    i18nIgnore: true,
    submenu: {
      bare: true,
      className: 'core-agent-submenu',
      placeholder: 'Delegate to…',
      heading: 'Agent',
      selectedId: delegate.id,
      options: [{ id: '', label: 'No agent', icon: <FlowGlyph name="noAgent" size={16}/> }, plainRow(listed.find(user => user.id === delegate.id) ?? delegate), ...layout.searchOnly.map(plainRow)],
      onChoose: id => { if (id === delegate.id) return; void (id ? onDelegate(id) : onRemoveAgent ? onRemoveAgent() : onDelegate('')) },
    },
  } : undefined
  let position = 0
  const options: PropertyOption[] = [
    { id: '', label: 'No assignee', icon: <NoAssigneeIcon size={16}/>, shortcut: '0' },
    ...layout.top.flatMap(user => [personRow(user, { shortcut: assigneeShortcut(++position) }), ...(delegateRow && user.id === layout.delegateAnchorId ? [delegateRow] : [])]),
    ...(delegateRow && !layout.delegateAnchorId ? [delegateRow] : []),
    ...layout.otherAgents.map(user => personRow(user, { groupId: 'assignee-other-agents', groupLabel: 'Other agents' })),
    ...layout.members.map(user => personRow(user, { groupId: 'assignee-team-members', groupLabel: 'Team members' })),
    ...layout.searchOnly.map(user => personRow(user, { searchOnly: true, groupId: 'assignee-other-agents', groupLabel: 'Other agents' })),
    ...(viewerCanInvite() ? [{ id: ASSIGNEE_INVITE_ID, label: 'Invite and assign…', icon: <FlowGlyph className="property-command-invite-icon" name="invitePlane" size={16}/>, groupId: 'assignee-new-user', groupLabel: 'New user', action: true }] : []),
  ]
  const choose = async (id: string) => {
    if (id === ASSIGNEE_INVITE_ID) { onInvite(); return }
    if (agentIds.has(id)) { if (id !== delegate?.id) await onDelegate(id); return }
    if (id) { await onAssign(id); return }
    // "No assignee" is the current row when nobody is assigned: a delegated agent stays (its submenu removes it).
    if (!assignee) return
    if (!delegate) { await onAssign(''); return }
    const remove = await confirmRemoveDelegate(delegate, delegateSessionActive, t)
    if (remove && onUnassignAll) { await onUnassignAll(); return }
    await onAssign('')
    if (remove) await onDelegate('')
  }
  return <PropertyMenu
    ariaLabel={ariaLabel}
    compact
    hoverClassName={hoverClassName}
    hoverContent={hoverContent}
    hoverPlacement={hoverPlacement}
    label="Assignee"
    onChange={choose}
    options={options}
    searchPlaceholder={t('Assign to…')}
    searchShortcut="A"
    selectedId={assignee?.id ?? ''}
    trigger={trigger}
    triggerClassName={triggerClassName}
    value={value}
    valueIsEntityName={Boolean(assignee)}
  />
}

/**
 * The issue's assignee control on property surfaces. `delegateRow` is the issue sidebar (the reference app
 * `IssueDetailsPaneSidebar`): the delegate also has its own row (`DelegatePicker`) hung under the assignee on a
 * NestingLine, so this trigger shows only the human ("Assign" when none). Compact surfaces (mobile chips, peek)
 * stack the delegate's avatar after the assignee's in the trigger. Both open `IssueAssigneeMenu`.
 */
export function AssigneePicker({ value, delegate, users, onChange, onDelegate, onRemoveAgent, onInvite = openMemberInvite, teamId, hoverContext, allowAgents = true, delegateRow = false, delegateSessionActive = false }: { value?: User; delegate?: User; users: User[]; onChange: (id: string) => void | Promise<void>; onDelegate?: (id: string) => void | Promise<void>; onRemoveAgent?: () => void | Promise<void>; onInvite?: () => void; teamId?: string; hoverContext?: { member?: WorkspaceMember; online?: boolean; workspaceName: string; project?: ProjectSummary }; allowAgents?: boolean; delegateRow?: boolean; delegateSessionActive?: boolean }) {
  const { t } = useI18n()
  useUserPreferences()
  const candidates = assigneeCandidates(users, teamId, { agents: allowAgents })
  const avatar = (user: User) => <PersonAvatar person={{ id: user.id, label: user.displayName, name: user.name, avatarUrl: user.avatarUrl, color: avatarColor(user.id) }}/>
  const assigneeName = value ? displayUserName({ displayName: personDisplayName({ ...value, label: value.displayName }), name: value.name && value.name !== value.id ? value.name : value.displayName }) : undefined
  const label = delegate && !value ? `Change assignee. ${delegate.displayName} is delegated` : value ? `Change assignee. ${value.displayName} is assigned` : 'Change assignee. Currently no one is assigned.'
  const personHover = Boolean(value && hoverContext)
  const stacked = Boolean(delegate && !delegateRow)
  const trigger = stacked && delegate
    ? <><span className="core-assignee-stack">{value && avatar(value)}{avatar(delegate)}</span><span data-i18n-ignore>{delegate.displayName}</span></>
    : value ? <>{avatar(value)}<span data-i18n-ignore>{assigneeName}</span></> : <><NoAssigneeIcon size={15}/><span>{t('Assign')}</span></>
  return <div className="core-property-picker"><IssueAssigneeMenu
    ariaLabel={t(label)}
    assignee={value}
    delegate={delegate}
    delegateSessionActive={delegateSessionActive}
    hoverContent={personHover && value && hoverContext ? <AssigneeHoverPreview user={value} {...hoverContext}/> : stacked && delegate ? <AgentHoverCard agent={delegate}/> : undefined}
    hoverClassName={!personHover && stacked ? AGENT_HOVER_CLASS : 'property-rich-hover assignee-hover-surface'}
    onAssign={onChange}
    onDelegate={onDelegate ?? onChange}
    onRemoveAgent={onRemoveAgent}
    onInvite={onInvite}
    people={candidates}
    trigger={trigger}
    triggerClassName={`core-property-trigger${value || stacked ? '' : ' muted'}`}
    value={assigneeName ?? t('Assign')}
  /></div>
}

/** The reference app's assignee menu: the delegated agent's avatar, 24px in, on a NestingLine from the assignee row above. */
export function NestedDelegateAvatar({ user }: { user: User }) {
  return <span className="core-nested-delegate-icon"><NestingLine className="core-nested-delegate-line" heightAbove={9}/><PersonAvatar person={{ id: user.id, label: user.displayName, name: user.name, avatarUrl: user.avatarUrl, color: avatarColor(user.id) }}/></span>
}

/**
 * The reference app's sidebar delegate row (`IssueDetailsPaneSidebar`, "Delegate to…"): the delegated agent's avatar and
 * name; the menu offers "No agent" (key 0), the delegated agent (checked) and the other agents that can work in
 * the issue's team. Picking another agent re-delegates; "No agent" removes the delegate (the human stays).
 */
export function DelegatePicker({ delegate, users, teamId, allowAgents = true, onDelegate }: { delegate: User; users: User[]; teamId?: string; allowAgents?: boolean; onDelegate: (id: string) => void | Promise<void> }) {
  const agents = assigneeCandidates(users, teamId, { agents: allowAgents }).filter(user => user.app && user.id !== delegate.id)
  const people = [delegate, ...agents].map(user => ({ ...user, label: user.displayName }))
  const name = delegate.displayName || delegate.name
  return <div className="core-property-picker"><PersonPicker
    ariaLabel={`Change agent. ${name} is delegated`}
    emptyOptionIcon={<FlowGlyph name="noAgent" size={16}/>}
    emptyOptionLabel="No agent"
    emptyOptionShortcut="0"
    emptyTriggerLabel="Delegate"
    hoverClassName={AGENT_HOVER_CLASS}
    hoverContent={<AgentHoverCard agent={delegate}/>}
    label="Agent"
    onChange={id => id === delegate.id ? undefined : onDelegate(id)}
    people={people}
    searchPlaceholder="Delegate to…"
    selectedId={delegate.id}
    trigger={<><PersonAvatar person={{ id: delegate.id, label: name, name: delegate.name, avatarUrl: delegate.avatarUrl, color: avatarColor(delegate.id) }}/><span data-i18n-ignore>{name}</span></>}
    triggerClassName="core-property-trigger issue-delegate-trigger"
  /></div>
}
