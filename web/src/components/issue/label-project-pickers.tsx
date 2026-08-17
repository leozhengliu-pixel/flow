import type { IssueLabel, Project, ProjectSummary } from '@/types/flow'
import { Plus } from 'lucide-react'
import { LabelIcon, NoProjectIcon, ProjectIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { LabelHoverPreview } from '@/components/property/label-hover-preview'
import { PropertyShortcutTooltip } from '@/components/property/issue-property-hover'

export function LabelPicker({ value, labels, onToggle, inline = false }: { value: IssueLabel[]; labels: IssueLabel[]; onToggle: (id: string) => void | Promise<void>; inline?: boolean }) {
  const options = labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope }))
  return <div className={`label-project-picker labels-picker${inline?' labels-picker--inline':''}`}>
    <div className="issue-label-chips" aria-label="Selected labels">{value.map(label => <LabelHoverPreview label={label} key={label.id}><span><i style={{ background: label.color }}/>{label.name}</span></LabelHoverPreview>)}</div>
    <PropertyMenu
      label="Labels"
      value={value.length ? `${value.length} labels` : 'Add label'}
      multiple
      selectedIds={value.map(label => label.id)}
      options={options}
      kind="labels"
      searchPlaceholder="Change or add labels…"
      ariaLabel="Add labels"
      triggerClassName="label-project-trigger"
      trigger={inline?<Plus size={15}/>:<><LabelIcon size={15}/><span>Add label</span></>}
      hoverContent={<PropertyShortcutTooltip label="Change or add labels" shortcut="L"/>}
      onChange={onToggle}
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
