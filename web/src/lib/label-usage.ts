const KEY = 'flow:label-usage'

/** How often the viewer picked each label in a label picker, kept per browser. The picker lists the labels they use under "Frequently used". */
export function readLabelUsage(): Record<string, number> {
  try {
    const value = JSON.parse(localStorage.getItem(KEY) ?? '{}')
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, number> : {}
  } catch { return {} }
}

export function recordLabelUsage(id: string) {
  try {
    const usage = readLabelUsage()
    usage[id] = (usage[id] ?? 0) + 1
    localStorage.setItem(KEY, JSON.stringify(usage))
  } catch { /* Usage history is best-effort. */ }
}
