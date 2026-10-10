import type { IssueQueryInput } from '@/lib/api'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

export function releaseIssueQuery(releaseId: string, cursor?: string): IssueQueryInput {
  return { releaseId, archived: 'false', limit: 100, includeTotal: false, cursor }
}

export function releasesForPipeline(data: BootstrapData, pipeline: ReleasePipeline, archived = false) {
  return data.releases
    .filter(item => item.pipelineId === pipeline.id && Boolean(item.archivedAt) === archived)
    .sort((left, right) => left.position - right.position || left.createdAt.localeCompare(right.createdAt))
}

export function releasesByStage(releases: Release[], pipeline: ReleasePipeline) {
  const groups = pipeline.stages.map(stage => ({ stage, releases: releases.filter(item => item.stage === stage) }))
  const unassigned = releases.filter(item => !item.stage || !pipeline.stages.includes(item.stage))
  return unassigned.length ? [...groups, { stage: 'Unassigned', releases: unassigned }] : groups
}

export function releaseProgress(data: BootstrapData, release: Release) {
  if (release.issueCount !== undefined) {
    return release.issueCount > 0 ? Math.round((release.completedCount ?? 0) / release.issueCount * 100) : 0
  }
  const issues = data.issues.filter(issue => release.issueIds.includes(issue.id))
  if (!issues.length) return 0
  const completed = issues.filter(issue => issue.state.type === 'completed' || issue.state.type === 'canceled').length
  return Math.round(completed / issues.length * 100)
}

export function pipelineSummary(data: BootstrapData, pipeline: ReleasePipeline) {
  const releases = releasesForPipeline(data, pipeline)
  const active = releases.filter(item => item.status === 'planned' || item.status === 'inProgress')
  const latest = [...releases].sort((left, right) => +new Date(right.updatedAt) - +new Date(left.updatedAt))[0]
  return { active: active.length, latest }
}

export function releaseStatusForStage(pipeline: ReleasePipeline, stage: string, fallback: Release['status'] = 'planned'): Release['status'] {
	return pipeline.stageStatuses?.[stage] ?? fallback
}

export function deletedReleasesForPipeline(data: BootstrapData, pipeline: ReleasePipeline) {
  return (data.trash ?? []).filter(item => {
    if (item.resourceType !== 'release') return false
    const payload = item.payload as { pipelineId?: string } | null | undefined
    if (payload && typeof payload === 'object' && payload.pipelineId) {
      return payload.pipelineId === pipeline.id
    }
    // Fallback: match by pipeline team membership when payload is unavailable.
    if (!item.teamIds?.length) return pipeline.teamIds.length === 0
    return item.teamIds.some(id => pipeline.teamIds.includes(id)) || pipeline.teamIds.length === 0
  }).sort((left, right) => right.deletedAt.localeCompare(left.deletedAt))
}

/** Latest completed (released) release — Linear pickChangelogTargetRelease spirit. */
export function pickChangelogTargetRelease(releases: Release[]) {
  const completed = releases.filter(item => item.status === 'released')
  if (!completed.length) return undefined
  const withDate = completed
    .filter(item => item.releasedAt)
    .sort((left, right) => (right.releasedAt ?? '').localeCompare(left.releasedAt ?? ''))
  if (withDate.length) return withDate[0]
  return completed[0]
}

export function releasesInInclusiveRange(releases: Release[], startId: string, endId: string) {
  const ordered = [...releases]
    .filter(item => item.status === 'released')
    .sort((left, right) => (left.releasedAt ?? left.targetDate ?? left.createdAt).localeCompare(right.releasedAt ?? right.targetDate ?? right.createdAt))
  const start = ordered.findIndex(item => item.id === startId)
  const end = ordered.findIndex(item => item.id === endId)
  if (start < 0 || end < 0) return []
  const [from, to] = start <= end ? [start, end] : [end, start]
  return ordered.slice(from, to + 1)
}

/** Linear's stage list ordering: started, planned, completed, then canceled stages. */
const STAGE_TYPE_ORDER: Record<Release['status'], number> = { inProgress: 0, planned: 1, released: 2, canceled: 3 }

/** Stage groups for a scheduled pipeline's release list, in Linear's group order (non-empty only). */
export function releaseStageGroups(releases: Release[], pipeline: ReleasePipeline) {
  const stages = pipeline.stages
    .map((stage, position) => ({ stage, position, type: releaseStatusForStage(pipeline, stage) }))
    .sort((left, right) => STAGE_TYPE_ORDER[left.type] - STAGE_TYPE_ORDER[right.type] || right.position - left.position)
  const groups = stages.map(({ stage }) => ({ stage, releases: releases.filter(item => item.stage === stage) }))
  const unassigned = releases.filter(item => !item.stage || !pipeline.stages.includes(item.stage))
  return [...groups, { stage: '', releases: unassigned }].filter(group => group.releases.length)
}

export type ReleaseOrdering = 'version' | 'stage' | 'releaseDate'
export type SortDirection = 'asc' | 'desc'

function releaseDateValue(release: Release) {
  return release.releasedAt ?? release.targetDate ?? ''
}

function compareVersions(left: string, right: string) {
  return left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' })
}

/** Orders a pipeline's releases like Linear's scheduled release list (undated releases sort last). */
export function sortReleases(releases: Release[], pipeline: ReleasePipeline, ordering: ReleaseOrdering, direction: SortDirection) {
  const sign = direction === 'asc' ? 1 : -1
  const stageIndex = (release: Release) => release.stage ? pipeline.stages.indexOf(release.stage) : -1
  return [...releases].sort((left, right) => {
    if (ordering === 'releaseDate') {
      const a = releaseDateValue(left), b = releaseDateValue(right)
      if (!a !== !b) return a ? -1 : 1
      return sign * a.localeCompare(b) || compareVersions(left.version || left.name, right.version || right.name)
    }
    if (ordering === 'stage') return sign * (stageIndex(left) - stageIndex(right)) || left.position - right.position
    return sign * compareVersions(left.version || left.name, right.version || right.name)
  })
}

/** Releases in a planned or started stage (Linear's "Active releases" column). */
export function activeReleases(data: BootstrapData, pipeline: ReleasePipeline) {
  return releasesForPipeline(data, pipeline).filter(item => item.status === 'planned' || item.status === 'inProgress')
}

/** The pipeline's most recently completed release. */
export function latestCompletedRelease(data: BootstrapData, pipeline: ReleasePipeline) {
  return releasesForPipeline(data, pipeline)
    .filter(item => item.status === 'released' && item.releasedAt)
    .sort((left, right) => (right.releasedAt ?? '').localeCompare(left.releasedAt ?? '') || right.createdAt.localeCompare(left.createdAt))[0]
}

/**
 * The release the Changelog asks notes for (Linear's pickChangelogTargetRelease): the latest
 * completed release, else the first release of a scheduled pipeline.
 */
export function changelogTargetRelease(data: BootstrapData, pipeline: ReleasePipeline) {
  const latest = latestCompletedRelease(data, pipeline)
  if (latest) return latest
  if (pipeline.type === 'continuous') return undefined
  return releasesForPipeline(data, pipeline)[0]
}

/** Whether a release has written release notes (note record or the release's own notes field). */
export function releaseHasNotes(data: BootstrapData, release: Release) {
  const note = data.releaseNotes?.find(item => item.releaseId === release.id)
  return Boolean((note?.body ?? release.releaseNotes ?? '').trim())
}

export type ReleaseCompletion = { progress: number; summary: string; tooltip: string; done: number; started: number; notStarted: number; total: number }

/** Completion ring data; canceled releases show none (Linear's release list). */
export function releaseCompletion(data: BootstrapData, release: Release): ReleaseCompletion | undefined {
  if (release.status === 'canceled') return undefined
  const issues = data.issues.filter(issue => release.issueIds.includes(issue.id))
  const total = release.issueCount ?? issues.length
  if (!total) return { progress: 0, summary: '0%', tooltip: 'No scoped issues', done: 0, started: 0, notStarted: 0, total: 0 }
  const done = release.completedCount ?? issues.filter(issue => issue.state.type === 'completed' || issue.state.type === 'canceled').length
  const started = issues.filter(issue => issue.state.type === 'started').length
  const progress = done / total
  return { progress, summary: `${Math.round(progress * 100)}%`, tooltip: '', done, started, notStarted: Math.max(total - done - started, 0), total }
}

const DAY = 86_400_000
function localDate(value: Date) { return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}` }
function addDays(base: Date, days: number) { const next = new Date(base); next.setDate(next.getDate() + days); return next }

/** Quick picks of Linear's target-date picker. */
export function releaseDateQuickOptions(now = new Date()) {
  const month = new Date(now); month.setMonth(month.getMonth() + 1)
  return [
    { id: 'tomorrow', label: 'Tomorrow', value: localDate(addDays(now, 1)) },
    { id: 'week', label: 'In 1 week', value: localDate(addDays(now, 7)) },
    { id: 'twoWeeks', label: 'In 2 weeks', value: localDate(addDays(now, 14)) },
    { id: 'month', label: 'In 1 month', value: localDate(month) },
  ]
}

const MONTHS = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec']

/** Parses the picker's free text ("24h", "7 days", "2 weeks", "Feb 9", "2026-03-01"). */
export function parseReleaseDateQuery(input: string, now = new Date()): string | undefined {
  const value = input.trim().toLowerCase()
  if (!value) return undefined
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value
  if (value === 'today') return localDate(now)
  if (value === 'tomorrow') return localDate(addDays(now, 1))
  const relative = value.match(/^(?:in\s+)?(\d+)\s*(h|hours?|d|days?|w|weeks?|m|months?|小时|天|周|个月|月)后?$/)
  if (relative) {
    const amount = Number(relative[1])
    const unit = ({ 小: 'h', 天: 'd', 周: 'w', 个: 'm', 月: 'm' } as Record<string, string>)[relative[2][0]] ?? relative[2][0]
    if (unit === 'h') return localDate(new Date(now.getTime() + Math.max(amount, 1) * 3_600_000))
    if (unit === 'd') return localDate(addDays(now, amount))
    if (unit === 'w') return localDate(addDays(now, amount * 7))
    const month = new Date(now); month.setMonth(month.getMonth() + amount); return localDate(month)
  }
  const chinese = value.match(/^(?:(\d{4})年)?(\d{1,2})月(\d{1,2})日?$/)
  if (chinese) {
    const year = chinese[1] ? Number(chinese[1]) : now.getFullYear()
    let date = new Date(year, Number(chinese[2]) - 1, Number(chinese[3]))
    if (!chinese[1] && date.getTime() < now.getTime() - DAY) date = new Date(year + 1, date.getMonth(), date.getDate())
    return localDate(date)
  }
  const named = value.match(/^([a-z]{3})[a-z]*\.?\s+(\d{1,2})(?:,?\s+(\d{4}))?$/)
  if (named && MONTHS.includes(named[1])) {
    const year = named[3] ? Number(named[3]) : now.getFullYear()
    let date = new Date(year, MONTHS.indexOf(named[1]), Number(named[2]))
    if (!named[3] && date.getTime() < now.getTime() - DAY) date = new Date(year + 1, date.getMonth(), date.getDate())
    return localDate(date)
  }
  return undefined
}

/** Day-only values ("2026-03-01") are local calendar days. */
export function dayValue(value: string) {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T12:00:00` : value
}
