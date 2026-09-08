import { Plus } from 'lucide-react'
import type { IssueLabel, LabelGroup } from '@/types/flow'
import { LabelIcon } from '@/components/issue/issue-icons'
import { toggleGroupedLabelIds } from '@/lib/labels'
import { PropertyMenu, type PropertyOption } from './property-menu'
import './project-label-picker.css'
import { projectLabelOptions } from './project-label-menu-model'
import { LabelHoverPreviewContent } from './label-hover-preview'

export function ProjectLabelControl({ labels, labelGroups, selectedIds, onChange, sidebar = false, open, onOpenChange, onCreateLabel }: {
  labels: IssueLabel[]
  labelGroups: LabelGroup[]
  selectedIds: string[]
  onChange: (ids: string[]) => void
  sidebar?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  onCreateLabel?: (name: string, groupId?: string) => Promise<IssueLabel>
}) {
  const options = projectLabelOptions(labels, labelGroups)
  const selected = options.filter(option => selectedIds.includes(option.id)).sort((a, b) => Number(Boolean(b.groupId)) - Number(Boolean(a.groupId)) || (a.groupLabel ?? a.label).localeCompare(b.groupLabel ?? b.label))
  const toggle = (id: string) => onChange(toggleGroupedLabelIds(selectedIds, id, labels))
  const create = onCreateLabel ? async (name: string, groupId?: string) => {
    const label = await onCreateLabel(name, groupId)
    onChange(toggleGroupedLabelIds(selectedIds, label.id, [...labels, label]))
  } : undefined
  const chip = (option: PropertyOption) => <PropertyMenu key={option.id} kind="project-labels" label="Labels" multiple selectedIds={selectedIds} options={options} onChange={toggle} onCreate={create} labelGroupId={option.groupId} side={sidebar ? 'left' : 'bottom'} ariaLabel={option.groupLabel ? `Change ${option.groupLabel}: ${option.label}` : `Change labels: ${option.label}`} hoverClassName="label-hover-preview" hoverContent={<LabelHoverPreviewContent label={{ name: option.label, color: option.color ?? '', description: option.description, issueCount: option.issueCount, scope: option.scope, resourceType: 'project' }}/>} triggerRole="button" triggerClassName="project-label-chip" trigger={<><i style={{ background: option.color }}/><span data-i18n-ignore>{option.label}</span>{option.groupId && <span className="project-label-chip-chevron"><svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M7.00194 10.6239C6.66861 10.8183 6.25 10.5779 6.25 10.192V5.80802C6.25 5.42212 6.66861 5.18169 7.00194 5.37613L10.7596 7.56811C11.0904 7.76105 11.0904 8.23895 10.7596 8.43189L7.00194 10.6239Z"/></svg></span>}</>}/>
  return <div className={`project-label-control${sidebar ? ' is-sidebar' : ''}`}>
    {selected.map(chip)}
    <div className="project-label-last">
      <PropertyMenu kind="project-labels" label="Labels" multiple options={options} selectedIds={selectedIds} onChange={toggle} onCreate={create} open={open} onOpenChange={onOpenChange} side={sidebar ? 'left' : 'bottom'} triggerRole="button" ariaLabel="Add label" triggerClassName={`project-label-add${sidebar && selected.length ? ' is-icon' : ''}`} trigger={sidebar && !selected.length ? <><LabelIcon size={16}/><span>Add label</span></> : sidebar ? <Plus size={16}/> : <span>Add label…</span>}/>
    </div>
  </div>
}
