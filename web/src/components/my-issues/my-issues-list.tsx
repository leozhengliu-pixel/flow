import { type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Check, ChevronDown, ChevronRight, Plus } from 'lucide-react'
import type { MyIssuesProperty } from './my-issues-surface'
import { CalendarIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu, type PropertyMenuKind } from '@/components/property/property-menu'
import { DueDatePicker } from '@/components/issue/due-date-picker'
import styles from './my-issues-list.module.css'
import { IssueSLAIndicator } from '@/components/issue/issue-sla-indicator'
import type { IssueSLA } from '@/types/flow'

export type MyIssuesStateType = 'backlog' | 'unstarted' | 'started' | 'completed' | 'canceled'
export type MyIssuesContextAction = 'status' | 'priority' | 'assignee' | 'dueDate' | 'labels' | 'project' | 'moreProperties' | 'createRelated' | 'markAs' | 'copy' | 'convertTo' | 'move' | 'openIn' | 'runLoop' | 'favorite' | 'remind' | 'delete'
export type MyIssuesEditableProperty = 'status' | 'priority' | 'assignee' | 'dueDate' | 'labels' | 'project'
export interface MyIssuesContextOption { id: string; label: string; color?: string; description?: string; issueCount?: number; scope?: string; groupId?: string; groupLabel?: string; avatarUrl?: string; kind?: MyIssuesEditableProperty; priority?: 0 | 1 | 2 | 3 | 4; shortcut?: string; stateType?: MyIssuesStateType }
export interface MyIssuesRowPropertyOptions {
  status: MyIssuesContextOption[]
  priority: MyIssuesContextOption[]
  assignee: MyIssuesContextOption[]
  dueDate: MyIssuesContextOption[]
  labels: MyIssuesContextOption[]
  project: MyIssuesContextOption[]
}

export interface MyIssuesRowData {
  id: string
  identifier: string
  title: string
  href?: string
  priority: 0 | 1 | 2 | 3 | 4
  state: { id: string; name: string; type: MyIssuesStateType; color: string }
  labels?: { id: string; name: string; color: string }[]
  project?: { id: string; name: string; color: string }
  assignee?: { id: string; name: string; avatarUrl?: string; color?: string }
  estimate?: number
  dueDate?: string
  sla?: IssueSLA & { ruleName?: string }
  createdAt: string
  updatedAt: string
  parentId?: string
  sortOrder?: number
}

export interface MyIssuesGroupData { id: string; label: string; stateType?: MyIssuesStateType; issues: MyIssuesRowData[] }

export interface MyIssuesListProps {
  groups: MyIssuesGroupData[]
  loading?: boolean
  error?: string
  selectedIds?: ReadonlySet<string>
  collapsedGroupIds?: ReadonlySet<string>
  displayProperties?: ReadonlySet<MyIssuesProperty>
  nestedSubIssues?: boolean
  propertyOptions?: MyIssuesRowPropertyOptions
  mutationErrors?: ReadonlyMap<string, string>
  onClearError?: () => void
  onContextAction?: (issue: MyIssuesRowData, action: MyIssuesContextAction) => void
  onCreateIssue?: (group: MyIssuesGroupData) => void
  onGroupCollapsedChange?: (groupId: string, collapsed: boolean) => void
  onOpenIssue?: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>
  onRetryMutation?: (issue: MyIssuesRowData) => void
  onSelectIssue?: (issueId: string, selected: boolean, range: boolean) => void
}

export function MyIssuesList({ groups, loading = false, error, selectedIds = EMPTY_SET, collapsedGroupIds = EMPTY_SET, displayProperties = DEFAULT_PROPERTIES, nestedSubIssues = false, propertyOptions = EMPTY_OPTIONS, mutationErrors = EMPTY_ERRORS, onClearError, onContextAction, onCreateIssue, onGroupCollapsedChange, onOpenIssue, onPropertyChange, onRetryMutation, onSelectIssue }: MyIssuesListProps) {
  if (loading) return <MyIssuesListSkeleton/>
  if (error) return <MyIssuesListError message={error} onRetry={onClearError}/>
  if (!groups.some(group => group.issues.length)) return <MyIssuesListEmpty/>
  return <div className={styles.list} role="list" aria-label="Issues">
    {groups.map(group => {
      const collapsed = collapsedGroupIds.has(group.id)
      return <section className={styles.group} key={group.id} aria-labelledby={`my-issues-group-${group.id}`}>
        <header className={styles.groupHeader}>
          <button className={styles.collapseButton} aria-label={collapsed ? 'Expand group' : 'Collapse group'} aria-expanded={!collapsed} onClick={() => onGroupCollapsedChange?.(group.id, !collapsed)}><ChevronDown size={12}/></button>
          <GroupStateIcon type={group.stateType}/><span id={`my-issues-group-${group.id}`} className={styles.groupName}>{group.label}</span><span className={styles.groupCount}>{group.issues.length}</span>
          <button className={styles.createButton} aria-label="Create new issue" onClick={() => onCreateIssue?.(group)}><Plus size={16}/></button>
        </header>
        {!collapsed && <div>{group.issues.map(issue => <MyIssuesRow key={issue.id} issue={issue} selected={selectedIds.has(issue.id)} displayProperties={displayProperties} nested={nestedSubIssues && Boolean(issue.parentId)} propertyOptions={propertyOptions} mutationError={mutationErrors.get(issue.id)} onContextAction={onContextAction} onOpen={onOpenIssue} onPropertyChange={onPropertyChange} onRetryMutation={onRetryMutation} onSelect={onSelectIssue}/>)}</div>}
      </section>
    })}
  </div>
}

export function MyIssuesRow({ issue, selected = false, displayProperties = DEFAULT_PROPERTIES, nested = false, propertyOptions = EMPTY_OPTIONS, mutationError, onContextAction, onOpen, onPropertyChange, onRetryMutation, onSelect }: {
  issue: MyIssuesRowData; selected?: boolean; displayProperties?: ReadonlySet<MyIssuesProperty>; nested?: boolean; propertyOptions?: MyIssuesRowPropertyOptions; mutationError?: string
  onContextAction?: (issue: MyIssuesRowData, action: MyIssuesContextAction) => void; onOpen?: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onRetryMutation?: (issue: MyIssuesRowData) => void
  onSelect?: (issueId: string, selected: boolean, range: boolean) => void
}) {
  const open = () => onOpen?.(issue)
  const keydown = (event: KeyboardEvent<HTMLAnchorElement>) => { if (event.target === event.currentTarget && event.key === ' ') { event.preventDefault(); open() } }
  const click = (event: MouseEvent<HTMLAnchorElement>) => {
    if ((event.target as Element).closest('button,input,[role="checkbox"]')) { event.preventDefault(); return }
    if (onOpen && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) { event.preventDefault(); open() }
  }
  const columns = ['8px', '18px', displayProperties.has('priority') && '16px', displayProperties.has('id') && '50px', displayProperties.has('status') && '16px', 'minmax(80px,1fr)', displayProperties.has('created') && '60px', displayProperties.has('updated') && '60px', '18px'].filter(Boolean).join(' ')
  const change = (property: MyIssuesEditableProperty, value: string | string[]) => onPropertyChange?.(issue, property, value)
  return <ContextMenu.Root>
    <ContextMenu.Trigger asChild>
      <a className={styles.row} style={{ '--row-columns': columns } as CSSProperties} data-selected={selected} data-nested={nested} href={issue.href} aria-label={rowAriaLabel(issue)} onClick={click} onKeyDown={keydown}>
        <span aria-hidden="true"/><IssueCheckbox checked={selected} onChange={(checked, range) => onSelect?.(issue.id, checked, range)}/>
        {displayProperties.has('priority') && <RowCommandPicker
          propertyLabel="Priority"
          label={`${priorityName(issue.priority)} Priority`}
          searchLabel="Change priority to..."
          selectedIds={[String(issue.priority)]}
          options={propertyOptions.priority}
          onSelect={value => change('priority', value)}
          trigger={<PriorityIcon priority={issue.priority}/>}
        />}
        {displayProperties.has('id') && <span className={styles.identifier}>{issue.identifier}</span>}
        {displayProperties.has('status') && <RowCommandPicker
          propertyLabel="Status"
          label={`Change status. Current status is ${issue.state.name}`}
          searchLabel="Change status..."
          searchShortcut="S"
          selectedIds={[issue.state.id]}
          options={propertyOptions.status}
          onSelect={value => change('status', value)}
          trigger={<StatusIcon state={{ id: issue.state.id, name: issue.state.name, type: issue.state.type, color: issue.state.color }} size={14}/>}
        />}
        <div className={styles.titleProperties}>
          <span className={styles.title}>{issue.title}</span>
          <span className={styles.badges}>
            {displayProperties.has('labels') && issue.labels?.length ? <RowCommandPicker propertyLabel="Labels" kind="labels" multi label={`Change labels. ${issue.labels.map(label => label.name).join(', ')} selected`} searchLabel="Change or add labels..." selectedIds={issue.labels.map(label => label.id)} options={propertyOptions.labels} onSelect={value => change('labels', toggleId(issue.labels?.map(label => label.id) ?? [], value))} trigger={<span className={styles.badgeGroup}>{issue.labels.map(label => <PropertyBadge key={label.id} color={label.color}>{label.name}</PropertyBadge>)}</span>}/> : null}
            {displayProperties.has('project') && issue.project ? <RowCommandPicker propertyLabel="Project" kind="project" label={`Change project. Current project is ${issue.project.name}`} searchLabel="Set project..." selectedIds={[issue.project.id]} options={propertyOptions.project} onSelect={value => change('project', value)} trigger={<PropertyBadge color={issue.project.color}>{issue.project.name}</PropertyBadge>}/> : null}
            {displayProperties.has('dueDate') && issue.dueDate ? <DueDatePicker value={issue.dueDate} onChange={value => change('dueDate', value)} ariaLabel={`Change due date. Current due date is ${formatDueDate(issue.dueDate)}`} triggerClassName={styles.propertyTrigger} trigger={<time className={styles.dueDate} dateTime={issue.dueDate}><CalendarIcon size={13}/>{formatDueDate(issue.dueDate)}</time>}/> : null}
            {issue.sla && <IssueSLAIndicator compact sla={issue.sla} ruleName={issue.sla.ruleName}/>}
            {displayProperties.has('assignee') && issue.assignee ? <RowCommandPicker propertyLabel="Assignee" label={`Assign to. Current assignee is ${issue.assignee.name}`} searchLabel="Assign to..." selectedIds={[issue.assignee.id]} options={propertyOptions.assignee} onSelect={value => change('assignee', value)} trigger={<Avatar assignee={issue.assignee}/>}/> : null}
            {mutationError && <button type="button" className={styles.rowError} title={mutationError} onClick={() => onRetryMutation?.(issue)}>Retry</button>}
          </span>
        </div>
        {displayProperties.has('created') && <time className={styles.rowDate} aria-label={`Created ${formatFullDate(issue.createdAt)}`} dateTime={issue.createdAt}>{formatRowDate(issue.createdAt)}</time>}
        {displayProperties.has('updated') && <time className={styles.rowDate} aria-label={`Updated ${formatFullDate(issue.updatedAt)}`} dateTime={issue.updatedAt}>{formatRowDate(issue.updatedAt)}</time>}
        <span aria-hidden="true"/>
      </a>
    </ContextMenu.Trigger>
    <ContextMenu.Portal><IssueContextMenu issue={issue} options={propertyOptions} onPropertyChange={change} onAction={action => onContextAction?.(issue, action)}/></ContextMenu.Portal>
  </ContextMenu.Root>
}

function RowCommandPicker({ propertyLabel, label, multi = false, onSelect, options, searchLabel, searchShortcut, selectedIds, trigger, kind = 'standard' }: { propertyLabel: string; label: string; multi?: boolean; onSelect: (id: string) => void | Promise<void>; options: MyIssuesContextOption[]; searchLabel: string; searchShortcut?: string; selectedIds: string[]; trigger: ReactNode; kind?: PropertyMenuKind }) {
  const commandOptions = options.map((option, index) => ({ ...option, icon: <OptionIcon option={option}/>, shortcut: option.kind === 'priority' ? option.id : option.kind === 'status' ? String(index + 1) : option.shortcut }))
  return <PropertyMenu label={propertyLabel} value={options.find(option => selectedIds.includes(option.id))?.label} multiple={multi} selectedId={selectedIds[0]} selectedIds={selectedIds} options={commandOptions} kind={kind} searchPlaceholder={searchLabel} searchShortcut={searchShortcut} ariaLabel={label} triggerClassName={styles.propertyTrigger} trigger={trigger} onChange={onSelect}/>
}

function IssueCheckbox({ checked, onChange }: { checked: boolean; onChange: (checked: boolean, range: boolean) => void }) {
  const click = (event: MouseEvent<HTMLButtonElement>) => { onChange(!checked, event.shiftKey) }
  return <span className={styles.checkboxCell}><button type="button" className={styles.checkbox} role="checkbox" aria-label="Select issue" aria-checked={checked} data-checked={checked} onClick={click}>{checked && <Check size={10}/>}</button></span>
}

function IssueContextMenu({ issue, options, onPropertyChange, onAction }: { issue: MyIssuesRowData; options: MyIssuesRowPropertyOptions; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onAction: (action: MyIssuesContextAction) => void }) {
  return <ContextMenu.Content className={styles.contextMenu} collisionPadding={10}>
    <ContextPropertySub label="Status" shortcut="S" options={options.status} selectedIds={[issue.state.id]} onSelect={id => onPropertyChange('status', id)}/>
    <ContextPropertySub label="Priority" shortcut="P" options={options.priority} selectedIds={[String(issue.priority)]} onSelect={id => onPropertyChange('priority', id)}/>
    <ContextPropertySub label="Assignee" shortcut="A" options={options.assignee} selectedIds={[issue.assignee?.id ?? '']} onSelect={id => onPropertyChange('assignee', id)}/>
    <ContextPropertySub label="Due date" shortcut="⇧ D" options={options.dueDate} selectedIds={[issue.dueDate ?? '']} onSelect={id => onPropertyChange('dueDate', id)}/>
    <ContextPropertySub multi label="Labels" shortcut="L" options={options.labels} selectedIds={issue.labels?.map(label => label.id) ?? []} onSelect={id => onPropertyChange('labels', toggleId(issue.labels?.map(label => label.id) ?? [], id))}/>
    <ContextPropertySub label="Project" shortcut="⇧ P" options={options.project} selectedIds={[issue.project?.id ?? '']} onSelect={id => onPropertyChange('project', id)}/>
    <MenuItem action="moreProperties" label="More properties" onAction={onAction}/><ContextMenu.Separator className={styles.menuSeparator}/>
    <MenuItem action="createRelated" label="Create related" onAction={onAction}/><MenuItem action="markAs" label="Mark as" onAction={onAction}/><ContextMenu.Separator className={styles.menuSeparator}/>
    <MenuItem action="copy" label="Copy" onAction={onAction} submenu={false}/><MenuItem action="convertTo" label="Convert to" onAction={onAction}/><MenuItem action="move" label="Move" onAction={onAction}/><MenuItem action="openIn" label="Open in" onAction={onAction}/><ContextMenu.Separator className={styles.menuSeparator}/>
    <MenuItem action="runLoop" label={`Run loop on ${issue.identifier}...`} onAction={onAction} submenu={false}/><ContextMenu.Separator className={styles.menuSeparator}/>
    <MenuItem action="favorite" label="Favorite" shortcut="⌥ F" onAction={onAction} submenu={false}/><MenuItem action="remind" label="Remind me" shortcut="⇧ H" onAction={onAction}/><ContextMenu.Separator className={styles.menuSeparator}/>
    <MenuItem action="delete" label="Delete" shortcut="⌘ ⌫" danger onAction={onAction} submenu={false}/>
  </ContextMenu.Content>
}

function ContextPropertySub({ label, multi = false, onSelect, options, selectedIds, shortcut }: { label: string; multi?: boolean; onSelect: (id: string) => void | Promise<void>; options: MyIssuesContextOption[]; selectedIds: string[]; shortcut?: string }) {
  const selected = new Set(selectedIds)
  const sections = multi && label === 'Labels' ? groupContextOptions(options) : [{ id: 'all', options }]
  return <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className={styles.contextSubmenu} sideOffset={3} alignOffset={-5}>
    {sections.map(section => <ContextMenu.Group key={section.id}>{section.label && <ContextMenu.Label className={styles.groupLabel}>{section.label}</ContextMenu.Label>}{section.options.map(option => multi ? <ContextMenu.CheckboxItem className={styles.submenuItem} key={option.id} checked={selected.has(option.id)} onSelect={event => event.preventDefault()} onCheckedChange={() => void onSelect(option.id)}><span className={styles.optionCheckbox}>{selected.has(option.id) && <Check size={11}/>}</span><OptionIcon option={option}/><span>{option.label}</span></ContextMenu.CheckboxItem> : <ContextMenu.Item className={styles.submenuItem} key={option.id || 'none'} onSelect={() => void onSelect(option.id)}><OptionIcon option={option}/><span>{option.label}</span>{selected.has(option.id) && <Check className={styles.optionCheck} size={13}/>}</ContextMenu.Item>)}</ContextMenu.Group>)}
  </ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
}

function MenuItem({ action, danger, label, onAction, shortcut, submenu = true }: { action: MyIssuesContextAction; danger?: boolean; label: string; onAction: (action: MyIssuesContextAction) => void; shortcut?: string; submenu?: boolean }) {
  return <ContextMenu.Item className={styles.menuItem} data-danger={danger} onSelect={() => onAction(action)}><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}{submenu && <ChevronRight size={12}/>}</ContextMenu.Item>
}

function OptionIcon({ option }: { option: MyIssuesContextOption }) {
  if (option.kind === 'priority' && option.priority !== undefined) return <PriorityIcon priority={option.priority}/>
  if (option.kind === 'status' && option.stateType && option.color) return <StatusIcon state={{ id: option.id, name: option.label, type: option.stateType, color: option.color }} size={14}/>
  if (option.avatarUrl) return <img className={styles.optionAvatar} src={option.avatarUrl} alt=""/>
  if (option.kind === 'assignee' && option.id) return <span className={styles.optionInitials}>{initials(option.label)}</span>
  if (option.kind === 'project' && option.id) return <ProjectIcon className={styles.optionIcon} size={14} style={{ color: option.color }}/>
  if (option.color) return <i className={styles.optionColor} style={{ backgroundColor: option.color }}/>
  if (option.kind === 'assignee') return <NoAssigneeIcon className={styles.optionIcon} size={14}/>
  if (option.kind === 'project') return <NoProjectIcon className={styles.optionIcon} size={14}/>
  if (option.kind === 'dueDate') return <CalendarIcon className={styles.optionIcon} size={14}/>
  if (option.kind === 'labels') return <LabelIcon className={styles.optionIcon} size={14}/>
  return <span className={styles.optionSpacer}/>
}
function GroupStateIcon({ type }: { type?: MyIssuesStateType }) { return type ? <span className={styles.groupState}><StatusIcon state={{ id: type, name: type, type, color: 'lch(63.304% 1.425 272)' }} size={14}/></span> : null }
function PropertyBadge({ children, color }: { children: ReactNode; color: string }) { return <span className={styles.badge}><i style={{ backgroundColor: color }}/><span>{children}</span></span> }
function Avatar({ assignee }: { assignee: NonNullable<MyIssuesRowData['assignee']> }) { const initials = assignee.name.split(/\s+/).filter(Boolean).slice(0, 2).map(part => part[0]).join('').toUpperCase(); return <span className={styles.avatar} aria-label={assignee.name} style={{ '--avatar': assignee.color ?? 'lch(70% 60 30)' } as CSSProperties}>{assignee.avatarUrl ? <img src={assignee.avatarUrl} alt=""/> : initials}</span> }

export function MyIssuesListSkeleton({ rows = 6 }: { rows?: number }) { return <div className={styles.skeleton} aria-label="Loading issues" aria-busy="true"><div className={styles.skeletonHeader}/>{Array.from({ length: rows }, (_, index) => <div className={styles.skeletonRow} key={index}><i/><i/><i/><i/></div>)}</div> }
export function MyIssuesListEmpty() { return <div className={styles.state} role="status"><span className={styles.emptyIcon}><Check size={15}/></span><strong>No issues</strong><p>No issues match this view.</p></div> }
export function MyIssuesListError({ message, onRetry }: { message: string; onRetry?: () => void }) { return <div className={styles.state} role="alert"><strong>Could not load issues</strong><p>{message}</p>{onRetry && <button type="button" onClick={onRetry}>Try again</button>}</div> }

const EMPTY_SET = new Set<string>()
const EMPTY_ERRORS = new Map<string, string>()
const DEFAULT_PROPERTIES = new Set<MyIssuesProperty>(['id', 'status', 'assignee', 'priority', 'project', 'labels', 'created'])
const EMPTY_OPTIONS: MyIssuesRowPropertyOptions = { status: [], priority: [], assignee: [], dueDate: [], labels: [], project: [] }
function toggleId(ids: string[], id: string) { return ids.includes(id) ? ids.filter(value => value !== id) : [...ids, id] }
function priorityName(priority: number) { return ['No', 'Urgent', 'High', 'Medium', 'Low'][priority] }
function rowAriaLabel(issue: MyIssuesRowData) { return `Select issue ${priorityName(issue.priority)} Priority ${issue.identifier} ${issue.title}` }
function formatRowDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(new Date(value)) }
function formatFullDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' }).format(new Date(value)) }
function formatDueDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(new Date(`${value}T00:00:00`)) }
function initials(value: string) { return value.split(/\s+/).filter(Boolean).slice(0, 2).map(part => part[0]).join('').toUpperCase() }
function groupContextOptions(options: MyIssuesContextOption[]) {
  const sections: { id: string; label?: string; options: MyIssuesContextOption[] }[] = []
  const indexes = new Map<string, number>()
  for (const option of options) {
    const id = option.groupId || option.groupLabel || 'ungrouped'
    let index = indexes.get(id)
    if (index === undefined) {
      index = sections.length
      indexes.set(id, index)
      sections.push({ id, label: id === 'ungrouped' ? undefined : option.groupLabel, options: [] })
    }
    sections[index].options.push(option)
  }
  return sections
}
