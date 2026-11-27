import { describe, expect, it } from 'vitest'
import { translateToChinese } from './translate'

describe('label grouping strings', () => {
  it('translates the grouping, label group and group type copy', () => {
    const strings = [
      'Status type', 'Label', 'Label group', 'Grouping', 'Sub-grouping', 'Columns', 'Rows', 'Show empty groups', 'Show empty columns', 'Show empty rows',
      'Workspace', 'No label', 'No customer', 'No lead team', 'No initiative', 'Lead team', 'Customer', 'Initiative',
      'Triage', 'Backlog', 'Unstarted', 'Started', 'Completed', 'Canceled', 'Planned', 'Paused',
      'Group type', 'Single-select', 'Multi-select',
    ]
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
  })
})
