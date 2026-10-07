import { insightAggregations, type InsightAggregation } from './insight-interaction'

export type SavedViewInsightMeasure = 'issueCount' | 'cycleTime' | 'leadTime' | 'issueAge' | 'timeInStatus'
export type SavedViewInsightDimension =
  | 'status' | 'statusType' | 'assignee' | 'agent' | 'agentSession' | 'creator' | 'priority' | 'label' | `labelGroup:${string}` | 'template' | 'externalSource'
  | 'project' | 'initiative' | 'projectLabel' | `projectLabelGroup:${string}` | 'cycle' | 'addedToCycle'
  | 'createdDate' | 'completedDate' | 'canceledDate' | 'startedDate' | 'dueDate' | 'burnUp'
export interface SavedViewInsightsConfig {
  measure: SavedViewInsightMeasure
  timeInStatusIds: string[]
  slice: SavedViewInsightDimension
  segment: SavedViewInsightDimension | 'none'
  showArchived: boolean
  /** Linear's "Hide <No value>": drops the segment's (or, unsegmented, the slice's) empty value. */
  hideEmptySegment?: boolean
  colors: 'status' | 'auto'
  aggregation?: InsightAggregation
  aggregations?: InsightAggregation[]
  latencyScale?: 'linear' | 'log'
}

/** Linear's default for a view nobody has configured: Issue count by Status, no segment. */
export const DEFAULT_INSIGHTS: SavedViewInsightsConfig = {
  measure: 'issueCount', timeInStatusIds: [], slice: 'status', segment: 'none', showArchived: false, colors: 'status',
}

const MEASURES: SavedViewInsightMeasure[] = ['issueCount', 'cycleTime', 'leadTime', 'issueAge', 'timeInStatus']
const DIMENSIONS = ['status', 'statusType', 'assignee', 'agent', 'agentSession', 'creator', 'priority', 'label', 'template', 'externalSource', 'project', 'initiative', 'projectLabel', 'cycle', 'addedToCycle', 'createdDate', 'completedDate', 'canceledDate', 'startedDate', 'dueDate', 'burnUp']

export function isInsightMeasure(value: unknown): value is SavedViewInsightMeasure { return typeof value === 'string' && MEASURES.includes(value as SavedViewInsightMeasure) }
export function isInsightDimension(value: unknown): value is SavedViewInsightDimension { return typeof value === 'string' && (DIMENSIONS.includes(value) || /^(labelGroup|projectLabelGroup):.+/.test(value)) }

/** Reads a stored configuration (saved view, team default or personal override), ignoring anything malformed. */
export function parseInsightsConfig(value: Record<string, unknown> | undefined | null, base: SavedViewInsightsConfig = DEFAULT_INSIGHTS): SavedViewInsightsConfig {
  const source = value && typeof value === 'object' ? value : {}
  return {
    ...base,
    ...(isInsightMeasure(source.measure) ? { measure: source.measure } : {}),
    ...(Array.isArray(source.timeInStatusIds) && source.timeInStatusIds.every(item => typeof item === 'string') ? { timeInStatusIds: source.timeInStatusIds as string[] } : {}),
    ...(isInsightDimension(source.slice) ? { slice: source.slice } : {}),
    ...(source.segment === 'none' || isInsightDimension(source.segment) ? { segment: source.segment as SavedViewInsightsConfig['segment'] } : {}),
    ...(typeof source.showArchived === 'boolean' ? { showArchived: source.showArchived } : {}),
    ...(typeof source.hideEmptySegment === 'boolean' ? { hideEmptySegment: source.hideEmptySegment } : {}),
    ...(source.colors === 'status' || source.colors === 'auto' ? { colors: source.colors } : {}),
    ...(insightAggregations.includes(source.aggregation as InsightAggregation) ? { aggregation: source.aggregation as InsightAggregation } : {}),
    ...(Array.isArray(source.aggregations) && source.aggregations.length && source.aggregations.every(item => insightAggregations.includes(item as InsightAggregation)) ? { aggregations: [...new Set(source.aggregations)] as InsightAggregation[] } : {}),
    ...(source.latencyScale === 'linear' || source.latencyScale === 'log' ? { latencyScale: source.latencyScale } : {}),
  }
}

/** Stable comparison key: key order and absent-vs-default flags never make a config "dirty". */
export function insightsConfigKey(config: SavedViewInsightsConfig) {
  return JSON.stringify([config.measure, config.timeInStatusIds, config.slice, config.segment, config.showArchived, Boolean(config.hideEmptySegment), config.colors, config.aggregation ?? null, config.aggregations ?? null, config.latencyScale ?? null])
}

/** The dimension whose empty value "Hide" removes, or undefined when that dimension always has a value. */
export function hideableDimension(config: SavedViewInsightsConfig): SavedViewInsightDimension | undefined {
  const dimension = config.segment !== 'none' ? config.segment : config.slice
  return dimension === 'status' || dimension === 'statusType' || dimension === 'burnUp' || dimension === 'createdDate' ? undefined : dimension
}
