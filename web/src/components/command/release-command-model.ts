/** Release command rules shared by ⌘K, the N then R shortcut and the app shell (no UI here, so the shell can import it cheaply). */
import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import { parseAppRoute } from '@/lib/app-routes'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

export function releasesAvailable(data: BootstrapData | undefined) {
  return Boolean(data && data.viewerRole !== 'guest' && workspaceFeatureEnabled(data.workspaceSettings?.featureFlags, 'releases'))
}

/** Releases are created in scheduled pipelines (continuous ones are filled by CI). */
export function creatablePipelines(data: BootstrapData) {
  return (data.releasePipelines ?? []).filter(pipeline => pipeline.type === 'scheduled')
}

/** The pipeline (and release) whose page is open. */
export function routePipeline(data: BootstrapData, pathname: string): { pipeline: ReleasePipeline; release?: Release } | undefined {
  const route = parseAppRoute(pathname)
  if (route.kind !== 'release-pipeline' && route.kind !== 'release') return undefined
  const pipeline = (data.releasePipelines ?? []).find(item => item.slugId === route.pipelineSlug || item.id === route.pipelineSlug)
  if (!pipeline) return undefined
  const release = route.kind === 'release' ? (data.releases ?? []).find(item => (item.slugId === route.releaseSlug || item.id === route.releaseSlug) && (!item.pipelineId || item.pipelineId === pipeline.id)) : undefined
  return { pipeline, release }
}

