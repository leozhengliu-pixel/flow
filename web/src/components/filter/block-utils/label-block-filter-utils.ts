/**
 * LS-0706 labelBlockFilterUtils — grouped label options for explorer / initiatives / projects.
 */
import type { FilterBlockDefinition, FilterBlockOption, FilterEntityType } from '../filter-block-types'

export interface FilterLabelLike {
  id: string
  name: string
  color?: string
  groupId?: string | null
  groupName?: string | null
  teamId?: string | null
  isGroup?: boolean
}

export function labelToFilterOption(label: FilterLabelLike): FilterBlockOption {
  return {
    id: label.id,
    label: label.groupName ? `${label.groupName} → ${label.name}` : label.name,
    color: label.color,
    keywords: [label.name, label.groupName, label.teamId].filter(Boolean).join(' '),
  }
}

/** Group labels under parent group headers; flat labels last. */
export function buildLabelFilterOptions(
  labels: FilterLabelLike[],
  options: { teamIds?: string[]; includeGroupsAsValues?: boolean } = {},
): FilterBlockOption[] {
  const scoped = options.teamIds?.length
    ? labels.filter(label => !label.teamId || options.teamIds!.includes(label.teamId))
    : labels
  const groups = new Map<string, FilterLabelLike[]>()
  const ungrouped: FilterLabelLike[] = []
  for (const label of scoped) {
    if (label.isGroup) continue
    if (label.groupId) {
      const list = groups.get(label.groupId) ?? []
      list.push(label)
      groups.set(label.groupId, list)
    } else {
      ungrouped.push(label)
    }
  }
  const result: FilterBlockOption[] = []
  if (options.includeGroupsAsValues) {
    for (const label of scoped.filter(item => item.isGroup)) {
      result.push({
        id: label.id,
        label: label.name,
        color: label.color,
        keywords: `${label.name} group`,
      })
    }
  }
  for (const [, members] of groups) {
    members.sort((a, b) => a.name.localeCompare(b.name))
    for (const member of members) result.push(labelToFilterOption(member))
  }
  ungrouped.sort((a, b) => a.name.localeCompare(b.name))
  for (const label of ungrouped) result.push(labelToFilterOption(label))
  return result
}

export function createLabelFilterBlock(args: {
  id?: string
  key?: string
  name?: string
  entityType: FilterEntityType
  sortPriority?: number
}): FilterBlockDefinition {
  return {
    id: args.id ?? 'labels',
    key: args.key ?? 'labelId',
    name: args.name ?? 'Labels',
    valueType: 'equalValue',
    inputType: 'options',
    entityType: args.entityType,
    sortPriority: args.sortPriority ?? 50,
    defaultCompareOption: values => (values.length > 1 ? 'is' : 'is'),
  }
}

export const labelBlockFilterUtils = {
  labelToFilterOption,
  buildLabelFilterOptions,
  createLabelFilterBlock,
}
