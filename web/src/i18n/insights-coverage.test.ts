import { describe, expect, it } from 'vitest'
import { translateToChinese } from './translate'

describe('insights panel and fullscreen strings', () => {
  it('translates every Insights control, menu item, state and sentence template', () => {
    const strings = [
      'Insights', 'Insight chart', 'Insights table', 'Insight settings', 'Selected issues', 'Close panel', 'Close fullscreen', 'Expand to fullscreen',
      'Insights display options', 'Close view insights', 'View insights', 'Open menu', 'Copy link', 'Copy insights as Markdown', 'Export insights as CSV…',
      'Insights documentation', 'Refresh', 'Documentation', 'Dismiss insights introduction', 'Set default for everyone', 'Save current insight', 'Saving…',
      'Insight saved as the default for everyone', 'Could not save the insight default', 'Insights copied as Markdown', 'View link copied',
      'Measure', 'Slice', 'Segment', 'Aggregations', 'Percentiles', 'Filter…', 'No results', 'Show archived issues', 'Hide', 'Y-axis scale', 'Log scale',
      'Linear scale', 'Colors', 'Status colors', 'Auto-color', 'Add filter', 'Could not load insights', 'Retry', 'Loading…', 'No matching issues',
      'No data for this insight', 'Across all statuses', 'Across all groups', 'issues hidden by display options', 'Show hidden issues',
      'issue', 'issues', 'Open insights', 'Close insights', 'New view', 'Active', 'Backlog', 'All issues', 'My issues',
      'Issue count', 'Cycle time', 'Lead time', 'Issue age', 'Time in status', 'No value', 'Status', 'Status type', 'Assignee', 'Agent session', 'Creator',
      'Priority', 'Label', 'Label group', 'Template', 'External source', 'Project', 'Initiative', 'Project label', 'Project label group', 'Cycle',
      'Added to cycle', 'Created date', 'Completed date', 'Canceled date', 'Started date', 'Due date', 'Burn-up',
      'No priority', 'Urgent', 'High', 'Medium', 'Low',
      'No Value', 'Reset', 'Save', 'None', 'Save insight', 'Publishing the configuration will make it the default for this view for everyone in the workspace.',
      'Use log scale', 'Number of individual issues', 'Time from started to completed', 'Time from created to completed', 'Time from created to now (not completed)',
      'Time spent in status', 'Triage', 'Unstarted', 'Started', 'Completed', 'Canceled', 'Duplicate', 'Showing all items', 'results',
      'Unassigned', 'No Agent', 'External', 'No Initiative', 'No Project', 'No Cycle', 'No Label', 'No label', 'No Project Label', 'No Priority', 'No Template',
      'No Source', 'No session', 'Across all status types', 'Across all assignees', 'Across all agents', 'Across all creators', 'Across all labels',
      'Across all label groups', 'Across all cycles', 'Across all projects', 'Across all priorities', 'Across all initiatives', 'Across all templates',
      'Across all external sources', 'Across all agent sessions', 'Across all project labels', 'Across all project label groups', 'Across all dates',
    ]
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
    expect(translateToChinese('Agent')).toBe('Agent')
    expect(translateToChinese('No {value}').replace('{value}', translateToChinese('Priority'))).toBe('无优先级')
    expect(translateToChinese('in {value}')).toContain('{value}')
    expect(translateToChinese('without {value}')).toContain('{value}')
    expect(translateToChinese('in {value} priority')).toContain('{value}')
    expect(translateToChinese('with {value}')).toContain('{value}')
    expect(translateToChinese('{value}% of issues are at or below this point')).toContain('{value}')
    expect(translateToChinese('{count} statuses')).toContain('{count}')
    expect(translateToChinese('{count} status types')).toContain('{count}')
  })
})
