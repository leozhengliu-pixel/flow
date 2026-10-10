import { describe, expect, it } from 'vitest'
import { translateToChinese } from './translate'

describe('agent mention strings', () => {
  it('translates the chip, hover card and reference card copy', () => {
    const strings = [
      'Unassigned', 'Cycle', 'Cycle {number}', '{percent}% of {count} issues', 'Last edited {date} by {name}', 'Last edited {date}',
      'Last updated {date}', 'In review', 'Upcoming', 'Current', 'Completed', 'Open', 'Approved', 'Merged', 'Closed', 'Active',
      'Proposed', 'Planned', 'Canceled', 'Update', 'Comment', 'Milestone', 'On track', 'At risk', 'Off track', 'No update',
      'No projects', 'No documents', 'No priority', 'Referenced issues',
    ]
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
    for (const template of ['Cycle {number}', '{percent}% of {count} issues', 'Last edited {date} by {name}', 'Last edited {date}', 'Last updated {date}']) {
      const translated = translateToChinese(template)
      for (const token of template.match(/\{\w+\}/g) ?? []) expect(translated, template).toContain(token)
    }
  })
})
