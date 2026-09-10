import { type ReactNode } from 'react'
import { PriorityIcon, ProjectStatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import styles from './project-row-menus.module.css'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import { usePeopleDirectory } from '@/components/property/people-context'
import { directoryPerson } from '@/lib/people'

export type ProjectPickerProperty = 'priority' | 'lead' | 'status'

export type ProjectPropertyOption = {
  avatarUrl?: string
  color?: string
  group?: string
  groupId?: string
  groupColor?: string
  keywords?: string
  label: string
  shortcut?: string
  statusType?: string
  value: string
}

const PICKER_COPY: Record<ProjectPickerProperty, { placeholder: string; keys: [string, string] }> = {
  priority: { placeholder: 'Change priority…', keys: ['P', 'P'] },
  lead: { placeholder: 'Set lead…', keys: ['P', 'A'] },
  status: { placeholder: 'Change status…', keys: ['P', 'S'] },
}

export function ProjectPropertyPicker({ buttonClassName = '', children, label, onChange, options, property, value }: {
  buttonClassName?: string
  children: ReactNode
  label: string
  onChange: (value: string) => void
  options: ProjectPropertyOption[]
  property: ProjectPickerProperty
  value: string
}) {
  const directory = usePeopleDirectory()
  const copy = PICKER_COPY[property]
  if (property === 'lead') return <PersonPicker ariaLabel={label} label="Lead" emptyTriggerLabel="Lead" emptyOptionLabel="No lead" emptyOptionShortcut="0" selectedId={value} trigger={children} triggerClassName={`lp-project-property-trigger ${buttonClassName}`} surfaceClassName={styles.command} searchPlaceholder={copy.placeholder} searchShortcut="P, then A" onChange={onChange}
    people={orderProjectPropertyOptions(options,property,value).filter(option=>option.value).map(option=>({...directoryPerson(directory.users,option.value),id:option.value,label:option.label,avatarUrl:option.avatarUrl ?? directory.users.get(option.value)?.avatarUrl,groupLabel:option.group}))}/>
  const menuOptions: PropertyOption[] = orderProjectPropertyOptions(options, property, value).map(option => ({
    id: option.value,
    label: option.label,
    keywords: option.keywords,
    shortcut: option.shortcut,
    groupId: option.group,
    groupLabel: option.group,
    icon: <ProjectPropertyOptionIcon option={option} property={property}/>,
    i18nIgnore: property === 'status',
  }))
  return <PropertyMenu
    align={property === 'status' ? 'end' : 'start'}
    ariaLabel={label}
    label={property === 'priority' ? 'Priority' : 'Status'}
    onChange={onChange}
    options={menuOptions}
    searchPlaceholder={copy.placeholder}
    searchShortcut={`${copy.keys[0]} then ${copy.keys[1]}`}
    selectedId={value}
    surfaceClassName={styles.command}
    trigger={children}
    triggerClassName={`lp-project-property-trigger ${buttonClassName}`}
    value={options.find(option => option.value === value)?.label}
    valueIsEntityName={property === 'status' && Boolean(value)}
  />
}

function ProjectPropertyOptionIcon({ option, property }: { option: ProjectPropertyOption; property: Exclude<ProjectPickerProperty,'lead'> }) {
  if (property === 'priority') return <PriorityIcon priority={priorityNumber(option.value)} size={15}/>
  return <ProjectStatusGlyph color={option.color} name={option.label} type={option.statusType}/>
}

export function ProjectStatusGlyph({ color: _color, name, progress, type }: { color?: string; name: string; progress?: number; type?: string }) {
  const normalized = `${type ?? ''} ${name}`.toLowerCase()
  const kind = normalized.includes('backlog') ? 'backlog' : normalized.includes('progress') || normalized.includes('started') ? 'started' : normalized.includes('complete') ? 'completed' : normalized.includes('cancel') ? 'canceled' : 'planned'
  const iconColor = ({ backlog: '#d6a526', started: '#d6a526', completed: '#5e6ad2', canceled: '#77777c', planned: '#b5b5ba' } as const)[kind]
  return <ProjectStatusIcon className={styles.statusIcon} color={_color || iconColor} name={name} progress={progress} type={type}/>
}

function orderProjectPropertyOptions(options: ProjectPropertyOption[], property: ProjectPickerProperty, selected: string) {
  if (property !== 'lead') return options
  const empty = options.find(option => !option.value)
  const selectedMatch = selected ? options.find(option => option.value === selected) : undefined
  const selectedOption = selectedMatch ? { ...selectedMatch, group: undefined } : undefined
  const rest = options.filter(option => option.value && option.value !== selected).map(option => ({ ...option, group: option.group ?? 'Users from the project team' }))
  return [empty, selectedOption, ...rest].filter((option): option is ProjectPropertyOption => Boolean(option))
}

function priorityNumber(value: string) { return ({ none: 0, urgent: 1, high: 2, medium: 3, low: 4 } as Record<string, number>)[value] ?? 0 }
