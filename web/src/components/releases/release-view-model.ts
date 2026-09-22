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
