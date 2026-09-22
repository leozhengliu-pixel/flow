import { describe, expect, it } from 'vitest'
import {
  flattenGroupedIssueFilterBlocks,
  getGroupedIssueFilterBlocks,
} from './grouped-issue-filter-blocks'

describe('GroupedIssueFilterBlocks (LS-0286)', () => {
  it('returns grouped pack including estimate', () => {
    const grouped = getGroupedIssueFilterBlocks()
    expect(grouped.some(entry => 'name' in entry && entry.name === 'Dates')).toBe(true)
    const flat = flattenGroupedIssueFilterBlocks(grouped)
    expect(flat.some(block => block.id === 'estimate')).toBe(true)
    expect(flat.some(block => block.id === 'assignee')).toBe(true)
  })
})
