import type { Cycle, Issue, IssueLabel, LabelGroup, Project, ProjectSummary, TeamSettings } from '@/types/flow'
import { CircleDashed, Plus } from 'lucide-react'
import { CycleIcon, LabelIcon, NoProjectIcon, ProjectIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { LabelHoverPreview } from '@/components/property/label-hover-preview'
import { PropertyShortcutTooltip } from '@/components/property/issue-property-hover'

export function LabelPicker({ value, labels, labelGroups = [], emptyLabel, onToggle, onCreate, inline = false, searchShortcut, showGroupHeadings, surfaceClassName }: { value: IssueLabel[]; labels: IssueLabel[]; labelGroups?: LabelGroup[]; emptyLabel?: string; onToggle: (id: string) => void | Promise<void>; onCreate?: (name: string) => void | Promise<void>; inline?: boolean; searchShortcut?: string; showGroupHeadings?: boolean; surfaceClassName?: string }) {
  const groupNames = new Map(labelGroups.map(group => [group.id, group.name]))
  const groupColors = new Map(labelGroups.map(group => [group.id, group.color]))
  const options = labels.map(label => { const groupLabel = label.groupId ? groupNames.get(label.groupId) : undefined; return { id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, resourceType: label.resourceType, groupId: label.groupId, groupLabel, groupColor: label.groupId ? groupColors.get(label.groupId) : undefined, keywords: groupLabel } })
  return <div className={`label-project-picker labels-picker${inline?' labels-picker--inline':''}`}>
    <PropertyMenu
      label="Labels"
      value={value.length ? `${value.length} labels` : 'Add label'}
      multiple
      selectedIds={value.map(label => label.id)}
      options={options}
      kind="labels"
      emptyLabel={emptyLabel}
      searchPlaceholder={value.length ? 'Change or add labels…' : 'Add labels…'}
      searchShortcut={searchShortcut ?? 'L'}
      showGroupHeadings={showGroupHeadings}
      surfaceClassName={surfaceClassName}
      ariaLabel="Add labels"
      customTrigger={({open,activeTrigger,openMenu})=><div className="labels-picker-trigger" aria-label="Change or add labels">
        <div className="issue-label-chips" aria-label="Selected labels">{value.map(label => <LabelHoverPreview label={label} key={label.id}><button type="button" className="issue-label-chip" aria-label={`Change or add labels. ${label.name} selected`} aria-haspopup="dialog" aria-expanded={open&&activeTrigger===`label:${label.id}`} onClick={()=>openMenu(`label:${label.id}`)}><i style={{ background: label.color }}/><span data-i18n-ignore>{label.name}</span></button></LabelHoverPreview>)}</div>
        <button type="button" className="label-project-trigger" aria-label="Add label" aria-haspopup="dialog" aria-expanded={open&&activeTrigger==='add'} onClick={()=>openMenu('add')}>{inline?<Plus size={15}/>:<><LabelIcon size={15}/><span>Add label</span></>}</button>
      </div>}
      hoverContent={<PropertyShortcutTooltip label="Change or add labels" shortcut="L"/>}
      onChange={onToggle}
      onCreate={onCreate}
    />
  </div>
}

export function ProjectPicker({ value, projects, teamName, onChange }: { value?: ProjectSummary; projects: Project[]; teamName?: string; onChange: (id: string) => void | Promise<void> }) {
  const options = [
    { id: '', label: 'No project', icon: <NoProjectIcon size={15}/> },
    ...projects.map(project => ({ id: project.id, label: project.name, color: project.color, icon: <ProjectIcon size={15} style={{ color: project.color }}/> })),
  ]
  return <div className="label-project-picker project-picker">
    <PropertyMenu
      label="Project"
      value={value?.name ?? 'Add to project'}
      selectedId={value?.id ?? ''}
      options={options}
      kind="project"
      teamName={teamName}
      searchPlaceholder="Add to project…"
      ariaLabel={value ? `Change project. Current project is ${value.name}` : 'Add to project'}
      triggerClassName="label-project-trigger"
      trigger={<><ProjectIcon size={15}/><span>{value?.name ?? 'Add to project'}</span></>}
      hoverContent={<PropertyShortcutTooltip label={value?'Change project':'Add to project'} shortcut="⇧ P"/>}
      onChange={onChange}
    />
  </div>
}

export function CyclePicker({ valueId, cycles, issues = [], teamId, onChange }: { valueId?: string; cycles: Cycle[]; issues?: Issue[]; teamId: string; onChange: (id: string) => void | Promise<void> }) {
  const available = cycles.filter(cycle => cycle.teamId === teamId && cycle.status !== 'completed')
  const value = cycles.find(cycle => cycle.id === valueId)
  const nextUpcomingId = [...available].filter(cycle => cycle.status === 'upcoming').sort((left, right) => left.startsAt.localeCompare(right.startsAt))[0]?.id
  const options = [{ id: '', label: 'No cycle', icon: <CycleIcon noCycle/> }, ...available.map(cycle => ({ id: cycle.id, label: cycle.name, icon: <CycleIcon cycle={cycle} nextUpcomingId={nextUpcomingId} progress={cycleProgress(issues,cycle.id)}/>, i18nIgnore: true }))]
  return <div className="label-project-picker cycle-picker"><PropertyMenu label="Cycle" value={value?.name ?? 'Add to cycle'} valueIsEntityName={Boolean(value)} selectedId={valueId ?? ''} options={options} searchPlaceholder="Add to cycle…" ariaLabel={value ? `Change cycle. Current cycle is ${value.name}` : 'Add to cycle'} triggerClassName="label-project-trigger" trigger={<><CycleIcon cycle={value} nextUpcomingId={nextUpcomingId} progress={value?cycleProgress(issues,value.id):0}/><span data-i18n-ignore={value ? true : undefined}>{value?.name ?? 'Add to cycle'}</span></>} onChange={onChange}/></div>
}

export function EstimatePicker({ value, estimateType, onChange }: { value?: number; estimateType: TeamSettings['estimateType']; onChange: (estimate: number) => void | Promise<void> }) {
  const values = estimateType === 'fibonacci' ? [0,1,2,3,5,8,13,21] : estimateType === 'exponential' ? [0,1,2,4,8,16] : [0,1,2,3,5,8]
  const label = value ? `${value} point${value === 1 ? '' : 's'}` : 'Estimate'
  return <div className="label-project-picker estimate-picker"><PropertyMenu label="Estimate" value={label} selectedId={String(value ?? 0)} options={values.map(estimate => ({ id: String(estimate), label: estimate ? `${estimate} point${estimate === 1 ? '' : 's'}` : 'No estimate', icon: <EstimateIcon value={estimate}/> }))} ariaLabel={value ? `Change estimate. Current estimate is ${value}` : 'Set estimate'} triggerClassName="label-project-trigger" trigger={<><EstimateIcon value={value ?? 0}/><span>{label}</span></>} onChange={id => onChange(Number(id))}/></div>
}

function EstimateIcon({ value }: { value: number }) { return value ? <span aria-hidden="true" className="estimate-value-icon">{value}</span> : <CircleDashed aria-hidden="true" size={15}/> }
function cycleProgress(issues: Issue[], cycleId: string) { const scoped=issues.filter(issue=>issue.cycleId===cycleId&&!issue.archivedAt);return scoped.length?Math.round(scoped.filter(issue=>issue.state.type==='completed'||issue.state.type==='canceled').length/scoped.length*100):0 }
