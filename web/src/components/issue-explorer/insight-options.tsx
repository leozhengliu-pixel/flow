import type { ReactNode } from 'react'
import { StatusIcon, WorkflowStatusGlyph } from '@/components/issue/issue-icons'
import type { BootstrapData, WorkflowState } from '@/types/flow'
import styles from './insights-panel.module.css'

/** Linear: Ctrl ⇧ F toggles the Insights fullscreen view. */
export const INSIGHTS_FULLSCREEN_SHORTCUT = 'Ctrl ⇧ F'
/** Linear's Measure / Slice / Segment menus open 5.5px below the select, flush with its right edge. */
export const INSIGHT_MENU_POSITION = { align: 'end', side: 'bottom', sideOffset: 5.5 } as const

export type InsightOption = { id: string; label: string; description?: string; separatorBefore?: boolean; icon?: ReactNode; children?: InsightOption[] }

type StatusKind = WorkflowState['type'] | 'triage' | 'duplicate'
const STATUS_KINDS: StatusKind[] = ['triage', 'backlog', 'unstarted', 'started', 'completed', 'canceled', 'duplicate']
const STATUS_KIND_LABELS: Record<StatusKind, string> = { triage: 'Triage', backlog: 'Backlog', unstarted: 'Unstarted', started: 'Started', completed: 'Completed', canceled: 'Canceled', duplicate: 'Duplicate' }
export type StatusNode = { key: string; label: string; values: string[]; icon: ReactNode; depth: 0 | 1 }

function statusKind(state: Pick<WorkflowState, 'id' | 'name' | 'type'>): StatusKind {
  const id = state.id.toLowerCase(), name = state.name.toLowerCase()
  return id.includes('triage') || name === 'triage' ? 'triage' : id.includes('duplicate') || name === 'duplicate' ? 'duplicate' : state.type
}

/** Linear's "Time in status" tree: each status type, then its statuses (one row per name across teams); Triage and Duplicate stand alone. */
export function timeInStatusTree(data: BootstrapData): StatusNode[] {
  const nodes: StatusNode[] = []
  const states = [...data.states].sort((left, right) => left.position - right.position)
  for (const kind of STATUS_KINDS) {
    const members = states.filter(state => statusKind(state) === kind)
    if (!members.length) continue
    const typeIcon = <WorkflowStatusGlyph state={{ id: `type-${kind}`, name: kind, type: kind === 'triage' || kind === 'duplicate' ? 'canceled' : kind, color: kind === 'triage' ? members[0].color : 'var(--pk-muted)' }} size={14}/>
    if (kind === 'triage' || kind === 'duplicate') { nodes.push({ key: kind, label: STATUS_KIND_LABELS[kind], values: members.map(state => state.id), icon: typeIcon, depth: 0 }); continue }
    nodes.push({ key: kind, label: STATUS_KIND_LABELS[kind], values: [`type:${kind}`], icon: typeIcon, depth: 0 })
    const byName = new Map<string, WorkflowState[]>()
    for (const state of members) byName.set(state.name, [...byName.get(state.name) ?? [], state])
    for (const [name, group] of byName) nodes.push({ key: `${kind}:${name}`, label: name, values: group.map(state => state.id), icon: <StatusIcon state={group[0]} size={14}/>, depth: 1 })
  }
  return nodes
}

export const MEASURE_OPTIONS: InsightOption[] = [
  { id: 'issueCount', label: 'Issue count', description: 'Number of individual issues' },
  { id: 'cycleTime', label: 'Cycle time', description: 'Time from started to completed', separatorBefore: true },
  { id: 'leadTime', label: 'Lead time', description: 'Time from created to completed' },
  { id: 'issueAge', label: 'Issue age', description: 'Time from created to now (not completed)' },
  { id: 'timeInStatus', label: 'Time in status', description: 'Time spent in status' },
]

export function dimensionOptions(data: BootstrapData, includeDates: boolean): InsightOption[] {
  const issueGroups = data.labelGroups.filter(group => group.resourceType === 'issue' && !group.archivedAt)
  const projectGroups = data.labelGroups.filter(group => group.resourceType === 'project' && !group.archivedAt)
  const dot = (color: string) => <i className={styles.optionDot} style={{ backgroundColor: color }}/>
  // Linear only offers the label-group dimensions when the workspace has label groups.
  const options: InsightOption[] = [
    { id: 'status', label: 'Status' }, { id: 'statusType', label: 'Status type' }, { id: 'assignee', label: 'Assignee' }, { id: 'agent', label: 'Agent' },
    { id: 'agentSession', label: 'Agent session' }, { id: 'creator', label: 'Creator' }, { id: 'priority', label: 'Priority' }, { id: 'label', label: 'Label' },
    ...issueGroups.length ? [{ id: 'labelGroup', label: 'Label group', children: issueGroups.map(group => ({ id: `labelGroup:${group.id}`, label: group.name, icon: dot(group.color) })) }] : [],
    // Linear offers Customer when Customer requests is enabled.
    ...data.workspaceSettings?.featureFlags?.['customer-requests'] !== false ? [{ id: 'customer', label: 'Customer' }] : [],
    { id: 'template', label: 'Template' }, { id: 'externalSource', label: 'External source' },
    { id: 'project', label: 'Project', separatorBefore: true }, { id: 'initiative', label: 'Initiative' }, { id: 'projectLabel', label: 'Project label' },
    ...projectGroups.length ? [{ id: 'projectLabelGroup', label: 'Project label group', children: projectGroups.map(group => ({ id: `projectLabelGroup:${group.id}`, label: group.name, icon: dot(group.color) })) }] : [],
    { id: 'cycle', label: 'Cycle' }, { id: 'addedToCycle', label: 'Added to cycle' },
  ]
  if (includeDates) options.push(
    { id: 'createdDate', label: 'Created date', separatorBefore: true }, { id: 'completedDate', label: 'Completed date' },
    { id: 'canceledDate', label: 'Canceled date' }, { id: 'startedDate', label: 'Started date' },
    { id: 'dueDate', label: 'Due date' }, { id: 'burnUp', label: 'Burn-up', separatorBefore: true },
  )
  return options
}

