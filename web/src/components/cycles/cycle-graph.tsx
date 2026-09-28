/**
 * LS-0175 CycleDetailComponents host — delegates to honest BurnUpGraph (LS-0102).
 */
import type { Cycle, Issue } from '@/types/flow'
import { BurnUpGraph } from '@/components/insights/burn-up-graph'
import type { BurnUpMeasure } from '@/components/insights/burn-up-model'

export function CycleGraph({
  cycle,
  issues,
  compact = false,
  measure = 'issue_count',
  unestimatedValue = 0,
}: {
  cycle: Cycle
  issues: Issue[]
  compact?: boolean
  measure?: BurnUpMeasure
  unestimatedValue?: number
}) {
  return <BurnUpGraph cycle={cycle} issues={issues} compact={compact} measure={measure} unestimatedValue={unestimatedValue} />
}
