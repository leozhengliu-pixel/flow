import type { WorkflowStateType } from '@/types/flow'
import { lightSurfaceStatusColor, normalizeHexColor } from './project-status-color'

/** Default colour per workflow status type for a newly created status (reference app palette). */
export const ISSUE_STATUS_COLORS: Record<WorkflowStateType, string> = {
  backlog: '#bec2c8',
  unstarted: '#e2e2e2',
  started: 'lch(80% 90 85)',
  completed: 'lch(48% 59.31 288.43)',
  canceled: '#95a2b3',
}

/** The reference app draws the old default workflow hexes with its LCH palette. */
const WORKFLOW_STROKE_OVERRIDES: Record<string, string> = {
  '#f2c94c': 'lch(80% 90 85)',
  '#6b6f76': '#bec2c8',
  '#5e6ad2': 'lch(48% 59.31 288.43)',
}

export function workflowStatusStrokeColor(color: string, teamId?: string, theme: 'light' | 'dark' = 'dark'): string {
  // Team colors are saved choices, even when they match a legacy global default.
  const hex = normalizeHexColor(color)
  const stroke = !teamId && hex ? WORKFLOW_STROKE_OVERRIDES[hex] ?? color : color
  return theme === 'light' ? lightSurfaceStatusColor(stroke) : stroke
}
