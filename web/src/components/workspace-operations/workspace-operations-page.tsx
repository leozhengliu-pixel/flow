import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Archive, ArchiveRestore, CalendarDays, Check, ChevronDown, ChevronRight, CircleDashed, Clock3, FileClock, FilePenLine, Filter, History, Inbox, Mail, Menu, MessageCircleQuestion, MoreHorizontal, Plus, Rocket, Search, Settings2, SlidersHorizontal, Star, Trash2, X } from 'lucide-react'
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { createAsk, createRelease, decideAsk, deleteAsk, deleteDraft, deleteRelease, purgeTrashEntry, recordRecentResource, restoreTrashEntry, searchWorkspace, updateRelease } from '@/lib/api'
import type { Ask, BootstrapData, Draft, Release, SearchResponse } from '@/types/flow'

import './workspace-operations.css'

export type OperationsView = 'drafts'|'releases'|'asks'|'favorites'|'recent'|'audit-log'|'deleted'

export function WorkspaceOperationsPage({ data, view, initialReleaseId, onOpenSidebar, onReload, onNavigate, onResumeDraft }: { data: BootstrapData; view: OperationsView; initialReleaseId?: string; onOpenSidebar: () => void; onReload: () => Promise<void>; onNavigate: (path: string) => void; onResumeDraft: (draft: Draft) => void }) {
  const [query, setQuery] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [editingRelease, setEditingRelease] = useState<Release>()
  const [askDecision, setAskDecision] = useState<{ ask: Ask; decision: 'approved'|'rejected' }>()
  const [recent, setRecent] = useState<SearchResponse['recent']>([])
  const [releaseSurface, setReleaseSurface] = useState<'pipelines'|'pipeline'>('pipeline')
  const [releaseTab, setReleaseTab] = useState<'releases'|'changelog'>('releases')
  const [askSurface, setAskSurface] = useState<'configuration'|'intake'>('configuration')
  const [archiveView, setArchiveView] = useState<ArchiveView>('recent-issues')
  const [archiveFilterOpen, setArchiveFilterOpen] = useState(false)
  const [auditHideSessions, setAuditHideSessions] = useState(false)
  const openedRelease = useRef('')
  useEffect(() => { if (view === 'recent') void searchWorkspace('', [], 30).then(value => setRecent(value.recent)).catch(() => setRecent([])) }, [view])
  useEffect(() => {
    if (view !== 'releases' || !initialReleaseId || openedRelease.current === initialReleaseId) return
    const release = data.releases.find(item => item.id === initialReleaseId && !item.archivedAt)
    if (!release) return
    openedRelease.current = initialReleaseId
    void recordRecentResource('release', release.id)
    setEditingRelease(release)
  }, [data.releases, initialReleaseId, view])
  const title = ({ drafts: 'Drafts', releases: 'Releases', asks: 'Asks', favorites: 'Favorites', recent: 'Recently viewed', 'audit-log': 'Audit log', deleted: 'Recently deleted' } as const)[view]
  const canCreate = view === 'releases' || view === 'asks'
  if (view === 'releases') return <>
    <ReleasesPage data={data} surface={releaseSurface} tab={releaseTab} onSurfaceChange={setReleaseSurface} onTabChange={setReleaseTab} onOpenSidebar={onOpenSidebar} onCreate={() => setCreateOpen(true)} onOpen={release => { void recordRecentResource('release', release.id); setEditingRelease(release) }} onDelete={async id => { await deleteRelease(id); await onReload() }}/>
    {createOpen && (
      <CreateOperationDialog view="releases" data={data} onClose={() => setCreateOpen(false)} onCreated={async () => { setCreateOpen(false); await onReload() }}/>
    )}
    {editingRelease && (
      <CreateOperationDialog view="releases" data={data} release={editingRelease} onClose={() => setEditingRelease(undefined)} onCreated={async () => { setEditingRelease(undefined); await onReload() }}/>
    )}
  </>
  if (view === 'asks') return <>
    <AsksPage data={data} surface={askSurface} onSurfaceChange={setAskSurface} onOpenSidebar={onOpenSidebar} onCreate={() => setCreateOpen(true)} onDecide={(ask, decision) => setAskDecision({ ask, decision })} onDelete={async id => { await deleteAsk(id); await onReload() }}/>
    {createOpen && (
      <CreateOperationDialog view="asks" data={data} onClose={() => setCreateOpen(false)} onCreated={async () => { setCreateOpen(false); await onReload() }}/>
    )}
    {askDecision && (
      <AskDecisionDialog value={askDecision} onClose={() => setAskDecision(undefined)} onSubmit={async note => { await decideAsk(askDecision.ask.id, askDecision.decision, note); setAskDecision(undefined); await onReload() }}/>
    )}
  </>
  if (view === 'deleted') return <ArchivePage data={data} selected={archiveView} filterOpen={archiveFilterOpen} query={query} onSelectedChange={setArchiveView} onFilterOpenChange={setArchiveFilterOpen} onQueryChange={setQuery} onOpenSidebar={onOpenSidebar} onRestore={async id => { await restoreTrashEntry(id); await onReload() }} onPurge={async id => { await purgeTrashEntry(id); await onReload() }}/>
  if (view === 'audit-log') return <AuditLogPage data={data} query={query} hideSessions={auditHideSessions} onQueryChange={setQuery} onHideSessionsChange={setAuditHideSessions} onOpenSidebar={onOpenSidebar}/>
  return <main className="main-panel operations-page" aria-label={title}>
    <header className="operations-header">
      <button className="operations-mobile-menu" aria-label="Open sidebar" onClick={onOpenSidebar}><Menu/></button>
      <h1>{title}</h1>
      {canCreate && <button className="operations-icon-button" aria-label={`New ${view === 'asks' ? 'ask' : 'release'}`} onClick={() => setCreateOpen(true)}><Plus size={16}/></button>}
    </header>
    <div className="operations-toolbar"><label><Search size={14}/><input aria-label={`Search ${title}`} placeholder={`Search ${title.toLowerCase()}…`} value={query} onChange={event => setQuery(event.target.value)}/>{query && <button aria-label="Clear search" onClick={() => setQuery('')}><X size={12}/></button>}</label></div>
    {view === 'drafts' && <DraftRows data={data} drafts={data.drafts} query={query} onDelete={async draft => { await deleteDraft(draft.id); clearLocalIssueDraft(draft); await onReload() }} onNavigate={onNavigate} onResume={onResumeDraft}/>} 
    {view === 'favorites' && <FavoriteRows data={data} query={query} onNavigate={onNavigate}/>} 
    {view === 'recent' && <RecentRows data={data} recent={recent} query={query} onNavigate={onNavigate}/>} 
  </main>
}

type ArchiveView = 'issues'|'projects'|'cycles'|'recent-issues'|'recent-projects'|'recent-initiatives'|'recent-documents'

function OperationsTopBar({ title, onOpenSidebar, children }: { title: ReactNode; onOpenSidebar: () => void; children?: ReactNode }) {
  return <header className="operations-header operations-special-header"><button className="operations-mobile-menu" aria-label="Open sidebar" onClick={onOpenSidebar}><Menu/></button><div className="operations-heading">{title}</div><div className="operations-header-actions">{children}</div></header>
}

function ReleasesPage({ data, surface, tab, onSurfaceChange, onTabChange, onOpenSidebar, onCreate, onOpen, onDelete }: { data: BootstrapData; surface: 'pipelines'|'pipeline'; tab: 'releases'|'changelog'; onSurfaceChange: (value: 'pipelines'|'pipeline') => void; onTabChange: (value: 'releases'|'changelog') => void; onOpenSidebar: () => void; onCreate: () => void; onOpen: (release: Release) => void; onDelete: (id: string) => Promise<void> }) {
  const releases = data.releases.filter(item => !item.archivedAt)
  const active = releases.filter(item => item.status === 'planned' || item.status === 'inProgress')
  const latest = [...releases].sort((a,b) => +new Date(b.updatedAt) - +new Date(a.updatedAt))[0]
  if (surface === 'pipelines') return <main className="main-panel operations-page release-pipelines-page" aria-label="Releases">
    <OperationsTopBar title={<h1>Releases</h1>} onOpenSidebar={onOpenSidebar}><button className="operations-icon-button" aria-label="Releases options"><MoreHorizontal/></button><button className="operations-icon-button" aria-label="Open production pipeline" onClick={() => onSurfaceChange('pipeline')}><Plus/></button></OperationsTopBar>
    <div className="release-pipelines-summary"><span>1 release pipeline</span><button aria-label="Display options"><SlidersHorizontal/></button></div>
    <div className="release-pipelines-table">
      <div className="release-pipeline-head"><button>Release pipeline <ChevronDown/></button><span>Active releases</span><span>Teams</span><span>Latest release</span></div>
      <button className="release-pipeline-row" onClick={() => onSurfaceChange('pipeline')}><span className="release-pipeline-mark"><Rocket/></span><strong>Production</strong><span>{active.length || '—'}</span><span className="release-team-stack">{data.teams.slice(0,3).map(team => <i key={team.id} title={team.name} style={{background:team.color}}>{team.key.slice(0,1)}</i>)}</span><span>{latest ? latest.name : '—'}</span><ChevronRight/></button>
    </div>
  </main>
  const groups: { status: Release['status']; label: string }[] = [{status:'planned',label:'Planned'},{status:'inProgress',label:'In progress'},{status:'released',label:'Released'},{status:'canceled',label:'Canceled'}]
  const changelog = releases.filter(item => item.status === 'released')
  return <main className="main-panel operations-page release-detail-page" aria-label="Production releases">
    <OperationsTopBar onOpenSidebar={onOpenSidebar} title={<div className="release-breadcrumb"><button onClick={() => onSurfaceChange('pipelines')}>Releases</button><ChevronRight/><h1>Production</h1><Star/></div>}><button className="operations-icon-button" aria-label="Pipeline options"><MoreHorizontal/></button><button className="operations-icon-button" aria-label="Create new release" onClick={onCreate}><Plus/></button></OperationsTopBar>
    <div className="release-detail-toolbar"><div className="operations-tabs"><button className={tab==='releases'?'active':''} onClick={()=>onTabChange('releases')}>Releases</button><button className={tab==='changelog'?'active':''} onClick={()=>onTabChange('changelog')}>Changelog</button></div><button className="round-tool" aria-label="Display options"><SlidersHorizontal/></button></div>
    {tab === 'changelog' ? <ReleaseChangelog releases={changelog}/> : releases.length ? <div className="release-groups"><div className="release-columns"><span>Release</span><span>Release notes</span><span>Release date</span><span>Completion</span></div>{groups.map(group => { const rows=releases.filter(item=>item.status===group.status); if(!rows.length)return null; return <section className="release-group" key={group.status}><header><ChevronDown/><CircleDashed/><strong>{group.label}</strong><span>{rows.length}</span></header>{rows.map(item=><div className="release-linear-row" key={item.id}><button className="release-row-main" onClick={()=>onOpen(item)}><CircleDashed/><span><strong>{item.name}</strong>{item.version&&<em>{item.version}</em>}<small>{item.description}</small></span></button><span className="release-note-cell">{item.description ? 'Draft' : '—'}</span><span className="release-date-cell"><CalendarDays/>{item.targetDate ? formatDate(item.targetDate) : '—'}</span><span className="release-completion"><i style={{'--progress':`${releaseProgress(data,item)}%`} as CSSProperties}/>{releaseProgress(data,item)}%</span><RowMenu><MenuItem icon={<Settings2/>} onSelect={()=>onOpen(item)}>Edit release</MenuItem><MenuItem icon={<Trash2/>} danger onSelect={()=>void onDelete(item.id)}>Delete release</MenuItem></RowMenu></div>)}</section>})}</div> : <div className="linear-empty-state"><Rocket/><strong>Create your first release</strong><span>Create a new release to start tracking what's shipping. Integrate with your CI/CD tool to automatically add issues to your releases.</span><div><button className="linear-primary" onClick={onCreate}>Create new release</button><button>Documentation</button></div></div>}
  </main>
}

function ReleaseChangelog({ releases }: { releases: Release[] }) {
  if (!releases.length) return <div className="linear-empty-state"><FileClock/><strong>No release notes yet</strong><span>Completed releases and their release notes will appear here.</span></div>
  return <div className="release-changelog">{releases.map(item=><article key={item.id}><span>{item.version || 'Release'}</span><div><h2>{item.name}</h2><p>{item.description || 'No release notes were added.'}</p></div><time>{formatDate(item.targetDate || item.updatedAt)}</time></article>)}</div>
}

function releaseProgress(data: BootstrapData, release: Release) { const issues=data.issues.filter(issue=>release.issueIds.includes(issue.id)); if(!issues.length)return 0; return Math.round(issues.filter(issue=>issue.state.type==='completed'||issue.state.type==='canceled').length/issues.length*100) }
function formatDate(value: string) { return new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric'}).format(new Date(value)) }

function AsksPage({ data, surface, onSurfaceChange, onOpenSidebar, onCreate, onDecide, onDelete }: { data: BootstrapData; surface: 'configuration'|'intake'; onSurfaceChange: (value: 'configuration'|'intake') => void; onOpenSidebar: () => void; onCreate: () => void; onDecide: (ask: Ask, decision: 'approved'|'rejected') => void; onDelete: (id: string) => Promise<void> }) {
  const pending=data.asks.filter(item=>item.status==='pending').length
  return <main className="main-panel operations-page asks-page" aria-label="Asks">
    <OperationsTopBar title={<h1>Asks</h1>} onOpenSidebar={onOpenSidebar}><button className="operations-icon-button" aria-label="Create ask" onClick={onCreate}><Plus/></button></OperationsTopBar>
    <div className="asks-content">
      <div className="asks-intro"><h2>Asks</h2><p>Let anyone submit bug reports, feature requests, and more using structured templates from Slack or email. <a href="https://linear.app/docs/linear-asks" target="_blank" rel="noreferrer">Docs ↗</a></p><div className="asks-mode"><button className={surface==='configuration'?'active':''} onClick={()=>onSurfaceChange('configuration')}>Configuration</button><button className={surface==='intake'?'active':''} onClick={()=>onSurfaceChange('intake')}>Intake {pending>0&&<span>{pending}</span>}</button></div></div>
      {surface === 'configuration' ? <div className="asks-config">
        <section><header><h3>Slack</h3><p>Allow anyone in your Slack workspace to submit Asks using templated forms</p></header><button className="asks-config-row"><span className="asks-source-icon slack"><MessageCircleQuestion/></span><span><strong>No workspaces connected</strong><small>Connect Slack to route requests into team Triage.</small></span><Plus/></button></section>
        <section><header><h3>Email</h3><p>Allow anyone to submit Asks by emailing a custom address</p></header><button className="asks-config-row"><span className="asks-source-icon mail"><Mail/></span><span><strong>No email addresses configured</strong><small>Create an intake address and choose a team and template.</small></span><Plus/></button></section>
      </div> : <div className="asks-intake"><div className="asks-intake-header"><span>{`${data.asks.length} requests`}</span><button className="linear-primary" onClick={onCreate}><Plus/> New request</button></div>{data.asks.length?<div className="asks-intake-list">{data.asks.map(item=><div className="ask-linear-row" key={item.id}><span className={`operations-ask-status ${item.status}`}>{item.status==='pending'?<Clock3/>:<Check/>}</span><div><strong>{item.title}</strong><small>{item.requester.displayName} · {item.source} · {relative(item.createdAt)} ago</small></div><span className={`ask-state ${item.status}`}>{item.status}</span><RowMenu>{item.status==='pending'&&<><MenuItem icon={<Check/>} onSelect={()=>onDecide(item,'approved')}>Approve...</MenuItem><MenuItem icon={<X/>} onSelect={()=>onDecide(item,'rejected')}>Reject...</MenuItem></>}<MenuItem icon={<Trash2/>} danger onSelect={()=>void onDelete(item.id)}>Delete ask</MenuItem></RowMenu></div>)}</div>:<div className="linear-empty-state compact"><Inbox/><strong>No asks yet</strong><span>Requests submitted through Slack or email will appear here for review.</span></div>}</div>}
    </div>
  </main>
}

const archiveTabs: { id: ArchiveView; label: string; resource?: string }[] = [
  {id:'issues',label:'Issues'}, {id:'projects',label:'Projects'}, {id:'cycles',label:'Cycles'},
  {id:'recent-issues',label:'Recently deleted issues',resource:'issue'}, {id:'recent-projects',label:'Recently deleted projects',resource:'project'},
  {id:'recent-initiatives',label:'Recently deleted initiatives',resource:'initiative'}, {id:'recent-documents',label:'Recently deleted documents',resource:'document'},
]

function ArchivePage({ data, selected, filterOpen, query, onSelectedChange, onFilterOpenChange, onQueryChange, onOpenSidebar, onRestore, onPurge }: { data: BootstrapData; selected: ArchiveView; filterOpen: boolean; query: string; onSelectedChange: (value: ArchiveView) => void; onFilterOpenChange: (value: boolean) => void; onQueryChange: (value: string) => void; onOpenSidebar: () => void; onRestore: (id: string) => Promise<void>; onPurge: (id: string) => Promise<void> }) {
  const tab=archiveTabs.find(item=>item.id===selected)!
  const rows=tab.resource ? data.trash.filter(item=>item.resourceType===tab.resource && matches(query,item.title,item.resourceType)) : []
  return <main className="main-panel operations-page archive-page" aria-label={tab.label}>
    <OperationsTopBar title={<h1>{tab.label} <span>{rows.length}</span></h1>} onOpenSidebar={onOpenSidebar}/>
    <div className="archive-toolbar"><div className="archive-tabs">{archiveTabs.map(item=><button key={item.id} className={selected===item.id?'active':''} onClick={()=>{onSelectedChange(item.id);onQueryChange('')}}>{item.label}</button>)}</div><button className="round-tool" aria-label="Add filter" data-active={filterOpen} onClick={()=>onFilterOpenChange(!filterOpen)}><Filter/></button></div>
    {filterOpen&&<div className="archive-filter"><Search/><input autoFocus placeholder={`Filter ${tab.label.toLowerCase()}…`} value={query} onChange={event=>onQueryChange(event.target.value)}/>{query&&<button aria-label="Clear filter" onClick={()=>onQueryChange('')}><X/></button>}</div>}
    {tab.resource ? rows.length ? <div className="archive-list">{rows.map(item=><div className="archive-row" key={item.id}><span className="archive-drag"><MoreHorizontal/></span><span className="archive-type-icon">{archiveIcon(item.resourceType)}</span><div><strong>{item.title}</strong><small>{`${capitalize(item.resourceType)} · deleted by ${item.deletedBy.displayName}`}</small></div><span className="archive-deleter">{item.deletedBy.displayName.slice(0,2).toUpperCase()}</span><time>{formatDate(item.deletedAt)}</time><RowMenu><MenuItem icon={<ArchiveRestore/>} onSelect={()=>void onRestore(item.id)}>Restore</MenuItem><MenuItem icon={<Trash2/>} danger onSelect={()=>void onPurge(item.id)}>Delete permanently</MenuItem></RowMenu></div>)}</div> : <div className="linear-empty-state archive-empty"><Archive/><strong>{archiveEmptyLabel(tab)}</strong></div> : <div className="linear-empty-state archive-empty"><Archive/><strong>{archiveEmptyLabel(tab)}</strong></div>}
  </main>
}

function archiveIcon(type: string) { if(type==='issue')return <CircleDashed/>; if(type==='project')return <Rocket/>; if(type==='document')return <FilePenLine/>; return <Archive/> }
function capitalize(value: string) { return value.charAt(0).toUpperCase()+value.slice(1) }
function archiveEmptyLabel(tab: (typeof archiveTabs)[number]) { const resource=tab.resource ?? tab.id.replace(/^recent-/,'').replace(/s$/,''); return `No matching ${resource === 'issue' ? 'issues' : `${resource}s`}` }

function AuditLogPage({ data, query, hideSessions, onQueryChange, onHideSessionsChange, onOpenSidebar }: { data: BootstrapData; query: string; hideSessions: boolean; onQueryChange: (value: string) => void; onHideSessionsChange: (value: boolean) => void; onOpenSidebar: () => void }) {
  const rows=data.auditLog.filter(item=>matches(query,item.action,item.resourceType,item.actor.displayName,item.actor.email)).filter(item=>!hideSessions||!item.action.toLowerCase().includes('session'))
  return <main className="main-panel operations-page audit-page" aria-label="Audit log">
    <OperationsTopBar title={<h1>Audit log</h1>} onOpenSidebar={onOpenSidebar}/>
    <div className="audit-content"><header><h2>Audit log</h2><p>Maintains an audit log for the workspace for 90 days</p></header><div className="audit-rule"/><p className="audit-description">Workspace administrators have access to audit log entries. Browse account access, subscriptions, and workspace setting changes below.</p><div className="audit-controls"><label><Search/><input aria-label="Search audit log" placeholder="Search audit log…" value={query} onChange={event=>onQueryChange(event.target.value)}/>{query&&<button aria-label="Clear search" onClick={()=>onQueryChange('')}><X/></button>}</label><button><Filter/> Event type <ChevronDown/></button><label className="audit-session-toggle"><input type="checkbox" checked={hideSessions} onChange={event=>onHideSessionsChange(event.target.checked)}/><span/>Hide session events</label></div>{rows.length?<div className="audit-table"><div className="audit-table-head"><span>Actor</span><span>Event</span><span>Resource</span><span>Date</span></div>{rows.map(item=><div className="audit-row" key={item.id}><span className="audit-avatar">{item.actor.displayName.slice(0,2).toUpperCase()}</span><div><strong>{item.actor.displayName}</strong><small>{item.actor.email}</small></div><code>{item.action.replaceAll('_',' ')}</code><span className="audit-resource">{item.resourceType.replaceAll('_',' ')}</span><time>{new Date(item.createdAt).toLocaleString()}</time><button aria-label="Audit entry options"><MoreHorizontal/></button></div>)}</div>:<div className="linear-empty-state compact"><History/><strong>No audit events</strong><span>Try changing your filters or search query.</span></div>}</div>
  </main>
}

function Empty({ icon, title, body }: { icon: ReactNode; title: string; body: string }) { return <div className="operations-empty">{icon}<strong>{title}</strong><span>{body}</span></div> }
function matches(query: string, ...values: (string|undefined)[]) { const needle = query.trim().toLowerCase(); return !needle || values.some(value => value?.toLowerCase().includes(needle)) }
function RowMenu({ children }: { children: ReactNode }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="operations-row-menu" aria-label="Open actions"><MoreHorizontal size={15}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="operations-menu" align="end" sideOffset={5}>{children}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function MenuItem({ icon, children, onSelect, danger }: { icon: ReactNode; children: ReactNode; onSelect: () => void; danger?: boolean }) { return <DropdownMenu.Item className={danger ? 'danger' : ''} onSelect={onSelect}>{icon}<span>{children}</span></DropdownMenu.Item> }
function relative(value: string) { const delta = Date.now() - new Date(value).getTime(); const minutes = Math.max(0, Math.floor(delta/60000)); if (minutes < 60) return `${minutes || 1}m`; const hours = Math.floor(minutes/60); if (hours < 24) return `${hours}h`; return `${Math.floor(hours/24)}d` }

function DraftRows({ data, drafts, query, onDelete, onNavigate, onResume }: { data: BootstrapData; drafts: Draft[]; query: string; onDelete: (draft: Draft) => Promise<void>; onNavigate: (path: string) => void; onResume: (draft: Draft) => void }) {
  const rows = drafts.filter(item => matches(query, item.title, item.body, item.type))
  if (!rows.length) return <Empty icon={<FilePenLine/>} title="No active drafts" body="Unfinished issues, comments, and documents appear here automatically."/>
  return <div className="operations-list">{rows.map(item => <div className="operations-row" key={item.id}><span className="operations-glyph"><FilePenLine/></span><button className="operations-row-main" onClick={() => item.type === 'issue' && !item.resourceId ? onResume(item) : item.resourceId && resourcePath(data,item.type,item.resourceId) ? onNavigate(resourcePath(data,item.type,item.resourceId)) : toast.info('Open the matching composer to continue this draft')}><strong>{item.title || 'Untitled draft'}</strong><small>{item.type} · Edited {relative(item.updatedAt)} ago</small></button><RowMenu><MenuItem icon={<Trash2/>} danger onSelect={() => void onDelete(item)}>Delete draft</MenuItem></RowMenu></div>)}</div>
}

function clearLocalIssueDraft(draft: Draft) {
  if (draft.type !== 'issue') return
  const teamId = typeof draft.metadata?.teamId === 'string' ? draft.metadata.teamId : ''
  if (!teamId) return
  try { localStorage.removeItem(`flow:create-issue-draft:${teamId}`) } catch { /* Storage cleanup is best-effort. */ }
}

function resourceLabel(data: BootstrapData, type: string, id: string) { if (type === 'issue') return data.issues.find(x => x.id === id)?.title; if (type === 'project') return data.projects.find(x => x.id === id)?.name; if (type === 'document') return data.documents.find(x => x.id === id)?.title; if (type === 'release') return data.releases.find(x => x.id === id)?.name; if (type === 'customer') return data.customers.find(x => x.id === id)?.name; if (type === 'initiative') return data.initiatives.find(x => x.id === id)?.name; if (type === 'view') return data.savedViews.find(x => x.id === id)?.name; return undefined }
function resourcePath(data: BootstrapData, type: string, id: string) { if (type === 'issue') { const value=data.issues.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/issue/${value.identifier}` : '' } if (type === 'project') { const value=data.projects.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/project/${value.slugId}/overview` : '' } if (type === 'document') { const value=data.documents.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/document/${value.slugId}` : '' } if(type==='initiative'){const value=data.initiatives.find(x=>x.id===id);return value?`/${data.workspace.urlKey}/initiative/${value.slugId}/overview`:''} if(type==='customer'){const value=data.customers.find(x=>x.id===id);return value?`/${data.workspace.urlKey}/customer/${operationSlug(value.name)}-${value.id.slice(-12)}`:''} if(type==='release')return `/${data.workspace.urlKey}/releases?release=${encodeURIComponent(id)}`; if(type==='view'){const value=data.savedViews.find(x=>x.id===id);if(!value)return '';const team=value.scope==='team'?data.teams.find(x=>x.id===value.teamId):undefined;const root=`/${data.workspace.urlKey}`;if((value.resource??'issues')==='projects')return team?`${root}/team/${team.key}/projects/view/${encodeURIComponent(id)}`:`${root}/projects/view/${encodeURIComponent(id)}`;return team?`${root}/team/${team.key}/view/${encodeURIComponent(id)}`:`${root}/view/${encodeURIComponent(id)}`} return '' }
function operationSlug(value:string){return value.normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]+/gu,'-').replace(/^-+|-+$/g,'').slice(0,80)||'item'}
function FavoriteRows({ data, query, onNavigate }: { data: BootstrapData; query: string; onNavigate: (path: string) => void }) { const rows = data.favorites.map(item => ({...item,label:resourceLabel(data,item.resourceType,item.resourceId)})).filter(item => item.label && matches(query,item.label,item.resourceType)); if (!rows.length) return <Empty icon={<Star/>} title="No favorites" body="Favorite an issue, project, document, customer, or view to find it here."/>; return <div className="operations-list">{rows.map(item => <button className="operations-row" key={item.id} onClick={() => { const path=resourcePath(data,item.resourceType,item.resourceId); if(path)onNavigate(path) }}><span className="operations-glyph"><Star/></span><span className="operations-row-main"><strong>{item.label}</strong><small>{item.resourceType}</small></span></button>)}</div> }
function RecentRows({ data, recent, query, onNavigate }: { data: BootstrapData; recent: SearchResponse['recent']; query: string; onNavigate: (path: string) => void }) { const rows=recent.map(item=>({...item,label:resourceLabel(data,item.resourceType,item.resourceId)})).filter(item=>item.label&&matches(query,item.label,item.resourceType)); if(!rows.length)return <Empty icon={<History/>} title="No recent items" body="Resources you open across the workspace will appear here."/>; return <div className="operations-list">{rows.map(item=><button className="operations-row" key={`${item.resourceType}:${item.resourceId}`} onClick={()=>{const path=resourcePath(data,item.resourceType,item.resourceId);if(path)onNavigate(path)}}><span className="operations-glyph"><History/></span><span className="operations-row-main"><strong>{item.label}</strong><small>{item.resourceType} · {relative(item.lastViewedAt)} ago</small></span></button>)}</div> }

function AskDecisionDialog({ value, onClose, onSubmit }: { value: { ask: Ask; decision: 'approved'|'rejected' }; onClose: () => void; onSubmit: (note: string) => Promise<void> }) {
  const [note,setNote]=useState('')
  const [saving,setSaving]=useState(false)
  const approve=value.decision==='approved'
  const submit=async()=>{setSaving(true);try{await onSubmit(note.trim())}catch(error){toast.error(error instanceof Error?error.message:'Could not update ask')}finally{setSaving(false)}}
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog operations-decision-dialog"><DialogTitle>{approve?'Approve ask':'Reject ask'}</DialogTitle><div className="operations-decision-summary"><span className={`operations-ask-status ${value.ask.status}`}><Clock3/></span><div><strong>{value.ask.title}</strong><small>{value.ask.requester.displayName} · {value.ask.source}</small></div></div><label><span>Decision note <small>(optional)</small></span><textarea autoFocus value={note} onChange={event=>setNote(event.target.value)} placeholder={approve?'Add context for the created issue…':'Explain why this request was rejected…'}/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className={approve?'primary':'danger'} disabled={saving} onClick={()=>void submit()}>{saving?'Saving…':approve?'Approve and create issue':'Reject ask'}</button></footer></DialogContent></Dialog>
}

function CreateOperationDialog({ view, data, release, onClose, onCreated }: { view: 'releases'|'asks'; data: BootstrapData; release?: Release; onClose: () => void; onCreated: () => Promise<void> }) {
  const [name,setName]=useState(release?.name??''); const [description,setDescription]=useState(release?.description??''); const [saving,setSaving]=useState(false); const [teamId,setTeamId]=useState(data.teams[0]?.id??'')
  const [templateId,setTemplateId]=useState('')
  const [version,setVersion]=useState(release?.version??''); const [status,setStatus]=useState<Release['status']>(release?.status??'planned'); const [targetDate,setTargetDate]=useState(release?.targetDate??'')
  const [projectIds,setProjectIds]=useState<string[]>(release?.projectIds??[]); const [issueIds,setIssueIds]=useState<string[]>(release?.issueIds??[])
  const [scopeOpen,setScopeOpen]=useState(false)
  const toggle=(values:string[],id:string,setter:(next:string[])=>void)=>setter(values.includes(id)?values.filter(value=>value!==id):[...values,id])
  const templates=data.issueTemplates.filter(item=>item.teamId===teamId)
  const submit=async()=>{setSaving(true);try{if(view==='releases'){const input={name,description,version,status,targetDate,projectIds,issueIds};if(release)await updateRelease(release.id,input);else await createRelease(input)}else await createAsk({title:name,body:description,teamId,templateId:templateId||undefined});await onCreated()}catch(error){toast.error(error instanceof Error?error.message:'Could not save')}finally{setSaving(false)}}
  if(view==='releases') return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent aria-describedby={undefined} className={`release-editor-dialog${scopeOpen?' is-scope-open':''}`}>
    <DialogTitle className="release-editor-title"><span>Production releases</span><ChevronRight/><strong>{release?'Edit release':'New release'}</strong></DialogTitle>
    <div className="release-editor-copy">
      <input aria-label="Release name" autoFocus className="release-editor-name" value={name} onChange={event=>setName(event.target.value)} placeholder="Release name"/>
      <input aria-label="Release version" className="release-editor-version" value={version} onChange={event=>setVersion(event.target.value)} placeholder="Version"/>
      <textarea aria-label="Release description" className="release-editor-description" value={description} onChange={event=>setDescription(event.target.value)} placeholder="Add description…"/>
    </div>
    <div className="release-editor-properties">
      <label className="release-editor-pill"><CircleDashed/><select aria-label="Change release stage" value={status} onChange={event=>setStatus(event.target.value as Release['status'])}><option value="planned">Planned</option><option value="inProgress">In progress</option><option value="released">Released</option><option value="canceled">Canceled</option></select><ChevronDown/></label>
      <label className="release-editor-pill release-date-pill"><CalendarDays/><span className={targetDate?'has-value':''}>{targetDate||'Target date'}</span><input aria-label="Target date" type="date" value={targetDate} onChange={event=>setTargetDate(event.target.value)}/></label>
      <button aria-expanded={scopeOpen} className="release-editor-pill release-scope-trigger" onClick={()=>setScopeOpen(value=>!value)} type="button"><Settings2/><span>Scope</span>{projectIds.length+issueIds.length>0&&<b>{projectIds.length+issueIds.length}</b>}<ChevronDown/></button>
    </div>
    {scopeOpen&&<div className="release-editor-scope"><fieldset><legend>Projects</legend>{data.projects.length?data.projects.map(project=><label key={project.id}><input type="checkbox" checked={projectIds.includes(project.id)} onChange={()=>toggle(projectIds,project.id,setProjectIds)}/><i style={{background:project.color}}/><span>{project.name}</span></label>):<small>No projects</small>}</fieldset><fieldset><legend>Issues</legend>{data.issues.length?data.issues.slice(0,100).map(issue=><label key={issue.id}><input type="checkbox" checked={issueIds.includes(issue.id)} onChange={()=>toggle(issueIds,issue.id,setIssueIds)}/><span><b>{issue.identifier}</b>{issue.title}</span></label>):<small>No issues</small>}</fieldset></div>}
    <footer><button disabled={saving} onClick={onClose} type="button">Cancel</button><button className="primary" disabled={!name.trim()||saving} onClick={()=>void submit()} type="button">{saving?'Saving…':release?'Save changes':'Create release'}</button></footer>
  </DialogContent></Dialog>
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog"><DialogTitle>Create ask</DialogTitle><label><span>Request title</span><input autoFocus value={name} onChange={e=>setName(e.target.value)} placeholder="What do you need?"/></label><div className="operations-dialog-fields"><label><span>Team</span><select value={teamId} onChange={e=>{setTeamId(e.target.value);setTemplateId('')}}>{data.teams.map(team=><option value={team.id} key={team.id}>{team.name}</option>)}</select></label><label><span>Issue template</span><select value={templateId} onChange={e=>setTemplateId(e.target.value)}><option value="">No template</option>{templates.map(template=><option value={template.id} key={template.id}>{template.name}</option>)}</select></label></div><label><span>Description</span><textarea value={description} onChange={e=>setDescription(e.target.value)} placeholder="Add details…"/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className="primary" disabled={!name.trim()||saving} onClick={()=>void submit()}>{saving?'Saving…':'Create'}</button></footer></DialogContent></Dialog>
}
