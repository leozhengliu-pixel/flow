/**
 * Pure layout math for the Projects › Timeline layout (Linear parity):
 * zoom scales, header segments, bar/milestone positions and dependency
 * connectors. Everything works in local calendar days so DST never shifts a
 * bar by an hour-sized sliver.
 */

export type TimelineZoom = 'week' | 'month' | 'quarter' | 'year'

export const TIMELINE_ZOOMS: readonly TimelineZoom[] = ['week', 'month', 'quarter', 'year']
export const DEFAULT_TIMELINE_ZOOM: TimelineZoom = 'quarter'

/** Pixels per calendar day for each zoom level. */
export const TIMELINE_PX_PER_DAY: Record<TimelineZoom, number> = { week: 36, month: 12, quarter: 3.5, year: 1.2 }

/** Minimum number of days the canvas spans so short timelines still fill the viewport. */
const MIN_SPAN_DAYS: Record<TimelineZoom, number> = { week: 56, month: 180, quarter: 540, year: 1460 }

export const TIMELINE_ZOOM_LABELS: Record<TimelineZoom, string> = { week: 'Week', month: 'Month', quarter: 'Quarter', year: 'Year' }

export function isTimelineZoom(value: unknown): value is TimelineZoom {
  return typeof value === 'string' && (TIMELINE_ZOOMS as readonly string[]).includes(value)
}

export function normalizeTimelineZoom(value: unknown): TimelineZoom {
  return isTimelineZoom(value) ? value : DEFAULT_TIMELINE_ZOOM
}

/** Zoom in (towards Week) with direction -1, zoom out (towards Year) with +1. Clamps at the ends. */
export function stepTimelineZoom(zoom: TimelineZoom, direction: -1 | 1): TimelineZoom {
  const index = TIMELINE_ZOOMS.indexOf(zoom)
  return TIMELINE_ZOOMS[Math.max(0, Math.min(TIMELINE_ZOOMS.length - 1, index + direction))]
}

const DAY_MS = 86_400_000

/** Parses `YYYY-MM-DD` (or an ISO timestamp) into a local-midnight date. */
export function parseTimelineDay(value?: string | null): Date | undefined {
  if (!value) return undefined
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(value)
  if (!match) return undefined
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
  return Number.isFinite(date.getTime()) ? date : undefined
}

export function isoTimelineDay(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function dayNumber(date: Date) { return Math.round(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) / DAY_MS) }

/** Whole calendar days from `from` to `to` (negative when `to` is earlier). */
export function daysBetween(from: Date, to: Date) { return dayNumber(to) - dayNumber(from) }

export function addTimelineDays(date: Date, days: number) { return new Date(date.getFullYear(), date.getMonth(), date.getDate() + days) }

function startOfWeek(date: Date) { const day = (date.getDay() + 6) % 7; return addTimelineDays(date, -day) }
function startOfMonth(date: Date) { return new Date(date.getFullYear(), date.getMonth(), 1) }
function startOfQuarter(date: Date) { return new Date(date.getFullYear(), Math.floor(date.getMonth() / 3) * 3, 1) }
function startOfYear(date: Date) { return new Date(date.getFullYear(), 0, 1) }

export type TimelineRange = { start: Date; end: Date; days: number; width: number; pxPerDay: number; zoom: TimelineZoom }

/**
 * Canvas range covering every dated value plus today, snapped to the zoom's
 * major unit with one unit of breathing room on each side.
 */
export function timelineRange(dates: Date[], today: Date, zoom: TimelineZoom): TimelineRange {
  const values = [today, ...dates].map(date => date.getTime())
  const min = new Date(Math.min(...values))
  const max = new Date(Math.max(...values))
  const snap = zoom === 'week' ? startOfWeek : zoom === 'month' ? startOfMonth : zoom === 'quarter' ? startOfQuarter : startOfYear
  const padBefore = zoom === 'week' ? addTimelineDays(min, -14) : zoom === 'month' ? new Date(min.getFullYear(), min.getMonth() - 1, 1) : zoom === 'quarter' ? new Date(min.getFullYear(), min.getMonth() - 3, 1) : new Date(min.getFullYear() - 1, 0, 1)
  const start = snap(padBefore)
  const padAfter = zoom === 'week' ? addTimelineDays(max, 21) : zoom === 'month' ? new Date(max.getFullYear(), max.getMonth() + 2, 1) : zoom === 'quarter' ? new Date(max.getFullYear(), max.getMonth() + 6, 1) : new Date(max.getFullYear() + 2, 0, 1)
  let end = snap(padAfter)
  if (daysBetween(start, end) < MIN_SPAN_DAYS[zoom]) end = addTimelineDays(start, MIN_SPAN_DAYS[zoom])
  const days = Math.max(1, daysBetween(start, end))
  const pxPerDay = TIMELINE_PX_PER_DAY[zoom]
  return { start, end, days, width: Math.round(days * pxPerDay), pxPerDay, zoom }
}

export function timelineX(range: Pick<TimelineRange, 'start' | 'pxPerDay'>, date: Date) {
  return daysBetween(range.start, date) * range.pxPerDay
}

/** Converts a horizontal pointer delta into whole days for the current zoom. */
export function timelineDaysForPixels(pixels: number, pxPerDay: number) {
  const days = Math.round(pixels / pxPerDay)
  return Object.is(days, -0) ? 0 : days
}

export function timelineDateAt(range: Pick<TimelineRange, 'start' | 'pxPerDay'>, x: number) {
  return addTimelineDays(range.start, Math.floor(x / range.pxPerDay))
}

export type TimelineSegment = { key: string; label: string; x: number; width: number; emphasis?: boolean }

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

function segments(range: TimelineRange, next: (date: Date) => Date, first: (date: Date) => Date, label: (date: Date) => string, emphasis?: (date: Date) => boolean): TimelineSegment[] {
  const output: TimelineSegment[] = []
  for (let cursor = first(range.start); cursor < range.end && output.length < 2000; cursor = next(cursor)) {
    const following = next(cursor)
    const from = cursor < range.start ? range.start : cursor
    const to = following > range.end ? range.end : following
    const x = timelineX(range, from)
    output.push({ key: isoTimelineDay(cursor), label: label(cursor), x, width: timelineX(range, to) - x, emphasis: emphasis?.(cursor) || undefined })
  }
  return output
}

/**
 * Two-tier header: `major` spans (months / quarters / years) above `minor`
 * ticks (days / weeks / months). Adapts to the zoom like Linear's scale.
 */
export function timelineHeader(range: TimelineRange): { major: TimelineSegment[]; minor: TimelineSegment[] } {
  const nextDay = (date: Date) => addTimelineDays(date, 1)
  const nextWeek = (date: Date) => addTimelineDays(date, 7)
  const nextMonth = (date: Date) => new Date(date.getFullYear(), date.getMonth() + 1, 1)
  const nextQuarter = (date: Date) => new Date(date.getFullYear(), date.getMonth() + 3, 1)
  const nextYear = (date: Date) => new Date(date.getFullYear() + 1, 0, 1)
  const monthLabel = (date: Date) => `${MONTHS[date.getMonth()]} ${date.getFullYear()}`
  const quarterLabel = (date: Date) => `Q${Math.floor(date.getMonth() / 3) + 1} ${date.getFullYear()}`
  if (range.zoom === 'week') return {
    major: segments(range, nextMonth, startOfMonth, monthLabel),
    minor: segments(range, nextDay, date => date, date => String(date.getDate()), date => date.getDay() === 1),
  }
  if (range.zoom === 'month') return {
    major: segments(range, nextMonth, startOfMonth, monthLabel),
    minor: segments(range, nextWeek, startOfWeek, date => String(date.getDate())),
  }
  if (range.zoom === 'quarter') return {
    major: segments(range, nextQuarter, startOfQuarter, quarterLabel),
    minor: segments(range, nextMonth, startOfMonth, date => MONTHS[date.getMonth()], date => date.getMonth() % 3 === 0),
  }
  return {
    major: segments(range, nextYear, startOfYear, date => String(date.getFullYear())),
    minor: segments(range, nextMonth, startOfMonth, date => MONTHS[date.getMonth()].slice(0, 1), date => date.getMonth() % 3 === 0),
  }
}

export type TimelineSpan = { start: Date; end: Date; hasStart: boolean; hasTarget: boolean }

/**
 * A project's bar. Missing start → starts at target; missing target → a
 * three-week provisional bar (Linear draws an open-ended bar too).
 */
export function projectTimelineSpan(startValue?: string, targetValue?: string): TimelineSpan | undefined {
  const start = parseTimelineDay(startValue)
  const target = parseTimelineDay(targetValue)
  if (!start && !target) return undefined
  const from = start ?? target!
  const to = target ?? addTimelineDays(from, 21)
  return { start: from, end: to < from ? from : to, hasStart: Boolean(start), hasTarget: Boolean(target) }
}

/** Bar geometry, inclusive of the target day. */
export function timelineBarBox(range: Pick<TimelineRange, 'start' | 'pxPerDay'>, span: Pick<TimelineSpan, 'start' | 'end'>, minWidth = 8) {
  const left = timelineX(range, span.start)
  const right = timelineX(range, addTimelineDays(span.end, 1))
  return { left, width: Math.max(minWidth, right - left) }
}

export type TimelineMilestoneInput = { id: string; name: string; targetDate?: string; progress: number }
export type TimelineMilestoneMarker = TimelineMilestoneInput & { x: number; date: Date; completed: boolean }

/** Diamond markers centred on each dated milestone's target day. */
export function timelineMilestoneMarkers(range: Pick<TimelineRange, 'start' | 'pxPerDay'>, milestones: TimelineMilestoneInput[] = []): TimelineMilestoneMarker[] {
  return milestones.flatMap(milestone => {
    const date = parseTimelineDay(milestone.targetDate)
    if (!date) return []
    return [{ ...milestone, date, x: timelineX(range, date) + range.pxPerDay / 2, completed: milestone.progress >= 100 }]
  }).sort((left, right) => left.x - right.x)
}

export type TimelineDependency = { blockerId: string; blockedId: string }
export type TimelineRowBox = { left: number; width: number; y: number; start: Date; end: Date }
export type TimelineDependencyPath = TimelineDependency & { d: string; conflict: boolean; from: { x: number; y: number }; to: { x: number; y: number } }

/** Linear flags a dependency when the blocked project starts before its blocker ends. */
export function isDependencyConflict(blocker: Pick<TimelineSpan, 'end'>, blocked: Pick<TimelineSpan, 'start'>) {
  return blocked.start.getTime() <= blocker.end.getTime()
}

/**
 * Connector from the end of the blocker bar to the start of the blocked bar.
 * Forward links are a smooth S-curve; backwards links route around with an
 * elbow so the arrow still enters the blocked bar from the left.
 */
export function dependencyPath(from: { x: number; y: number }, to: { x: number; y: number }) {
  const round = (value: number) => Math.round(value * 10) / 10
  const gap = 12
  if (to.x - from.x >= gap * 2) {
    const mid = round((from.x + to.x) / 2)
    return `M${round(from.x)},${round(from.y)} C${mid},${round(from.y)} ${mid},${round(to.y)} ${round(to.x)},${round(to.y)}`
  }
  const midY = round((from.y + to.y) / 2)
  return `M${round(from.x)},${round(from.y)} H${round(from.x + gap)} V${midY} H${round(to.x - gap)} V${round(to.y)} H${round(to.x)}`
}

export function timelineDependencyPaths(dependencies: TimelineDependency[], rows: Map<string, TimelineRowBox>): TimelineDependencyPath[] {
  const output: TimelineDependencyPath[] = []
  const seen = new Set<string>()
  for (const dependency of dependencies) {
    const key = `${dependency.blockerId}>${dependency.blockedId}`
    if (seen.has(key) || dependency.blockerId === dependency.blockedId) continue
    seen.add(key)
    const blocker = rows.get(dependency.blockerId)
    const blocked = rows.get(dependency.blockedId)
    if (!blocker || !blocked) continue
    const from = { x: blocker.left + blocker.width, y: blocker.y }
    const to = { x: blocked.left, y: blocked.y }
    output.push({ ...dependency, from, to, d: dependencyPath(from, to), conflict: isDependencyConflict(blocker, blocked) })
  }
  return output
}

type DependencyRelation = { projectId: string; relatedProjectId: string; type: string }

/**
 * Normalises `dependencyIds` (blocked-by ids stored on the blocked project)
 * and explicit project relations into blocker → blocked edges.
 */
export function projectDependencyEdges(projects: Array<{ id: string; dependencyIds?: string[] }>, relations: DependencyRelation[] = []): TimelineDependency[] {
  const edges = new Map<string, TimelineDependency>()
  const add = (blockerId: string, blockedId: string) => { if (blockerId !== blockedId) edges.set(`${blockerId}>${blockedId}`, { blockerId, blockedId }) }
  for (const project of projects) for (const blockerId of project.dependencyIds ?? []) add(blockerId, project.id)
  for (const relation of relations) {
    if (relation.type === 'blocks') {
      edges.delete(`${relation.relatedProjectId}>${relation.projectId}`)
      add(relation.projectId, relation.relatedProjectId)
    } else if (relation.type === 'blocked_by' || relation.type === 'dependency') {
      edges.delete(`${relation.projectId}>${relation.relatedProjectId}`)
      add(relation.relatedProjectId, relation.projectId)
    }
  }
  return [...edges.values()]
}

/**
 * The `dependencyRelations` payload that keeps every relation the blocked
 * project already owns and adds `blockerId` as a blocker.
 */
export function dependencyRelationsWithBlocker(blockedId: string, blockerId: string, relations: DependencyRelation[] = [], fallbackDependencyIds: string[] = []) {
  const inputs = relations
    .filter(relation => relation.projectId === blockedId && (relation.type === 'blocked_by' || relation.type === 'blocks' || relation.type === 'dependency'))
    .map(relation => ({ projectId: relation.relatedProjectId, type: relation.type === 'blocks' ? 'blocks' as const : 'blocked_by' as const }))
  const represented = new Set(inputs.map(input => input.projectId))
  for (const projectId of fallbackDependencyIds) if (!represented.has(projectId)) { inputs.push({ projectId, type: 'blocked_by' }); represented.add(projectId) }
  const existing = inputs.find(input => input.projectId === blockerId)
  if (existing) existing.type = 'blocked_by'
  else inputs.push({ projectId: blockerId, type: 'blocked_by' })
  return inputs
}
