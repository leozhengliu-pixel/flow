import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { CalendarDays, ChevronRight, CircleDashed, MoreHorizontal, Paperclip } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'

import type { BootstrapData, Issue } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { CycleIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { IssueTitleEditor } from '@/components/issue/issue-title-editor'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { DueDateCommand } from '@/components/issue/due-date-picker'
import { labelsForResource, toggleGroupedLabelIds } from '@/lib/labels'

export interface SubIssueInput {
  title: string
  description: string
  stateId: string
  priority: number
  estimate?: number
  assigneeId?: string
  projectId?: string
  cycleId?: string
  dueDate?: string
  labelIds: string[]
  attachments: File[]
}

export function SubIssueEditor({ parent, data, onCancel, onCreate }: { parent: Issue; data: BootstrapData; onCancel: () => void; onCreate: (input: SubIssueInput) => Promise<void> }) {
  const teamStates = useMemo(() => {
    const specific = data.states.some(state => state.teamId === parent.team.id)
    return data.states.filter(state => specific ? state.teamId === parent.team.id : !state.teamId).sort((a, b) => (a.position??0) - (b.position??0))
  }, [data.states, parent.team.id])
  const defaultState = teamStates.find(state => state.type === 'unstarted') ?? teamStates[0] ?? parent.state
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState<DescriptionSnapshot | null>(null)
  const [saving, setSaving] = useState(false)
  const [stateId, setStateId] = useState(defaultState.id)
  const [priority, setPriority] = useState(parent.priority)
  const [estimate, setEstimate] = useState(parent.estimate ?? 0)
  const [assigneeId, setAssigneeId] = useState(parent.assignee?.id ?? '')
  const [projectId, setProjectId] = useState(parent.project?.id ?? '')
  const [cycleId, setCycleId] = useState('')
  const [dueDate, setDueDate] = useState('')
  const [labelIds, setLabelIds] = useState<string[]>([])
  const [attachments, setAttachments] = useState<File[]>([])
  const fileRef = useRef<HTMLInputElement>(null)
  const state = teamStates.find(item => item.id === stateId) ?? defaultState
  const assignee = data.users.find(user => user.id === assigneeId)
  const project = data.projects.find(item => item.id === projectId)
  const cycle = data.cycles.find(item => item.id === cycleId)
  const cycles = data.cycles.filter(item => item.teamId === parent.team.id && item.status !== 'completed')
  const nextUpcomingCycleId = [...cycles].filter(item => item.status === 'upcoming').sort((left,right)=>left.startsAt.localeCompare(right.startsAt))[0]?.id
  const labels = labelsForResource(data.labels, 'issue', data.labelGroups).filter(label => !label.scope || label.scope === 'Workspace' || label.scope === parent.team.id)
  const labelGroupNames = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.name])), [data.labelGroups])
  const labelGroupColors = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.color])), [data.labelGroups])
  const estimateType = data.teamSettings[parent.team.id]?.estimateType ?? 'notUsed'
  const estimateValues = estimateType === 'fibonacci' ? [0,1,2,3,5,8,13,21] : estimateType === 'exponential' ? [0,1,2,4,8,16] : [0,1,2,3,5,8]
  const toggleLabel = (id: string) => setLabelIds(current => toggleGroupedLabelIds(current, id, labels))
  const submit = async () => {
    if (!title.trim() || saving) return
    setSaving(true)
    try {
      await onCreate({ title: title.trim(), description: description?.markdown.trim() ?? '', stateId, priority, estimate: estimate || undefined, assigneeId: assigneeId || undefined, projectId: projectId || undefined, cycleId: cycleId || undefined, dueDate: dueDate || undefined, labelIds, attachments })
    } finally { setSaving(false) }
  }
  return <form className="sub-issue-editor" onSubmit={event => { event.preventDefault(); void submit() }}>
    <div className="sub-issue-title-row">
      <PropertyMenu ariaLabel={`Change status. ${state.name} is selected`} compact label="Status" value={state.name} selectedId={stateId} icon={<StatusIcon state={state}/>} options={teamStates.map(item => ({ id: item.id, label: item.name, icon: <StatusIcon state={item}/> }))} onChange={setStateId} trigger={<StatusIcon state={state} size={14}/>} triggerClassName="sub-issue-status-trigger"/>
      <IssueTitleEditor autoFocus value={title} onChange={setTitle} onEnter={() => document.querySelector<HTMLElement>('.sub-issue-description-editor')?.focus()} onSubmit={() => void submit()} className="sub-issue-title-editor"/>
    </div>
    <IssueDescriptionEditor value={description?.markdown ?? ''} state={description?.documentJSON} onChange={setDescription} onSubmit={() => void submit()} className="sub-issue-description-editor"/>
    <div className="sub-issue-actions"><div className="sub-issue-properties">
      <button type="button" className="sub-issue-team" aria-label="Set team" disabled><TeamIcon team={parent.team} /><span data-i18n-ignore>{parent.team.key}</span></button>
      <PropertyMenu compact label="Priority" value={priority ? ['', 'Urgent', 'High', 'Medium', 'Low'][priority] : 'Priority'} selectedId={String(priority)} icon={<PriorityIcon priority={priority}/>} options={['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({ id: String(id), label, icon: <PriorityIcon priority={id}/> }))} onChange={id => setPriority(Number(id))}/>
      {estimateType!=='notUsed'&&<PropertyMenu compact label="Estimate" value={estimate?`${estimate} point${estimate===1?'':'s'}`:'Estimate'} selectedId={String(estimate)} icon={<EstimateGlyph value={estimate}/>} options={estimateValues.map(value=>({id:String(value),label:value?`${value} point${value===1?'':'s'}`:'No estimate',icon:<EstimateGlyph value={value}/>}))} onChange={id=>setEstimate(Number(id))}/>}
      <PropertyMenu compact label="Assignee" value={assignee?.displayName ?? 'Assignee'} selectedId={assigneeId} icon={assignee ? <Avatar name={assignee.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No assignee', icon: <NoAssigneeIcon size={14}/> }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))]} onChange={setAssigneeId}/>
      <PropertyMenu compact multiple label="Labels" value={labelIds.length ? `${labelIds.length} labels` : 'Labels'} selectedIds={labelIds} icon={<LabelIcon size={14}/>} options={labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, resourceType: label.resourceType, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined, groupColor: label.groupId ? labelGroupColors.get(label.groupId) : undefined }))} onChange={toggleLabel}/>
      <PropertyMenu compact label="Cycle" value={cycle?.name ?? ''} valueIsEntityName={Boolean(cycle)} selectedId={cycleId} icon={<CycleIcon size={14}/>} options={[{ id: '', label: 'No cycle', icon: <CycleIcon noCycle size={14}/> }, ...cycles.map(item => ({ id: item.id, label: item.name, icon: <CycleIcon cycle={item} nextUpcomingId={nextUpcomingCycleId} progress={cycleIssueProgress(data.issues,item.id)} size={14}/>, i18nIgnore: true }))]} onChange={setCycleId} trigger={cycle ? <><CycleIcon cycle={cycle} nextUpcomingId={nextUpcomingCycleId} progress={cycleIssueProgress(data.issues,cycle.id)} size={14}/><span data-i18n-ignore>{cycle.name}</span></> : <CycleIcon size={14}/>} triggerClassName="sub-issue-cycle-trigger" ariaLabel="Add to cycle"/>
      <SubIssueMoreMenu data={data} dueDate={dueDate} project={project} onDueDate={setDueDate} onProject={setProjectId}/>
      <button type="button" className="sub-issue-attach" aria-label="Attach images, files, or videos" title={attachments.length ? `${attachments.length} file${attachments.length === 1 ? '' : 's'} selected` : undefined} onClick={() => fileRef.current?.click()}><Paperclip/>{attachments.length > 0 && <span>{attachments.length}</span>}</button>
      <input ref={fileRef} type="file" multiple hidden onChange={event => { setAttachments(Array.from(event.target.files ?? [])); event.target.value = '' }}/>
    </div><button type="button" className="sub-issue-cancel" aria-label="Discard sub-issue" onClick={onCancel}>Cancel</button><button type="submit" className="sub-issue-create" disabled={!title.trim() || saving}>{saving ? 'Creating…' : 'Create'}</button></div>
  </form>
}

function SubIssueMoreMenu({ data, dueDate, project, onDueDate, onProject }: { data: BootstrapData; dueDate: string; project?: BootstrapData['projects'][number]; onDueDate: (value: string) => void; onProject: (id: string) => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="sub-issue-more" type="button" role="combobox" aria-label="More actions"><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="sub-issue-more-menu" sideOffset={4} align="start">
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><ProjectIcon/><span>Project</span><kbd>⇧ P</kbd><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="sub-issue-more-menu" sideOffset={4}><DropdownMenu.Item onSelect={() => onProject('')}><NoProjectIcon/><span>No project</span></DropdownMenu.Item>{data.projects.map(item => <DropdownMenu.Item key={item.id} onSelect={() => onProject(item.id)}><ProjectIcon style={{ color: item.color }}/><span data-i18n-ignore>{item.name}</span>{item.id === project?.id && <span className="sub-issue-selected">✓</span>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><CalendarDays/><span>{dueDate || 'Set due date'}</span><kbd>⇧ D</kbd><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="sub-issue-due-menu" sideOffset={4}><DueDateCommand value={dueDate} onSelect={async value => onDueDate(value)}/></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function EstimateGlyph({ value }: { value: number }) { return value ? <span aria-hidden="true" className="estimate-value-icon">{value}</span> : <CircleDashed aria-hidden="true"/> }
function cycleIssueProgress(issues: Issue[], cycleId: string) { const scoped=issues.filter(issue=>issue.cycleId===cycleId&&!issue.archivedAt);return scoped.length?Math.round(scoped.filter(issue=>issue.state.type==='completed'||issue.state.type==='canceled').length/scoped.length*100):0 }
