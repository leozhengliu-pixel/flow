/**
 * LS-0017 ActivityHistoryGroupSummary — collapsed group label helpers.
 * Linear: filter by action ∈ {breached,changed,added}, uniqBy value, sortBy weight,
 * cap at 5 phrased as `action values`, joined with commas.
 */

export type ActivityHistorySummaryAction = 'breached' | 'changed' | 'added'

export type ActivityHistorySummaryItem = {
  action: ActivityHistorySummaryAction
  value: string
  weight?: number
}

const ACTION_ORDER: ActivityHistorySummaryAction[] = ['breached', 'changed', 'added']

/** Format `Show {N} event(s)` with optional `: {phrases}…` detail. */
export function formatActivityHistoryGroupSummary(
  count: number,
  items: ActivityHistorySummaryItem[],
  maxItems = 5,
): string {
  const noun = count === 1 ? 'event' : 'events'
  const head = `Show ${count} ${noun}`
  const detail = summarizeActivityHistoryGroup(items, maxItems)
  return detail ? `${head}: ${detail}…` : head
}

/** Phrase body only (no Show N / ellipsis) — mirrors Linear `n(items)`. */
export function summarizeActivityHistoryGroup(
  items: ActivityHistorySummaryItem[],
  maxItems = 5,
): string | undefined {
  let remaining = maxItems
  const phrases: string[] = []

  for (const action of ACTION_ORDER) {
    const bucket = pickActionItems(items, action).slice(0, remaining)
    if (!bucket.length) continue
    phrases.push(phraseAction(bucket))
    remaining -= bucket.length
    if (remaining <= 0) break
  }

  return phrases.length ? phrases.join(', ') : undefined
}

function pickActionItems(items: ActivityHistorySummaryItem[], action: ActivityHistorySummaryAction) {
  const filtered = items.filter(item => item.action === action)
  const unique = new Map<string, ActivityHistorySummaryItem>()
  for (const item of filtered) {
    if (!unique.has(item.value)) unique.set(item.value, item)
  }
  return [...unique.values()].sort(
    (left, right) => (right.weight ?? 0) - (left.weight ?? 0) || left.value.localeCompare(right.value),
  )
}

function phraseAction(items: ActivityHistorySummaryItem[]) {
  const action = items[0]?.action ?? 'changed'
  return `${action} ${joinValues(items.map(item => item.value))}`
}

function joinValues(values: string[]) {
  if (values.length <= 1) return values[0] ?? ''
  if (values.length === 2) return `${values[0]} and ${values[1]}`
  return `${values.slice(0, -1).join(', ')}, and ${values[values.length - 1]}`
}

/** Map Flow activity categories onto Linear-style summary actions. */
export function summaryItemsFromActivityTexts(
  items: Array<{ category?: string; text?: string; summaryItem?: string }>,
): ActivityHistorySummaryItem[] {
  return items.map((item, index) => {
    const value = (item.summaryItem || item.text || 'change').trim()
    const action = categoryToAction(item.category)
    return { action, value, weight: items.length - index }
  })
}

function categoryToAction(category?: string): ActivityHistorySummaryAction {
  switch (category) {
    case 'assignment':
      return 'added'
    case 'status':
    case 'property':
      return 'changed'
    case 'system':
      return 'breached'
    default:
      return 'changed'
  }
}
