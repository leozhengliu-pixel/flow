export function groupOptionSections<T extends { id: string; groupId?: string; groupLabel?: string }>(options: T[]) {
  const sections: Array<{ id: string; label?: string; options: T[] }> = []
  const indexes = new Map<string, number>()
  for (const option of options) {
    const id = option.groupId || option.groupLabel || 'ungrouped'
    let index = indexes.get(id)
    if (index === undefined) {
      index = sections.length
      indexes.set(id, index)
      sections.push({ id, label: id === 'ungrouped' ? undefined : option.groupLabel, options: [] })
    }
    sections[index].options.push(option)
  }
  return sections
}
