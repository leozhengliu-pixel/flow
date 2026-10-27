import type { BootstrapData } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { emptyDimensionLabel, type SavedViewInsightsConfig, type SavedViewInsightDimension, type SavedViewInsightMeasure } from './insight-config'
import { aggregateInsightValues, insightAggregations, type InsightSample, type InsightAggregation } from './insight-interaction'
import { statusGroupIndex, type StatusGroupIndex } from '@/lib/status-groups'

/** `stateIds`: on Status and Status type values, the underlying status IDs (best representative first). */
export type InsightValue = { id: string; label: string; color?: string; stateIds?: string[] }
export type InsightRow = InsightValue & { total: number; segments: Record<string, number>; values: number[]; aggregations: Partial<Record<InsightAggregation, number>> }

export type InsightData = { rows: InsightRow[]; segments: Array<InsightValue & { count: number }>; samples: InsightSample<MyIssuesRowData>[] }

export function buildInsightData(rows: MyIssuesRowData[], config: SavedViewInsightsConfig, data: BootstrapData): InsightData {
  const rowMap = new Map<string, InsightRow>()
  const segmentMap = new Map<string, InsightValue & { count: number }>()
  const samples: InsightSample<MyIssuesRowData>[] = []
  const now = Date.now()
  const hideSlice = Boolean(config.hideEmptySlice && emptyDimensionLabel(config.slice))
  const hideSegment = Boolean(config.hideEmptySegment && emptyDimensionLabel(config.segment))
  // Status values merge across team workflows (see statusGroupId); `statuses` keeps each seen status to name and colour its group.
  const groups = statusGroupIndex(data)
  const statuses = new Map<string, MyIssuesRowData['state']>()
  const timeInStatusGroups = new Set(config.timeInStatusIds.flatMap(id => groups.groupOf(id) ?? []))
  for (const row of rows) {
    const metric = metricValue(row, config, now, groups, timeInStatusGroups)
    if (metric == null || !Number.isFinite(metric)) continue
    statuses.set(row.state.id, row.state)
    let slices = dimensionValues(row, config.slice, data, groups)
    let segments = config.segment === 'none' ? [{ id: 'all', label: 'No value' }] : dimensionValues(row, config.segment, data, groups)
    // "Hide <No value>": the empty value leaves the chart, the table and the counts.
    if (hideSegment) segments = segments.filter(value => !isEmptyInsightValue(config.segment as SavedViewInsightDimension, value.id))
    if (hideSlice) slices = slices.filter(value => !isEmptyInsightValue(config.slice, value.id))
    // "Hide Unknown customer": requests whose customer no longer exists leave a Customer slice/segment.
    if (config.hideUnknownCustomer) {
      if (config.slice === 'customer') slices = slices.filter(value => value.id !== UNKNOWN_CUSTOMER_ID)
      if (config.segment === 'customer') segments = segments.filter(value => value.id !== UNKNOWN_CUSTOMER_ID)
    }
    if (!slices.length || !segments.length) continue
    samples.push({ item: row, value: metric, slices: slices.map(value => value.id), segments: config.segment === 'none' ? [] : segments.map(value => value.id) })
    for (const segmentValue of segments) {
      const current = segmentMap.get(segmentValue.id)
      segmentMap.set(segmentValue.id, { ...segmentValue, stateIds: mergeStateIds(current?.stateIds, segmentValue.stateIds), count: (current?.count ?? 0) + 1 })
    }
    for (const sliceValue of slices) {
      const current = rowMap.get(sliceValue.id) ?? { ...sliceValue, stateIds: undefined, total: 0, segments: Object.create(null) as Record<string, number>, values: [], aggregations: {} }
      current.stateIds = mergeStateIds(current.stateIds, sliceValue.stateIds)
      current.total += metric
      current.values.push(metric)
      for (const segmentValue of segments) current.segments[segmentValue.id] = (current.segments[segmentValue.id] ?? 0) + metric
      rowMap.set(sliceValue.id, current)
    }
  }
  for (const row of rowMap.values()) {
    if (config.measure !== 'issueCount') {
      for (const kind of insightAggregations) row.aggregations[kind] = aggregateInsightValues(row.values, kind)
      row.total = row.aggregations.average ?? 0
    }
  }
  // A merged status reads as its representative: the shallowest team's status, whatever order the issues came in.
  const represent = (value: InsightValue, dimension: SavedViewInsightDimension | 'none') => {
    if (!value.stateIds || (dimension !== 'status' && dimension !== 'statusType')) return
    value.stateIds.sort(groups.compare)
    const state = statuses.get(value.stateIds[0])
    if (!state) return
    if (dimension === 'status') value.label = state.name
    value.color = state.color
  }
  for (const row of rowMap.values()) represent(row, config.slice)
  for (const segment of segmentMap.values()) represent(segment, config.segment)
  const statusOrder = (value: InsightValue) => { const state = value.stateIds && statuses.get(value.stateIds[0]); return state ? groups.order(state) : [999, 0] }
  const byStatus = (left: InsightValue, right: InsightValue) => { const a = statusOrder(left), b = statusOrder(right); return a[0] - b[0] || a[1] - b[1] || left.label.localeCompare(right.label) }
  return {
    rows: [...rowMap.values()].sort((left, right) => config.slice === 'status' ? byStatus(left, right) : config.slice === 'priority' ? Number(left.id) - Number(right.id) : config.slice.endsWith('Date') || config.slice === 'burnUp' ? left.id.localeCompare(right.id) : config.slice === 'customer' ? Number(left.id === 'none') - Number(right.id === 'none') || left.label.localeCompare(right.label) : right.total - left.total || left.label.localeCompare(right.label)),
    // Priority and status segments keep their natural order (the reference app: No priority, Urgent … Low); others rank by size.
    segments: [...segmentMap.values()].sort((left, right) => config.segment === 'priority' ? Number(left.id) - Number(right.id) : config.segment === 'status' ? byStatus(left, right) : right.count - left.count || left.label.localeCompare(right.label)),
    samples,
  }
}

function mergeStateIds(current: string[] | undefined, next: string[] | undefined) {
  if (!next) return current
  if (!current) return [...next]
  for (const id of next) if (!current.includes(id)) current.push(id)
  return current
}

function dimensionValues(row: MyIssuesRowData, dimension: SavedViewInsightDimension, data: BootstrapData, groups: StatusGroupIndex): InsightValue[] {
  if (dimension === 'label') return row.labels?.length ? row.labels.map(label => ({ id: label.id, label: label.name, color: label.color })) : [noValue('label')]
  if (dimension.startsWith('labelGroup:')) {
    const groupId = dimension.slice('labelGroup:'.length)
    const values = row.labels?.filter(label => label.groupId === groupId) ?? []
    return values.length ? values.map(label => ({ id: label.id, label: label.name, color: label.color })) : [noValue('label')]
  }
  if (dimension === 'customer') {
    // Multi-membership like labels: an issue counts once for every requesting customer.
    const values: InsightValue[] = (row.customers ?? []).map(customer => ({ id: customer.id, label: customer.name }))
    if (row.hasUnknownCustomer) values.push({ id: UNKNOWN_CUSTOMER_ID, label: 'Unknown customer' })
    return values.length ? values : [noValue('customer')]
  }
  if (dimension === 'status') return [{ id: groups.groupOf(row.state) ?? row.state.id, label: row.state.name, color: row.state.color, stateIds: [row.state.id] }]
  if (dimension === 'statusType') return [{ id: row.state.type, label: titleCase(row.state.type), color: row.state.color, stateIds: [row.state.id] }]
  if (dimension === 'priority') return [{ id: String(row.priority), label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][row.priority], color: ['var(--status-neutral)', 'var(--priority-urgent)', 'var(--priority-high)', 'var(--priority-medium)', 'var(--priority-low)'][row.priority] }]
  if (dimension === 'project') return [{ id: row.project?.id ?? 'none', label: row.project?.name ?? 'No project', color: row.project?.color }]
  if (dimension === 'assignee') return [{ id: row.assignee?.id ?? 'none', label: row.assignee?.name ?? 'No assignee', color: row.assignee?.color }]
  if (dimension === 'agent') return [{ id: row.delegate?.id ?? 'none', label: row.delegate?.name ?? 'No agent', color: row.delegate?.color }]
  if (dimension === 'agentSession') return [{ id: row.agentSessionId ?? 'none', label: row.agentSessionId ?? 'No agent session' }]
  if (dimension === 'creator') return [{ id: row.creatorId ?? 'none', label: row.creatorName ?? 'No creator' }]
  if (dimension === 'template') { const template = data.issueTemplates.find(item => item.id === row.templateId); return [{ id: template?.id ?? 'none', label: template?.name ?? 'No template' }] }
  if (dimension === 'externalSource') return [{ id: row.externalSource ?? 'none', label: row.externalSource ?? 'No external source' }]
  if (dimension === 'initiative') { const values = data.initiatives.filter(item => row.initiativeIds?.includes(item.id)); return values.length ? values.map(item => ({ id: item.id, label: item.name, color: item.color })) : [noValue('initiative')] }
  if (dimension === 'projectLabel') { const values = data.labels.filter(item => row.projectLabelIds?.includes(item.id)); return values.length ? values.map(item => ({ id: item.id, label: item.name, color: item.color })) : [noValue('project label')] }
  if (dimension.startsWith('projectLabelGroup:')) { const groupId = dimension.slice('projectLabelGroup:'.length); const values = data.labels.filter(item => item.groupId === groupId && row.projectLabelIds?.includes(item.id)); return values.length ? values.map(item => ({ id: item.id, label: item.name, color: item.color })) : [noValue('project label')] }
  if (dimension === 'cycle') { const cycle = data.cycles.find(item => item.id === row.cycleId); return [{ id: cycle?.id ?? 'none', label: cycle?.name ?? 'No cycle' }] }
  if (dimension === 'addedToCycle') return [{ id: row.addedToCycle ?? 'none', label: row.addedToCycle ? ({ planned: 'Planned', during: 'During cycle', after: 'After cycle' }[row.addedToCycle] ?? row.addedToCycle) : 'No value' }]
  if (dimension === 'createdDate' || dimension === 'burnUp') return [dateValue(row.createdAt, 'created date')]
  if (dimension === 'completedDate') return [dateValue(row.completedAt, 'completed date')]
  if (dimension === 'canceledDate') return [dateValue(row.canceledAt, 'canceled date')]
  if (dimension === 'startedDate') return [dateValue(row.startedAt, 'started date')]
  if (dimension === 'dueDate') return [dateValue(row.dueDate, 'due date')]
  return [noValue('value')]
}

function metricValue(row: MyIssuesRowData, config: SavedViewInsightsConfig, now: number, groups: StatusGroupIndex, selectedGroups: Set<string>) {
  if (config.measure === 'issueCount') return 1
  const created = dateMs(row.createdAt)
  if (config.measure === 'cycleTime') return elapsed(row.startedAt, row.completedAt)
  if (config.measure === 'leadTime') return elapsed(row.createdAt, row.completedAt)
  if (config.measure === 'issueAge') return row.completedAt || row.canceledAt ? null : Math.max(0, now - created)
  const selected = config.timeInStatusIds
  // A stored status ID stands for its merged group, so the same status in a sub-team's own workflow counts too.
  const intervals = row.statusIntervals?.filter(interval => !selected.length || selected.includes(interval.stateId) || Boolean(interval.stateType && selected.includes(`type:${interval.stateType}`)) || selectedGroups.has(groups.groupOf(interval.stateId) ?? '')) ?? []
  if (!intervals.length) return null
  return intervals.reduce((total, interval) => total + Math.max(0, (interval.exitedAt ? dateMs(interval.exitedAt) : now) - dateMs(interval.enteredAt)), 0)
}

export function insightColor(config: SavedViewInsightsConfig, segmentColor: string | undefined, sliceColor: string | undefined, index: number) { return config.colors === 'status' ? segmentColor || sliceColor || 'var(--data-vis-neutral)' : ['var(--data-vis-neutral)', 'var(--data-vis-1)', 'var(--data-vis-2)', 'var(--data-vis-3)', 'var(--data-vis-4)', 'var(--data-vis-5)'][index % 6] }
/** The reference app's `customer_id_unknown` bucket: requests whose customer was deleted. */
export const UNKNOWN_CUSTOMER_ID = 'customer_id_unknown'
function noValue(kind: string): InsightValue { return { id: 'none', label: `No ${kind}` } }
/** Priority's empty value is "0" (No priority); every other dimension uses "none". */
export function isEmptyInsightValue(dimension: SavedViewInsightDimension, id: string) { return dimension === 'priority' ? id === '0' : id === 'none' }
function dateValue(value: string | undefined, kind: string): InsightValue { return value ? { id: value.slice(0, 10), label: value.slice(0, 10) } : noValue(kind) }
function dateMs(value: string | undefined) { return value === undefined ? Date.now() : Date.parse(value) }
function elapsed(start: string | undefined, end: string | undefined) { if (!start || !end) return null; const duration = dateMs(end) - dateMs(start); return Number.isFinite(duration) ? Math.max(0, duration) : null }
export function titleCase(value: string) { return value ? value[0].toUpperCase() + value.slice(1).replaceAll(/([A-Z])/g, ' $1') : value }
export function formatMetric(value: number, measure: SavedViewInsightMeasure) {
  if (measure === 'issueCount') return String(Math.round(value))
  const minutes = Math.round(value / 60_000)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 6) / 10
  if (hours < 24) return `${hours}h`
  const days = hours / 24
  if (days < 30.45) return `${Math.round(days * 10) / 10}d`
  if (days < 365.4) return `${Math.round(days / 30.45)}mo`
  return `${Math.round(days / 365.4)}y`
}
