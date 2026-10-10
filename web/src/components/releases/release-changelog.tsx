import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { useMemo, useState, type ReactNode } from 'react'

import { MentionBody } from '@/components/editor/mentions/mention-body'
import { AgentWriteGlyph } from '@/components/ui/agent-glyph'
import { LinearDropdownMenuContent, LinearMenuOptions } from '@/components/ui/row-context-menu'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { AppLink } from '@/components/ui/app-link'
import { canCreateReleasePipeline } from '@/lib/settings-access'
import { releasePath, releasePipelineSettingsPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

import { ChangelogIllustration, ReleasesIllustration } from './release-illustrations'
import { ReleaseStatusIcon } from './release-icons'
import { RELEASE_DOCS_URL, ReleaseEmptyState } from './release-page-chrome'
import { changelogTargetRelease, dayValue, releaseHasNotes, releaseStatusForStage, releasesForPipeline } from './release-view-model'

type Article = { release: Release; title: string; body: string; date: string }

function releaseTime(release: Release) {
  return release.releasedAt ?? release.targetDate ?? release.createdAt
}

/** A release chip (stage icon + name), the Changelog's "Missing release notes [◌ QA release 1]". */
function ReleaseChip({ pipeline, release }: { pipeline: ReleasePipeline; release: Release }) {
  return <span className="flow-release-chip" data-i18n-ignore><ReleaseStatusIcon status={releaseStatusForStage(pipeline, release.stage ?? '', release.status)} size={14}/><span>{release.name || release.version}</span></span>
}

/** Pipeline Changelog (Linear PipelineReleaseNotesPage): release notes feed, a missing-notes prompt and empty states. */
export function ReleaseChangelog({ data, pipeline, onCreate, onNavigate }: { data: BootstrapData; pipeline: ReleasePipeline; onCreate: () => void; onNavigate: (path: string) => void }) {
  const { t, formatDate } = useI18n()
  const workspace = data.workspace.urlKey
  const canManage = canCreateReleasePipeline(data)
  const scheduled = pipeline.type === 'scheduled'
  const releases = releasesForPipeline(data, pipeline)
  const [pickedId, setPickedId] = useState<string>()
  const articles = useMemo<Article[]>(() => releases.filter(release => releaseHasNotes(data, release)).map(release => {
    const note = data.releaseNotes?.find(item => item.releaseId === release.id)
    return { release, title: note?.title || release.name, body: note?.body ?? release.releaseNotes ?? '', date: releaseTime(release) }
  }).sort((left, right) => right.date.localeCompare(left.date)), [data, releases])
  const target = (pickedId ? releases.find(item => item.id === pickedId) : undefined) ?? changelogTargetRelease(data, pipeline)
  const openNotes = (release: Release) => onNavigate(releasePath(workspace, pipeline.slugId, release.slugId, 'release-notes'))
  const settingsHref = `${releasePipelineSettingsPath(workspace, pipeline.slugId)}#auto-generate-release-notes-on-completion`
  const picker = (trigger: ReactNode) => <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
    <DropdownMenu.Portal><LinearDropdownMenuContent label={t('Select release')} className="flow-release-stage-menu">
      <LinearMenuOptions placeholder="Select release…" emptyLabel="No releases missing release notes" selected={new Set(target ? [target.id] : [])} onChoose={setPickedId}
        options={releases.filter(item => !releaseHasNotes(data, item)).map(item => ({ id: item.id, label: item.name, translate: false, icon: <ReleaseStatusIcon status={releaseStatusForStage(pipeline, item.stage ?? '', item.status)}/>, detail: item.version && item.version !== item.name ? item.version : undefined }))}/>
    </LinearDropdownMenuContent></DropdownMenu.Portal>
  </DropdownMenu.Root>

  if (!releases.length) {
    if (!scheduled) return <ReleaseEmptyState art={<ReleasesIllustration/>} title={t(canManage ? 'Create your first release' : 'No releases yet')} paragraphs={[t(canManage ? 'Create a new release in order to create release notes. All release notes added to this pipeline will show up here.' : 'Release notes will appear here after releases are added to this pipeline.')]} secondary={canManage ? { label: 'Set up integration', href: `${RELEASE_DOCS_URL}#example-for-continuous-deployments` } : undefined}/>
    return <ReleaseEmptyState art={<ChangelogIllustration/>} title={t(canManage ? 'Create your first release' : 'No releases yet')} paragraphs={[t(canManage ? 'Create a new release in order to create release notes. All release notes added to this pipeline will show up here.' : 'Release notes will appear here after releases are added to this pipeline.')]} primary={canManage ? { label: 'Create new release', onClick: onCreate } : undefined} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#release-notes` }}/>
  }
  if (!articles.length) {
    if (!canManage) return <ReleaseEmptyState art={<ChangelogIllustration/>} title={t('No release notes yet')} paragraphs={[t('Release notes added to this pipeline will appear here.')]} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#release-notes` }}/>
    if (!target) return <ReleaseEmptyState art={<ChangelogIllustration/>} title={t('No release notes yet')} paragraphs={[t('Create release notes from a release or range of releases to share what shipped.')]} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#release-notes` }}/>
    const several = scheduled && releases.length > 1
    return <ReleaseEmptyState art={<ChangelogIllustration/>}
      title={<span className="flow-release-missing-title">{t('Missing release notes')}<ReleaseChip pipeline={pipeline} release={target}/></span>}
      paragraphs={[
        t(several ? 'Create release notes for this release or select another release.' : 'Create release notes for this release.'),
        ...(scheduled ? [(() => {
          const [before, after = ''] = t('You can enable auto-generation and set a release notes template in the {link}.').split('{link}')
          return <>{before}<AppLink className="flow-release-inline-link" href={settingsHref} onClick={event => { if (event.metaKey || event.ctrlKey) return; event.preventDefault(); onNavigate(settingsHref) }}>{t('pipeline settings')}</AppLink>{after}</>
        })()] : []),
      ]}
      primary={{ label: 'Create', onClick: () => openNotes(target) }}
      extra={several ? picker(<button className="flow-releases-secondary" type="button">{t('Select another release')}</button>) : undefined}/>
  }
  const missing = canManage && target && !releaseHasNotes(data, target) ? target : undefined
  return <div className="flow-release-changelog">
    <div className="flow-release-changelog__feed">
      {missing && <div className="flow-release-changelog__missing">
        <span className="flow-release-missing-title">{t('Missing release notes')}{picker(<button className="flow-release-chip is-button" type="button"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, missing.stage ?? '', missing.status)} size={14}/><span data-i18n-ignore>{missing.name || missing.version}</span></button>)}</span>
        <span className="flow-release-changelog__missing-actions">
          <button className="flow-releases-secondary is-small" onClick={() => openNotes(missing)} type="button">{t('Create')}</button>
          <ScopedFlowTooltip label={missing.issueIds.length ? undefined : t('Add issues to this release to write release notes')}><span><button className="flow-releases-secondary is-small" disabled={!missing.issueIds.length} onClick={() => onNavigate(`${releasePath(workspace, pipeline.slugId, missing.slugId, 'release-notes')}?agent=1`)} type="button"><AgentWriteGlyph size={14}/>{t('Write with Agent')}</button></span></ScopedFlowTooltip>
        </span>
      </div>}
      {articles.map(article => <article key={article.release.id} className="flow-release-changelog__article" data-release-note-id={article.release.id} id={`release-note-${article.release.id}`}>
        <header>
          <AppLink className="flow-release-changelog__title" href={releasePath(workspace, pipeline.slugId, article.release.slugId, 'release-notes')} onClick={event => { if (event.metaKey || event.ctrlKey) return; event.preventDefault(); openNotes(article.release) }} data-i18n-ignore>{article.title}</AppLink>
          <div className="flow-release-changelog__meta">
            {article.release.version && <span className="flow-release-version is-chip" data-i18n-ignore>{article.release.version}</span>}
            {article.release.stage && <span className="flow-release-changelog__stage"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, article.release.stage, article.release.status)} size={14}/>{article.release.status !== 'released' && <span data-i18n-ignore>{article.release.stage}</span>}</span>}
            {article.release.releasedAt ? <span>{t('Released {date}').replace('{date}', formatDate(article.release.releasedAt, { month: 'short', day: 'numeric', year: 'numeric' }))}</span>
              : article.release.targetDate ? <span>{formatDate(dayValue(article.release.targetDate), { month: 'short', day: 'numeric', year: 'numeric' })}</span> : null}
          </div>
        </header>
        {article.body.trim() ? <MentionBody className="flow-release-changelog__body" body={article.body}/> : <p className="flow-release-changelog__empty">{t('No content yet.')}</p>}
      </article>)}
    </div>
    <nav className="flow-release-changelog__toc" aria-label={t('Release notes')}>
      {articles.map(article => <a key={article.release.id} href={`#release-note-${article.release.id}`} onClick={event => { event.preventDefault(); document.getElementById(`release-note-${article.release.id}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' }) }}>
        <span data-i18n-ignore>{article.title}</span><time>{formatDate(dayValue(article.date), { month: 'short', day: 'numeric' })}</time>
      </a>)}
    </nav>
  </div>
}
