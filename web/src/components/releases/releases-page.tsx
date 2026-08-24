import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import * as Select from '@radix-ui/react-select'
import { Archive, ArchiveRestore, ArrowDownWideNarrow, ArrowUpNarrowWide, Check, ChevronDown, ChevronRight, CircleDashed, Copy, FileClock, FileText, Filter, Link2, Menu, MoreHorizontal, PanelRightClose, PanelRightOpen, Pencil, Plus, Rocket, Search, Settings2, SlidersHorizontal, Star, Trash2, X } from 'lucide-react'
import { forwardRef, useEffect, useMemo, useRef, useState, type ComponentPropsWithoutRef, type CSSProperties, type ReactNode } from 'react'
import { toast } from 'sonner'

import { CalendarIcon } from '@/components/issue/issue-icons'
import { MyIssuesDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { defaultMyIssuesDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesList, type MyIssuesEditableProperty } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { toggleFilterOption, updateFilterOperator, updateFilterValues } from '@/components/my-issues/my-issues-filter-types'
import { ISSUE_FILTER_LABELS, applyExplorerFilters, buildExplorerIssueGroups, explorerFilterOptions, explorerPropertyOptions, explorerUpdateForProperty, issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { addFavorite, deleteRelease, recordRecentResource, removeFavorite, updateIssue, updateRelease } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { newReleasePipelinePath, releasePath, releasePipelinePath, releasePipelineSettingsPath, workspaceLibraryPath, type ReleasePipelineTab, type ReleaseRouteTab } from '@/lib/app-routes'
import type { BootstrapData, Release, ReleasePipeline, ReleaseResource } from '@/types/flow'

import { ReleaseEditorDialog } from './release-editor-dialog'
import { pipelineSummary, releaseProgress, releasesByStage, releasesForPipeline, releaseStatusForStage } from './release-view-model'
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
type Tab = 'releases'|'changelog'

export function ReleasesPage({ data, pipelineSlug, releaseSlug, pipelineTab, releaseTab, onOpenSidebar, onNavigate, onReload }: Props) {
  const { t } = useI18n()
  const release = data.releases.find(item => item.slugId === releaseSlug)
  const pipeline = data.releasePipelines.find(item => item.slugId === pipelineSlug || item.id === release?.pipelineId)
  const tab: Tab = pipelineTab === 'changelog' ? 'changelog' : 'releases'
  const archive = pipelineTab === 'archive'
  const [editing, setEditing] = useState<Release|undefined>()
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<Release|undefined>()
  if (releaseSlug && !release) return <ReleaseRouteNotFound title={t('Release not found')} onOpenSidebar={onOpenSidebar}/>
  if (pipelineSlug && !pipeline) return <ReleaseRouteNotFound title={t('Release pipeline not found')} onOpenSidebar={onOpenSidebar}/>
  const openPipeline = (next: ReleasePipeline, nextArchive = false, nextTab: Tab = 'releases') => onNavigate(releasePipelinePath(data.workspace.urlKey, next.slugId, nextArchive ? 'archive' : nextTab))
  const changeTab = (next: Tab) => { if (pipeline) openPipeline(pipeline, false, next) }
  const openRelease = (item: Release) => {
    const owner = data.releasePipelines.find(value => value.id === item.pipelineId) ?? pipeline
    if (!owner) return
    void recordRecentResource('release', item.id)
    onNavigate(releasePath(data.workspace.urlKey, owner.slugId, item.slugId, 'issues'))
  }
  if (release && pipeline) return <>
    <ReleaseDetailPage data={data} pipeline={pipeline} release={release} tab={releaseTab ?? 'issues'} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onEdit={() => setEditing(release)} onDelete={() => setDeleting(release)} onReload={onReload}/>
    {editing&&<ReleaseEditorDialog data={data} pipeline={pipeline} release={editing} onClose={()=>setEditing(undefined)} onSaved={async()=>{setEditing(undefined);await onReload()}}/>}
    {deleting&&<DeleteReleaseDialog release={deleting} onClose={()=>setDeleting(undefined)} onDeleted={async()=>{await deleteRelease(deleting.id);setDeleting(undefined);await onReload();openPipeline(pipeline)}}/>}
  </>
  return <>
    {pipeline ? <ReleasePipelineView data={data} pipeline={pipeline} tab={tab} archive={archive} onArchiveChange={value => openPipeline(pipeline, value, 'releases')} onCreate={() => setCreating(true)} onDelete={setDeleting} onEdit={setEditing} onOpen={openRelease} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onReload={onReload} onTabChange={changeTab}/>
      : <ReleasePipelinesView data={data} onCreate={() => onNavigate(newReleasePipelinePath(data.workspace.urlKey))} onOpen={openPipeline} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate}/>
    }
    {pipeline && creating && <ReleaseEditorDialog data={data} pipeline={pipeline} onClose={() => setCreating(false)} onSaved={async()=>{setCreating(false);await onReload()}}/>}
    {pipeline && editing && <ReleaseEditorDialog data={data} pipeline={pipeline} release={editing} onClose={() => setEditing(undefined)} onSaved={async()=>{setEditing(undefined);await onReload()}}/>}
    {deleting && <DeleteReleaseDialog release={deleting} onClose={() => setDeleting(undefined)} onDeleted={async () => { await deleteRelease(deleting.id); setDeleting(undefined); await onReload() }}/>}
    <span className="sr-only" aria-live="polite">{pipeline ? <span data-i18n-ignore>{pipeline.name}</span> : t('Releases')}</span>
  </>
}

function ReleaseRouteNotFound({title,onOpenSidebar}:{title:string;onOpenSidebar:()=>void}){return <main className="main-panel flow-releases-page"><TopBar title={<h1>{title}</h1>} onOpenSidebar={onOpenSidebar}/><EmptyState icon={<Rocket/>} title={title} description="404"/></main>}

function TopBar({ title, onOpenSidebar, children }: { title: ReactNode; onOpenSidebar: () => void; children?: ReactNode }) {
  const { t } = useI18n()
  return <header className="flow-releases-topbar"><button className="flow-releases-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Menu/></button><div className="flow-releases-heading">{title}</div><div className="flow-releases-topbar__actions">{children}</div></header>
}

function ReleasePipelinesView({ data, onCreate, onOpen, onOpenSidebar, onNavigate }: { data: BootstrapData; onCreate: () => void; onOpen: (pipeline: ReleasePipeline) => void; onOpenSidebar: () => void; onNavigate:(path:string)=>void }) {
  const { t, formatNumber } = useI18n()
  const [grouping, setGrouping] = useState<'none'|'team'>('none')
  const [order, setOrder] = useState<'position'|'type'|'latest'>('position')
  const [direction, setDirection] = useState<'asc'|'desc'>('asc')
  const [properties, setProperties] = useState({ active: true, teams: true, latest: true })
  const [displayOpen, setDisplayOpen] = useState(false)
  const [optionsOpen, setOptionsOpen] = useState(false)
  const [optionsQuery, setOptionsQuery] = useState('')
  const optionsSearchRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (!optionsOpen) return
    const frame = window.requestAnimationFrame(() => optionsSearchRef.current?.focus())
    return () => window.cancelAnimationFrame(frame)
  }, [optionsOpen])
  const compact = useMediaQuery('(max-width: 560px)')
  const pipelines = useMemo(() => [...data.releasePipelines].sort((a,b) => {
    const comparison = order === 'type' ? a.type.localeCompare(b.type)
      : order === 'latest' ? +(pipelineSummary(data, a).latest?.updatedAt ? new Date(pipelineSummary(data, a).latest!.updatedAt) : new Date(0)) - +(pipelineSummary(data, b).latest?.updatedAt ? new Date(pipelineSummary(data, b).latest!.updatedAt) : new Date(0))
      : a.position - b.position
    return direction === 'asc' ? comparison : -comparison
  }), [data, direction, order])
  const groups = grouping === 'team' ? [...data.teams.map(team => ({ id: team.id, label: team.name, pipelines: pipelines.filter(item => item.teamIds.includes(team.id)) })), { id: 'all', label: t('All teams'), pipelines: pipelines.filter(item => !item.teamIds.length) }].filter(group => group.pipelines.length) : [{ id: 'all', label: '', pipelines }]
  const columns = ['minmax(230px,1fr)', properties.active && '130px', properties.teams && '120px', properties.latest && '150px'].filter(Boolean).join(' ')
  const toggleProperty = (key: keyof typeof properties) => setProperties(current => ({ ...current, [key]: !current[key] }))
  const sortBy=(next:typeof order)=>{if(order===next)setDirection(value=>value==='asc'?'desc':'asc');else{setOrder(next);setDirection('asc')}}
  const settingsLabel = t('Go to settings')
  const showSettings = settingsLabel.toLocaleLowerCase().includes(optionsQuery.trim().toLocaleLowerCase())
  const releasesOptions = <DropdownMenu.Root open={optionsOpen} onOpenChange={open => { setOptionsOpen(open); if (!open) setOptionsQuery('') }}><DropdownMenu.Trigger asChild><IconButton className="flow-releases-directory-options" label={t('Releases options')}><MoreHorizontal/></IconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-releases-directory-menu" align="start" sideOffset={5}><label className="flow-releases-directory-menu__search"><Search/><input ref={optionsSearchRef} aria-label={t('Filter…')} onChange={event => setOptionsQuery(event.target.value)} onKeyDown={event => { if (event.key !== 'Escape') event.stopPropagation() }} placeholder={t('Filter…')} role="searchbox" value={optionsQuery}/></label>{showSettings ? <MenuItem icon={<Settings2/>} onSelect={() => onNavigate(`/${data.workspace.urlKey}/settings/releases`)}>{settingsLabel}</MenuItem> : <div className="flow-releases-directory-menu__empty">{t('No results')}</div>}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  return <main className="main-panel flow-releases-page" aria-label={t('Releases')}>
    <TopBar title={<div className="flow-releases-directory-title"><h1>{t('Releases')}</h1>{releasesOptions}</div>} onOpenSidebar={onOpenSidebar}>
      <IconButton className="flow-new-pipeline-button" label={t('Create new pipeline')} onClick={onCreate}><Plus/><span>{t('New pipeline')}</span></IconButton>
    </TopBar>
    <div className="flow-pipelines-summary"><span>{formatNumber(pipelines.length)} {t(pipelines.length === 1 ? 'release pipeline' : 'release pipelines')}</span><Popover.Root open={displayOpen} onOpenChange={setDisplayOpen}><Popover.Trigger asChild><button aria-label={t('Display options')} aria-pressed={displayOpen}><SlidersHorizontal/></button></Popover.Trigger><Popover.Portal><Popover.Content aria-label={t('Display options')} className="flow-pipeline-display-popover" align="end" collisionPadding={8} sideOffset={4}>
      <div className="flow-pipeline-display-config">
        <div className="flow-pipeline-display-row"><span>{t('Grouping')}</span><ReleaseDisplaySelect ariaLabel={t('Grouping')} className="is-grouping" onChange={setGrouping} options={[['none',t('No grouping')],['team',t('Team')]]} value={grouping}/></div>
        <div className="flow-pipeline-display-row"><span>{t('Ordering')}</span><div className="flow-pipeline-display-order"><button aria-label={t(direction === 'asc' ? 'Ascending' : 'Descending')} onClick={() => setDirection(value => value === 'asc' ? 'desc' : 'asc')} type="button">{direction === 'asc' ? <ArrowDownWideNarrow/> : <ArrowUpNarrowWide/>}</button><ReleaseDisplaySelect ariaLabel={t('Ordering')} className="is-ordering" onChange={setOrder} options={[['position',t('Release pipeline')],['type',t('Type')],['latest',t('Latest release')]]} value={order}/></div></div>
      </div>
      <div className="flow-pipeline-display-properties"><span>{t('Display properties')}</span><div>{([['active','Active releases'],['teams','Teams'],['latest','Latest release']] as const).map(([key,label])=><button aria-pressed={properties[key]} data-active={properties[key]||undefined} key={key} onClick={()=>toggleProperty(key)} type="button">{t(label)}</button>)}</div></div>
    </Popover.Content></Popover.Portal></Popover.Root></div>
    {pipelines.length ? <div className="flow-pipelines-table"><div className="flow-pipeline-head" style={compact ? undefined : { gridTemplateColumns: columns }}><button aria-label={`${t('Order by')} ${t('Release pipeline')}`} onClick={()=>sortBy('position')}>{t('Release pipeline')}{order==='position'&&(direction==='asc'?<ArrowDownWideNarrow/>:<ArrowUpNarrowWide/>)}</button>{properties.active&&<span>{t('Active releases')}</span>}{properties.teams&&<span>{t('Teams')}</span>}{properties.latest&&<button aria-label={`${t('Order by')} ${t('Latest release')}`} onClick={()=>sortBy('latest')}>{t('Latest release')}{order==='latest'&&(direction==='asc'?<ArrowDownWideNarrow/>:<ArrowUpNarrowWide/>)}</button>}</div>{groups.map(group => <section className="flow-pipeline-team-group" key={group.id}>{group.label&&<header data-i18n-ignore>{group.label}</header>}{group.pipelines.map(item => { const summary=pipelineSummary(data,item);return <button className="flow-pipeline-row" style={compact ? undefined : { gridTemplateColumns: columns }} key={item.id} onClick={()=>onOpen(item)}><span><Rocket/><strong data-i18n-ignore>{item.name}</strong></span>{properties.active&&<span>{item.type==='continuous'?t('Continuous'):summary.active || '—'}</span>}{properties.teams&&<span className="flow-release-team-stack">{item.teamIds.slice(0,3).map(id=>{const team=data.teams.find(value=>value.id===id);return team?<i key={id} title={team.name} style={{background:team.color}} data-i18n-ignore>{team.key.slice(0,1)}</i>:null})}{!item.teamIds.length&&<em>{t('All teams')}</em>}</span>}{properties.latest&&<span data-i18n-ignore>{summary.latest?.name || '—'}</span>}<ChevronRight/></button>})}</section>)}</div> : <EmptyState icon={<Rocket/>} title={t('No release pipelines')} description={t('Create a release pipeline to start tracking what ships.')} action={<button className="flow-releases-primary" onClick={onCreate}>{t('Create release pipeline')}</button>}/>}
  </main>
}

function ReleaseDisplaySelect<T extends string>({ ariaLabel, className, onChange, options, value }: { ariaLabel:string;className:string;onChange:(value:T)=>void;options:readonly (readonly [T,string])[];value:T }) {
  return <Select.Root onValueChange={next => onChange(next as T)} value={value}><Select.Trigger aria-label={ariaLabel} className="flow-pipeline-display-select"><Select.Value/><Select.Icon><ChevronDown/></Select.Icon></Select.Trigger><Select.Portal><Select.Content align="end" className={`flow-pipeline-display-select-menu ${className}`} collisionPadding={8} position="popper" side="bottom" sideOffset={4}><Select.Viewport>{options.map(([option,label])=><Select.Item className="flow-pipeline-display-select-item" key={option} value={option}><Select.ItemText>{label}</Select.ItemText><Select.ItemIndicator><Check/></Select.ItemIndicator></Select.Item>)}</Select.Viewport></Select.Content></Select.Portal></Select.Root>
}

function ReleasePipelineView({ data, pipeline, tab, archive, onArchiveChange, onCreate, onDelete, onEdit, onOpen, onNavigate, onOpenSidebar, onReload, onTabChange }: { data:BootstrapData;pipeline:ReleasePipeline;tab:Tab;archive:boolean;onArchiveChange:(value:boolean)=>void;onCreate:()=>void;onDelete:(release:Release)=>void;onEdit:(release:Release)=>void;onOpen:(release:Release)=>void;onNavigate:(path:string)=>void;onOpenSidebar:()=>void;onReload:()=>Promise<void>;onTabChange:(value:Tab)=>void }) {
  const { t } = useI18n()
  const releases = releasesForPipeline(data,pipeline,archive)
  const changelog = releasesForPipeline(data,pipeline).filter(item=>item.status==='released')
  const favorite = data.favorites.some(item=>item.resourceType==='release_pipeline'&&item.resourceId===pipeline.id)
  const toggleFavorite = async()=>{try{if(favorite)await removeFavorite('release_pipeline',pipeline.id);else await addFavorite('release_pipeline',pipeline.id);await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not update favorite'))}}
  const copyUrl=async()=>{try{await navigator.clipboard.writeText(window.location.href);toast.success(t('URL copied'))}catch{toast.error(t('Could not copy URL'))}}
  const pipelineMenu = <DropdownMenu.Root><DropdownMenu.Trigger asChild><IconButton label={t('Pipeline options')}><MoreHorizontal/></IconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-pipeline-options" align="start" sideOffset={4}>{pipeline.type==='scheduled'&&<MenuItem icon={<Plus/>} onSelect={onCreate}>{t('Create release')}</MenuItem>}<MenuItem icon={<Star fill={favorite?'currentColor':'none'}/>} onSelect={()=>void toggleFavorite()}>{t(favorite?'Remove from favorites':'Favorite')}</MenuItem><MenuItem icon={<Copy/>} onSelect={()=>void copyUrl()}>{t('Copy URL')}</MenuItem><DropdownMenu.Separator/><MenuItem icon={<Settings2/>} onSelect={()=>onNavigate(releasePipelineSettingsPath(data.workspace.urlKey,pipeline.slugId))}>{t('Pipeline settings')}</MenuItem><DropdownMenu.Separator/><MenuItem icon={<Archive/>} onSelect={()=>onArchiveChange(!archive)}>{t(archive?'View active releases':'Open archive')}</MenuItem><MenuItem icon={<Trash2/>} onSelect={()=>onNavigate(`${workspaceLibraryPath(data.workspace.urlKey,'deleted')}?resource=release`)}>{t('View recently deleted releases')}</MenuItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  return <main className="main-panel flow-releases-page" aria-label={`${pipeline.name} ${t('Releases')}`}>
    <TopBar onOpenSidebar={onOpenSidebar} title={<div className="flow-release-title"><h1 data-i18n-ignore>{pipeline.name}</h1><div className="flow-release-title-actions"><IconButton aria-pressed={favorite} label={t(favorite?'Remove from favorites':'Favorite')} onClick={()=>void toggleFavorite()}><Star fill={favorite?'currentColor':'none'}/></IconButton>{pipelineMenu}</div></div>}>
      {!archive&&pipeline.type==='scheduled'&&<IconButton label={t('Create new release')} onClick={onCreate}><Plus/></IconButton>}
    </TopBar>
    <div className="flow-release-toolbar"><div className="flow-release-tabs"><button className={tab==='releases'?'active':''} aria-selected={tab==='releases'} onClick={()=>onTabChange('releases')}>{t(archive?'Archive':'Releases')}</button>{!archive&&<button className={tab==='changelog'?'active':''} aria-selected={tab==='changelog'} onClick={()=>onTabChange('changelog')}>{t('Changelog')}</button>}</div><span>{archive?t('Archived releases'):t(`${releases.length} releases`)}</span></div>
    {tab==='changelog'&&!archive?<ReleaseChangelog pipeline={pipeline} releases={changelog} onOpen={onOpen}/>:<ReleaseList data={data} pipeline={pipeline} releases={releases} archived={archive} onCreate={onCreate} onDelete={onDelete} onEdit={onEdit} onOpen={onOpen} onReload={onReload}/>}
  </main>
}

function ReleaseList({data,pipeline,releases,archived,onCreate,onDelete,onEdit,onOpen,onReload}:{data:BootstrapData;pipeline:ReleasePipeline;releases:Release[];archived:boolean;onCreate:()=>void;onDelete:(release:Release)=>void;onEdit:(release:Release)=>void;onOpen:(release:Release)=>void;onReload:()=>Promise<void>}) {
  const {t,formatDate}=useI18n()
  const [collapsed,setCollapsed]=useState<string[]>([])
  const [properties,setProperties]=useState({description:true,version:true,notes:true,date:true,completion:true})
  const [grouping,setGrouping]=useState<'none'|'stage'>('stage')
  const [order,setOrder]=useState<'position'|'date'|'name'>('date')
  const [direction,setDirection]=useState<'asc'|'desc'>('desc')
  const [displayOpen,setDisplayOpen]=useState(false)
  const compact=useMediaQuery('(max-width: 560px)')
  const sorted=[...releases].sort((left,right)=>{const comparison=order==='name'?left.name.localeCompare(right.name):order==='date'?(left.targetDate??'').localeCompare(right.targetDate??''):left.position-right.position;return direction==='asc'?comparison:-comparison})
  const groups=(grouping==='stage'?releasesByStage(sorted,pipeline):[{stage:'',releases:sorted}])
  const toggleGroup=(stage:string)=>setCollapsed(current=>current.includes(stage)?current.filter(value=>value!==stage):[...current,stage])
  const sortBy=(next:typeof order)=>{if(order===next)setDirection(value=>value==='asc'?'desc':'asc');else{setOrder(next);setDirection(next==='date'?'desc':'asc')}}
  const columns=['minmax(260px,1fr)',properties.notes&&'150px',properties.date&&'120px',properties.completion&&'110px','30px'].filter(Boolean).join(' ')
  if(!releases.length)return <EmptyState icon={archived?<Archive/>:<Rocket/>} title={t(archived?'No archived releases':pipeline.type==='continuous'?'Awaiting first release':'Create your first release')} description={t(archived?'Archived releases will appear here.':pipeline.type==='continuous'?'Releases added to this pipeline will appear here. Integrate with your CI/CD tool to automatically group issues into releases.':"Create a new release to start tracking what's shipping.")} action={!archived&&pipeline.type==='scheduled'?<button className="flow-releases-primary" onClick={onCreate}>{t('Create new release')}</button>:pipeline.type==='continuous'?<a className="flow-releases-primary flow-releases-doc-link" href="https://linear.app/docs/releases#example-for-continuous-deployments" target="_blank" rel="noreferrer">{t('Set up integration')}</a>:undefined}/>
  return <div className="flow-release-list">
    <div className="flow-release-list-options"><Popover.Root open={displayOpen} onOpenChange={setDisplayOpen}><Popover.Trigger asChild><IconButton label={t('Display options')}><SlidersHorizontal/></IconButton></Popover.Trigger><Popover.Portal><Popover.Content aria-label={t('Display options')} className="flow-pipeline-display-popover flow-release-list-display-popover" align="end" collisionPadding={8} sideOffset={4}><div className="flow-pipeline-display-config"><div className="flow-pipeline-display-row"><span>{t('Grouping')}</span><ReleaseDisplaySelect ariaLabel={t('Grouping')} className="is-grouping" onChange={setGrouping} options={[['none',t('No grouping')],['stage',t('Stage')]]} value={grouping}/></div><div className="flow-pipeline-display-row"><span>{t('Ordering')}</span><div className="flow-pipeline-display-order"><button aria-label={t(direction==='asc'?'Ascending':'Descending')} onClick={()=>setDirection(value=>value==='asc'?'desc':'asc')} type="button">{direction==='asc'?<ArrowDownWideNarrow/>:<ArrowUpNarrowWide/>}</button><ReleaseDisplaySelect ariaLabel={t('Ordering')} className="is-ordering" onChange={setOrder} options={[['date',t('Release date')],['position',t('Release')],['name',t('Name')]]} value={order}/></div></div></div><div className="flow-pipeline-display-properties"><span>{t('Display properties')}</span><div>{([['description','Description'],['version','Version'],['date','Release date'],['completion','Completion'],['notes','Release notes']] as const).map(([key,label])=><button aria-pressed={properties[key]} data-active={properties[key]||undefined} key={key} onClick={()=>setProperties(current=>({...current,[key]:!current[key]}))} type="button">{t(label)}</button>)}</div></div></Popover.Content></Popover.Portal></Popover.Root></div>
    <div className="flow-release-columns" style={compact ? undefined : {gridTemplateColumns:columns}}><button aria-label={`${t('Order by')} ${t('Release')}`} onClick={()=>sortBy('name')}>{t('Release')}{order==='name'&&(direction==='asc'?<ArrowDownWideNarrow/>:<ArrowUpNarrowWide/>)}</button>{properties.notes&&<span>{t('Release notes')}</span>}{properties.date&&<button aria-label={`${t('Order by')} ${t('Release date')}`} onClick={()=>sortBy('date')}>{t('Release date')}{order==='date'&&(direction==='asc'?<ArrowDownWideNarrow/>:<ArrowUpNarrowWide/>)}</button>}{properties.completion&&<span>{t('Completion')}</span>}<span/></div>
    {groups.map(group=>group.releases.length?<section className="flow-release-group" key={group.stage||'all'}>{grouping==='stage'&&<header><button aria-expanded={!collapsed.includes(group.stage)} onClick={()=>toggleGroup(group.stage)}><ChevronDown/><CircleDashed/><strong data-i18n-ignore>{group.stage}</strong><span>{group.releases.length}</span></button></header>}{!collapsed.includes(group.stage)&&group.releases.map(item=><div className="flow-release-row" key={item.id} style={compact ? undefined : {gridTemplateColumns:columns}}><button className="flow-release-row__main" onClick={()=>onOpen(item)}><CircleDashed/><span data-i18n-ignore><strong>{item.name}</strong>{properties.version&&item.version&&<em>{item.version}</em>}{properties.description&&item.description&&<small>{item.description}</small>}</span></button>{properties.notes&&<span>{item.releaseNotes?t('Ready'):'—'}</span>}{properties.date&&<span className="flow-release-row__date"><CalendarIcon/>{item.targetDate?formatDate(item.targetDate,{month:'short',day:'numeric'}):'—'}</span>}{properties.completion&&<span className="flow-release-progress"><i style={{'--progress':`${releaseProgress(data,item)}%`} as CSSProperties}/>{releaseProgress(data,item)}%</span>}<ReleaseRowMenu archived={archived} item={item} onDelete={()=>onDelete(item)} onEdit={()=>onEdit(item)} onRestore={async()=>{await updateRelease(item.id,{archived:false});await onReload()}}/></div>)}</section>:null)}
  </div>
}

function ReleaseRowMenu({item,archived,onEdit,onRestore,onDelete}:{item:Release;archived:boolean;onEdit:()=>void;onRestore:()=>Promise<void>;onDelete:()=>void}){const{t}=useI18n();return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="flow-release-row-menu" aria-label={`${t('Open actions')} ${item.name}`}><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu" align="end" sideOffset={5}>{!archived&&<MenuItem icon={<Settings2/>} onSelect={onEdit}>{t('Edit release')}</MenuItem>}{archived&&<MenuItem icon={<ArchiveRestore/>} onSelect={()=>void onRestore()}>{t('Restore')}</MenuItem>}<DropdownMenu.Separator/><MenuItem danger icon={<Trash2/>} onSelect={onDelete}>{t('Delete release')}</MenuItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}

function ReleaseChangelog({pipeline,releases,onOpen}:{pipeline:ReleasePipeline;releases:Release[];onOpen:(release:Release)=>void}){const{t,formatDate}=useI18n();if(!releases.length)return <EmptyState icon={<FileClock/>} title={t('No release notes yet')} description={t('Completed releases and their release notes will appear here.')}/>;const missing=releases.find(item=>!item.releaseNotes);if(missing)return <EmptyState icon={<FileClock/>} title={t('Missing release notes')} description={<><span data-i18n-ignore>{missing.name}</span> {t('Create release notes for this release or select another release.')}</>} action={<button className="flow-releases-primary" onClick={()=>onOpen(missing)}>{t('Create release notes')}</button>}/>;return <div className="flow-release-changelog" aria-label={pipeline.name}>{releases.map(item=><article key={item.id}><span>{item.version?<span data-i18n-ignore>{item.version}</span>:t('Release')}</span><div><h2 data-i18n-ignore>{item.name}</h2><p data-i18n-ignore>{item.releaseNotes}</p></div><time>{formatDate(item.releasedAt||item.targetDate||item.updatedAt,{month:'short',day:'numeric',year:'numeric'})}</time></article>)}</div>}

function ReleaseDetailPage({data,pipeline,release,tab,onNavigate,onOpenSidebar,onDelete,onEdit,onReload}:{data:BootstrapData;pipeline:ReleasePipeline;release:Release;tab:ReleaseRouteTab;onNavigate:(path:string)=>void;onOpenSidebar:()=>void;onDelete:()=>void;onEdit:()=>void;onReload:()=>Promise<void>}) {
  const {t,formatDate}=useI18n()
  const [notes,setNotes]=useState(release.releaseNotes??'')
  const [savedNotes,setSavedNotes]=useState(release.releaseNotes??'')
  const [savingNotes,setSavingNotes]=useState(false)
  const [stage,setStage]=useState(release.stage??'')
  const [detailsOpen,setDetailsOpen]=useState(true)
  const [filters,setFilters]=useState<MyIssuesAppliedFilter[]>([])
  const [filterOpen,setFilterOpen]=useState(false)
  const [displayOpen,setDisplayOpen]=useState(false)
  const [releaseMenuQuery,setReleaseMenuQuery]=useState('')
  const [display,setDisplay]=useState<MyIssuesDisplayOptions>(()=>({...defaultMyIssuesDisplayOptions,grouping:'status',completedWindow:'all',properties:new Set(defaultMyIssuesDisplayOptions.properties)}))
  const [collapsedGroups,setCollapsedGroups]=useState<Set<string>>(new Set())
  const [linkOpen,setLinkOpen]=useState(false)
  const [documentOpen,setDocumentOpen]=useState(false)
  const [detailTab,setDetailTab]=useState<'assignees'|'labels'|'priority'|'projects'>('assignees')
  const changeFilterOpen=(open:boolean)=>{setFilterOpen(open);if(open)setDisplayOpen(false)}
  const changeDisplayOpen=(open:boolean)=>{setDisplayOpen(open);if(open)setFilterOpen(false)}
  const issues=data.issues.filter(issue=>release.issueIds.includes(issue.id))
  const issueOptions=useMemo(()=>explorerPropertyOptions(data,issues),[data,issues])
  const visibleIssues=useMemo(()=>applyExplorerFilters(issues,filters,data),[data,filters,issues])
  const issueRows=useMemo(()=>visibleIssues.map(issue=>issueToExplorerRow(issue,data.workspace.urlKey,data.issues,data)),[data,visibleIssues])
  const issueGroups=useMemo(()=>buildExplorerIssueGroups(issueRows,display,data),[data,display,issueRows])
  const favorite=data.favorites.some(item=>item.resourceType==='release'&&item.resourceId===release.id)
  const toggleFavorite=async()=>{try{if(favorite)await removeFavorite('release',release.id);else await addFavorite('release',release.id);await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not update favorite'))}}
  const copyUrl=async()=>{try{await navigator.clipboard.writeText(window.location.href);toast.success(t('URL copied'))}catch{toast.error(t('Could not copy URL'))}}
  const updateStage=async(value:string)=>{try{setStage(value);await updateRelease(release.id,{stage:value,status:releaseStatusForStage(pipeline,value,release.status)});await onReload()}catch(error){setStage(release.stage??'');toast.error(error instanceof Error?error.message:t('Could not save release'))}}
  const saveNotes=async()=>{setSavingNotes(true);try{await updateRelease(release.id,{releaseNotes:notes});setSavedNotes(notes);await onReload();toast.success(t('Release notes saved'))}catch(error){toast.error(error instanceof Error?error.message:t('Could not save release notes'))}finally{setSavingNotes(false)}}
  const route=(next:ReleaseRouteTab)=>onNavigate(releasePath(data.workspace.urlKey,pipeline.slugId,release.slugId,next))
  const resources=release.resources??[]
  const detailValues=detailTab==='assignees'?[...new Map(issues.filter(issue=>issue.assignee).map(issue=>[issue.assignee!.id,{id:issue.assignee!.id,label:issue.assignee!.displayName,count:issues.filter(item=>item.assignee?.id===issue.assignee!.id).length}])).values()]:detailTab==='labels'?[...new Map(issues.flatMap(issue=>issue.labels).map(label=>[label.id,{id:label.id,label:label.name,count:issues.filter(issue=>issue.labels.some(item=>item.id===label.id)).length}])).values()]:detailTab==='priority'?[...new Map(issues.map(issue=>[String(issue.priority),{id:String(issue.priority),label:issue.priorityLabel,count:issues.filter(item=>item.priority===issue.priority).length}])).values()]:[...new Map(issues.filter(issue=>issue.project).map(issue=>[issue.project!.id,{id:issue.project!.id,label:issue.project!.name,count:issues.filter(item=>item.project?.id===issue.project!.id).length}])).values()]
  const saveResources=async(next:ReleaseResource[])=>{try{await updateRelease(release.id,{resources:next});await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not save release'))}}
  const toggleIssueFilter=(field:MyIssuesFilterKey,option:MyIssuesFilterOption)=>{const label=ISSUE_FILTER_LABELS[field];if(label)setFilters(current=>toggleFilterOption(current,field,label,option))}
  const changeIssueProperty=async(row:ReturnType<typeof issueToExplorerRow>,property:MyIssuesEditableProperty,value:string|string[])=>{const input=explorerUpdateForProperty(property,value);if(!input)return;try{await updateIssue(row.id,input);await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not update issue'))}}
  const menuMatches=(label:string)=>t(label).toLocaleLowerCase().includes(releaseMenuQuery.trim().toLocaleLowerCase())
  const releaseMenu=<DropdownMenu.Root onOpenChange={open=>{if(!open)setReleaseMenuQuery('')}}><DropdownMenu.Trigger asChild><IconButton label={t('Release options')}><MoreHorizontal/></IconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-release-options" align="start" sideOffset={4}><input className="flow-release-options__filter" aria-label={t('Filter…')} autoFocus onChange={event=>setReleaseMenuQuery(event.target.value)} onKeyDown={event=>{if(event.key!=='Escape')event.stopPropagation()}} placeholder={t('Filter…')} role="searchbox" value={releaseMenuQuery}/>{menuMatches('Edit…')&&<MenuItem icon={<Pencil/>} onSelect={onEdit}>{t('Edit…')}</MenuItem>}{menuMatches('Stage')&&<DropdownMenu.Sub><DropdownMenu.SubTrigger className="flow-release-menu-item"><CircleDashed/><span>{t('Stage')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="flow-releases-menu flow-release-stage-menu" sideOffset={4}>{pipeline.stages.map(value=><MenuItem icon={value===stage?<Check/>:<CircleDashed/>} key={value} onSelect={()=>void updateStage(value)}><span data-i18n-ignore>{value}</span></MenuItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}<DropdownMenu.Separator/>{menuMatches('Add issues to release…')&&<MenuItem icon={<Plus/>} shortcut="⌥ R" onSelect={onEdit}>{t('Add issues to release…')}</MenuItem>}{menuMatches('Add document…')&&<MenuItem icon={<FileText/>} onSelect={()=>setDocumentOpen(true)}>{t('Add document…')}</MenuItem>}{menuMatches('Add link…')&&<MenuItem icon={<Link2/>} shortcut="Ctrl L" onSelect={()=>setLinkOpen(true)}>{t('Add link…')}</MenuItem>}<DropdownMenu.Separator/>{menuMatches(favorite?'Remove from favorites':'Favorite')&&<MenuItem icon={<Star fill={favorite?'currentColor':'none'}/>} shortcut="⌥ F" onSelect={()=>void toggleFavorite()}>{t(favorite?'Remove from favorites':'Favorite')}</MenuItem>}{menuMatches('Copy URL')&&<MenuItem icon={<Copy/>} shortcut="⌘ ⇧ ," onSelect={()=>void copyUrl()}>{t('Copy URL')}</MenuItem>}<DropdownMenu.Separator/>{menuMatches('Delete')&&<MenuItem danger icon={<Trash2/>} onSelect={onDelete}>{t('Delete')}</MenuItem>}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  return <main className="main-panel flow-release-page" aria-label={`${pipeline.name} ${release.name}`}>
    <TopBar onOpenSidebar={onOpenSidebar} title={<div className="flow-release-breadcrumb"><button data-i18n-ignore onClick={()=>onNavigate(releasePipelinePath(data.workspace.urlKey,pipeline.slugId))}>{pipeline.name}</button><ChevronRight/><h1 data-i18n-ignore>{release.name}</h1><div className="flow-release-title-actions"><IconButton aria-pressed={favorite} label={t(favorite?'Remove from favorites':'Favorite')} onClick={()=>void toggleFavorite()}><Star fill={favorite?'currentColor':'none'}/></IconButton>{releaseMenu}</div></div>}/>
    <div className="flow-release-page-toolbar" role="tablist" onKeyDown={event=>{if(!['ArrowLeft','ArrowRight'].includes(event.key))return;event.preventDefault();route(tab==='issues'?'release-notes':'issues')}}><button aria-selected={tab==='issues'} onClick={()=>route('issues')} role="tab">{t('Issues')}</button><button aria-selected={tab==='release-notes'} onClick={()=>route('release-notes')} role="tab">{t('Release notes')}</button><span/>{tab==='issues'&&<><MyIssuesFilterMenu open={filterOpen} onOpenChange={changeFilterOpen} filters={filters} options={field=>explorerFilterOptions(field,issueOptions)} onToggle={toggleIssueFilter} trigger={<button aria-label={t('Add filter')}><Filter/></button>}/><MyIssuesDisplayMenu open={displayOpen} onOpenChange={changeDisplayOpen} options={display} onChange={setDisplay}/></>}<button aria-expanded={detailsOpen} aria-label={t(detailsOpen?'Close release details':'Open release details')} onClick={()=>setDetailsOpen(value=>!value)}>{detailsOpen?<PanelRightClose/>:<PanelRightOpen/>}</button></div>
    <div className={`flow-release-page-body${detailsOpen?' has-details':''}`}>
      <section className="flow-release-page-content">
        {tab==='issues'?<div className="flow-release-issue-list">{filters.length>0&&<MyIssuesFilterBar filters={filters} filterOptions={filter=>explorerFilterOptions(filter.field,issueOptions)} onAdd={()=>setFilterOpen(true)} onClear={()=>setFilters([])} onOperatorChange={(id,operator)=>setFilters(current=>updateFilterOperator(current,id,operator))} onRemove={id=>setFilters(current=>current.filter(filter=>filter.id!==id))} onValuesChange={(id,options)=>setFilters(current=>updateFilterValues(current,id,options))}/>} {issues.length?<MyIssuesList groups={issueGroups} collapsedGroupIds={collapsedGroups} displayProperties={display.properties} nestedSubIssues={display.nestedSubIssues} propertyOptions={issueOptions} onGroupCollapsedChange={(id,collapsed)=>setCollapsedGroups(current=>{const next=new Set(current);if(collapsed)next.add(id);else next.delete(id);return next})} onOpenIssue={row=>onNavigate(row.href??`/${data.workspace.urlKey}/issue/${row.identifier}`)} onPropertyChange={changeIssueProperty}/>:<EmptyState icon={<Rocket/>} title={<span data-i18n-ignore>{release.name}</span>} description={t('No issues in this release yet.')} action={<button className="flow-releases-primary" onClick={onEdit}>{t('Add issues')}</button>}/>}</div>:<div className="flow-release-detail-notes"><textarea aria-label={t('Release notes')} value={notes} onChange={event=>setNotes(event.target.value)} placeholder={t('Write release notes…')}/><footer><button disabled={savingNotes||notes===savedNotes} onClick={()=>void saveNotes()}>{t(savingNotes?'Saving…':'Save release notes')}</button></footer></div>}
      </section>
      {detailsOpen&&<aside className="flow-release-page-details" aria-label={t('Release details')}><header><strong data-i18n-ignore>{release.name}</strong><IconButton aria-pressed={favorite} label={t(favorite?'Remove from favorites':'Favorite')} onClick={()=>void toggleFavorite()}><Star fill={favorite?'currentColor':'none'}/></IconButton>{releaseMenu}</header><div className="flow-release-detail-meta"><DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="flow-release-detail-stage"><CircleDashed/><span data-i18n-ignore>{stage||pipeline.stages[0]}</span><ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-release-stage-menu" align="start">{pipeline.stages.map(value=><MenuItem icon={value===stage?<Check/>:<CircleDashed/>} key={value} onSelect={()=>void updateStage(value)}><span data-i18n-ignore>{value}</span></MenuItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>{release.targetDate?<time>{formatDate(release.targetDate,{month:'short',day:'numeric',year:'numeric'})}</time>:<button className="flow-release-add-date" onClick={onEdit}>{t('Release date')}</button>}</div>{release.version&&<p data-i18n-ignore>{release.version}</p>}{release.description&&<p className="flow-release-detail-description" data-i18n-ignore>{release.description}</p>}<div className="flow-release-resources">{resources.map(resource=>{const document=data.documents.find(item=>item.id===resource.documentId);return <div key={resource.id}>{resource.type==='document'?<FileText/>:<Link2/>}<a href={resource.url||`/${data.workspace.urlKey}/document/${document?.slugId||resource.documentId}`} data-i18n-ignore>{resource.title}</a><button aria-label={t('Remove')} onClick={()=>void saveResources(resources.filter(item=>item.id!==resource.id))}><X/></button></div>})}<button onClick={()=>setLinkOpen(true)}><Plus/>{t('Add document or link…')}</button></div>{issues.length>0&&<><div className="flow-release-detail-property-tabs" role="tablist" onKeyDown={event=>{if(!['ArrowLeft','ArrowRight'].includes(event.key))return;event.preventDefault();const values=['assignees','labels','priority','projects'] as const;const index=values.indexOf(detailTab);setDetailTab(values[(index+(event.key==='ArrowRight'?1:values.length-1))%values.length])}}>{(['assignees','labels','priority','projects'] as const).map(value=><button aria-selected={detailTab===value} key={value} onClick={()=>setDetailTab(value)} role="tab">{t(value==='assignees'?'Assignees':value==='labels'?'Labels':value==='priority'?'Priority':'Projects')}</button>)}</div><div className="flow-release-detail-property-values">{detailValues.map(value=><div key={value.id}><span data-i18n-ignore>{value.label}</span><b>{value.count}</b></div>)}{!detailValues.length&&<p>{t('None')}</p>}</div></>}</aside>}
    </div>
    {linkOpen&&<ReleaseLinkDialog onClose={()=>setLinkOpen(false)} onSave={async resource=>{await saveResources([...resources,resource]);setLinkOpen(false)}}/>}
    {documentOpen&&<ReleaseDocumentDialog data={data} onClose={()=>setDocumentOpen(false)} onSave={async resource=>{await saveResources([...resources,resource]);setDocumentOpen(false)}}/>}
  </main>
}
function ReleaseLinkDialog({onClose,onSave}:{onClose:()=>void;onSave:(resource:ReleaseResource)=>Promise<void>}){const{t}=useI18n();const[title,setTitle]=useState('');const[url,setUrl]=useState('');const[busy,setBusy]=useState(false);return <BasicDialog title={t('Add link')} onClose={onClose}><label>{t('Title')}<input autoFocus value={title} onChange={event=>setTitle(event.target.value)}/></label><label>{t('URL')}<input type="url" value={url} onChange={event=>setUrl(event.target.value)}/></label><footer><button onClick={onClose}>{t('Cancel')}</button><button className="primary" disabled={busy||!title.trim()||!url.trim()} onClick={()=>{setBusy(true);void onSave({id:`release_resource_${Date.now()}`,type:'link',title:title.trim(),url:url.trim(),createdAt:new Date().toISOString()}).catch(error=>{setBusy(false);toast.error(error instanceof Error?error.message:t('Could not save release'))})}}>{t('Add link')}</button></footer></BasicDialog>}
function ReleaseDocumentDialog({data,onClose,onSave}:{data:BootstrapData;onClose:()=>void;onSave:(resource:ReleaseResource)=>Promise<void>}){const{t}=useI18n();const[query,setQuery]=useState('');const[busy,setBusy]=useState(false);const documents=data.documents.filter(item=>item.title.toLowerCase().includes(query.toLowerCase()));return <BasicDialog title={t('Add document')} onClose={onClose}><label>{t('Search')}<input autoFocus value={query} onChange={event=>setQuery(event.target.value)} placeholder={t('Search documents…')}/></label><div className="flow-release-document-results">{documents.map(document=><button disabled={busy} key={document.id} onClick={()=>{setBusy(true);void onSave({id:`release_resource_${Date.now()}`,type:'document',title:document.title,documentId:document.id,createdAt:new Date().toISOString()}).catch(error=>{setBusy(false);toast.error(error instanceof Error?error.message:t('Could not save release'))})}}><FileText/><span data-i18n-ignore>{document.title}</span></button>)}{!documents.length&&<p>{t('No documents found')}</p>}</div></BasicDialog>}
function DeleteReleaseDialog({release,onClose,onDeleted}:{release:Release;onClose:()=>void;onDeleted:()=>Promise<void>}){const{t}=useI18n();const[busy,setBusy]=useState(false);return <BasicDialog title={t('Delete release')} onClose={onClose}><p>{t('This moves the release to recently deleted.')} <strong data-i18n-ignore>{release.name}</strong></p><footer><button onClick={onClose}>{t('Cancel')}</button><button className="danger" disabled={busy} onClick={()=>{setBusy(true);void onDeleted().catch(error=>{setBusy(false);toast.error(error instanceof Error?error.message:t('Could not delete release'))})}}>{t(busy?'Deleting…':'Delete')}</button></footer></BasicDialog>}
function BasicDialog({title,onClose,children}:{title:string;onClose:()=>void;children:ReactNode}){const{t}=useI18n();return <Dialog.Root open onOpenChange={open=>!open&&onClose()}><Dialog.Portal><Dialog.Overlay className="flow-release-dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="flow-release-basic-dialog"><Dialog.Title>{title}</Dialog.Title><Dialog.Close aria-label={t('Close')}><X/></Dialog.Close>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>}
const IconButton = forwardRef<HTMLButtonElement, {label:string;children:ReactNode} & Omit<ComponentPropsWithoutRef<'button'>, 'aria-label'|'children'>>(({label,children,className,...props},ref)=><button {...props} ref={ref} className={`flow-releases-icon-button${className?` ${className}`:''}`} aria-label={label} title={label}>{children}</button>)
IconButton.displayName='IconButton'
function MenuItem({icon,children,onSelect,danger,shortcut}:{icon:ReactNode;children:ReactNode;onSelect:()=>void;danger?:boolean;shortcut?:string}){return <DropdownMenu.Item className={`flow-release-menu-item${danger?' danger':''}`} onSelect={onSelect}>{icon}<span>{children}</span>{shortcut&&<kbd>{shortcut}</kbd>}</DropdownMenu.Item>}
function EmptyState({icon,title,description,action}:{icon:ReactNode;title:ReactNode;description:ReactNode;action?:ReactNode}){return <div className="flow-release-empty">{icon}<strong>{title}</strong><span>{description}</span>{action&&<div>{action}</div>}</div>}

function useMediaQuery(query:string){
  const [matches,setMatches]=useState(()=>window.matchMedia(query).matches)
  useEffect(()=>{const media=window.matchMedia(query);const update=()=>setMatches(media.matches);update();media.addEventListener('change',update);return()=>media.removeEventListener('change',update)},[query])
  return matches
}
