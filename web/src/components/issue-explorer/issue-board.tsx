import { useState, type CSSProperties, type DragEvent, type MouseEvent } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Ellipsis, Minus, Plus } from 'lucide-react'
import { IssueContextMenu, IssueParentTrail, RowCommandPicker, SubIssueProgress, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData, type MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import type { MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { CalendarIcon, NoAssigneeIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { DueDatePicker } from '@/components/issue/due-date-picker'
import { useI18n } from '@/i18n/i18n'
import styles from './issue-board.module.css'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { UserAvatar } from '@/components/ui/user-avatar'
import { toggleGroupedLabelIds } from '@/lib/labels'

const EMPTY_OPTIONS: MyIssuesRowPropertyOptions = { status: [], priority: [], assignee: [], dueDate: [], labels: [], project: [], cycle: [] }

export function IssueBoard({ groups, hiddenGroupIds = [], properties, propertyOptions = EMPTY_OPTIONS, selectedIds, createIssueLabel = 'Add new issue', onCreateIssue, onHideGroup, onShowGroup, onMove, onOpenIssue, onPropertyChange, onSelectIssue }: {
  groups: MyIssuesGroupData[]
  hiddenGroupIds?: string[]
  properties: ReadonlySet<MyIssuesProperty>
  propertyOptions?: MyIssuesRowPropertyOptions
  selectedIds: ReadonlySet<string>
  createIssueLabel?: string
  onCreateIssue?: (group: MyIssuesGroupData) => void
  onHideGroup?: (groupId: string) => void
  onShowGroup?: (groupId: string) => void
  onMove: (issue: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => void
  onOpenIssue: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>
  onSelectIssue: (issueId: string, selected: boolean, range: boolean) => void
}) {
  const { t } = useI18n()
  const [draggingId, setDraggingId] = useState<string>()
  const [over, setOver] = useState<{ groupId: string; index: number }>()
  const sourceGroup = groups.find(group => group.issues.some(issue => issue.id === draggingId))
  const drop = (event: DragEvent, group: MyIssuesGroupData, index: number) => {
    event.preventDefault()
    const issue = sourceGroup?.issues.find(item => item.id === draggingId)
    if (issue && sourceGroup) {
      const sourceIndex = sourceGroup.issues.findIndex(item => item.id === issue.id)
      const targetIndex = sourceGroup.id === group.id && sourceIndex < index ? index - 1 : index
      if (sourceGroup.id !== group.id || sourceIndex !== targetIndex) onMove(issue, sourceGroup.id, group.id, targetIndex)
    }
    setDraggingId(undefined); setOver(undefined)
  }

  const hiddenGroups=groups.filter(group=>hiddenGroupIds.includes(group.id))
  return <div className={styles.board} role="list" aria-label={t('Issue board')} data-dragging={Boolean(draggingId)}>
    {groups.filter(group=>!hiddenGroupIds.includes(group.id)).map(group => <section className={styles.column} role="listitem" key={group.id} aria-label={group.label}>
      <header className={styles.columnHeader}>
        {group.stateType && <StatusIcon
          state={{ id: group.id, name: group.label, type: group.stateType, color: group.state?.color ?? 'var(--status-neutral)' }}
          size={14}
        />}
        <strong data-i18n-ignore>{group.label}</strong><button className={styles.count} type="button" aria-label={t(`${group.issues.length} issues`)}>{group.issues.length}</button>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={styles.headerButton} type="button" aria-label={t('Open menu')}><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content aria-label={t('Open menu')} className={styles.columnMenu} align="end" sideOffset={4}><DropdownMenu.Item className={styles.columnMenuItem} onSelect={()=>group.issues.forEach(issue=>onSelectIssue(issue.id,true,false))}>{t('Select all in column')}</DropdownMenu.Item><DropdownMenu.Item className={styles.columnMenuItem} onSelect={()=>onHideGroup?.(group.id)}>{t('Hide column')}</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        <button className={styles.headerButton} type="button" aria-label={t(createIssueLabel)} onClick={() => onCreateIssue?.(group)}><Plus size={15}/></button>
      </header>
      <div className={styles.cards} data-over={over?.groupId === group.id} data-over-end={over?.groupId === group.id && over.index === group.issues.length} onDragOver={event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; if (event.target === event.currentTarget) setOver({ groupId: group.id, index: group.issues.length }) }} onDrop={event => { event.stopPropagation(); drop(event, group, group.issues.length) }}>
        {group.issues.map((issue, index) => <IssueBoardCard
          key={issue.id} issue={issue} properties={properties} propertyOptions={propertyOptions} selected={selectedIds.has(issue.id)} dragging={draggingId === issue.id}
          dropBefore={over?.groupId === group.id && over.index === index}
          onDragStart={() => setDraggingId(issue.id)} onDragEnd={() => { setDraggingId(undefined); setOver(undefined) }}
          onDragOver={event => { event.preventDefault(); event.stopPropagation(); const rect = event.currentTarget.getBoundingClientRect(); setOver({ groupId: group.id, index: index + (event.clientY > rect.top + rect.height / 2 ? 1 : 0) }) }}
          onDrop={event => { event.stopPropagation(); drop(event, group, over?.groupId === group.id ? over.index : index) }}
          onOpen={() => onOpenIssue(issue)} onOpenSubIssue={onOpenIssue} onPropertyChange={(property, value) => onPropertyChange?.(issue, property, value)} onSelect={(selected, range) => onSelectIssue(issue.id, selected, range)}/>) }
        <button className={styles.addIssue} type="button" onClick={() => onCreateIssue?.(group)}><Plus size={14}/>{t(createIssueLabel)}</button>
      </div>
    </section>)}
    {hiddenGroups.length>0&&<section className={styles.hiddenColumns} aria-label={t('Hidden columns')}><strong>{t('Hidden columns')}</strong>{hiddenGroups.map(group=><button key={group.id} type="button" onClick={()=>onShowGroup?.(group.id)}><StatusIcon state={{id:group.id,name:group.label,type:group.stateType??'unstarted',color:group.state?.color??'var(--status-neutral)'}} size={14}/><span data-i18n-ignore>{group.label}</span><b>{group.issues.length}</b></button>)}</section>}
  </div>
}

function IssueBoardCard({ issue, properties, propertyOptions, selected, dragging, dropBefore, onDragStart, onDragEnd, onDragOver, onDrop, onOpen, onOpenSubIssue, onPropertyChange, onSelect }: {
  issue: MyIssuesRowData; properties: ReadonlySet<MyIssuesProperty>; propertyOptions: MyIssuesRowPropertyOptions; selected: boolean; dragging: boolean; dropBefore: boolean
  onDragStart: () => void; onDragEnd: () => void; onDragOver: (event: DragEvent<HTMLElement>) => void; onDrop: (event: DragEvent<HTMLElement>) => void; onOpen: () => void; onOpenSubIssue:(issue:MyIssuesRowData)=>void; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onSelect: (selected: boolean, range: boolean) => void
}) {
  const click = (event: MouseEvent) => { if ((event.target as Element).closest('button,input,[role="dialog"]')) return; onOpen() }
  const change = (property: MyIssuesEditableProperty, value: string | string[]) => onPropertyChange(property, value)
  return <ContextMenu.Root><ContextMenu.Trigger asChild><article className={styles.card} role="button" tabIndex={0} draggable aria-grabbed={dragging} data-selected={selected} data-dragging={dragging} data-drop-before={dropBefore} aria-label={`${issue.identifier} ${issue.title}`} onClick={click} onKeyDown={event => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); onOpen() } }} onDragStart={event => { event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', issue.id); onDragStart() }} onDragEnd={onDragEnd} onDragOver={event => { event.dataTransfer.dropEffect = 'move'; onDragOver(event) }} onDrop={onDrop}>
    <div className={styles.cardTop}>
      {properties.has('id') && <span data-i18n-ignore>{issue.identifier}</span>}
      {issue.ancestors?.length?<IssueParentTrail ancestors={issue.ancestors} board/>:null}
      {properties.has('assignee') && <RowCommandPicker
        propertyLabel="Assignee" label={issue.assignee ? `Assign to. Current assignee is ${issue.assignee.name}` : 'Assign issue'} searchLabel="Assign to..."
        selectedIds={[issue.assignee?.id ?? '']} options={propertyOptions.assignee} onSelect={value => change('assignee', value)} triggerClassName={styles.assigneeTrigger}
        trigger={issue.assignee ? <BoardAssigneeAvatar assignee={issue.assignee}/> : <span className={styles.noAssignee}><NoAssigneeIcon size={18}/></span>}
      />}
    </div>
    <div className={styles.titleRow} data-has-status={properties.has('status')}>
      {properties.has('status') && <span className={styles.status}><StatusIcon state={issue.state} size={14}/></span>}
      <button className={styles.checkbox} type="button" role="checkbox" aria-label="Select issue" aria-checked={selected} data-checked={selected} onClick={event => onSelect(!selected, event.shiftKey)}>{selected && <CheckboxMark/>}</button>
      <strong className={styles.title} data-i18n-ignore>{issue.title}</strong>
    </div>
    <div className={styles.cardFooter}><CardProperties issue={issue} properties={properties} propertyOptions={propertyOptions} onPropertyChange={change}/>{issue.subIssueProgress&&issue.subIssues?.length?<SubIssueProgress progress={issue.subIssueProgress} subIssues={issue.subIssues} onOpenSubIssue={onOpenSubIssue}/>:null}</div>
    {properties.has('created') && <span className={styles.created}>Created {formatDate(issue.createdAt)}</span>}
  </article></ContextMenu.Trigger><ContextMenu.Portal><IssueContextMenu editable issue={issue} options={propertyOptions} onPropertyChange={change}/></ContextMenu.Portal></ContextMenu.Root>
}

function CardProperties({ issue, properties, propertyOptions, onPropertyChange }: { issue: MyIssuesRowData; properties: ReadonlySet<MyIssuesProperty>; propertyOptions: MyIssuesRowPropertyOptions; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void> }) {
  const labels = properties.has('labels') ? issue.labels ?? [] : []
  const shownLabels = labels.slice(0, 4)
  const hiddenLabelCount = labels.length - shownLabels.length
  const visible = properties.has('priority') || Boolean(issue.estimate) || Boolean(properties.has('project') && issue.project) || shownLabels.length > 0 || Boolean(properties.has('dueDate') && issue.dueDate)
  if (!visible) return null
  return <div className={styles.cardMeta}>
    {properties.has('priority') && <RowCommandPicker propertyLabel="Priority" label={`${priorityName(issue.priority)} priority`} searchLabel="Change priority to..." selectedIds={[String(issue.priority)]} options={propertyOptions.priority} onSelect={value => onPropertyChange('priority', value)} triggerClassName={`${styles.metaTrigger} ${styles.iconBadge}`} trigger={<PriorityIcon priority={issue.priority} size={14}/>}/>}
    {issue.estimate != null && issue.estimate > 0 && <span className={styles.badge}><span className={styles.estimateIcon}><Minus size={10}/></span><span>{issue.estimate}</span></span>}
    {properties.has('project') && issue.project && <RowCommandPicker propertyLabel="Project" kind="project" label={`Change project. Current project is ${issue.project.name}`} searchLabel="Set project..." selectedIds={[issue.project.id]} options={propertyOptions.project} onSelect={value => onPropertyChange('project', value)} triggerClassName={`${styles.metaTrigger} ${styles.projectBadge}`} trigger={<><ProjectIcon size={14}/><span data-i18n-ignore>{issue.project.name}</span></>}/>}
    {shownLabels.length > 0 && <RowCommandPicker propertyLabel="Labels" kind="labels" multi label={`Change labels. ${labels.map(label => label.name).join(', ')} selected`} searchLabel="Change or add labels..." selectedIds={labels.map(label => label.id)} options={propertyOptions.labels} onSelect={value => onPropertyChange('labels', toggleGroupedLabelIds(labels.map(label => label.id), value, propertyOptions.labels))} triggerClassName={styles.labelsTrigger} trigger={<>{shownLabels.map(label => <Badge key={label.id} color={label.color}>{label.name}</Badge>)}{hiddenLabelCount > 0 && <span className={styles.badge}><span className={styles.labelStack}>{labels.slice(4, 6).map((label, index) => <i key={label.id} style={{ '--dot-index': index, backgroundColor: label.color } as CSSProperties}/>)}</span><span>+{hiddenLabelCount} labels</span></span>}</>}/>}
    {properties.has('dueDate') && issue.dueDate && <DueDatePicker value={issue.dueDate} onChange={value => onPropertyChange('dueDate', value)} ariaLabel={`Change due date. Current due date is ${formatDate(issue.dueDate)}`} triggerClassName={styles.metaTrigger} trigger={<><CalendarIcon size={13}/><span>{formatDate(issue.dueDate)}</span></>}/>}
  </div>
}

function Badge({ children, color }: { children: string; color: string }) { return <span className={styles.badge}><i className={styles.labelDot} style={{ backgroundColor: color }}/><span data-i18n-ignore>{children}</span></span> }
function BoardAssigneeAvatar({ assignee }: { assignee: NonNullable<MyIssuesRowData['assignee']> }) { return <UserAvatar avatarUrl={assignee.avatarUrl} className={styles.avatar} color={assignee.color ?? 'var(--avatar-fallback)'} name={assignee.name}/> }
function formatDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(new Date(value.length === 10 ? `${value}T00:00:00` : value)) }
function priorityName(priority: number) { return ['No', 'Urgent', 'High', 'Medium', 'Low'][priority] ?? 'No' }
