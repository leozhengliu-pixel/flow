import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Archive, ArchiveRestore, ArrowDown, ArrowUp, Check, ChevronDown, ChevronRight, CircleDashed, Copy, FileClock, Menu, MoreHorizontal, Plus, Rocket, Settings2, SlidersHorizontal, Star, Trash2, X } from 'lucide-react'
import { forwardRef, useEffect, useMemo, useState, type ComponentPropsWithoutRef, type CSSProperties, type ReactNode } from 'react'
import { toast } from 'sonner'

import { CalendarIcon } from '@/components/issue/issue-icons'
import { addFavorite, deleteRelease, recordRecentResource, removeFavorite, updateRelease } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { newReleasePipelinePath, workspaceLibraryPath } from '@/lib/app-routes'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

import { ReleaseEditorDialog } from './release-editor-dialog'
import { pipelineSummary, releaseProgress, releasesByStage, releasesForPipeline, releaseStatusForStage } from './release-view-model'
import './releases.css'

type Props = {
  data: BootstrapData
  initialReleaseId?: string
  onOpenSidebar: () => void
  onNavigate: (path: string) => void
  onReload: () => Promise<void>
}
type Tab = 'releases'|'changelog'
type PipelineDirectory = 'active'|'archived'

export function ReleasesPage({ data, initialReleaseId, onOpenSidebar, onNavigate, onReload }: Props) {
  const { t } = useI18n()
  const initialParams = new URLSearchParams(window.location.search)
  const initialRelease = initialReleaseId ? data.releases.find(item => item.id === initialReleaseId) : undefined
  const initialPipeline = data.releasePipelines.find(item => item.id === (initialRelease?.pipelineId || initialParams.get('pipeline')))
  const [pipeline, setPipeline] = useState<ReleasePipeline|undefined>(initialPipeline)
  const [tab, setTab] = useState<Tab>(initialParams.get('tab') === 'changelog' ? 'changelog' : 'releases')
  const [archive, setArchive] = useState(initialParams.get('archive') === '1')
  const [directory] = useState<PipelineDirectory>(initialParams.get('pipelines') === 'archived' ? 'archived' : 'active')
  const [editing, setEditing] = useState<Release|undefined>()
  const [opened, setOpened] = useState<Release|undefined>(initialRelease)
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<Release|undefined>()
  useEffect(() => {
    if (!initialReleaseId) return
    const release = data.releases.find(item => item.id === initialReleaseId)
    if (!release) return
    const owner = data.releasePipelines.find(item => item.id === release.pipelineId)
    if (owner) setPipeline(owner)
    setArchive(Boolean(release.archivedAt))
    setOpened(release)
  }, [data.releasePipelines, data.releases, initialReleaseId])
  const root = `/${data.workspace.urlKey}/releases`
  const openPipeline = (next: ReleasePipeline, nextArchive = false, nextTab: Tab = 'releases') => {
    setPipeline(next); setArchive(nextArchive); setTab(nextTab)
    onNavigate(`${root}?pipeline=${encodeURIComponent(next.id)}${nextTab === 'changelog' ? '&tab=changelog' : ''}${nextArchive ? '&archive=1' : ''}`)
  }
  const changeTab = (next: Tab) => { if (!pipeline) return; setTab(next); openPipeline(pipeline, archive, next) }
  const openRelease = (release: Release) => {
    const owner = data.releasePipelines.find(item => item.id === release.pipelineId) ?? pipeline
    if (owner) setPipeline(owner)
    setOpened(release)
    void recordRecentResource('release', release.id)
    onNavigate(`${root}?pipeline=${encodeURIComponent(release.pipelineId || owner?.id || '')}&release=${encodeURIComponent(release.id)}${release.archivedAt ? '&archive=1' : ''}`)
  }
  const closeRelease = () => {
    setOpened(undefined)
    if (pipeline) openPipeline(pipeline, archive, tab)
    else onNavigate(root)
  }
  return <>
    {pipeline ? <ReleasePipelineView data={data} pipeline={pipeline} tab={tab} archive={archive} onArchiveChange={value => openPipeline(pipeline, value, 'releases')} onCreate={() => setCreating(true)} onDelete={setDeleting} onEdit={release => setEditing(release)} onOpen={openRelease} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onReload={onReload} onTabChange={changeTab}/>
      : <ReleasePipelinesView data={data} directory={directory} onCreate={() => onNavigate(newReleasePipelinePath(data.workspace.urlKey))} onOpen={openPipeline} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate}/>
    }
    {pipeline && creating && <ReleaseEditorDialog data={data} pipeline={pipeline} onClose={() => setCreating(false)} onSaved={onReload}/>}
    {pipeline && editing && <ReleaseEditorDialog data={data} pipeline={pipeline} release={editing} onClose={() => { setEditing(undefined); if (initialReleaseId) openPipeline(pipeline, archive, tab) }} onSaved={onReload}/>}
    {opened && <ReleaseDetailSurface data={data} release={opened} onClose={closeRelease} onDelete={() => { closeRelease(); setDeleting(opened) }} onEdit={() => { setOpened(undefined); setEditing(opened) }} onReload={onReload}/>}
    {deleting && <DeleteReleaseDialog release={deleting} onClose={() => setDeleting(undefined)} onDeleted={async () => { await deleteRelease(deleting.id); setDeleting(undefined); await onReload() }}/>}
    <span className="sr-only" aria-live="polite">{pipeline ? <span data-i18n-ignore>{pipeline.name}</span> : t('Releases')}</span>
  </>
}

function TopBar({ title, onOpenSidebar, children }: { title: ReactNode; onOpenSidebar: () => void; children?: ReactNode }) {
  const { t } = useI18n()
  return <header className="flow-releases-topbar"><button className="flow-releases-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Menu/></button><div className="flow-releases-heading">{title}</div><div className="flow-releases-topbar__actions">{children}</div></header>
}

function ReleasePipelinesView({ data, directory, onCreate, onOpen, onOpenSidebar, onNavigate }: { data: BootstrapData; directory: PipelineDirectory; onCreate: () => void; onOpen: (pipeline: ReleasePipeline) => void; onOpenSidebar: () => void; onNavigate:(path:string)=>void }) {
  const { t, formatNumber } = useI18n()
  const [grouping, setGrouping] = useState<'none'|'team'>('none')
  const [order, setOrder] = useState<'position'|'type'|'latest'>('position')
  const [direction, setDirection] = useState<'asc'|'desc'>('asc')
  const [properties, setProperties] = useState({ active: true, teams: true, latest: true })
  const [displayOpen, setDisplayOpen] = useState(false)
  const compact = useMediaQuery('(max-width: 560px)')
  const pipelines = useMemo(() => data.releasePipelines.filter(item => Boolean(item.archivedAt) === (directory === 'archived')).sort((a,b) => {
    const comparison = order === 'type' ? a.type.localeCompare(b.type)
      : order === 'latest' ? +(pipelineSummary(data, a).latest?.updatedAt ? new Date(pipelineSummary(data, a).latest!.updatedAt) : new Date(0)) - +(pipelineSummary(data, b).latest?.updatedAt ? new Date(pipelineSummary(data, b).latest!.updatedAt) : new Date(0))
      : a.position - b.position
    return direction === 'asc' ? comparison : -comparison
  }), [data, directory, direction, order])
  const groups = grouping === 'team' ? [...data.teams.map(team => ({ id: team.id, label: team.name, pipelines: pipelines.filter(item => item.teamIds.includes(team.id)) })), { id: 'all', label: t('All teams'), pipelines: pipelines.filter(item => !item.teamIds.length) }].filter(group => group.pipelines.length) : [{ id: 'all', label: '', pipelines }]
  const columns = ['minmax(230px,1fr)', properties.active && '130px', properties.teams && '120px', properties.latest && '150px'].filter(Boolean).join(' ')
  const toggleProperty = (key: keyof typeof properties) => setProperties(current => ({ ...current, [key]: !current[key] }))
  return <main className="main-panel flow-releases-page" aria-label={t('Releases')}>
    <TopBar title={<h1>{t('Releases')}</h1>} onOpenSidebar={onOpenSidebar}>
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><IconButton label={t('Releases options')}><MoreHorizontal/></IconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu" align="end" sideOffset={5}><MenuItem icon={<Settings2/>} onSelect={() => onNavigate(`/${data.workspace.urlKey}/settings/releases`)}>{t('Go to settings')}</MenuItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
      <IconButton className="flow-new-pipeline-button" label={t('Create new pipeline')} onClick={onCreate}><Plus/><span>{t('New pipeline')}</span></IconButton>
    </TopBar>
    <div className="flow-pipelines-summary"><span>{formatNumber(pipelines.length)} {t(pipelines.length === 1 ? 'release pipeline' : 'release pipelines')}</span><DropdownMenu.Root open={displayOpen} onOpenChange={setDisplayOpen}><DropdownMenu.Trigger asChild><button aria-label={t('Display options')}><SlidersHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-pipeline-display-menu" align="end" sideOffset={0.5}>
      <DropdownMenu.Label>{t('Grouping')}</DropdownMenu.Label><DropdownMenu.Sub><DropdownMenu.SubTrigger className="flow-release-menu-item"><span>{t(grouping === 'team' ? 'Team' : 'No grouping')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="flow-releases-menu flow-release-compact-menu" onEscapeKeyDown={()=>setDisplayOpen(false)} sideOffset={5}><MenuItem icon={grouping==='none'?<Check/>:<span/>} onSelect={()=>setGrouping('none')}>{t('No grouping')}</MenuItem><MenuItem icon={grouping==='team'?<Check/>:<span/>} onSelect={()=>setGrouping('team')}>{t('Team')}</MenuItem></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Label>{t('Ordering')}</DropdownMenu.Label><div className="flow-release-order-control"><DropdownMenu.Sub><DropdownMenu.SubTrigger className="flow-release-menu-item"><span>{t(order === 'position' ? 'Release pipeline' : order === 'latest' ? 'Latest release' : 'Type')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="flow-releases-menu" onEscapeKeyDown={()=>setDisplayOpen(false)} sideOffset={5}>{([['position','Release pipeline'],['type','Type'],['latest','Latest release']] as const).map(([value,label])=><MenuItem icon={order===value?<Check/>:<span/>} key={value} onSelect={()=>setOrder(value)}>{t(label)}</MenuItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub><button aria-label={t(direction === 'asc' ? 'Ascending' : 'Descending')} onClick={event => { event.preventDefault(); setDirection(value => value === 'asc' ? 'desc' : 'asc') }}>{direction === 'asc' ? <ArrowDown/> : <ArrowUp/>}</button></div>
      <DropdownMenu.Label>{t('Display properties')}</DropdownMenu.Label>{([['active','Active releases'],['teams','Teams'],['latest','Latest release']] as const).map(([key,label])=><DropdownMenu.CheckboxItem checked={properties[key]} className="flow-release-menu-item" key={key} onCheckedChange={()=>toggleProperty(key)}><span className="flow-release-menu-check">{properties[key]&&<Check/>}</span><span>{t(label)}</span></DropdownMenu.CheckboxItem>)}
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>
    {pipelines.length ? <div className="flow-pipelines-table"><div className="flow-pipeline-head" style={compact ? undefined : { gridTemplateColumns: columns }}><span>{t('Release pipeline')}</span>{properties.active&&<span>{t('Active releases')}</span>}{properties.teams&&<span>{t('Teams')}</span>}{properties.latest&&<span>{t('Latest release')}</span>}</div>{groups.map(group => <section className="flow-pipeline-team-group" key={group.id}>{group.label&&<header data-i18n-ignore>{group.label}</header>}{group.pipelines.map(item => { const summary=pipelineSummary(data,item);return <button className="flow-pipeline-row" style={compact ? undefined : { gridTemplateColumns: columns }} key={item.id} onClick={()=>onOpen(item)}><span><Rocket/><strong data-i18n-ignore>{item.name}</strong></span>{properties.active&&<span>{summary.active || '—'}</span>}{properties.teams&&<span className="flow-release-team-stack">{item.teamIds.slice(0,3).map(id=>{const team=data.teams.find(value=>value.id===id);return team?<i key={id} title={team.name} style={{background:team.color}} data-i18n-ignore>{team.key.slice(0,1)}</i>:null})}{!item.teamIds.length&&<em>{t('All teams')}</em>}</span>}{properties.latest&&<span data-i18n-ignore>{summary.latest?.name || '—'}</span>}<ChevronRight/></button>})}</section>)}</div> : <EmptyState icon={<Rocket/>} title={directory==='archived' ? t('No archived pipelines') : t('No release pipelines')} description={directory==='archived' ? t('Archived release pipelines will appear here.') : t('Create a release pipeline to start tracking what ships.')} action={directory==='active'?<button className="flow-releases-primary" onClick={onCreate}>{t('Create release pipeline')}</button>:undefined}/>}
  </main>
}

function ReleasePipelineView({ data, pipeline, tab, archive, onArchiveChange, onCreate, onDelete, onEdit, onOpen, onNavigate, onOpenSidebar, onReload, onTabChange }: { data:BootstrapData;pipeline:ReleasePipeline;tab:Tab;archive:boolean;onArchiveChange:(value:boolean)=>void;onCreate:()=>void;onDelete:(release:Release)=>void;onEdit:(release:Release)=>void;onOpen:(release:Release)=>void;onNavigate:(path:string)=>void;onOpenSidebar:()=>void;onReload:()=>Promise<void>;onTabChange:(value:Tab)=>void }) {
  const { t } = useI18n()
  const releases = releasesForPipeline(data,pipeline,archive)
  const changelog = releasesForPipeline(data,pipeline).filter(item=>item.status==='released')
  const favorite = data.favorites.some(item=>item.resourceType==='release_pipeline'&&item.resourceId===pipeline.id)
  const toggleFavorite = async()=>{try{if(favorite)await removeFavorite('release_pipeline',pipeline.id);else await addFavorite('release_pipeline',pipeline.id);await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not update favorite'))}}
  const copyUrl=async()=>{try{await navigator.clipboard.writeText(window.location.href);toast.success(t('URL copied'))}catch{toast.error(t('Could not copy URL'))}}
  const pipelineMenu = <DropdownMenu.Root><DropdownMenu.Trigger asChild><IconButton label={t('Pipeline options')}><MoreHorizontal/></IconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-pipeline-options" align="start" sideOffset={4}><MenuItem icon={<Plus/>} onSelect={onCreate}>{t('Create release')}</MenuItem><MenuItem icon={<Star fill={favorite?'currentColor':'none'}/>} onSelect={()=>void toggleFavorite()}>{t(favorite?'Remove from favorites':'Favorite')}</MenuItem><MenuItem icon={<Copy/>} onSelect={()=>void copyUrl()}>{t('Copy URL')}</MenuItem><DropdownMenu.Separator/><MenuItem icon={<Settings2/>} onSelect={()=>onNavigate(`/${data.workspace.urlKey}/settings/releases`)}>{t('Pipeline settings')}</MenuItem><DropdownMenu.Separator/><MenuItem icon={<Archive/>} onSelect={()=>onArchiveChange(!archive)}>{t(archive?'View active releases':'Open archive')}</MenuItem><MenuItem icon={<Trash2/>} onSelect={()=>onNavigate(`${workspaceLibraryPath(data.workspace.urlKey,'deleted')}?resource=release`)}>{t('View recently deleted releases')}</MenuItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  return <main className="main-panel flow-releases-page" aria-label={`${pipeline.name} ${t('Releases')}`}>
    <TopBar onOpenSidebar={onOpenSidebar} title={<div className="flow-release-title"><h1 data-i18n-ignore>{pipeline.name}</h1><div className="flow-release-title-actions"><IconButton aria-pressed={favorite} label={t(favorite?'Remove from favorites':'Favorite')} onClick={()=>void toggleFavorite()}><Star fill={favorite?'currentColor':'none'}/></IconButton>{pipelineMenu}</div></div>}>
      {!archive&&<IconButton label={t('Create new release')} onClick={onCreate}><Plus/></IconButton>}
    </TopBar>
    <div className="flow-release-toolbar"><div className="flow-release-tabs"><button className={tab==='releases'?'active':''} aria-selected={tab==='releases'} onClick={()=>onTabChange('releases')}>{t(archive?'Archive':'Releases')}</button>{!archive&&<button className={tab==='changelog'?'active':''} aria-selected={tab==='changelog'} onClick={()=>onTabChange('changelog')}>{t('Changelog')}</button>}</div><span>{archive?t('Archived releases'):t(`${releases.length} releases`)}</span></div>
    {tab==='changelog'&&!archive?<ReleaseChangelog releases={changelog} onOpen={onOpen}/>:<ReleaseList data={data} pipeline={pipeline} releases={releases} archived={archive} onCreate={onCreate} onDelete={onDelete} onEdit={onEdit} onOpen={onOpen} onReload={onReload}/>}
  </main>
}

function ReleaseList({data,pipeline,releases,archived,onCreate,onDelete,onEdit,onOpen,onReload}:{data:BootstrapData;pipeline:ReleasePipeline;releases:Release[];archived:boolean;onCreate:()=>void;onDelete:(release:Release)=>void;onEdit:(release:Release)=>void;onOpen:(release:Release)=>void;onReload:()=>Promise<void>}) {
  const {t,formatDate}=useI18n()
  const [collapsed,setCollapsed]=useState<string[]>([])
  const [properties,setProperties]=useState({description:true,version:true,notes:true,date:true,completion:true})
  const [order,setOrder]=useState<'position'|'date'|'name'>('date')
  const [direction,setDirection]=useState<'asc'|'desc'>('desc')
  const compact=useMediaQuery('(max-width: 560px)')
  const groups=releasesByStage(releases,pipeline).map(group=>({...group,releases:[...group.releases].sort((left,right)=>{const comparison=order==='name'?left.name.localeCompare(right.name):order==='date'?(left.targetDate??'').localeCompare(right.targetDate??''):left.position-right.position;return direction==='asc'?comparison:-comparison})}))
  const toggleGroup=(stage:string)=>setCollapsed(current=>current.includes(stage)?current.filter(value=>value!==stage):[...current,stage])
  const columns=['minmax(260px,1fr)',properties.notes&&'150px',properties.date&&'120px',properties.completion&&'110px','30px'].filter(Boolean).join(' ')
  if(!releases.length)return <EmptyState icon={archived?<Archive/>:<Rocket/>} title={t(archived?'No archived releases':'Create your first release')} description={t(archived?'Archived releases will appear here.':"Create a new release to start tracking what's shipping.")} action={!archived?<button className="flow-releases-primary" onClick={onCreate}>{t('Create new release')}</button>:undefined}/>
  return <div className="flow-release-list">
    <div className="flow-release-list-options"><DropdownMenu.Root><DropdownMenu.Trigger asChild><IconButton label={t('Display options')}><SlidersHorizontal/></IconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu flow-release-list-display-menu" align="end">
      <DropdownMenu.Label>{t('Grouping')}</DropdownMenu.Label><div className="flow-release-display-static"><span>{t('Stage')}</span></div>
      <DropdownMenu.Label>{t('Ordering')}</DropdownMenu.Label><div className="flow-release-order-control"><DropdownMenu.Sub><DropdownMenu.SubTrigger className="flow-release-menu-item"><span>{t(order==='date'?'Release date':order==='name'?'Name':'Release pipeline')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="flow-releases-menu" sideOffset={5}>{([['date','Release date'],['position','Release pipeline'],['name','Name']] as const).map(([value,label])=><MenuItem icon={order===value?<Check/>:<span/>} key={value} onSelect={()=>setOrder(value)}>{t(label)}</MenuItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub><button aria-label={t(direction==='asc'?'Ascending':'Descending')} onClick={event=>{event.preventDefault();setDirection(value=>value==='asc'?'desc':'asc')}}>{direction==='asc'?<ArrowDown/>:<ArrowUp/>}</button></div>
      <DropdownMenu.Label>{t('Display properties')}</DropdownMenu.Label>{([['description','Description'],['version','Version'],['date','Release date'],['completion','Completion'],['notes','Release notes']] as const).map(([key,label])=><DropdownMenu.CheckboxItem checked={properties[key]} className="flow-release-menu-item" key={key} onCheckedChange={value=>setProperties(current=>({...current,[key]:value===true}))}><span className="flow-release-menu-check">{properties[key]&&<Check/>}</span><span>{t(label)}</span></DropdownMenu.CheckboxItem>)}
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>
    <div className="flow-release-columns" style={compact ? undefined : {gridTemplateColumns:columns}}><span>{t('Release')}</span>{properties.notes&&<span>{t('Release notes')}</span>}{properties.date&&<span>{t('Release date')}</span>}{properties.completion&&<span>{t('Completion')}</span>}<span/></div>
    {groups.map(group=>group.releases.length?<section className="flow-release-group" key={group.stage}><header><button aria-expanded={!collapsed.includes(group.stage)} onClick={()=>toggleGroup(group.stage)}><ChevronDown/><CircleDashed/><strong data-i18n-ignore>{group.stage}</strong><span>{group.releases.length}</span></button></header>{!collapsed.includes(group.stage)&&group.releases.map(item=><div className="flow-release-row" key={item.id} style={compact ? undefined : {gridTemplateColumns:columns}}><button className="flow-release-row__main" onClick={()=>onOpen(item)}><CircleDashed/><span data-i18n-ignore><strong>{item.name}</strong>{properties.version&&item.version&&<em>{item.version}</em>}{properties.description&&item.description&&<small>{item.description}</small>}</span></button>{properties.notes&&<span>{item.releaseNotes?t('Ready'):'—'}</span>}{properties.date&&<span className="flow-release-row__date"><CalendarIcon/>{item.targetDate?formatDate(item.targetDate,{month:'short',day:'numeric'}):'—'}</span>}{properties.completion&&<span className="flow-release-progress"><i style={{'--progress':`${releaseProgress(data,item)}%`} as CSSProperties}/>{releaseProgress(data,item)}%</span>}<ReleaseRowMenu archived={archived} item={item} onDelete={()=>onDelete(item)} onEdit={()=>onEdit(item)} onRestore={async()=>{await updateRelease(item.id,{archived:false});await onReload()}}/></div>)}</section>:null)}
  </div>
}

function ReleaseRowMenu({item,archived,onEdit,onRestore,onDelete}:{item:Release;archived:boolean;onEdit:()=>void;onRestore:()=>Promise<void>;onDelete:()=>void}){const{t}=useI18n();return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="flow-release-row-menu" aria-label={`${t('Open actions')} ${item.name}`}><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu" align="end" sideOffset={5}>{!archived&&<MenuItem icon={<Settings2/>} onSelect={onEdit}>{t('Edit release')}</MenuItem>}{archived&&<MenuItem icon={<ArchiveRestore/>} onSelect={()=>void onRestore()}>{t('Restore')}</MenuItem>}<DropdownMenu.Separator/><MenuItem danger icon={<Trash2/>} onSelect={onDelete}>{t('Delete release')}</MenuItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}

function ReleaseChangelog({releases,onOpen}:{releases:Release[];onOpen:(release:Release)=>void}){const{t,formatDate}=useI18n();if(!releases.length)return <EmptyState icon={<FileClock/>} title={t('No release notes yet')} description={t('Completed releases and their release notes will appear here.')}/>;return <div className="flow-release-changelog">{releases.map(item=><article key={item.id}><span>{item.version?<span data-i18n-ignore>{item.version}</span>:t('Release')}</span><div><h2 data-i18n-ignore>{item.name}</h2>{item.releaseNotes?<p data-i18n-ignore>{item.releaseNotes}</p>:<button className="flow-release-notes-create" onClick={()=>onOpen(item)}>{t('Create release notes')}</button>}</div><time>{formatDate(item.releasedAt||item.targetDate||item.updatedAt,{month:'short',day:'numeric',year:'numeric'})}</time></article>)}</div>}

function ReleaseDetailSurface({data,release,onClose,onDelete,onEdit,onReload}:{data:BootstrapData;release:Release;onClose:()=>void;onDelete:()=>void;onEdit:()=>void;onReload:()=>Promise<void>}) {
  const {t,formatDate}=useI18n()
  const [tab,setTab]=useState<'issues'|'notes'>(release.status==='released'&&!release.releaseNotes?'notes':'issues')
  const [notes,setNotes]=useState(release.releaseNotes??'')
  const [savedNotes,setSavedNotes]=useState(release.releaseNotes??'')
  const [savingNotes,setSavingNotes]=useState(false)
  const [stage,setStage]=useState(release.stage??'')
  const issues=data.issues.filter(issue=>release.issueIds.includes(issue.id))
  const issueGroups=[...new Map(issues.map(issue=>[issue.state.id,issue.state])).values()].map(state=>({state,issues:issues.filter(issue=>issue.state.id===state.id)}))
  const favorite=data.favorites.some(item=>item.resourceType==='release'&&item.resourceId===release.id)
  const toggleFavorite=async()=>{try{if(favorite)await removeFavorite('release',release.id);else await addFavorite('release',release.id);await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not update favorite'))}}
  const copyUrl=async()=>{try{await navigator.clipboard.writeText(window.location.href);toast.success(t('URL copied'))}catch{toast.error(t('Could not copy URL'))}}
  const updateStage=async(value:string)=>{try{setStage(value);await updateRelease(release.id,{stage:value,status:releaseStatusForStage(pipeline!,value,release.status)});await onReload()}catch(error){setStage(release.stage??'');toast.error(error instanceof Error?error.message:t('Could not save release'))}}
  const saveNotes=async()=>{setSavingNotes(true);try{await updateRelease(release.id,{releaseNotes:notes});setSavedNotes(notes);await onReload();toast.success(t('Release notes saved'))}catch(error){toast.error(error instanceof Error?error.message:t('Could not save release notes'))}finally{setSavingNotes(false)}}
  const pipeline=data.releasePipelines.find(item=>item.id===release.pipelineId)
  return <Dialog.Root modal={false} open onOpenChange={open=>!open&&onClose()}><Dialog.Portal><Dialog.Content aria-describedby={undefined} className="flow-release-detail-surface">
    <header><Dialog.Title data-i18n-ignore>{release.name}</Dialog.Title><div><button aria-label={t(favorite?'Remove from favorites':'Favorite')} aria-pressed={favorite} onClick={()=>void toggleFavorite()}><Star fill={favorite?'currentColor':'none'}/></button><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Release options')}><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu" align="end"><MenuItem icon={<Settings2/>} onSelect={onEdit}>{t('Edit release')}</MenuItem><MenuItem icon={<Star fill={favorite?'currentColor':'none'}/>} onSelect={()=>void toggleFavorite()}>{t(favorite?'Remove from favorites':'Favorite')}</MenuItem><MenuItem icon={<Copy/>} onSelect={()=>void copyUrl()}>{t('Copy URL')}</MenuItem><DropdownMenu.Separator/><MenuItem danger icon={<Trash2/>} onSelect={onDelete}>{t('Delete release')}</MenuItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root><Dialog.Close aria-label={t('Close')}><X/></Dialog.Close></div></header>
    <div className="flow-release-detail-meta">{pipeline&&<DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="flow-release-detail-stage"><CircleDashed/>{stage?<span data-i18n-ignore>{stage}</span>:<span>{t('Stage')}</span>}<ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-releases-menu" align="start">{pipeline.stages.map(value=><MenuItem icon={value===stage?<Check/>:<CircleDashed/>} key={value} onSelect={()=>void updateStage(value)}><span data-i18n-ignore>{value}</span></MenuItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}<span data-i18n-ignore>{release.version}</span>{release.targetDate&&<time>{formatDate(release.targetDate,{month:'short',day:'numeric',year:'numeric'})}</time>}</div>
    {release.description&&<p className="flow-release-detail-description" data-i18n-ignore>{release.description}</p>}
    <div className="flow-release-detail-tabs" onKeyDown={event=>{if(event.key!=='ArrowLeft'&&event.key!=='ArrowRight')return;event.preventDefault();const buttons=[...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role=tab]')];const current=buttons.indexOf(document.activeElement as HTMLButtonElement);const next=event.key==='ArrowRight'?(current+1)%buttons.length:(current-1+buttons.length)%buttons.length;buttons[next]?.focus();setTab(buttons[next]?.dataset.tab as 'issues'|'notes')}} role="tablist"><button aria-selected={tab==='issues'} data-tab="issues" onClick={()=>setTab('issues')} role="tab" tabIndex={tab==='issues'?0:-1}>{t('Issues')}<span>{issues.length}</span></button><button aria-selected={tab==='notes'} data-tab="notes" onClick={()=>setTab('notes')} role="tab" tabIndex={tab==='notes'?0:-1}>{t('Release notes')}</button></div>
    {tab==='issues'?<div className="flow-release-detail-issues">{issueGroups.length?issueGroups.map(group=><section key={group.state.id}><header><CircleDashed/><strong data-i18n-ignore>{group.state.name}</strong><span>{group.issues.length}</span></header>{group.issues.map(issue=><div key={issue.id}><CircleDashed/><span><b data-i18n-ignore>{issue.identifier}</b><strong data-i18n-ignore>{issue.title}</strong></span><small data-i18n-ignore>{issue.state.name}</small></div>)}</section>):<EmptyState icon={<CircleDashed/>} title={t('No issues in scope')} description={t('Issues added to this release will appear here.')} action={<button className="flow-releases-primary" onClick={onEdit}>{t('Add issues')}</button>}/>}</div>:<div className="flow-release-detail-notes"><textarea aria-label={t('Release notes')} autoFocus={tab==='notes'} value={notes} onChange={event=>setNotes(event.target.value)} placeholder={t('Write release notes…')}/><footer><button disabled={savingNotes||notes===savedNotes} onClick={()=>void saveNotes()}>{t(savingNotes?'Saving…':'Save release notes')}</button></footer></div>}
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}
function DeleteReleaseDialog({release,onClose,onDeleted}:{release:Release;onClose:()=>void;onDeleted:()=>Promise<void>}){const{t}=useI18n();const[busy,setBusy]=useState(false);return <BasicDialog title={t('Delete release')} onClose={onClose}><p>{t('This moves the release to recently deleted.')} <strong data-i18n-ignore>{release.name}</strong></p><footer><button onClick={onClose}>{t('Cancel')}</button><button className="danger" disabled={busy} onClick={()=>{setBusy(true);void onDeleted().catch(error=>{setBusy(false);toast.error(error instanceof Error?error.message:t('Could not delete release'))})}}>{t(busy?'Deleting…':'Delete')}</button></footer></BasicDialog>}
function BasicDialog({title,onClose,children}:{title:string;onClose:()=>void;children:ReactNode}){const{t}=useI18n();return <Dialog.Root open onOpenChange={open=>!open&&onClose()}><Dialog.Portal><Dialog.Overlay className="flow-release-dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="flow-release-basic-dialog"><Dialog.Title>{title}</Dialog.Title><Dialog.Close aria-label={t('Close')}><X/></Dialog.Close>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>}
const IconButton = forwardRef<HTMLButtonElement, {label:string;children:ReactNode} & Omit<ComponentPropsWithoutRef<'button'>, 'aria-label'|'children'>>(({label,children,className,...props},ref)=><button {...props} ref={ref} className={`flow-releases-icon-button${className?` ${className}`:''}`} aria-label={label} title={label}>{children}</button>)
IconButton.displayName='IconButton'
function MenuItem({icon,children,onSelect,danger}:{icon:ReactNode;children:ReactNode;onSelect:()=>void;danger?:boolean}){return <DropdownMenu.Item className={`flow-release-menu-item${danger?' danger':''}`} onSelect={onSelect}>{icon}<span>{children}</span></DropdownMenu.Item>}
function EmptyState({icon,title,description,action}:{icon:ReactNode;title:string;description:string;action?:ReactNode}){return <div className="flow-release-empty">{icon}<strong>{title}</strong><span>{description}</span>{action&&<div>{action}</div>}</div>}

function useMediaQuery(query:string){
  const [matches,setMatches]=useState(()=>window.matchMedia(query).matches)
  useEffect(()=>{const media=window.matchMedia(query);const update=()=>setMatches(media.matches);update();media.addEventListener('change',update);return()=>media.removeEventListener('change',update)},[query])
  return matches
}
