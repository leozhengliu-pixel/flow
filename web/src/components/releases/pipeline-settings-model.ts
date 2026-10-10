import type { BootstrapData, ReleasePipeline } from '@/types/flow'

export const RELEASES_DOCS_URL = 'https://flow.app/docs/releases'
/** The release API and CI setup guide (docs/release-automation.md). */
export const RELEASE_API_DOCS_URL = 'https://github.com/leozhengliu-pixel/flow/blob/main/docs/release-automation.md'
export const RELEASE_GITHUB_ACTIONS_DOCS_URL = `${RELEASE_API_DOCS_URL}#github-actions`

export type PipelineRowSummary = { releaseCount: number; latestCompletedAt?: string }

/** Release count (unarchived) and the latest completion date for each pipeline. */
export function pipelineRowSummaries(data: Pick<BootstrapData, 'releases'>): Map<string, PipelineRowSummary> {
  const summaries = new Map<string, PipelineRowSummary>()
  for (const release of data.releases ?? []) {
    if (!release.pipelineId || release.archivedAt) continue
    const summary = summaries.get(release.pipelineId) ?? { releaseCount: 0 }
    summary.releaseCount += 1
    if (release.status === 'released' && release.releasedAt && (!summary.latestCompletedAt || release.releasedAt > summary.latestCompletedAt)) summary.latestCompletedAt = release.releasedAt
    summaries.set(release.pipelineId, summary)
  }
  return summaries
}

export type AccessKeyState = 'active' | 'expiring' | 'none'

/** Linear's key states: Active, Expiring (revocation scheduled) or none. */
export function accessKeyState(pipeline: Pick<ReleasePipeline, 'accessKeyPrefix' | 'accessKeyRevokedAt'>, now = Date.now()): AccessKeyState {
  if (!pipeline.accessKeyPrefix) return 'none'
  if (!pipeline.accessKeyRevokedAt) return 'active'
  return new Date(pipeline.accessKeyRevokedAt).getTime() > now ? 'expiring' : 'none'
}
