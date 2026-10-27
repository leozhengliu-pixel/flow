import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useCurrentTime } from '@/hooks/use-current-time'

import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { toast } from '@/lib/toast'
import { AgentChevronDownIcon } from '@/components/agent/agent-icons'
import { AgentWriteGlyph } from '@/components/ui/agent-glyph'
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuOptions, FlowMenuSeparator, FlowSubmenu } from '@/components/ui/row-context-menu'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { AppLink } from '@/components/ui/app-link'
import { canCreateReleasePipeline } from '@/lib/settings-access'
import { updateRelease, updateReleaseNote } from '@/lib/api'
import { releasePath, releasePipelineSettingsPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

import { ChangelogIllustration, ReleasesIllustration } from './release-illustrations'
import { FlowGlyph } from '@/components/ui/menu-glyphs'

import { ReleaseStatusIcon } from './release-icons'
import { absoluteUrl, copyToClipboard } from './release-actions-model'
import { RELEASE_DOCS_URL, ReleaseEmptyState } from './release-page-chrome'
import { changelogTargetRelease, dayValue, releaseHasNotes, releaseStatusForStage, releasesForPipeline } from './release-view-model'

/** The reference app's "Missing release notes" scope menu. */
const SCOPES = [{ id: 'single', label: 'Single release' }, { id: 'range', label: 'Release range' }, { id: 'lastCovered', label: 'From last covered release' }]

type Article = { release: Release; title: string; body: string; date: string }

function releaseTime(release: Release) {
  return release.releasedAt ?? release.targetDate ?? release.createdAt
}

/** A release chip (stage icon + name), the Changelog's "Missing release notes [◌ QA release 1]". */
function ReleaseChip({ pipeline, release }: { pipeline: ReleasePipeline; release: Release }) {
  return <div className="flow-release-title-chip-slot" data-i18n-ignore><div className="flow-release-title-chip"><div><ReleaseStatusIcon status={releaseStatusForStage(pipeline, release.stage ?? '', release.status)} size={14}/></div><span>{release.name || release.version}</span></div></div>
}

/** Pipeline Changelog (the reference app PipelineReleaseNotesPage): release notes feed, a missing-notes prompt and empty states. */
export function ReleaseChangelog({ data, pipeline, onCreate, onNavigate, onReload }: { data: BootstrapData; pipeline: ReleasePipeline; onCreate: () => void; onNavigate: (path: string) => void; onReload?: () => Promise<void> }) {
  const { t, formatDate } = useI18n()
  const workspace = data.workspace.urlKey
  const canManage = canCreateReleasePipeline(data)
  const scheduled = pipeline.type === 'scheduled'
  const releases = releasesForPipeline(data, pipeline)
  const [pickedId, setPickedId] = useState<string>()
  const [scope, setScope] = useState<'single' | 'range' | 'lastCovered'>('lastCovered')
  const [fromId, setFromId] = useState<string>()
  const articles = useMemo<Article[]>(() => releases.filter(release => releaseHasNotes(data, release)).map(release => {
    const note = data.releaseNotes?.find(item => item.releaseId === release.id)
    return { release, title: note?.title || release.name, body: note?.body ?? release.releaseNotes ?? '', date: releaseTime(release) }
  }).sort((left, right) => right.date.localeCompare(left.date)), [data, releases])
  const target = (pickedId ? releases.find(item => item.id === pickedId) : undefined) ?? changelogTargetRelease(data, pipeline)
  const notesUrl = (article: Article) => absoluteUrl(releasePath(workspace, pipeline.slugId, article.release.slugId, 'release-notes'))
  const openNotes = (release: Release) => onNavigate(releasePath(workspace, pipeline.slugId, release.slugId, 'release-notes'))
  const currentYear = useCurrentTime(0).getFullYear()
  const shortDate = (value: string) => formatDate(value, new Date(value).getFullYear() === currentYear ? { month: 'short', day: 'numeric' } : { month: 'short', day: 'numeric', year: 'numeric' })
  const clearNotes = async (article: Article) => {
    const note = data.releaseNotes?.find(item => item.releaseId === article.release.id)
    try {
      if (note) await updateReleaseNote(article.release.id, note.id, { body: '', title: '' })
      await updateRelease(article.release.id, { releaseNotes: '' })
      await onReload?.()
    } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not save release')) }
  }
  const settingsHref = `${releasePipelineSettingsPath(workspace, pipeline.slugId)}#auto-generate-release-notes-on-completion`
  const releaseOption = (item: Release) => ({ id: item.id, label: item.name, translate: false, icon: <ReleaseStatusIcon status={releaseStatusForStage(pipeline, item.stage ?? '', item.status)}/>, detail: formatDate(releaseTime(item), { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) })
  const menu = (trigger: ReactNode, label: string, placeholder: string | undefined, options: ReturnType<typeof releaseOption>[] | { id: string; label: string }[], selected: string | undefined, onChoose: (id: string) => void, emptyLabel = 'No results') => <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
    <DropdownMenu.Portal><FlowDropdownMenuContent label={t(label)} className={placeholder ? 'flow-release-stage-menu' : undefined}>
      <FlowMenuOptions placeholder={placeholder} emptyLabel={emptyLabel} selected={new Set(selected && !placeholder ? [selected] : [])} onChoose={onChoose} options={options}/>
    </FlowDropdownMenuContent></DropdownMenu.Portal>
  </DropdownMenu.Root>
  const picker = (trigger: ReactNode) => menu(trigger, 'Select release', 'Select release…', releases.filter(item => !releaseHasNotes(data, item)).map(releaseOption), target?.id, setPickedId, 'No releases missing release notes')

  if (!releases.length) {
    if (!scheduled) return <ReleaseEmptyState art={<ReleasesIllustration/>} title={t(canManage ? 'Create your first release' : 'No releases yet')} paragraphs={[t(canManage ? 'Create a new release in order to create release notes. All release notes added to this pipeline will show up here.' : 'Release notes will appear here after releases are added to this pipeline.')]} secondary={canManage ? { label: 'Set up integration', href: `${RELEASE_DOCS_URL}#example-for-continuous-deployments` } : undefined}/>
    return <ReleaseEmptyState art={<ChangelogIllustration/>} title={t(canManage ? 'Create your first release' : 'No releases yet')} paragraphs={[t(canManage ? 'Create a new release in order to create release notes. All release notes added to this pipeline will show up here.' : 'Release notes will appear here after releases are added to this pipeline.')]} primary={canManage ? { label: 'Create new release', onClick: onCreate } : undefined} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#release-notes` }}/>
  }
  if (!articles.length) {
    if (!canManage) return <ReleaseEmptyState art={<ChangelogIllustration/>} title={t('No release notes yet')} paragraphs={[t('Release notes added to this pipeline will appear here.')]} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#release-notes` }}/>
    if (!target) return <ReleaseEmptyState art={<ChangelogIllustration/>} title={t('No release notes yet')} paragraphs={[t('Create release notes from a release or range of releases to share what shipped.')]} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#release-notes` }}/>
    const several = scheduled && releases.length > 1
    return <ReleaseEmptyState art={<ChangelogIllustration/>}
      title={<div className="flow-release-missing-title">{t('Missing release notes')}<ReleaseChip pipeline={pipeline} release={target}/></div>}
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
  const lastCovered = articles.map(article => article.release).sort((left, right) => releaseTime(right).localeCompare(releaseTime(left)))[0]
  const rangeStart = (fromId ? releases.find(item => item.id === fromId) : undefined) ?? lastCovered ?? releases.find(item => item.id !== missing?.id)
  const releaseButton = (release: Release) => <button className="flow-release-secondary-button" data-i18n-ignore type="button"><span className="flow-release-button-icon"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, release.stage ?? '', release.status)} size={14}/></span>{release.name || release.version}</button>
  const agentDisabled = scope === 'single' && !missing?.issueIds.length
  return <ChangelogFeed articles={articles} pipeline={pipeline}>
    {missing && <div className="flow-release-changelog__missing">
      <div className="flow-release-changelog__missing-row">
        <span className="flow-release-changelog__missing-label">{t('Missing release notes')}</span>
        <div className="flow-release-changelog__missing-actions">
          <button className="flow-release-ghost-button" onClick={() => openNotes(missing)} type="button"><span className="flow-release-button-icon"><FlowGlyph name="document" size={14}/></span>{t('Create')}</button>
          <ScopedFlowTooltip label={agentDisabled ? t('Add issues to this release to write release notes') : undefined}><div className="flow-release-changelog__tooltip-anchor"><button className="flow-release-ghost-button" disabled={agentDisabled} onClick={() => onNavigate(`${releasePath(workspace, pipeline.slugId, missing.slugId, 'release-notes')}?agent=1`)} type="button"><span className="flow-release-button-icon"><AgentWriteGlyph size={14}/></span>{t('Write with Agent')}</button></div></ScopedFlowTooltip>
        </div>
      </div>
      <div className="flow-release-changelog__range">
        <div className="flow-release-changelog__range-slot">{menu(<button className="flow-release-secondary-button is-dropdown" type="button">{t(SCOPES.find(item => item.id === scope)?.label ?? '')}<AgentChevronDownIcon className="flow-release-button-chevron"/></button>, 'Select release notes scope', undefined, SCOPES, scope, id => setScope(id as typeof scope))}</div>
        {scope === 'lastCovered' && lastCovered && <><div className="flow-release-changelog__range-slot"><div className="flow-release-changelog__range-chip" data-i18n-ignore><ReleaseStatusIcon status={releaseStatusForStage(pipeline, lastCovered.stage ?? '', lastCovered.status)} size={14}/><span>{lastCovered.name || lastCovered.version}</span></div></div><span className="flow-release-changelog__arrow" aria-hidden="true">→</span></>}
        {scope === 'range' && rangeStart && <><div className="flow-release-changelog__range-slot">{menu(releaseButton(rangeStart), 'Select starting release', 'Select release…', releases.map(releaseOption), rangeStart.id, setFromId)}</div><span className="flow-release-changelog__arrow" aria-hidden="true">→</span></>}
        <div className="flow-release-changelog__range-slot">{picker(releaseButton(missing))}</div>
      </div>
    </div>}
    {articles.map(article => <div className="flow-release-changelog__item" key={article.release.id}><article className="flow-release-changelog__article" data-release-note-id={article.release.id} id={`release-note-${article.release.id}`}>
      <div className="flow-release-changelog__header">
        <div className="flow-release-changelog__title-row">
          <AppLink className="flow-release-changelog__title" href={releasePath(workspace, pipeline.slugId, article.release.slugId, 'release-notes')} onClick={event => { if (event.metaKey || event.ctrlKey) return; event.preventDefault(); openNotes(article.release) }} data-i18n-ignore><span>{article.title}</span></AppLink>
          <div className="flow-release-changelog__more-slot">
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild><button aria-label={t('Release notes options')} className="flow-release-ghost-button is-icon" type="button"><span className="flow-release-button-icon"><FlowGlyph name="ellipsis" size={14}/></span></button></DropdownMenu.Trigger>
              <DropdownMenu.Portal><FlowDropdownMenuContent align="end" label={t('Release notes options')}>
                {canManage && <><FlowMenuItem icon={<FlowGlyph name="edit"/>} label="Rename…" shortcut="⇧ R" onSelect={() => openNotes(article.release)}/><FlowMenuSeparator/></>}
                <FlowSubmenu icon={<FlowGlyph name="copy"/>} label="Copy">
                  <FlowMenuItem icon={<IssueActionGlyph label="Copy URL" fallback={null}/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => void copyToClipboard(notesUrl(article), t('URL copied to clipboard'), t('Could not copy URL'))}/>
                  <FlowMenuItem icon={<IssueActionGlyph label="Copy title" fallback={null}/>} label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => void copyToClipboard(article.title, t('Copied to clipboard'), t('Could not copy'))}/>
                  <FlowMenuItem icon={<IssueActionGlyph label="Copy title as link" fallback={null}/>} label="Copy title as link" shortcut="⌘ C" onSelect={() => void copyToClipboard(`[${article.title}](${notesUrl(article)})`, t('Copied to clipboard'), t('Could not copy'))}/>
                  <FlowMenuItem icon={<IssueActionGlyph label="Copy content as Markdown" fallback={null}/>} label="Copy content as Markdown" shortcut="⌘ ⌥ C" onSelect={() => void copyToClipboard(article.body, t('Copied to clipboard'), t('Could not copy'))}/>
                </FlowSubmenu>
                <FlowMenuItem icon={<FlowGlyph name="chatHistory"/>} label="Show history" onSelect={() => openNotes(article.release)}/>
                {canManage && <><FlowMenuSeparator/><FlowMenuItem icon={<FlowGlyph name="delete"/>} label="Delete" onSelect={() => { void clearNotes(article) }}/></>}
              </FlowDropdownMenuContent></DropdownMenu.Portal>
            </DropdownMenu.Root>
          </div>
        </div>
        <div className="flow-release-changelog__meta">
          {article.release.version && <div className="flow-release-changelog__meta-slot"><AppLink className="flow-release-changelog__version" href={releasePath(workspace, pipeline.slugId, article.release.slugId)} onClick={event => { if (event.metaKey || event.ctrlKey) return; event.preventDefault(); onNavigate(releasePath(workspace, pipeline.slugId, article.release.slugId)) }} data-i18n-ignore>{article.release.version}</AppLink></div>}
          {article.release.stage && <div className="flow-release-changelog__stage"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, article.release.stage, article.release.status)} size={16}/><div><span>{article.release.releasedAt ? t('Released {date}').replace('{date}', shortDate(article.release.releasedAt)) : article.release.targetDate ? <><span data-i18n-ignore>{article.release.stage}</span> · {shortDate(dayValue(article.release.targetDate))}</> : <span data-i18n-ignore>{article.release.stage}</span>}</span></div></div>}
        </div>
      </div>
      <div className="flow-release-changelog__body">{article.body.trim() ? <div className="flow-release-changelog__prose" data-i18n-ignore><IssueDescriptionEditor ariaLabel={article.title} readOnly value={article.body}/></div> : <p className="flow-release-changelog__empty">{t('No content yet.')}</p>}</div>
    </article></div>)}
  </ChangelogFeed>
}

/** The centred feed plus the reference app's left-edge table of contents (ticks that open a list on hover). */
function ChangelogFeed({ articles, pipeline, children }: { articles: Article[]; pipeline: ReleasePipeline; children: ReactNode }) {
  const { t, formatDate } = useI18n()
  const scroller = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState(0)
  useEffect(() => {
    const element = scroller.current
    if (!element) return
    const update = () => {
      const top = element.getBoundingClientRect().top + 48
      let index = 0
      articles.forEach((article, position) => { const node = document.getElementById(`release-note-${article.release.id}`); if (node && node.getBoundingClientRect().top <= top + 1) index = position })
      setActive(index)
    }
    update()
    element.addEventListener('scroll', update, { passive: true })
    return () => element.removeEventListener('scroll', update)
  }, [articles])
  return <div className="flow-release-changelog" data-pipeline={pipeline.id}>
    <div className="flow-release-changelog__scroller" ref={scroller}>
      <div className="flow-release-changelog__center"><div className="flow-release-changelog__feed">{children}</div></div>
    </div>
    {articles.length > 1 && <nav className="flow-release-changelog__toc" aria-label={t('Release notes')}>
      <div className="flow-release-changelog__ticks" aria-hidden="true"><div className="flow-release-changelog__ticks-hit"><div className="flow-release-changelog__ticks-list">
        {articles.map((article, index) => <div className="flow-release-changelog__tick" key={article.release.id}><i className={index === active ? 'is-active' : undefined}/></div>)}
      </div></div></div>
      <div className="flow-release-changelog__toc-list"><div className="flow-release-changelog__toc-card">
        {articles.map((article, index) => <a className={index === active ? 'is-active' : undefined} key={article.release.id} href={`#release-note-${article.release.id}`} onClick={event => { event.preventDefault(); document.getElementById(`release-note-${article.release.id}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' }) }}>
          <span data-i18n-ignore>{article.title}</span><span>{formatDate(dayValue(article.date), { month: 'short', day: 'numeric' })}</span>
        </a>)}
      </div></div>
    </nav>}
  </div>
}
