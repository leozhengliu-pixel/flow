import { describe, expect, it } from 'vitest'
import { translateToChinese } from './translate'

describe('saved-view editing and advanced filter strings', () => {
  it('translates every operator, band and editor label', () => {
    const strings = ['include', 'do not include', 'include all of', 'include any of', 'exclude if any of', 'exclude if all', 'is any of', 'before', 'after', 'more than', 'less than',
      'Open advanced filter', 'Remove advanced filter', 'Delete group', 'Add filter group', 'Toggle filter operator, currently and', 'Toggle filter operator, currently or',
      'Clear all filters', 'Save to this view', 'Create new view…', 'hidden by filters', 'Clear Filters', 'No matching issues', 'Create view', 'Choose icon', 'Save view options',
      'Show display options', 'Add Filter', 'Add another filter', 'Overdue', '1 week from now', 'Custom date or timeframe…', 'Time in current status', 'Has auto-closed date']
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
    expect(translateToChinese('2 labels')).toBe('2 个标签')
    expect(translateToChinese('3 statuses')).toBe('3 个状态')
    expect(translateToChinese('and {count} more conditions').replace('{count}', '2')).toBe('以及另外 2 个条件')
  })
})
