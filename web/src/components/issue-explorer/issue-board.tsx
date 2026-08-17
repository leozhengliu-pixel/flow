import { useState, type CSSProperties, type DragEvent, type MouseEvent } from 'react'
import { CalendarDays, Check, Ellipsis, Minus, Plus } from 'lucide-react'
import type { MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { NoAssigneeIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import styles from './issue-board.module.css'

export function IssueBoard({ groups, properties, selectedIds, onCreateIssue, onMove, onOpenIssue, onSelectIssue }: {
  groups: MyIssuesGroupData[]
  properties: ReadonlySet<MyIssuesProperty>
  selectedIds: ReadonlySet<string>
  onCreateIssue?: (group: MyIssuesGroupData) => void
  onMove: (issue: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => void
  onOpenIssue: (issue: MyIssuesRowData) => void
  onSelectIssue: (issueId: string, selected: boolean, range: boolean) => void
}) {
  const [draggingId, setDraggingId] = useState<string>()
  const [over, setOver] = useState<{ groupId: string; index: number }>()
  const sourceGroup = groups.find(group => group.issues.some(issue => issue.id === draggingId))
  const drop = (event: DragEvent, group: MyIssuesGroupData, index: number) => {
    event.preventDefault()
    const issue = sourceGroup?.issues.find(item => item.id === draggingId)
    if (issue && sourceGroup) onMove(issue, sourceGroup.id, group.id, index)
    setDraggingId(undefined); setOver(undefined)
  }

  return <div className={styles.board} role="list" aria-label="Issue board">
    {groups.map(group => <section className={styles.column} role="listitem" key={group.id} aria-label={group.label} onDragOver={event => { event.preventDefault(); if (event.target === event.currentTarget) setOver({ groupId: group.id, index: group.issues.length }) }} onDrop={event => drop(event, group, group.issues.length)}>
      <header className={styles.columnHeader}>
        {group.stateType && <StatusIcon state={{ id: group.id, name: group.label, type: group.stateType, color: '#8a8d93' }} size={14}/>}
        <strong>{group.label}</strong><button className={styles.count} type="button" aria-label={`${group.issues.length} issues`}>{group.issues.length}</button>
        <button className={styles.headerButton} type="button" aria-label="Open menu"><Ellipsis size={14}/></button>
        <button className={styles.headerButton} type="button" aria-label="Create new issue" onClick={() => onCreateIssue?.(group)}><Plus size={15}/></button>
      </header>
      <div className={styles.cards} data-over={over?.groupId === group.id && over.index === group.issues.length}>
        {group.issues.map((issue, index) => <IssueBoardCard
          key={issue.id} issue={issue} properties={properties} selected={selectedIds.has(issue.id)} dragging={draggingId === issue.id}
          dropBefore={over?.groupId === group.id && over.index === index}
          onDragStart={() => setDraggingId(issue.id)} onDragEnd={() => { setDraggingId(undefined); setOver(undefined) }}
          onDragOver={event => { event.preventDefault(); event.stopPropagation(); const rect = event.currentTarget.getBoundingClientRect(); setOver({ groupId: group.id, index: index + (event.clientY > rect.top + rect.height / 2 ? 1 : 0) }) }}
          onDrop={event => { event.stopPropagation(); drop(event, group, over?.groupId === group.id ? over.index : index) }}
          onOpen={() => onOpenIssue(issue)} onSelect={(selected, range) => onSelectIssue(issue.id, selected, range)}/>) }
        <button className={styles.addIssue} type="button" onClick={() => onCreateIssue?.(group)}><Plus size={14}/>Add new issue</button>
      </div>
    </section>)}
  </div>
}

function IssueBoardCard({ issue, properties, selected, dragging, dropBefore, onDragStart, onDragEnd, onDragOver, onDrop, onOpen, onSelect }: {
  issue: MyIssuesRowData; properties: ReadonlySet<MyIssuesProperty>; selected: boolean; dragging: boolean; dropBefore: boolean
  onDragStart: () => void; onDragEnd: () => void; onDragOver: (event: DragEvent<HTMLElement>) => void; onDrop: (event: DragEvent<HTMLElement>) => void; onOpen: () => void; onSelect: (selected: boolean, range: boolean) => void
}) {
  const click = (event: MouseEvent) => { if ((event.target as Element).closest('button')) return; onOpen() }
  return <article className={styles.card} role="button" tabIndex={0} draggable data-selected={selected} data-dragging={dragging} data-drop-before={dropBefore} aria-label={`${issue.identifier} ${issue.title}`} onClick={click} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpen() } }} onDragStart={event => { event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', issue.id); onDragStart() }} onDragEnd={onDragEnd} onDragOver={onDragOver} onDrop={onDrop}>
    <div className={styles.cardTop}>
      {properties.has('id') && <span>{issue.identifier}</span>}
      {properties.has('assignee') && (issue.assignee ? <Avatar assignee={issue.assignee}/> : <span className={styles.noAssignee}><NoAssigneeIcon size={18}/></span>)}
    </div>
    <div className={styles.titleRow}>
      {properties.has('status') && <span className={styles.status}><StatusIcon state={issue.state} size={14}/></span>}
      <button className={styles.checkbox} type="button" role="checkbox" aria-label="Select issue" aria-checked={selected} data-checked={selected} onClick={event => onSelect(!selected, event.shiftKey)}>{selected && <Check size={11}/>}</button>
      <strong className={styles.title}>{issue.title}</strong>
    </div>
    <CardProperties issue={issue} properties={properties}/>
    {properties.has('created') && <span className={styles.created}>Created {formatDate(issue.createdAt)}</span>}
  </article>
}

function CardProperties({ issue, properties }: { issue: MyIssuesRowData; properties: ReadonlySet<MyIssuesProperty> }) {
  const labels = properties.has('labels') ? issue.labels ?? [] : []
  const shownLabels = labels.slice(0, 4)
  const hiddenLabelCount = labels.length - shownLabels.length
  const visible = properties.has('priority') || Boolean(issue.estimate) || Boolean(properties.has('project') && issue.project) || shownLabels.length > 0 || Boolean(properties.has('dueDate') && issue.dueDate)
  if (!visible) return null
  return <div className={styles.cardMeta}>
    {properties.has('priority') && <span className={`${styles.badge} ${styles.iconBadge}`} title={`${priorityName(issue.priority)} priority`}><PriorityIcon priority={issue.priority} size={14}/></span>}
    {issue.estimate != null && issue.estimate > 0 && <span className={styles.badge}><span className={styles.estimateIcon}><Minus size={10}/></span><span>{issue.estimate}</span></span>}
    {properties.has('project') && issue.project && <span className={`${styles.badge} ${styles.projectBadge}`}><ProjectIcon size={14}/><span>{issue.project.name}</span></span>}
    {shownLabels.map(label => <Badge key={label.id} color={label.color}>{label.name}</Badge>)}
    {hiddenLabelCount > 0 && <span className={styles.badge}><span className={styles.labelStack}>{labels.slice(4, 6).map((label, index) => <i key={label.id} style={{ '--dot-index': index, backgroundColor: label.color } as CSSProperties}/>)}</span><span>+{hiddenLabelCount} labels</span></span>}
    {properties.has('dueDate') && issue.dueDate && <span className={styles.badge}><CalendarDays size={13}/><span>{formatDate(issue.dueDate)}</span></span>}
  </div>
}

function Badge({ children, color }: { children: string; color: string }) { return <span className={styles.badge}><i className={styles.labelDot} style={{ backgroundColor: color }}/><span>{children}</span></span> }
function Avatar({ assignee }: { assignee: NonNullable<MyIssuesRowData['assignee']> }) { const initials = assignee.name.split(/\s+/).filter(Boolean).slice(0,2).map(part => part[0]).join('').toUpperCase(); return <span className={styles.avatar} style={{ '--avatar': assignee.color ?? '#d95f66' } as CSSProperties}>{assignee.avatarUrl ? <img src={assignee.avatarUrl} alt=""/> : initials}</span> }
function formatDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(new Date(value.length === 10 ? `${value}T00:00:00` : value)) }
function priorityName(priority: number) { return ['No', 'Urgent', 'High', 'Medium', 'Low'][priority] ?? 'No' }
