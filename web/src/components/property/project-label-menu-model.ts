import type { PropertyOption } from './property-menu'
import type { IssueLabel, LabelGroup } from '@/types/flow'

export function projectLabelOptions(labels: IssueLabel[], groups: LabelGroup[]): PropertyOption[] {
  const byId = new Map(groups.filter(group => group.resourceType === 'project').map(group => [group.id, group]))
  return labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, resourceType: 'project', scope: label.scope, groupId: byId.has(label.groupId ?? '') ? label.groupId : undefined, groupLabel: byId.get(label.groupId ?? '')?.name, groupColor: byId.get(label.groupId ?? '')?.color, archived: Boolean(label.archivedAt || byId.get(label.groupId ?? '')?.archivedAt), i18nIgnore: true }))
}

export type ProjectLabelEntry = {
  id: string
  label: string
  color?: string
  checked: boolean
  option?: PropertyOption
  children?: PropertyOption[]
  detail?: string
}

export function projectLabelEntries(options: PropertyOption[], selectedIds: string[], query = ''): ProjectLabelEntry[] {
  const selected = new Set(selectedIds)
  const groups = new Map<string, ProjectLabelEntry>()
  const entries: ProjectLabelEntry[] = []
  const needle = query.trim().toLocaleLowerCase()
  for (const option of options) {
    if (option.groupId && option.groupLabel) {
      let group = groups.get(option.groupId)
      if (!group) {
        group = { id: `group:${option.groupId}`, label: option.groupLabel, color: option.groupColor, checked: false, children: [] }
        groups.set(option.groupId, group)
        entries.push(group)
      }
      group.children!.push(option)
      if (selected.has(option.id)) {
        group.checked = true
        group.detail = option.label
        group.color = option.color
      }
    } else entries.push({ id: option.id, label: option.label, color: option.color, checked: selected.has(option.id), option })
  }
  const result: ProjectLabelEntry[] = entries.flatMap<ProjectLabelEntry>(entry => {
    if (!needle) return [entry]
    if (!entry.children) return matchesProjectLabel(entry.option!, needle) ? [entry] : []
    // Nested actions are searchable from the root after three characters.
    const children = needle.length >= 3 ? entry.children.filter(option => matchesProjectLabel(option, needle)).map(option => ({ id: option.id, label: option.label, color: option.color, checked: selected.has(option.id), option, detail: entry.label })) : []
    return entry.label.toLocaleLowerCase().includes(needle) ? [entry, ...children] : children
  })
  return result.sort((a, b) => Number(b.checked) - Number(a.checked) || Number(Boolean(b.children)) - Number(Boolean(a.children)) || a.label.localeCompare(b.label))
}

export function matchesProjectLabel(option: PropertyOption, needle: string) {
  return `${option.groupLabel ?? ''} ${option.label} ${option.keywords ?? ''}`.toLocaleLowerCase().includes(needle)
}
