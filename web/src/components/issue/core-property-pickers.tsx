import type { ReactNode } from 'react'
import { Building2, Clock3, Layers3 } from 'lucide-react'
import type { ActivityEvent, ProjectSummary, User, WorkspaceMember, WorkflowState } from '@/types/flow'
import { NoAssigneeIcon, PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { AssigneeHoverPreview, PropertyShortcutTooltip, StatusHoverPreview } from '@/components/property/issue-property-hover'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { PersonIdentityDetails } from '@/components/property/person-info'
import { personDisplayName, personSearchText } from '@/lib/people'
import { displayUserName, useUserPreferences } from '@/lib/runtime-preferences'

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
    value={value ? labels[value] : 'Priority'}
    selectedId={String(value)}
    options={options}
    searchPlaceholder="Change priority…"
    searchShortcut="P"
    ariaLabel={`Change priority. ${value ? `${labels[value]} is selected` : 'No priority is selected'}`}
    triggerClassName={`core-property-trigger${value === 0 ? ' muted' : ''}`}
    trigger={<><PriorityIcon priority={value}/><span>{value ? labels[value] : 'Priority'}</span></>}
    hoverContent={<PropertyShortcutTooltip label="Change priority" shortcut="P"/>}
    onChange={id => onChange(Number(id))}
  /></div>
}

export type PersonPickerOption = {
  searchOnly?: boolean
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

export function PersonPicker({ ariaLabel, closeOnSelect, emptyOptionLabel, emptyOptionShortcut, emptyOptionEnd, emptyTriggerLabel, hoverClassName, hoverContent, icon, label, multiple = false, onChange, optionHoverClassName, optionHoverContent, people, searchPlaceholder, searchShortcut, selectedId, selectedIds = [], showUnselectedGroupWhenEmpty = false, surfaceClassName, trigger, triggerClassName, unselectedGroupLabel, value, embedded = false, side, alignOffset, extraOptions = [] }: {
  ariaLabel: string
  closeOnSelect?: boolean
  emptyOptionLabel?: string
  emptyOptionShortcut?: string
  emptyOptionEnd?: string
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
}) {
  const { t } = useI18n()
  useUserPreferences()
  people = people.map(person=>({...person,label:displayUserName({displayName:personDisplayName(person),name:person.name && person.name!==person.id ? person.name : personDisplayName(person)})}))
  const selectedSet = new Set(multiple ? selectedIds : selectedId ? [selectedId] : [])
  const selectedPeople = people.filter(person => selectedSet.has(person.id))
  const selected = selectedPeople[0]
  const orderedPeople = selectedPeople.length ? [...selectedPeople, ...people.filter(person => !selectedSet.has(person.id))] : people
  const shouldGroupUnselected = Boolean(unselectedGroupLabel && (showUnselectedGroupWhenEmpty || selectedPeople.length > 0))
  const displayValue = value ?? (multiple
    ? selectedPeople.length ? t(`${selectedPeople.length} ${selectedPeople.length === 1 ? 'member' : 'members'}`) : t(emptyTriggerLabel)
    : selected?.label ?? t(emptyTriggerLabel))
  const options = [
    ...(emptyOptionLabel ? [{ id: '', label: emptyOptionLabel, icon: <NoAssigneeIcon size={15}/>, shortcut: emptyOptionShortcut, end: emptyOptionEnd }] : []),
    ...orderedPeople.map(person => {
      const grouped = !selectedSet.has(person.id) && shouldGroupUnselected
      return {
        id: person.id,
        label: person.label,
        keywords: personSearchText(person),
        person,
        searchOnly: person.searchOnly,
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
    ...extraOptions,
  ]
  return <PropertyMenu
    embedded={embedded}
    side={side}
    alignOffset={alignOffset}
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
    emptyTriggerLabel="Assignee"
    hoverContent={value&&hoverContext?<AssigneeHoverPreview user={value} {...hoverContext}/>:undefined}
    hoverClassName="property-rich-hover assignee-hover-surface"
    label="Assignee"
    onChange={onChange}
    people={users.map(user => ({ ...user, label: user.displayName }))}
    searchPlaceholder="Change assignee…"
    searchShortcut="A"
    selectedId={value?.id}
    triggerClassName={`core-property-trigger${value ? '' : ' muted'}`}
  /></div>
}
