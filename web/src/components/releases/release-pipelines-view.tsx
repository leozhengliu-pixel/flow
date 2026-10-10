import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { MoreHorizontal, Plus } from 'lucide-react'
import { useMemo, useState } from 'react'

import { ContentViewHeaderSearch } from '@/components/content-view/content-view-header-search'
import { TeamIcon } from '@/components/issue/issue-icons'
import { useLinearRowShortcuts } from '@/components/ui/menu-shortcuts'
import { LinearDropdownMenuContent, LinearMenuItem } from '@/components/ui/row-context-menu'
import { DisplayIcon } from '@/components/ui/view-action-icons'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { toggleFavoriteFor } from '@/lib/favorites'
import { canCreateReleasePipeline } from '@/lib/settings-access'
import { newReleasePipelinePath, releasePipelinePath, releasePipelineSettingsPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, ReleasePipeline } from '@/types/flow'

import { PipelineEmptyIllustration } from './pipeline-empty-illustration'
import { ReleaseStatusIcon, SettingsGearIcon } from './release-icons'
import { PipelineRowContextMenu, type PipelineMenuActions } from './release-menus'
import { HeaderIconButton, RELEASE_DOCS_URL, ReleaseEmptyState, ReleasesHeader, ToolbarPillButton } from './release-page-chrome'
import { PIPELINE_MENU_SHORTCUTS } from './release-menu-shortcuts'
import { DisplayOptionsBody, SortHeader } from './release-display-options'
import { absoluteUrl, copyToClipboard, isFavorite } from './release-actions-model'
import { activeReleases, latestCompletedRelease } from './release-view-model'

type Ordering = 'title' | 'type' | 'latestRelease'
type Props = { data: BootstrapData; onNavigate: (path: string) => void; onOpenSidebar: () => void; onCreateRelease: (pipeline: ReleasePipeline) => void }

/** In-app Releases: the release pipeline directory (Linear ReleasePipelinesPage). */
export function ReleasePipelinesView({ data, onNavigate, onOpenSidebar, onCreateRelease }: Props) {
  const { t, formatRelative, formatDate } = useI18n()
  const [query, setQuery] = useState('')
  const [grouping, setGrouping] = useState<'none' | 'team'>('none')
  const [ordering, setOrdering] = useState<Ordering>('title')
  const [direction, setDirection] = useState<'asc' | 'desc'>('asc')
  const [properties, setProperties] = useState({ active: true, teams: true, latest: true })
  const canManage = canCreateReleasePipeline(data)
  const createPipeline = () => onNavigate(newReleasePipelinePath(data.workspace.urlKey))
  useLinearRowShortcuts(PIPELINE_MENU_SHORTCUTS)
  const pipelines = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    const latest = (pipeline: ReleasePipeline) => latestCompletedRelease(data, pipeline)?.releasedAt ?? ''
    return data.releasePipelines
      .filter(item => !needle || item.name.toLocaleLowerCase().includes(needle))
      .sort((left, right) => {
        const value = ordering === 'type' ? left.type.localeCompare(right.type) || left.name.localeCompare(right.name)
          : ordering === 'latestRelease' ? latest(left).localeCompare(latest(right))
            : left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: 'base' })
        return direction === 'asc' ? value : -value
      })
  }, [data, direction, ordering, query])
  const groups = grouping === 'team'
    ? [...[...data.teams].sort((a, b) => a.name.localeCompare(b.name)).map(team => ({ id: team.id, team, pipelines: pipelines.filter(item => item.teamIds.includes(team.id)) })), { id: 'none', team: undefined, pipelines: pipelines.filter(item => !item.teamIds.length) }].filter(group => group.pipelines.length)
    : [{ id: 'all', team: undefined, pipelines }]
  const columns = ['minmax(200px,1fr)', properties.teams && 'minmax(100px,auto)', properties.latest && '100px', '12px'].filter(Boolean).join(' ')
  const actionsFor = (pipeline: ReleasePipeline): PipelineMenuActions => ({
    onCreateRelease: () => onCreateRelease(pipeline),
    onToggleFavorite: () => { void toggleFavoriteFor(data, 'release_pipeline', pipeline.id, undefined, isFavorite(data, 'release_pipeline', pipeline.id)) },
    onCopyUrl: () => void copyToClipboard(absoluteUrl(releasePipelinePath(data.workspace.urlKey, pipeline.slugId)), t('URL copied to clipboard'), t('Could not copy URL')),
    onOpenSettings: () => onNavigate(releasePipelineSettingsPath(data.workspace.urlKey, pipeline.slugId)),
    onToggleArchive: () => onNavigate(releasePipelinePath(data.workspace.urlKey, pipeline.slugId, 'archive')),
    onOpenDeleted: () => onNavigate(releasePipelinePath(data.workspace.urlKey, pipeline.slugId, 'deleted')),
  })
  const sort = (next: Ordering) => { if (ordering === next) setDirection(value => value === 'asc' ? 'desc' : 'asc'); else { setOrdering(next); setDirection('asc') } }
  const options = <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild><HeaderIconButton label={t('Releases options')} tooltip=""><MoreHorizontal/></HeaderIconButton></DropdownMenu.Trigger>
    <DropdownMenu.Portal><LinearDropdownMenuContent label={t('Releases options')} className="flow-release-actions-menu">
      <LinearMenuItem icon={<SettingsGearIcon/>} label="Go to settings" onSelect={() => onNavigate(`/${data.workspace.urlKey}/settings/releases`)}/>
    </LinearDropdownMenuContent></DropdownMenu.Portal>
  </DropdownMenu.Root>
  return <main className="main-panel flow-releases-page" aria-label={t('Releases')}>
    <ReleasesHeader onOpenSidebar={onOpenSidebar} actions={canManage && <ScopedFlowTooltip label={t('New release pipeline')}><button className="flow-releases-new-button" aria-label={t('Create new pipeline')} onClick={createPipeline} type="button"><Plus/><span>{t('New pipeline')}</span></button></ScopedFlowTooltip>}>
      <h1 className="flow-releases-header__heading">{t('Releases')}</h1>{options}
    </ReleasesHeader>
    {data.releasePipelines.length > 0 && <div className="flow-releases-subheader">
      <ContentViewHeaderSearch alwaysVisible placeholder={t('Find release pipelines…')} value={query} onChange={setQuery} className="flow-releases-search"/>
      <Popover.Root>
        <Popover.Trigger asChild><ToolbarPillButton label={t('Display options')}><DisplayIcon/></ToolbarPillButton></Popover.Trigger>
        <Popover.Portal><Popover.Content data-flow-motion="floating" aria-label={t('Display options')} className="flow-pipeline-display-popover" align="end" collisionPadding={8} sideOffset={4}>
          <DisplayOptionsBody
            grouping={{ value: grouping, onChange: setGrouping, options: [['none', 'No grouping'], ['team', 'Team']] }}
            ordering={{ value: ordering, onChange: next => { setOrdering(next); setDirection('asc') }, options: [['title', 'Release pipeline'], ['type', 'Type'], ['latestRelease', 'Latest release']], direction, onDirection: setDirection }}
            properties={[['active', 'Active releases'], ['teams', 'Teams'], ['latest', 'Latest release']].map(([key, label]) => ({ key, label, active: properties[key as keyof typeof properties], onToggle: () => setProperties(current => ({ ...current, [key]: !current[key as keyof typeof current] })) }))}/>
        </Popover.Content></Popover.Portal>
      </Popover.Root>
    </div>}
    {!data.releasePipelines.length
      ? <ReleaseEmptyState art={<PipelineEmptyIllustration className="flow-release-art"/>} title={t('Create release pipelines')} paragraphs={[t("Create a new release pipeline to manage upcoming releases and track what's ready to ship."), t('Continuous pipelines capture changes as they deploy. Scheduled pipelines model planned releases with stages, target dates, and freezes.')]} primary={canManage ? { label: 'Create new pipeline', onClick: createPipeline } : undefined} secondary={{ label: 'Documentation', href: RELEASE_DOCS_URL }}/>
      : !pipelines.length
        ? <div className="flow-releases-no-match">{t('No matching release pipelines')}</div>
        : <div className="flow-releases-list" role="grid" aria-label={t('Release pipelines')}>
          <div className="flow-releases-list__head" role="row" style={{ gridTemplateColumns: columns }}>
            <span className="flow-pipeline-cell-title">
              <SortHeader label="Release pipeline" active={ordering === 'title'} direction={direction} ariaLabel={direction === 'asc' ? 'A–Z' : 'Z–A'} onClick={() => sort('title')}/>
              {properties.active && <span className="flow-releases-list__label is-right">{t('Active releases')}</span>}
            </span>
            {properties.teams && <span className="flow-releases-list__label">{t('Teams')}</span>}
            {properties.latest && <SortHeader label="Latest release" active={ordering === 'latestRelease'} direction={direction} ariaLabel={`${t('Order by')} ${t('Latest release')}`} onClick={() => sort('latestRelease')}/>}
            <span/>
          </div>
          {groups.map(group => <section key={group.id} className="flow-releases-list__group">
            {grouping === 'team' && <header className="flow-releases-group-header">{group.team ? <><TeamIcon team={group.team} size={14}/><strong data-i18n-ignore>{group.team.name}</strong></> : <strong>{t('No team')}</strong>}<span>{group.pipelines.length}</span></header>}
            {group.pipelines.map(pipeline => {
              const active = activeReleases(data, pipeline)
              const latest = latestCompletedRelease(data, pipeline)
              const teams = pipeline.teamIds.map(id => data.teams.find(team => team.id === id)).filter(team => team !== undefined)
              const href = releasePipelinePath(data.workspace.urlKey, pipeline.slugId)
              return <PipelineRowContextMenu key={pipeline.id} pipeline={pipeline} favorite={isFavorite(data, 'release_pipeline', pipeline.id)} canManage={canManage} actions={actionsFor(pipeline)}>
                <a className="flow-releases-row flow-pipeline-row" data-linear-menu-row="" href={href} role="row" style={{ gridTemplateColumns: columns }} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); onNavigate(href) }}>
                  <span className="flow-pipeline-cell-title">
                    <strong className="flow-pipeline-row__name" data-i18n-ignore title={pipeline.name}>{pipeline.name}</strong>
                    {properties.active && (pipeline.type === 'continuous'
                      ? <span className="flow-pipeline-row__type" title={t("Continuous pipelines don't have active releases")}>{t('Continuous')}</span>
                      : active.length > 0 && <span className="flow-pipeline-row__active">
                        {(active.length - 3 > 1 ? active.slice(0, 3) : active).map(release => <span key={release.id} className="flow-release-chip" data-i18n-ignore><ReleaseStatusIcon status={release.status} size={14}/><span>{release.name}</span></span>)}
                        {active.length - 3 > 1 && <span className="flow-release-chip">{t('+{count} releases').replace('{count}', String(active.length - 3))}</span>}
                      </span>)}
                  </span>
                  {properties.teams && <span className="flow-pipeline-row__teams">{teams.length > 0 && <ScopedFlowTooltip label={<span data-i18n-ignore>{teams.map(team => team.name).join(', ')}</span>}><span className="flow-pipeline-row__teams-label">{teams.length === 1 && <TeamIcon team={teams[0]} size={14}/>}<span data-i18n-ignore>{teams.slice(0, 2).map(team => team.key).join(', ')}{teams.length > 2 ? ` +${teams.length - 2}` : ''}</span></span></ScopedFlowTooltip>}</span>}
                  {properties.latest && <span className="flow-pipeline-row__latest" title={latest?.releasedAt ? formatDate(latest.releasedAt, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) : undefined}>{latest?.releasedAt ? formatRelative(latest.releasedAt) : ''}</span>}
                  <span/>
                </a>
              </PipelineRowContextMenu>
            })}
          </section>)}
        </div>}
  </main>
}
