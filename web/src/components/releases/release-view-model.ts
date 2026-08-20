import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

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
