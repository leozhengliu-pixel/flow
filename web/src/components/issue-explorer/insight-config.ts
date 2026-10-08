import { insightAggregations, type InsightAggregation } from './insight-interaction'

export type SavedViewInsightMeasure = 'issueCount' | 'cycleTime' | 'leadTime' | 'issueAge' | 'timeInStatus'
export type SavedViewInsightDimension =
  | 'status' | 'statusType' | 'assignee' | 'agent' | 'agentSession' | 'creator' | 'priority' | 'label' | `labelGroup:${string}` | 'customer' | 'template' | 'externalSource'
  | 'project' | 'initiative' | 'projectLabel' | `projectLabelGroup:${string}` | 'cycle' | 'addedToCycle'
  | 'createdDate' | 'completedDate' | 'canceledDate' | 'startedDate' | 'dueDate' | 'burnUp'
export interface SavedViewInsightsConfig {
  measure: SavedViewInsightMeasure
  timeInStatusIds: string[]
  slice: SavedViewInsightDimension
  segment: SavedViewInsightDimension | 'none'
  showArchived: boolean
  /** Linear's "Hide <No value>" for the segment: drops the segment's empty value. */
  hideEmptySegment?: boolean
  /** Linear's "Hide <No value>" for the slice (`hideEmptyDimension`): drops the slice's empty value. */
  hideEmptySlice?: boolean
  /** Linear's "Hide Unknown customer" (`hideUnknownCustomer`) when the slice or segment is Customer. */
  hideUnknownCustomer?: boolean
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
const DIMENSIONS = ['status', 'statusType', 'assignee', 'agent', 'agentSession', 'creator', 'priority', 'label', 'customer', 'template', 'externalSource', 'project', 'initiative', 'projectLabel', 'cycle', 'addedToCycle', 'createdDate', 'completedDate', 'canceledDate', 'startedDate', 'dueDate', 'burnUp']

export function isInsightMeasure(value: unknown): value is SavedViewInsightMeasure { return typeof value === 'string' && MEASURES.includes(value as SavedViewInsightMeasure) }
export function isInsightDimension(value: unknown): value is SavedViewInsightDimension { return typeof value === 'string' && (DIMENSIONS.includes(value) || /^(labelGroup|projectLabelGroup):.+/.test(value)) }

/** Reads a stored configuration (saved view, team default or personal override), ignoring anything malformed. */
export function parseInsightsConfig(value: Record<string, unknown> | undefined | null, base: SavedViewInsightsConfig = DEFAULT_INSIGHTS): SavedViewInsightsConfig {
  const source = value && typeof value === 'object' ? value : {}
  const parsed: SavedViewInsightsConfig = {
    ...base,
    ...(isInsightMeasure(source.measure) ? { measure: source.measure } : {}),
    ...(Array.isArray(source.timeInStatusIds) && source.timeInStatusIds.every(item => typeof item === 'string') ? { timeInStatusIds: source.timeInStatusIds as string[] } : {}),
    ...(isInsightDimension(source.slice) ? { slice: source.slice } : {}),
    ...(source.segment === 'none' || isInsightDimension(source.segment) ? { segment: source.segment as SavedViewInsightsConfig['segment'] } : {}),
    ...(typeof source.showArchived === 'boolean' ? { showArchived: source.showArchived } : {}),
    ...(typeof source.hideEmptySegment === 'boolean' ? { hideEmptySegment: source.hideEmptySegment } : {}),
    ...(typeof source.hideEmptySlice === 'boolean' ? { hideEmptySlice: source.hideEmptySlice } : {}),
    ...(typeof source.hideUnknownCustomer === 'boolean' ? { hideUnknownCustomer: source.hideUnknownCustomer } : {}),
    ...(source.colors === 'status' || source.colors === 'auto' ? { colors: source.colors } : {}),
    ...(insightAggregations.includes(source.aggregation as InsightAggregation) ? { aggregation: source.aggregation as InsightAggregation } : {}),
    // Linear lets every percentile be switched off ("None").
    ...(Array.isArray(source.aggregations) && source.aggregations.every(item => insightAggregations.includes(item as InsightAggregation)) ? { aggregations: [...new Set(source.aggregations)] as InsightAggregation[] } : {}),
    ...(source.latencyScale === 'linear' || source.latencyScale === 'log' ? { latencyScale: source.latencyScale } : {}),
  }
  // Before the slice got its own toggle, "Hide" on an unsegmented insight hid the slice's empty value.
  if (parsed.segment === 'none' && parsed.hideEmptySegment && typeof source.hideEmptySlice !== 'boolean') return { ...parsed, hideEmptySegment: false, hideEmptySlice: true }
  return parsed
}

/** Stable comparison key: key order and absent-vs-default flags never make a config "dirty". */
export function insightsConfigKey(config: SavedViewInsightsConfig) {
  return JSON.stringify([config.measure, config.timeInStatusIds, config.slice, config.segment, config.showArchived, Boolean(config.hideEmptySegment), Boolean(config.hideEmptySlice), Boolean(config.hideUnknownCustomer), config.colors, config.aggregation ?? null, config.aggregations ?? null, config.latencyScale ?? null])
}

const EMPTY_LABELS: Partial<Record<SavedViewInsightDimension, string>> = {
  assignee: 'Unassigned', agent: 'No Agent', creator: 'External', initiative: 'No Initiative', project: 'No Project', cycle: 'No Cycle',
  label: 'No Label', customer: 'No customer', projectLabel: 'No Project Label', priority: 'No Priority', template: 'No Template', externalSource: 'No Source', agentSession: 'No session',
}

/** Linear's `getEmptyDimensionLabel`: the chip in "Hide <value>", or undefined when the dimension always has a value (statuses, dates). */
export function emptyDimensionLabel(dimension: SavedViewInsightDimension | 'none'): string | undefined {
  if (dimension === 'none') return undefined
  if (dimension.startsWith('labelGroup:') || dimension.startsWith('projectLabelGroup:')) return 'No label'
  return EMPTY_LABELS[dimension]
}

const AGGREGATION_ORDER: InsightAggregation[] = ['p25', 'median', 'p75', 'p95', 'average', 'min', 'max']
/** The percentiles a duration insight shows, in Linear's order (P50, P75, P95); an empty list is Linear's "None". */
export function selectedAggregations(config: Pick<SavedViewInsightsConfig, 'aggregation' | 'aggregations'>): InsightAggregation[] {
  const values = config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as InsightAggregation[])
  return [...values].sort((left, right) => AGGREGATION_ORDER.indexOf(left) - AGGREGATION_ORDER.indexOf(right))
}
/** Linear's `formatPercentile`: the median is "P50". */
export function percentileLabel(aggregation: InsightAggregation) { return ({ p25: 'P25', median: 'P50', p75: 'P75', p95: 'P95', average: 'Average', min: 'Min', max: 'Max' } as const)[aggregation] }
/** Linear's `formatPercentileTooltip` with the default "at or below" operator. */
export function percentileShare(aggregation: InsightAggregation) { return ({ p25: 25, median: 50, p75: 75, p95: 95 } as Partial<Record<InsightAggregation, number>>)[aggregation] }

const ACROSS_ALL: Record<string, string> = {
  status: 'Across all statuses', statusType: 'Across all status types', assignee: 'Across all assignees', agent: 'Across all agents', creator: 'Across all creators',
  label: 'Across all labels', labelGroup: 'Across all label groups', cycle: 'Across all cycles', project: 'Across all projects', priority: 'Across all priorities',
  initiative: 'Across all initiatives', customer: 'Across all customers', template: 'Across all templates', externalSource: 'Across all external sources', agentSession: 'Across all agent sessions',
  projectLabel: 'Across all project labels', projectLabelGroup: 'Across all project label groups',
}
/** Linear's `getAllDimensionLabel`: the duration table's total row, e.g. "Across all assignees". */
export function acrossAllLabel(dimension: SavedViewInsightDimension) {
  const base = dimension.replace(/:.*$/, '')
  return ACROSS_ALL[base] ?? 'Across all dates'
}
