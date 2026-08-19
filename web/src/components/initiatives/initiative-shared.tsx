import * as Popover from '@radix-ui/react-popover'
import { Check, FolderKanban, Search } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Avatar } from '@/components/issue/issue-row'
import { PriorityIcon } from '@/components/issue/issue-icons'
import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { ProjectPropertyPicker, ProjectStatusGlyph, type ProjectPropertyOption } from '@/components/projects-page/project-property-picker'
import { ProjectTargetDatePicker } from '@/components/projects-page/project-target-date-picker'
import { LabelPicker } from '@/components/issue/label-project-pickers'
import type { Initiative, InitiativeMutationInput, IssueLabel, LabelGroup, Project, User } from '@/types/flow'
import { formatTarget, titleCase } from './initiative-model'

const INITIATIVE_STATUS_OPTIONS: ProjectPropertyOption[] = [
  { value: 'proposed', label: 'Proposed', statusType: 'backlog', shortcut: '1' },
  { value: 'planned', label: 'Planned', statusType: 'planned', shortcut: '2' },
  { value: 'active', label: 'Active', statusType: 'started', shortcut: '3' },
  { value: 'completed', label: 'Completed', statusType: 'completed', shortcut: '4' },
  { value: 'canceled', label: 'Canceled', statusType: 'canceled', shortcut: '5' },
]
const PRIORITY_OPTIONS: ProjectPropertyOption[] = [
  { value: 'none', label: 'No priority', shortcut: '0' }, { value: 'urgent', label: 'Urgent', shortcut: '1' },
  { value: 'high', label: 'High', shortcut: '2' }, { value: 'medium', label: 'Medium', shortcut: '3' }, { value: 'low', label: 'Low', shortcut: '4' },
]

export function InitiativeStatusIcon({ status }: { status: Initiative['status'] }) {
  const option = INITIATIVE_STATUS_OPTIONS.find(item => item.value === status)!
  return <ProjectStatusGlyph name={option.label} type={option.statusType}/>
}

export function InitiativeProperties({ initiative, users, onUpdate, compact = false, only }: {
	initiative: Initiative; users: User[]; compact?: boolean; only?: 'status'|'priority'|'owner'|'target'; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const ownerOptions = useMemo<ProjectPropertyOption[]>(() => [{ value: '', label: 'No owner', group: 'Users' }, ...users.map(user => ({ value: user.id, label: user.displayName || user.name, avatarUrl: user.avatarUrl, group: 'Users' }))], [users])
  const controls = {
    status: <ProjectPropertyPicker label="Change status" onChange={value => onUpdate({ status: value as Initiative['status'] })} options={INITIATIVE_STATUS_OPTIONS} property="status" value={initiative.status}>
      <InitiativeStatusIcon status={initiative.status}/><span>{titleCase(initiative.status)}</span>
    </ProjectPropertyPicker>,
    priority: <ProjectPropertyPicker label="Change priority" onChange={value => onUpdate({ priority: priorityNumber(value) })} options={PRIORITY_OPTIONS} property="priority" value={priorityValue(initiative.priority)}>
      <PriorityIcon priority={initiative.priority} size={15}/><span>{initiative.priorityLabel}</span>
    </ProjectPropertyPicker>,
    owner: <ProjectPropertyPicker label="Change initiative owner" onChange={value => onUpdate({ ownerId: value })} options={ownerOptions} property="lead" value={initiative.owner?.id ?? ''}>
      {initiative.owner ? <Avatar name={initiative.owner.displayName || initiative.owner.name}/> : <NoAssigneeIcon size={15}/>}<span>{initiative.owner?.displayName || initiative.owner?.name || 'Owner'}</span>
    </ProjectPropertyPicker>,
    target: <ProjectTargetDatePicker displayValue={formatTarget(initiative.targetDate)} onChange={value => onUpdate({ targetDate: value })} value={initiative.targetDate}>
      <span className={`li-target-glyph ${initiative.targetDate ? '' : 'is-empty'}`} aria-hidden="true"/><span>{formatTarget(initiative.targetDate) || 'Target date'}</span>
    </ProjectTargetDatePicker>,
  }
  return <div className={`${compact ? 'li-properties li-properties--compact' : 'li-properties'} ${only ? `li-properties--${only}` : ''}`}>{only ? controls[only] : <>{controls.status}{controls.priority}{controls.owner}{controls.target}</>}</div>
}

export function InitiativeLabelsPicker({ initiative, labels, labelGroups = [], onUpdate, compact = false }: {
  initiative: Initiative
  labels: IssueLabel[]
  labelGroups?: LabelGroup[]
  compact?: boolean
  onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const selected = labels.filter(label => initiative.labelIds.includes(label.id))
  return <div className={compact ? 'li-label-picker li-label-picker--compact' : 'li-label-picker'}>
    <LabelPicker labels={labels} labelGroups={labelGroups} value={selected} onToggle={labelId => { void onUpdate({ labelIds: initiative.labelIds.includes(labelId) ? initiative.labelIds.filter(id => id !== labelId) : [...initiative.labelIds, labelId] }) }}/>
  </div>
}

export function ProjectAssociationPicker({ children, initiative, projects, onUpdate, label = 'Add project' }: {
	children?: ReactNode; initiative: Initiative; projects: Project[]; label?: string; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const filtered = projects.filter(project => `${project.name} ${project.summary}`.toLowerCase().includes(query.trim().toLowerCase()))
  const toggle = (id: string) => {
    const next = initiative.projectIds.includes(id) ? initiative.projectIds.filter(item => item !== id) : [...initiative.projectIds, id]
    void onUpdate({ projectIds: next })
  }
  return <Popover.Root open={open} onOpenChange={next => { setOpen(next); if (!next) setQuery('') }}>
    <Popover.Trigger asChild>{children ?? <button className="li-icon-button" aria-label={label} type="button"><span>+</span></button>}</Popover.Trigger>
    <Popover.Portal><Popover.Content align="end" className="li-project-picker" collisionPadding={8} sideOffset={4} onOpenAutoFocus={event => event.preventDefault()}>
      <label><Search size={14}/><input autoFocus aria-label="Search projects…" placeholder="Search projects…" value={query} onChange={event => setQuery(event.target.value)}/></label>
      <div role="listbox" aria-multiselectable="true">{filtered.map(project => <button aria-checked={initiative.projectIds.includes(project.id)} key={project.id} onClick={() => toggle(project.id)} role="option" type="button"><span className="li-picker-checkbox">{initiative.projectIds.includes(project.id) && <Check size={11}/>}</span><span className="li-project-icon" style={{ color: project.color }}><FolderKanban size={14}/></span><span><strong>{project.name}</strong><small>{project.summary || project.status.name}</small></span></button>)}{!filtered.length && <p>No projects found</p>}</div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function priorityValue(value: number) { return ['none', 'urgent', 'high', 'medium', 'low'][value] ?? 'none' }
function priorityNumber(value: string) { return Math.max(0, ['none', 'urgent', 'high', 'medium', 'low'].indexOf(value)) }
