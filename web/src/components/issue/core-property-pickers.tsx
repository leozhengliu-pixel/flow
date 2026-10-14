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
import { ASSIGNEE_INVITE_ID, assigneeCandidates, assigneeShortcut } from '@/lib/agent-members'
import { openMemberInvite, viewerCanInvite } from '@/lib/invite-navigation'
import { avatarColor } from '@/components/issue/avatar-color'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { NestingLine } from '@/components/ui/nesting-line'
import { confirmAction } from '@/components/ui/action-dialog-service'

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
  const options = labels.map((label, priority) => ({ id: String(priority), label, icon: <PriorityIcon priority={priority}/>, shortcut: String(priority) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Priority"
    value={value ? labels[value] : 'Priority'}
    selectedId={String(value)}
    options={options}
    searchPlaceholder="Change priority…"
    searchShortcut="P"
    ariaLabel={`Change priority. ${value ? `${labels[value]} is selected` : 'No priority is selected'}`}
    triggerClassName={`core-property-trigger${value === 0 ? ' muted' : ''}`}
    trigger={<><PriorityIcon priority={value}/><span>{value ? labels[value] : 'Set priority'}</span></>}
    hoverContent={<PropertyShortcutTooltip label="Change priority" shortcut="P"/>}
    onChange={id => onChange(Number(id))}
  /></div>
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

/**
 * The reference app's assignee picker: "No assignee" (key 0), the people (keys 1-9), then the agents that can work in
 * the issue's team, continuing the numbering, all in one list with no group header or pill (the "Agent"
 * pill is only in an agent row's hover card). Below, "New user / Invite and assign…" opens the invite flow.
 *
 * There is no separate "Delegate to agent" control, so this picker carries the delegation too:
 *  - picking a person assigns them (`onChange`); an existing delegate stays;
 *  - picking an agent delegates the issue to it (`onDelegate`, else `onChange`, which the server also turns
 *    into a delegation; with no human assignee the server makes the viewer the assignee);
 *  - both the assignee and the delegate show a check.
 *
 * Compact surfaces (mobile chips) stack the delegate's avatar after the assignee's in the trigger; there
 * picking the delegated agent again removes it and "No assignee" with only a delegate removes the delegate.
 *
 * `delegateRow` is the issue sidebar (the reference app `IssueDetailsPaneSidebar`): the delegate has its own row
 * (`DelegatePicker`) hung under the assignee on a NestingLine, so this trigger shows only the human ("Assign"
 * when none); in the menu the delegated agent sits right under the assignee on the same curve; picking it
 * again changes nothing; "No assignee" unassigns and then asks "Remove <agent> too?" (the reference app `bB`).
 */
export function AssigneePicker({ value, delegate, users, onChange, onDelegate, onInvite = openMemberInvite, teamId, hoverContext, allowAgents = true, delegateRow = false, delegateSessionActive = false }: { value?: User; delegate?: User; users: User[]; onChange: (id: string) => void | Promise<void>; onDelegate?: (id: string) => void | Promise<void>; onInvite?: () => void; teamId?: string; hoverContext?: { member?: WorkspaceMember; online?: boolean; workspaceName: string; project?: ProjectSummary }; allowAgents?: boolean; delegateRow?: boolean; delegateSessionActive?: boolean }) {
  const { t } = useI18n()
  const candidates = assigneeCandidates(users, teamId, { agents: allowAgents })
  // A delegate the list no longer offers (e.g. lost team access) stays listed so it can be removed.
  const listed = delegate && !candidates.some(user => user.id === delegate.id) ? [...candidates, delegate] : candidates
  const nestDelegate = Boolean(delegateRow && value && delegate && listed.some(user => user.id === value.id))
  const ordered = nestDelegate && delegate ? listed.filter(user => user.id !== delegate.id).flatMap(user => user.id === value?.id ? [user, delegate] : [user]) : listed
  const people = ordered.map(user => ({ ...user, label: user.displayName, ...(nestDelegate && user.id === delegate?.id ? { icon: <NestedDelegateAvatar user={user}/> } : {}) }))
  const agentIds = new Set(people.filter(person => person.app).map(person => person.id))
  const change = async (id: string) => {
    if (id === ASSIGNEE_INVITE_ID) { onInvite(); return }
    if (delegateRow) {
      if (agentIds.has(id)) return delegate?.id === id ? undefined : onDelegate ? onDelegate(id) : onChange(id)
      if (id) return onChange(id)
      if (!value) return
      await onChange('')
      if (delegate && onDelegate && await confirmRemoveDelegate(delegate, delegateSessionActive, t)) await onDelegate('')
      return
    }
    if (agentIds.has(id)) return onDelegate ? onDelegate(delegate?.id === id ? '' : id) : onChange(id)
    if (!id && !value && delegate && onDelegate) return onDelegate('')
    return onChange(id)
  }
  const checked = [value?.id, delegate?.id].filter((id): id is string => Boolean(id))
  const avatar = (user: User) => <PersonAvatar person={{ id: user.id, label: user.displayName, name: user.name, avatarUrl: user.avatarUrl, color: avatarColor(user.id) }}/>
  const label = delegate && !value ? `Change assignee. ${delegate.displayName} is delegated` : value ? `Change assignee. ${value.displayName} is assigned` : 'Change assignee. Currently no one is assigned.'
  const personHover = Boolean(value && hoverContext)
  const stacked = Boolean(delegate && !delegateRow)
  return <div className="core-property-picker"><PersonPicker
    ariaLabel={label}
    emptyOptionLabel="No assignee"
    emptyOptionShortcut="0"
    emptyTriggerLabel="Assign"
    extraOptions={viewerCanInvite() ? [{ id: ASSIGNEE_INVITE_ID, label: 'Invite and assign…', icon: <FlowGlyph className="property-command-invite-icon" name="invitePlane" size={16}/>, groupLabel: 'New user', action: true }] : []}
    hoverContent={personHover && value && hoverContext ? <AssigneeHoverPreview user={value} {...hoverContext}/> : stacked && delegate ? <AgentHoverCard agent={delegate}/> : undefined}
    hoverClassName={!personHover && stacked ? AGENT_HOVER_CLASS : 'property-rich-hover assignee-hover-surface'}
    label="Assignee"
    numberedOptions
    onChange={change}
    people={people}
    searchPlaceholder="Assign to…"
    searchShortcut="A"
    selectedId={value?.id}
    selectedIds={checked}
    trigger={stacked && delegate ? <><span className="core-assignee-stack">{value && avatar(value)}{avatar(delegate)}</span><span data-i18n-ignore>{delegate.displayName}</span></> : delegateRow && !value ? <><NoAssigneeIcon size={15}/><span>{t('Assign')}</span></> : undefined}
    triggerClassName={`core-property-trigger${value || stacked ? '' : ' muted'}`}
  /></div>
}

/** The reference app's assignee menu: the delegated agent's avatar, 24px in, on a NestingLine from the assignee row above. */
function NestedDelegateAvatar({ user }: { user: User }) {
  return <span className="core-nested-delegate-icon"><NestingLine className="core-nested-delegate-line" heightAbove={9}/><PersonAvatar person={{ id: user.id, label: user.displayName, name: user.name, avatarUrl: user.avatarUrl, color: avatarColor(user.id) }}/></span>
}

/** The reference app `bB`: after unassigning, ask whether the delegated agent should go too ("Keep agent" leaves it). */
async function confirmRemoveDelegate(delegate: User, sessionActive: boolean, translate: (source: string) => string) {
  const name = delegate.displayName || delegate.name
  const description = sessionActive
    ? translate('{name} is still set to work on this issue. You can keep the agent on the issue, or remove it and archive its working session.')
    : translate('{name} is still set to work on this issue. You can keep the agent on the issue.')
  return confirmAction(translate('Remove {name} too?').replace('{name}', name), { description: description.replace('{name}', name), confirmLabel: translate('Remove agent'), cancelLabel: translate('Keep agent'), danger: false })
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
