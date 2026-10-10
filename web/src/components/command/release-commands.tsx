/* oxlint-disable react/only-export-components -- command builders and their page ids share one module, like the document commands. */
/**
 * Release commands for ⌘K (Linear parity): the workspace ones (go to release
 * pipelines, open a pipeline, create a release), the ones that follow the open
 * pipeline or release page (view releases, archive, recently deleted, pipeline
 * settings) and the issue ones (add to release…, remove from release…).
 */
import { Archive, Settings, Trash2, X } from 'lucide-react'
import { toast } from 'sonner'

import { ReleasePipelineIcon, ReleasesIcon, ReleaseStatusIcon } from '@/components/releases/release-icons'
import { setIssueReleases } from '@/lib/api'
import { releasePipelinePath, releasePipelinesPath, releasePipelineSettingsPath } from '@/lib/app-routes'
import type { BootstrapData, Issue, Release, ReleasePipeline } from '@/types/flow'
import type { CommandPage, ContextAction, PageOption } from './context-commands'
import { creatablePipelines, releasesAvailable, routePipeline } from './release-command-model'

/** What the palette needs from the app to run release commands. */
export interface ReleaseCommandHost {
  navigate: (path: string) => void
  /** Opens the New release composer for a pipeline. */
  createRelease: (pipeline: ReleasePipeline) => void
}

export type ReleaseGlobalAction = ContextAction & { group: 'Navigation' | 'Releases' }

/** Pages of the workspace release commands (they work without a registered context). */
export const GLOBAL_RELEASE_PAGES: ReadonlySet<string> = new Set(['releaseOpenPipeline', 'releaseCreate'])

export function releaseCommands({ data, pathname, page, host, close }: {
  data: BootstrapData | undefined
  pathname: string
  page: CommandPage | undefined
  host: ReleaseCommandHost
  close: () => void
}): { global: ReleaseGlobalAction[]; options: PageOption[] } {
  if (!data || !releasesAvailable(data)) return { global: [], options: [] }
  const workspace = data.workspace.urlKey
  const pipelines = data.releasePipelines ?? []
  const open = routePipeline(data, pathname)
  const go = (path: string) => () => { close(); host.navigate(path) }
  const creatable = creatablePipelines(data)
  const global: ReleaseGlobalAction[] = [
    { id: 'go-release-pipelines', group: 'Navigation', label: 'Go to release pipelines', icon: <ReleasesIcon/>, keywords: 'open releases', run: go(releasePipelinesPath(workspace)) },
  ]
  const others = open ? pipelines.filter(pipeline => pipeline.id !== open.pipeline.id) : pipelines
  if (others.length) global.push({ id: 'open-release-pipeline', group: 'Navigation', label: 'Open release pipeline…', icon: <ReleasesIcon/>, keywords: 'goto', page: { id: 'releaseOpenPipeline', label: 'Open release pipeline…' } })
  if (creatable.length) {
    const target = open?.pipeline.type === 'scheduled' ? open.pipeline : undefined
    global.push(target
      ? { id: 'create-release', group: 'Releases', label: 'Create new release', icon: <ReleasesIcon/>, shortcut: ['N', 'then', 'R'], keywords: 'add create', run: () => { close(); host.createRelease(target) } }
      : { id: 'create-release', group: 'Releases', label: 'Create new release…', icon: <ReleasesIcon/>, shortcut: ['N', 'then', 'R'], keywords: 'add new', page: { id: 'releaseCreate', label: 'Create new release…' } })
  }
  if (open) {
    const { pipeline } = open
    global.push(
      { id: 'view-releases', group: 'Releases', label: 'View releases', icon: <ReleasesIcon/>, keywords: 'releases pipeline list', run: go(releasePipelinePath(workspace, pipeline.slugId)) },
      { id: 'open-release-archive', group: 'Releases', label: 'Open archive', icon: <Archive/>, keywords: 'archived pruned releases archive', run: go(releasePipelinePath(workspace, pipeline.slugId, 'archive')) },
      { id: 'release-pipeline-settings', group: 'Releases', label: 'Pipeline settings', icon: <Settings/>, keywords: 'pipeline settings preferences configuration', run: go(releasePipelineSettingsPath(workspace, pipeline.slugId)) },
      { id: 'view-deleted-releases', group: 'Releases', label: 'View recently deleted releases', icon: <Trash2/>, keywords: 'deleted trashed removed releases trash', run: go(releasePipelinePath(workspace, pipeline.slugId, 'deleted')) },
    )
  }

  let options: PageOption[] = []
  if (page?.id === 'releaseOpenPipeline') options = others.map(pipeline => ({ id: pipeline.id, label: pipeline.name, entity: true, icon: <ReleasePipelineIcon/>, select: go(releasePipelinePath(workspace, pipeline.slugId)) }))
  if (page?.id === 'releaseCreate') options = creatable.map(pipeline => ({ id: pipeline.id, label: pipeline.name, entity: true, icon: <ReleasePipelineIcon/>, select: () => { close(); host.createRelease(pipeline) } }))
  return { global, options }
}

const detailDate = (release: Release) => release.releasedAt || release.targetDate

function releaseLabel(release: Release) {
  return release.version && release.version !== release.name ? `${release.name} · ${release.version}` : release.name
}

function releaseDetail(release: Release, pipelines: ReleasePipeline[], formatDate: (value: string) => string) {
  const date = detailDate(release)
  const pipeline = pipelines.find(item => item.id === release.pipelineId)?.name
  const shown = date ? formatDate(date) : ''
  return [pipeline, shown].filter(Boolean).join(' · ') || undefined
}

/** Releases (by id) each issue belongs to, from the workspace's releases. */
function releaseIdsOf(data: BootstrapData, issueId: string) {
  return (data.releases ?? []).filter(release => release.issueIds.includes(issueId)).map(release => release.id)
}

/** Adds the releases to every issue, or removes them when all issues already have them. */
async function toggleReleases(data: BootstrapData, issues: Issue[], target: Release, onChanged?: () => Promise<void>, remove = false) {
  const all = remove || issues.every(issue => target.issueIds.includes(issue.id))
  try {
    await Promise.all(issues.map(async issue => {
      const current = releaseIdsOf(data, issue.id)
      const next = all ? current.filter(id => id !== target.id) : [...new Set([...current, target.id])]
      if (next.length === current.length && next.every(id => current.includes(id))) return
      await setIssueReleases(issue.id, next)
      dispatchEvent(new CustomEvent('flow:issue-releases', { detail: { issueId: issue.id, releaseIds: next } }))
    }))
    await onChanged?.()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Could not update releases')
  }
}

/** "Add to release…" and "Remove from release…" for the issues in the command context. */
export function issueReleaseCommands({ data, issues, page, query, close, formatDate, onChanged }: {
  data: BootstrapData
  issues: Issue[]
  page: CommandPage | undefined
  query: string
  close: () => void
  formatDate: (value: string) => string
  onChanged?: () => Promise<void>
}): { actions: ContextAction[]; options: PageOption[] } {
  if (!releasesAvailable(data) || !issues.length || !(data.releasePipelines ?? []).length) return { actions: [], options: [] }
  const pipelines = data.releasePipelines ?? []
  const releases = (data.releases ?? []).filter(release => !release.archivedAt && pipelines.some(pipeline => pipeline.id === release.pipelineId))
  const memberOf = (release: Release) => issues.filter(issue => release.issueIds.includes(issue.id)).length
  const selected = releases.filter(release => memberOf(release) > 0)
  const actions: ContextAction[] = [
    { id: 'ctx-release', label: 'Add to release…', icon: <ReleasesIcon/>, shortcut: ['⌥', 'R'], keywords: 'release add change', page: { id: 'issueRelease', label: 'Add to release…' } },
  ]
  // Linear shows "Remove from release" only through search.
  if (selected.length && query.trim()) {
    actions.push(selected.length === 1
      ? { id: 'ctx-release-remove', label: 'Remove from release', icon: <X/>, keywords: selected[0].name, run: () => { close(); return toggleReleases(data, issues, selected[0], onChanged, true) } }
      : { id: 'ctx-release-remove', label: 'Remove from release…', icon: <X/>, page: { id: 'issueReleaseRemove', label: 'Remove from release…' } })
  }
  let options: PageOption[] = []
  const toOption = (release: Release, group: string): PageOption => {
    const count = memberOf(release)
    return {
      id: release.id, label: releaseLabel(release), entity: true, group, keywords: pipelines.find(item => item.id === release.pipelineId)?.name,
      icon: <ReleaseStatusIcon status={release.status}/>, detail: releaseDetail(release, pipelines, formatDate),
      checked: count === issues.length ? true : count > 0 ? 'mixed' : false,
      select: () => toggleReleases(data, issues, release, onChanged),
    }
  }
  if (page?.id === 'issueRelease') {
    const search = query.trim().length >= 3
    const rest = releases.filter(release => memberOf(release) === 0 && (!release.stageFrozenAt))
    options = [
      ...selected.map(release => toOption(release, 'Selected releases')),
      ...rest.filter(release => release.status === 'inProgress').map(release => toOption(release, 'In progress')),
      ...rest.filter(release => release.status === 'planned').map(release => toOption(release, 'Planned')),
      ...(search ? rest.filter(release => release.status === 'released').map(release => toOption(release, 'Completed')) : []),
      ...(search ? rest.filter(release => release.status === 'canceled').map(release => toOption(release, 'Canceled')) : []),
    ]
  }
  if (page?.id === 'issueReleaseRemove') options = selected.map(release => ({ ...toOption(release, 'Selected releases'), checked: undefined, select: () => { close(); return toggleReleases(data, issues, release, onChanged, true) } }))
  return { actions, options }
}
