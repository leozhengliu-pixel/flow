import type { ReactNode } from 'react'
import { Building2, Clock3, Layers3 } from 'lucide-react'
import type { ActivityEvent, ProjectSummary, User, WorkspaceMember, WorkflowState } from '@/types/flow'
import { NoAssigneeIcon, PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { AssigneeHoverPreview, PropertyShortcutTooltip, StatusHoverPreview } from '@/components/property/issue-property-hover'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'

export function StatusPicker({ value, states, onChange, hoverHistory }: { value: WorkflowState; states: WorkflowState[]; onChange: (id: string) => void | Promise<void>; hoverHistory?: { activities: ActivityEvent[]; issueCreatedAt: string } }) {
  const options = [...states].sort((left,right)=>(left.position??0)-(right.position??0)).map((state, index) => ({ id: state.id, label: state.name, icon: <StatusIcon state={state}/>, shortcut: String(index + 1) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Status"
    value={value.name}
    selectedId={value.id}
    options={options}
    searchPlaceholder="Change status…"
    searchShortcut="S"
    ariaLabel={`Change status. Current status is ${value.name}`}
    triggerClassName="core-property-trigger"
    trigger={<><StatusIcon state={value}/><span>{value.name}</span></>}
    hoverContent={hoverHistory?<StatusHoverPreview state={value} activities={hoverHistory.activities} issueCreatedAt={hoverHistory.issueCreatedAt}/>:undefined}
    hoverClassName="property-rich-hover"
    onChange={onChange}
  /></div>
}

export function PriorityPicker({ value, onChange }: { value: number; onChange: (value: number) => void | Promise<void> }) {
  const labels = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
  const options = labels.map((label, priority) => ({ id: String(priority), label, icon: <PriorityIcon priority={priority}/>, shortcut: String(priority) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Priority"
    value={value ? labels[value] : 'Set priority'}
    selectedId={String(value)}
    options={options}
    searchPlaceholder="Change priority…"
    ariaLabel={`Change priority. ${value ? `${labels[value]} is selected` : 'No priority is selected'}`}
    triggerClassName={`core-property-trigger${value === 0 ? ' muted' : ''}`}
    trigger={<><PriorityIcon priority={value}/><span>{value ? labels[value] : 'Set priority'}</span></>}
    hoverContent={<PropertyShortcutTooltip label="Change priority" shortcut="P"/>}
    onChange={id => onChange(Number(id))}
  /></div>
}

export type PersonPickerOption = {
  id: string
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
}

export function PersonHoverPreview({ person, projectName, workspaceName }: { person: PersonPickerOption; projectName?: string; workspaceName: string }) {
  const { t } = useI18n()
  const invited = person.invited || person.end === 'Invited'
  const online = !invited && person.active !== false && person.online === true
  return <div className="assignee-hover-preview">
    <header><PersonAvatar person={person}/><div><strong data-i18n-ignore>{person.label}</strong><span data-i18n-ignore>{person.name || person.email || person.label}</span></div></header>
    <div className="assignee-hover-preview__details">
      <span><i className={online ? undefined : 'offline'}/>{invited ? t('Invited') : online ? t('Online') : t('Offline')}</span>
      {!invited && <><span><Clock3/><time>{new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date())}</time><small>{t('local time')}</small></span><span><Building2/><span data-i18n-ignore>{workspaceName}</span></span><span><Layers3/><span data-i18n-ignore>{projectName ?? t('No project')}</span></span></>}
      {invited && <span><Clock3/><time>{t('Invitation pending')}</time></span>}
    </div>
  </div>
}

export function PersonPicker({ ariaLabel, closeOnSelect, emptyOptionLabel, emptyOptionShortcut, emptyTriggerLabel, hoverClassName, hoverContent, icon, label, multiple = false, onChange, optionHoverClassName, optionHoverContent, people, searchPlaceholder, searchShortcut, selectedId, selectedIds = [], showUnselectedGroupWhenEmpty = false, surfaceClassName, trigger, triggerClassName, unselectedGroupLabel, value }: {
  ariaLabel: string
  closeOnSelect?: boolean
  emptyOptionLabel?: string
  emptyOptionShortcut?: string
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
}) {
  const { t } = useI18n()
  const selectedSet = new Set(multiple ? selectedIds : selectedId ? [selectedId] : [])
  const selectedPeople = people.filter(person => selectedSet.has(person.id))
  const selected = selectedPeople[0]
  const orderedPeople = selectedPeople.length ? [...selectedPeople, ...people.filter(person => !selectedSet.has(person.id))] : people
  const shouldGroupUnselected = Boolean(unselectedGroupLabel && (showUnselectedGroupWhenEmpty || selectedPeople.length > 0))
  const displayValue = value ?? (multiple
    ? selectedPeople.length ? t(`${selectedPeople.length} ${selectedPeople.length === 1 ? 'member' : 'members'}`) : t(emptyTriggerLabel)
    : selected?.label ?? t(emptyTriggerLabel))
  const options = [
    ...(emptyOptionLabel ? [{ id: '', label: emptyOptionLabel, icon: <NoAssigneeIcon size={15}/>, shortcut: emptyOptionShortcut }] : []),
    ...orderedPeople.map(person => {
      const grouped = !selectedSet.has(person.id) && shouldGroupUnselected
      return {
        id: person.id,
        label: person.label,
        keywords: [person.name, person.email].filter(Boolean).join(' '),
        icon: <PersonAvatar person={person}/>,
        end: person.end ?? (person.invited ? 'Invited' : undefined),
        disabled: person.disabled,
        groupId: unselectedGroupLabel ? grouped ? person.groupId ?? `people-${unselectedGroupLabel}` : undefined : person.groupId,
        groupLabel: unselectedGroupLabel ? grouped ? person.groupLabel ?? unselectedGroupLabel : undefined : person.groupLabel,
        hoverContent: person.hoverContent ?? optionHoverContent?.(person),
        hoverClassName: person.hoverClassName ?? optionHoverClassName,
        i18nIgnore: true,
      }
    }),
  ]
  return <PropertyMenu
    ariaLabel={t(ariaLabel)}
    closeOnSelect={closeOnSelect}
    compact
    hoverClassName={hoverClassName}
    hoverContent={hoverContent}
    keepSelectedVisible={Boolean(selectedId) || multiple}
    label={t(label)}
    multiple={multiple}
    onChange={onChange}
    options={options}
    searchPlaceholder={t(searchPlaceholder)}
    searchShortcut={searchShortcut}
    selectedId={multiple ? undefined : selectedId ?? ''}
    selectedIds={multiple ? selectedIds : undefined}
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

function avatarColor(value: string) {
  const colors = ['#d15f5f', '#5e6ad2', '#4c9a67', '#d09b42']
  return colors[[...value].reduce((sum, character) => sum + character.charCodeAt(0), 0) % colors.length]
}

export function AssigneePicker({ value, users, onChange, hoverContext }: { value?: User; users: User[]; onChange: (id: string) => void | Promise<void>; hoverContext?: { member?: WorkspaceMember; online?: boolean; workspaceName: string; project?: ProjectSummary } }) {
  return <div className="core-property-picker"><PersonPicker
    ariaLabel={`Change assignee. ${value ? `${value.displayName} is assigned` : 'Currently no one is assigned.'}`}
    emptyOptionLabel="No assignee"
    emptyTriggerLabel="Assign to…"
    hoverContent={value&&hoverContext?<AssigneeHoverPreview user={value} {...hoverContext}/>:undefined}
    hoverClassName="property-rich-hover assignee-hover-surface"
    label="Assignee"
    onChange={onChange}
    people={users.map(user => ({ id: user.id, label: user.displayName, email: user.email, name: user.name, avatarUrl: user.avatarUrl, active: user.active }))}
    searchPlaceholder="Change assignee…"
    selectedId={value?.id}
    triggerClassName={`core-property-trigger${value ? '' : ' muted'}`}
  /></div>
}
