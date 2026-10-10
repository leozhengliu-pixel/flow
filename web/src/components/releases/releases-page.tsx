import { useState } from 'react'

import { useI18n } from '@/i18n/i18n'
import { releasePath, type ReleasePipelineTab, type ReleaseRouteTab } from '@/lib/app-routes'
import type { BootstrapData, ReleasePipeline } from '@/types/flow'

import { ReleaseDetailPage } from './release-detail-page'
import { ReleaseEditorDialog } from './release-editor-dialog'
import { ReleasesIllustration } from './release-illustrations'
import { ReleaseEmptyState, ReleasesHeader } from './release-page-chrome'
import { ReleasePipelineView } from './release-pipeline-view'
import { ReleasePipelinesView } from './release-pipelines-view'
import './releases.css'

type Props = {
  data: BootstrapData
  pipelineSlug?: string
  releaseSlug?: string
  pipelineTab?: ReleasePipelineTab
  releaseTab?: ReleaseRouteTab
  onOpenSidebar: () => void
  onNavigate: (path: string) => void
  onReload: () => Promise<void>
}

/** In-app Releases: pipeline directory, pipeline (Releases / Changelog / archive / deleted) and release detail. */
export function ReleasesPage({ data, pipelineSlug, releaseSlug, pipelineTab, releaseTab, onOpenSidebar, onNavigate, onReload }: Props) {
  const { t } = useI18n()
  const [creating, setCreating] = useState<ReleasePipeline>()
  const release = data.releases.find(item => item.slugId === releaseSlug)
  const pipeline = data.releasePipelines.find(item => item.slugId === pipelineSlug || item.id === release?.pipelineId)
  if ((releaseSlug && !release) || (pipelineSlug && !pipeline)) {
    const title = t(releaseSlug ? 'Release not found' : 'Release pipeline not found')
    return <main className="main-panel flow-releases-page"><ReleasesHeader onOpenSidebar={onOpenSidebar}><h1 className="flow-releases-header__heading">{title}</h1></ReleasesHeader><ReleaseEmptyState art={<ReleasesIllustration/>} title={title} paragraphs={[t('It may have been deleted, or the link is incorrect.')]}/></main>
  }
  const editor = creating && <ReleaseEditorDialog data={data} pipeline={creating} onClose={() => setCreating(undefined)} onSaved={onReload} onOpenRelease={created => onNavigate(releasePath(data.workspace.urlKey, creating.slugId, created.slugId))}/>
  if (release && pipeline) return <ReleaseDetailPage key={release.id} data={data} pipeline={pipeline} release={release} tab={releaseTab ?? 'issues'} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onReload={onReload}/>
  if (pipeline) return <>
    <ReleasePipelineView data={data} pipeline={pipeline} tab={pipelineTab ?? 'releases'} onCreate={() => setCreating(pipeline)} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onReload={onReload}/>
    {editor}
  </>
  return <>
    <ReleasePipelinesView data={data} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onCreateRelease={setCreating}/>
    {editor}
  </>
}
