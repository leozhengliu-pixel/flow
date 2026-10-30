import { describe, expect, it } from 'vitest'
import { ISSUE_STATUS_COLORS, workflowStatusStrokeColor } from './status-default-colors'

describe('ISSUE_STATUS_COLORS', () => {
  it('uses the reference app default workflow palette', () => {
    expect(ISSUE_STATUS_COLORS).toEqual({
      backlog: '#bec2c8',
      unstarted: '#e2e2e2',
      started: 'lch(80% 90 85)',
      completed: 'lch(48% 59.31 288.43)',
      canceled: '#95a2b3',
    })
  })
})

describe('workflowStatusStrokeColor', () => {
  it('maps the old default workflow hexes to the reference palette', () => {
    expect(workflowStatusStrokeColor('#F2C94C')).toBe('lch(80% 90 85)')
    expect(workflowStatusStrokeColor('#6B6F76')).toBe('#bec2c8')
    expect(workflowStatusStrokeColor('#5E6AD2')).toBe('lch(48% 59.31 288.43)')
  })

  it('keeps workspace-chosen colours and LCH values', () => {
    expect(workflowStatusStrokeColor('#26b5ce')).toBe('#26b5ce')
    expect(workflowStatusStrokeColor('lch(80% 90 85)')).toBe('lch(80% 90 85)')
  })
})
