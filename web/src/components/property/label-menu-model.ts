import type { PropertyOption } from './property-menu'

/** A label group row of the labels picker: it opens a submenu of the group's labels. */
export interface LabelMenuGroup {
  id: string
  label: string
  color?: string
  options: PropertyOption[]
  /** One of the group's labels is selected: the row leads the list with a ticked box. */
  checked: boolean
  /** The selected labels of the group, shown after the group's name. */
  detail?: string
  teamScoped: boolean
}

/**
 * A row of the labels picker. A label of a group shows its group's name: after the label ("detail", a frequently used
 * label) or before it as "Group › Label" ("path", a label found by a search).
 */
export type LabelMenuEntry = { type: 'group'; group: LabelMenuGroup } | { type: 'label'; option: PropertyOption; groupName?: 'detail' | 'path' }

export interface LabelMenuModel {
  /** Selected labels and groups with a selected label, groups first. */
  selected: LabelMenuEntry[]
  /** Unselected team labels and groups, groups first. */
  team: LabelMenuEntry[]
  /** Unselected workspace labels and groups, groups first. */
  workspace: LabelMenuEntry[]
}

/** A search reaches into label groups (lists their labels as "Group › Label") from this many characters. */
export const LABEL_GROUP_SEARCH_MIN = 3

/** A query's length for that threshold: a CJK character counts as two, so a two-character Chinese word ("缺陷") already searches inside groups. */
export function labelSearchWidth(query: string) {
  let width = 0
  for (const char of query.trim()) width += /[\u2e80-\u9fff\uac00-\ud7af\uf900-\ufaff\uff00-\uffef]/.test(char) ? 2 : 1
  return width
}

/** A label (or a label group) that belongs to one team rather than the whole workspace. */
export function isTeamScopedLabel(option: Pick<PropertyOption, 'scope'>) {
  return Boolean(option.scope && option.scope !== 'Workspace')
}

/**
 * Splits label options into the labels picker's sections. Labels with a group collapse into one row per group (kept in
 * the order its first label appears); a group with a selected label moves to the selected rows and names that label.
 *
 * With a search, `matches` tells which labels match (their own name or their group's) and `groupMatches` which group
 * names do: a group row stays only when its name matches, and from three characters the matching labels of groups list
 * on their own as "Group › Label".
 */
export function labelMenuModel(options: PropertyOption[], selectedIds: ReadonlySet<string>, search?: { query: string; matches: (option: PropertyOption) => boolean; groupMatches: (name: string) => boolean }): LabelMenuModel {
  const query = search?.query.trim() ?? ''
  const groups = new Map<string, LabelMenuGroup>()
  const order: LabelMenuEntry[] = []
  const paths: LabelMenuEntry[] = []
  for (const option of options) {
    if (!option.groupId || !option.groupLabel) {
      if (!query || search!.matches(option)) order.push({ type: 'label', option })
      continue
    }
    let group = groups.get(option.groupId)
    if (!group) {
      group = { id: option.groupId, label: option.groupLabel, color: option.groupColor, options: [], checked: false, teamScoped: isTeamScopedLabel(option) }
      groups.set(option.groupId, group)
      if (!query || search!.groupMatches(option.groupLabel)) order.push({ type: 'group', group })
    }
    group.options.push(option)
    if (selectedIds.has(option.id)) {
      group.checked = true
      group.detail = group.detail ? `${group.detail}, ${option.label}` : option.label
    }
    if (labelSearchWidth(query) >= LABEL_GROUP_SEARCH_MIN && search!.matches(option)) paths.push({ type: 'label', option, groupName: 'path' })
  }
  const checked = (entry: LabelMenuEntry) => entry.type === 'group' ? entry.group.checked : selectedIds.has(entry.option.id)
  const teamScoped = (entry: LabelMenuEntry) => entry.type === 'group' ? entry.group.teamScoped : isTeamScopedLabel(entry.option)
  const sorted = (entries: LabelMenuEntry[]) => [...entries.filter(entry => entry.type === 'group'), ...entries.filter(entry => entry.type === 'label' && entry.groupName), ...entries.filter(entry => entry.type === 'label' && !entry.groupName)]
  const all = [...order, ...paths]
  const rest = all.filter(entry => !checked(entry))
  return {
    selected: sorted(all.filter(checked)),
    team: sorted(rest.filter(teamScoped)),
    workspace: sorted(rest.filter(entry => !teamScoped(entry))),
  }
}
