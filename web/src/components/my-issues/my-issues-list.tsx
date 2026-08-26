import { useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import * as Popover from '@radix-ui/react-popover'
import { Check, ChevronDown, ChevronRight, Plus } from 'lucide-react'
import type { MyIssuesProperty } from './my-issues-surface'
import { CalendarIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu, type PropertyMenuKind } from '@/components/property/property-menu'
import { LabelHoverPreview } from '@/components/property/label-hover-preview'
import { DueDatePicker } from '@/components/issue/due-date-picker'
import styles from './my-issues-list.module.css'
import { UserAvatar } from '@/components/ui/user-avatar'
import { IssueSLAIndicator } from '@/components/issue/issue-sla-indicator'
import { SubIssueProgressRing } from '@/components/issue/sub-issue-progress-ring'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
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
  description?: string
  href?: string
  priority: 0 | 1 | 2 | 3 | 4
  state: { id: string; name: string; type: MyIssuesStateType; color: string }
  labels?: { id: string; name: string; color: string; description?:string; issueCount?:number; scope?:string; groupId?: string }[]
  project?: { id: string; name: string; color: string }
  assignee?: { id: string; name: string; avatarUrl?: string; color?: string }
  delegate?: { id: string; name: string; avatarUrl?: string; color?: string }
  creatorId?: string
  creatorName?: string
  isAssignedToViewer?: boolean
  cycleId?: string
  addedToCycle?: string
  agentSessionId?: string
  suggestedLabelIds?: string[]
  externalSource?: string
  autoClosed?: boolean
  autoClosedAt?: string
  triagedAt?: string
  templateId?: string
  initiativeIds?: string[]
  projectStatusId?: string
  projectStatusType?: string
  projectPriority?: number
  projectLabelIds?: string[]
  projectLeadId?: string
  projectMilestoneNames?: string[]
  releaseIds?: string[]
  releasePipelineIds?: string[]
  releaseStages?: string[]
  releaseStatuses?: string[]
  hasReleasedRelease?: boolean
  subscriberIds?: string[]
  relationTypes?: string[]
  hasLinks?: boolean
  hasContent?: boolean
  estimate?: number
  dueDate?: string
  sla?: IssueSLA & { ruleName?: string }
  createdAt: string
  updatedAt: string
  completedAt?: string
  startedAt?: string
  statusChangedAt?: string
  statusIntervals?: { stateId: string; stateType?: string; enteredAt: string; exitedAt?: string }[]
  canceledAt?: string
  archivedAt?: string
  parentId?: string
  parent?: { id: string; identifier: string; title: string }
  ancestors?: { id: string; identifier: string; title: string }[]
  subIssueProgress?: { completed: number; total: number }
  subIssues?: MyIssuesRowData[]
  sortOrder?: number
  viewMatch?: boolean
}

export interface MyIssuesGroupData { id: string; label: string; stateType?: MyIssuesStateType; state?: MyIssuesRowData['state']; issues: MyIssuesRowData[] }

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
  createIssueLabel?: string
}

export function MyIssuesList({ groups, loading = false, error, selectedIds = EMPTY_SET, collapsedGroupIds = EMPTY_SET, displayProperties = DEFAULT_PROPERTIES, nestedSubIssues=false, propertyOptions = EMPTY_OPTIONS, mutationErrors = EMPTY_ERRORS, onClearError, onContextAction, onCreateIssue, onGroupCollapsedChange, onOpenIssue, onPropertyChange, onRetryMutation, onSelectIssue, createIssueLabel = 'Create new issue' }: MyIssuesListProps) {
  if (loading) return <MyIssuesListSkeleton/>
  if (error) return <MyIssuesListError message={error} onRetry={onClearError}/>
  if (!groups.some(group => group.issues.length)) return <MyIssuesListEmpty/>
  const identifierLength = Math.max(6, ...groups.flatMap(group => group.issues.map(issue => [...issue.identifier].length)))
  return <div className={styles.list} role="list" aria-label="Issues" style={{ '--issue-identifier-width': `${identifierLength}ch` } as CSSProperties}>
    {groups.map(group => {
      const collapsed = collapsedGroupIds.has(group.id)
      const nestedLines = nestedSubIssues ? nestedLinesByIssue(group.issues) : EMPTY_LINE_MAP
      return <section className={styles.group} key={group.id} aria-labelledby={`my-issues-group-${group.id}`}>
        <header className={styles.groupHeader}>
          <button className={styles.collapseButton} aria-label={collapsed ? 'Expand group' : 'Collapse group'} aria-expanded={!collapsed} onClick={() => onGroupCollapsedChange?.(group.id, !collapsed)}><ChevronDown size={12}/></button>
          <GroupStateIcon state={group.state ?? group.issues[0]?.state} type={group.stateType}/><span data-i18n-ignore id={`my-issues-group-${group.id}`} className={styles.groupName}>{group.label}</span><span className={styles.groupCount}>{group.issues.length}</span>
          {onCreateIssue && <button className={styles.createButton} aria-label={createIssueLabel} onClick={() => onCreateIssue(group)}><Plus size={16}/></button>}
        </header>
        {!collapsed && <div>{group.issues.map(issue => <MyIssuesRow key={issue.id} issue={issue} selected={selectedIds.has(issue.id)} displayProperties={displayProperties} nestedLines={nestedLines.get(issue.id)??EMPTY_LINES} showSubIssueProgress={!nestedSubIssues} propertyOptions={propertyOptions} mutationError={mutationErrors.get(issue.id)} onContextAction={onContextAction} onOpen={onOpenIssue} onPropertyChange={onPropertyChange} onRetryMutation={onRetryMutation} onSelect={onSelectIssue}/>)}</div>}
      </section>
    })}
  </div>
}

export function MyIssuesRow({ issue, selected = false, displayProperties = DEFAULT_PROPERTIES, nestedLines=EMPTY_LINES, showSubIssueProgress=true, propertyOptions = EMPTY_OPTIONS, mutationError, onContextAction, onOpen, onPropertyChange, onRetryMutation, onSelect }: {
  issue: MyIssuesRowData; selected?: boolean; displayProperties?: ReadonlySet<MyIssuesProperty>; nestedLines?:readonly boolean[]; showSubIssueProgress?:boolean; propertyOptions?: MyIssuesRowPropertyOptions; mutationError?: string
  onContextAction?: (issue: MyIssuesRowData, action: MyIssuesContextAction) => void; onOpen?: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onRetryMutation?: (issue: MyIssuesRowData) => void
  onSelect?: (issueId: string, selected: boolean, range: boolean) => void
}) {
  const nestedDepth=nestedLines.length
  const open = () => onOpen?.(issue)
  const keydown = (event: KeyboardEvent<HTMLAnchorElement>) => { if (event.target === event.currentTarget && event.key === ' ') { event.preventDefault(); open() } }
  const click = (event: MouseEvent<HTMLAnchorElement>) => {
    if ((event.target as Element).closest('button,input,[role="checkbox"]')) { event.preventDefault(); return }
    if (onOpen && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) { event.preventDefault(); open() }
  }
  const columns = ['8px', '18px', displayProperties.has('priority') && '16px', displayProperties.has('id') && 'var(--issue-identifier-width, 52px)', displayProperties.has('status') && '16px', 'minmax(80px,1fr)', displayProperties.has('created') && '60px', displayProperties.has('updated') && '60px', '18px'].filter(Boolean).join(' ')
  const change = (property: MyIssuesEditableProperty, value: string | string[]) => onPropertyChange?.(issue, property, value)
  return <ContextMenu.Root>
    <ContextMenu.Trigger asChild>
      <a className={styles.row} style={{'--row-columns':columns,'--nested-depth':nestedDepth} as CSSProperties} data-selected={selected} data-nested={nestedDepth>0} href={issue.href} aria-label={rowAriaLabel(issue)} onClick={click} onKeyDown={keydown}>
        {nestedDepth>0&&<NestedIssueGuide lines={nestedLines}/>}
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
          <span className={styles.title} data-i18n-ignore>{issue.title}</span>
          {!nestedDepth&&issue.parent&&<IssueParentTrail ancestors={issue.ancestors??[issue.parent]}/>}
          {showSubIssueProgress&&issue.subIssueProgress&&issue.subIssues?.length?<SubIssueProgress
            progress={issue.subIssueProgress}
            subIssues={issue.subIssues}
            onOpenSubIssue={onOpen}
          />:null}
          <span className={styles.badges}>
            {displayProperties.has('labels') && issue.labels?.length ? <RowCommandPicker propertyLabel="Labels" kind="labels" multi label={`Change labels. ${issue.labels.map(label => label.name).join(', ')} selected`} searchLabel="Change or add labels..." selectedIds={issue.labels.map(label => label.id)} options={propertyOptions.labels} onSelect={value => change('labels', toggleId(issue.labels?.map(label => label.id) ?? [], value))} triggerClassName={styles.labelsTrigger} trigger={<span className={styles.badgeGroup}>{issue.labels.map(label => <PropertyBadge key={label.id} label={label}/>)}</span>}/> : null}
            {displayProperties.has('project') && issue.project ? <RowCommandPicker propertyLabel="Project" kind="project" label={`Change project. Current project is ${issue.project.name}`} searchLabel="Set project..." selectedIds={[issue.project.id]} options={propertyOptions.project} onSelect={value => change('project', value)} trigger={<PropertyBadge color={issue.project.color}>{issue.project.name}</PropertyBadge>}/> : null}
            {displayProperties.has('dueDate') && issue.dueDate ? <DueDatePicker value={issue.dueDate} onChange={value => change('dueDate', value)} ariaLabel={`Change due date. Current due date is ${formatDueDate(issue.dueDate)}`} triggerClassName={styles.propertyTrigger} trigger={<time className={styles.dueDate} dateTime={issue.dueDate}><CalendarIcon size={13}/>{formatDueDate(issue.dueDate)}</time>}/> : null}
            {issue.sla && <IssueSLAIndicator compact sla={issue.sla} ruleName={issue.sla.ruleName}/>}
            {displayProperties.has('assignee') && issue.assignee ? <RowCommandPicker propertyLabel="Assignee" label={`Assign to. Current assignee is ${issue.assignee.name}`} searchLabel="Assign to..." selectedIds={[issue.assignee.id]} options={propertyOptions.assignee} onSelect={value => change('assignee', value)} trigger={<MyIssuesAssigneeAvatar assignee={issue.assignee}/>}/> : null}
            {mutationError && <button type="button" className={styles.rowError} title={mutationError} onClick={() => onRetryMutation?.(issue)}>Retry</button>}
          </span>
        </div>
        {displayProperties.has('created') && <time className={styles.rowDate} aria-label={`Created ${formatFullDate(issue.createdAt)}`} dateTime={issue.createdAt}>{formatRowDate(issue.createdAt)}</time>}
        {displayProperties.has('updated') && <time className={styles.rowDate} aria-label={`Updated ${formatFullDate(issue.updatedAt)}`} dateTime={issue.updatedAt}>{formatRowDate(issue.updatedAt)}</time>}
        <span aria-hidden="true"/>
      </a>
    </ContextMenu.Trigger>
    <ContextMenu.Portal><IssueContextMenu editable={Boolean(onPropertyChange)} issue={issue} options={propertyOptions} onPropertyChange={change} onAction={onContextAction?action=>onContextAction(issue,action):undefined}/></ContextMenu.Portal>
  </ContextMenu.Root>
}

const EMPTY_LINES:readonly boolean[]=[]
const EMPTY_LINE_MAP=new Map<string,readonly boolean[]>()
function nestedLinesByIssue(issues:MyIssuesRowData[]){
  const byId=new Map(issues.map(item=>[item.id,item])),children=new Map<string,MyIssuesRowData[]>()
  for(const item of issues){if(!item.parentId||!byId.has(item.parentId))continue;const siblings=children.get(item.parentId)??[];siblings.push(item);children.set(item.parentId,siblings)}
  const result=new Map<string,readonly boolean[]>()
  for(const issue of issues){const path:MyIssuesRowData[]=[];let child=issue;while(child.parentId){const parent=byId.get(child.parentId);if(!parent)break;path.unshift(child);child=parent}result.set(issue.id,path.map(item=>children.get(item.parentId!)?.at(-1)?.id===item.id))}
  return result
}
function NestedIssueGuide({lines}:{lines:readonly boolean[]}){return <span aria-hidden="true" className={styles.nestingGuide}>{lines.map((last,level)=>{const current=level===lines.length-1;if(!current&&last)return null;return <span className={`${styles.nestingSegment}${current?` ${styles.nestingCurrent}`:''}${current&&last?` ${styles.nestingLast}`:''}`} key={level} style={{'--nest-level':level} as CSSProperties}>{current&&<svg className={styles.nestingBranch} viewBox="0 0 10 9"><path d="M0 0h1v1c0 2.5 2.212 3.546 2.212 3.546L9.737 8.06c.568.306.094 1.186-.474.88l-6.48-3.488S0 4 0 1V0Z"/></svg>}</span>})}</span>}

export function IssueParentTrail({ancestors,board=false}:{ancestors:NonNullable<MyIssuesRowData['ancestors']>;board?:boolean}){if(!ancestors.length)return null;const shown=board?ancestors:ancestors.slice(0,1);return <span className={`${styles.parentTrail}${board?` ${styles.boardParentTrail}`:''}`} data-i18n-ignore>{shown.map(parent=><span key={parent.id}><b>›</b>{parent.title}</span>)}</span>}

export function SubIssueProgress({progress,subIssues,onOpenSubIssue}:{progress:NonNullable<MyIssuesRowData['subIssueProgress']>;subIssues:MyIssuesRowData[];onOpenSubIssue?:(issue:MyIssuesRowData)=>void}){
  const [open,setOpen]=useState(false),closeTimer=useRef<number|undefined>(undefined)
  const enter=()=>{if(closeTimer.current)window.clearTimeout(closeTimer.current);setOpen(true)},leave=()=>{closeTimer.current=window.setTimeout(()=>setOpen(false),100)}
  return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button type="button" className={styles.subIssueProgress} aria-label={`${progress.completed} of ${progress.total} sub-issues completed`} onPointerEnter={enter} onPointerLeave={leave} onFocus={enter} onBlur={leave} onClick={event=>{event.preventDefault();event.stopPropagation();setOpen(value=>!value)}}><SubIssueProgressRing completed={progress.completed} total={progress.total}/><span>{progress.completed}/{progress.total}</span></button></Popover.Trigger><Popover.Portal><Popover.Content className={styles.subIssuePopover} side="top" align="start" sideOffset={4} collisionPadding={8} onOpenAutoFocus={event=>event.preventDefault()} onCloseAutoFocus={event=>event.preventDefault()} onPointerEnter={enter} onPointerLeave={leave}>{subIssues.map(item=><button type="button" className={styles.subIssuePopoverRow} key={item.id} onClick={event=>{event.preventDefault();event.stopPropagation();setOpen(false);onOpenSubIssue?.(item)}}><StatusIcon state={{id:item.state.id,name:item.state.name,type:item.state.type,color:item.state.color}} size={16}/><span data-i18n-ignore>{item.title}</span></button>)}</Popover.Content></Popover.Portal></Popover.Root>
}

export function RowCommandPicker({ propertyLabel, label, multi = false, onSelect, options, searchLabel, searchShortcut, selectedIds, trigger, triggerClassName, kind = 'standard' }: { propertyLabel: string; label: string; multi?: boolean; onSelect: (id: string) => void | Promise<void>; options: MyIssuesContextOption[]; searchLabel: string; searchShortcut?: string; selectedIds: string[]; trigger: ReactNode; triggerClassName?: string; kind?: PropertyMenuKind }) {
  const commandOptions = options.map((option, index) => ({ ...option, icon: <MyIssuesOptionIcon option={option}/>, shortcut: option.kind === 'priority' ? option.id : option.kind === 'status' ? String(index + 1) : option.shortcut }))
  return <PropertyMenu label={propertyLabel} value={options.find(option => selectedIds.includes(option.id))?.label} multiple={multi} selectedId={selectedIds[0]} selectedIds={selectedIds} options={commandOptions} kind={kind} searchPlaceholder={searchLabel} searchShortcut={searchShortcut} ariaLabel={label} triggerClassName={triggerClassName ?? styles.propertyTrigger} trigger={trigger} onChange={onSelect}/>
}

function IssueCheckbox({ checked, onChange }: { checked: boolean; onChange: (checked: boolean, range: boolean) => void }) {
  const click = (event: MouseEvent<HTMLButtonElement>) => { onChange(!checked, event.shiftKey) }
  return <span className={styles.checkboxCell}><button type="button" className={styles.checkbox} role="checkbox" aria-label="Select issue" aria-checked={checked} data-checked={checked} onClick={click}>{checked && <CheckboxMark/>}</button></span>
}

function IssueContextMenu({ editable, issue, options, onPropertyChange, onAction }: { editable:boolean;issue:MyIssuesRowData;options: MyIssuesRowPropertyOptions; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onAction?: (action: MyIssuesContextAction) => void }) {
  return <ContextMenu.Content className={styles.contextMenu} collisionPadding={10}>
    {editable&&<><ContextPropertySub label="Status" shortcut="S" options={options.status} selectedIds={[issue.state.id]} onSelect={id => onPropertyChange('status', id)}/>
    <ContextPropertySub label="Priority" shortcut="P" options={options.priority} selectedIds={[String(issue.priority)]} onSelect={id => onPropertyChange('priority', id)}/>
    <ContextPropertySub label="Assignee" shortcut="A" options={options.assignee} selectedIds={[issue.assignee?.id ?? '']} onSelect={id => onPropertyChange('assignee', id)}/>
    <ContextPropertySub label="Due date" shortcut="⇧ D" options={options.dueDate} selectedIds={[issue.dueDate ?? '']} onSelect={id => onPropertyChange('dueDate', id)}/>
    <ContextPropertySub multi label="Labels" shortcut="L" options={options.labels} selectedIds={issue.labels?.map(label => label.id) ?? []} onSelect={id => onPropertyChange('labels', toggleId(issue.labels?.map(label => label.id) ?? [], id))}/>
    <ContextPropertySub label="Project" shortcut="⇧ P" options={options.project} selectedIds={[issue.project?.id ?? '']} onSelect={id => onPropertyChange('project', id)}/></>}
    {onAction&&<>{editable&&<ContextMenu.Separator className={styles.menuSeparator}/>}<MyIssuesMenuItem action="copy" label="Copy" onAction={onAction} submenu={false}/><ContextMenu.Separator className={styles.menuSeparator}/><MyIssuesMenuItem action="delete" label="Delete" shortcut="⌘ ⌫" danger onAction={onAction} submenu={false}/></>}
  </ContextMenu.Content>
}

function ContextPropertySub({ label, multi = false, onSelect, options, selectedIds, shortcut }: { label: string; multi?: boolean; onSelect: (id: string) => void | Promise<void>; options: MyIssuesContextOption[]; selectedIds: string[]; shortcut?: string }) {
  const selected = new Set(selectedIds)
  const sections = multi && label === 'Labels' ? groupContextOptions(options) : [{ id: 'all', options }]
  return <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className={styles.contextSubmenu} sideOffset={3} alignOffset={-5}>
    {sections.map(section => <ContextMenu.Group key={section.id}>{section.label && <ContextMenu.Label className={styles.groupLabel}>{section.label}</ContextMenu.Label>}{section.options.map(option => multi ? <ContextMenu.CheckboxItem className={styles.submenuItem} key={option.id} checked={selected.has(option.id)} onSelect={event => event.preventDefault()} onCheckedChange={() => void onSelect(option.id)}><span className={styles.optionCheckbox}>{selected.has(option.id) && <CheckboxMark/>}</span><MyIssuesOptionIcon option={option}/><span>{option.label}</span></ContextMenu.CheckboxItem> : <ContextMenu.Item className={styles.submenuItem} key={option.id || 'none'} onSelect={() => void onSelect(option.id)}><MyIssuesOptionIcon option={option}/><span>{option.label}</span>{selected.has(option.id) && <Check className={styles.optionCheck} size={13}/>}</ContextMenu.Item>)}</ContextMenu.Group>)}
  </ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
}

function MyIssuesMenuItem({ action, danger, label, onAction, shortcut, submenu = true }: { action: MyIssuesContextAction; danger?: boolean; label: string; onAction: (action: MyIssuesContextAction) => void; shortcut?: string; submenu?: boolean }) {
  return <ContextMenu.Item className={styles.menuItem} data-danger={danger} onSelect={() => onAction(action)}><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}{submenu && <ChevronRight size={12}/>}</ContextMenu.Item>
}

function MyIssuesOptionIcon({ option }: { option: MyIssuesContextOption }) {
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
function GroupStateIcon({ state, type }: { state?: MyIssuesRowData['state']; type?: MyIssuesStateType }) { return state || type ? <span className={styles.groupState}><StatusIcon state={state ?? { id: type!, name: type!, type: type!, color: 'var(--theme-text-secondary)' }} size={14}/></span> : null }
function PropertyBadge(props:{label:NonNullable<MyIssuesRowData['labels']>[number]}|{children:ReactNode;color:string}){
  if('label'in props){const{label}=props;return <LabelHoverPreview label={label} side="bottom" align="start"><span className={styles.badge}><i style={{backgroundColor:label.color}}/><span data-i18n-ignore>{label.name}</span></span></LabelHoverPreview>}
  return <span className={styles.badge}><i style={{backgroundColor:props.color}}/><span data-i18n-ignore>{props.children}</span></span>
}
function MyIssuesAssigneeAvatar({ assignee }: { assignee: NonNullable<MyIssuesRowData['assignee']> }) { return <UserAvatar avatarUrl={assignee.avatarUrl} className={styles.avatar} color={assignee.color ?? 'var(--avatar-fallback)'} name={assignee.name}/> }

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
