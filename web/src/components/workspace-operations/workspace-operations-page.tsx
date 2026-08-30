import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, Clock3, FilePenLine, Inbox, Mail, Menu, MessageCircleQuestion, MoreHorizontal, Plus, Trash2, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { ReleasesPage } from '@/components/releases/releases-page'
import { createAsk, decideAsk, deleteAllDrafts, deleteAsk, deleteDraft } from '@/lib/api'
import type { Ask, BootstrapData, Draft } from '@/types/flow'
import type { ReleasePipelineTab, ReleaseRouteTab } from '@/lib/app-routes'
import { releasePath, releasePipelinePath, settingsPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import { StatusIcon } from '@/components/issue/issue-icons'

import './workspace-operations.css'

export type OperationsView = 'drafts'|'releases'|'asks'

export function WorkspaceOperationsPage({ data, view, pipelineSlug, releaseSlug, pipelineTab, releaseTab, onOpenSidebar, onReload, onNavigate, onResumeDraft }: { data: BootstrapData; view: OperationsView; pipelineSlug?: string; releaseSlug?: string; pipelineTab?: ReleasePipelineTab; releaseTab?: ReleaseRouteTab; onOpenSidebar: () => void; onReload: () => Promise<void>; onNavigate: (path: string) => void; onResumeDraft: (draft: Draft) => void }) {
  const [createOpen, setCreateOpen] = useState(false)
  const [askDecision, setAskDecision] = useState<{ ask: Ask; decision: 'approved'|'rejected' }>()
  const [askSurface, setAskSurface] = useState<'configuration'|'intake'>('configuration')
  if (view === 'releases') return <ReleasesPage data={data} pipelineSlug={pipelineSlug} releaseSlug={releaseSlug} pipelineTab={pipelineTab} releaseTab={releaseTab} onOpenSidebar={onOpenSidebar} onReload={onReload} onNavigate={onNavigate}/>
  if (view === 'asks') return <>
    <AsksPage data={data} surface={askSurface} onSurfaceChange={setAskSurface} onOpenSidebar={onOpenSidebar} onCreate={() => setCreateOpen(true)} onOpenSettings={page=>onNavigate(settingsPath(data.workspace.urlKey,page))} onDecide={(ask, decision) => setAskDecision({ ask, decision })} onDelete={async id => { await deleteAsk(id); await onReload() }}/>
    {createOpen && (
      <CreateAskDialog data={data} onClose={() => setCreateOpen(false)} onCreated={async () => { setCreateOpen(false); await onReload() }}/>
    )}
    {askDecision && (
      <AskDecisionDialog value={askDecision} onClose={() => setAskDecision(undefined)} onSubmit={async note => { await decideAsk(askDecision.ask.id, askDecision.decision, note); setAskDecision(undefined); await onReload() }}/>
    )}
  </>
  if (view === 'drafts') return <DraftsPage data={data} onOpenSidebar={onOpenSidebar} onReload={onReload} onNavigate={onNavigate} onResume={onResumeDraft}/>
  return null
}

function OperationsTopBar({ title, onOpenSidebar, children }: { title: ReactNode; onOpenSidebar: () => void; children?: ReactNode }) {
  return <header className="operations-header operations-special-header"><button className="operations-mobile-menu" aria-label="Open sidebar" onClick={onOpenSidebar}><Menu/></button><div className="operations-heading">{title}</div><div className="operations-header-actions">{children}</div></header>
}

function AsksPage({ data, surface, onSurfaceChange, onOpenSidebar, onCreate, onOpenSettings, onDecide, onDelete }: { data: BootstrapData; surface: 'configuration'|'intake'; onSurfaceChange: (value: 'configuration'|'intake') => void; onOpenSidebar: () => void; onCreate: () => void; onOpenSettings:(page:'asks'|'integrations')=>void; onDecide: (ask: Ask, decision: 'approved'|'rejected') => void; onDelete: (id: string) => Promise<void> }) {
  const pending=data.asks.filter(item=>item.status==='pending').length
  return <main className="main-panel operations-page asks-page" aria-label="Asks">
    <OperationsTopBar title={<h1>Asks</h1>} onOpenSidebar={onOpenSidebar}><button className="operations-icon-button" aria-label="Create ask" onClick={onCreate}><Plus/></button></OperationsTopBar>
    <div className="asks-content">
      <div className="asks-intro"><h2>Asks</h2><p>Let anyone submit bug reports, feature requests, and more using structured templates from Slack or email. <a href="https://flow.app/docs/asks" target="_blank" rel="noreferrer">Docs ↗</a></p><div className="asks-mode"><button className={surface==='configuration'?'active':''} onClick={()=>onSurfaceChange('configuration')}>Configuration</button><button className={surface==='intake'?'active':''} onClick={()=>onSurfaceChange('intake')}>Intake {pending>0&&<span>{pending}</span>}</button></div></div>
      {surface === 'configuration' ? <div className="asks-config">
        <section><header><h3>Slack</h3><p>Allow anyone in your Slack workspace to submit Asks using templated forms</p></header><button className="asks-config-row" onClick={()=>onOpenSettings('integrations')}><span className="asks-source-icon slack"><MessageCircleQuestion/></span><span><strong>No workspaces connected</strong><small>Connect Slack to route requests into team Triage.</small></span><Plus/></button></section>
        <section><header><h3>Email</h3><p>Allow anyone to submit Asks by emailing a custom address</p></header><button className="asks-config-row" onClick={()=>onOpenSettings('asks')}><span className="asks-source-icon mail"><Mail/></span><span><strong>No email addresses configured</strong><small>Create an intake address and choose a team and template.</small></span><Plus/></button></section>
      </div> : <div className="asks-intake"><div className="asks-intake-header"><span>{`${data.asks.length} requests`}</span><button className="flow-primary" onClick={onCreate}><Plus/> New request</button></div>{data.asks.length?<div className="asks-intake-list">{data.asks.map(item=><div className="ask-flow-row" key={item.id}><span className={`operations-ask-status ${item.status}`}>{item.status==='pending'?<Clock3/>:<Check/>}</span><div><strong>{item.title}</strong><small>{item.requester.displayName} · {item.source} · {relative(item.createdAt)} ago</small></div><span className={`ask-state ${item.status}`}>{item.status}</span><RowMenu>{item.status==='pending'&&<><OperationsMenuItem icon={<Check/>} onSelect={()=>onDecide(item,'approved')}>Approve...</OperationsMenuItem><OperationsMenuItem icon={<X/>} onSelect={()=>onDecide(item,'rejected')}>Reject...</OperationsMenuItem></>}<OperationsMenuItem icon={<Trash2/>} danger onSelect={()=>void onDelete(item.id)}>Delete ask</OperationsMenuItem></RowMenu></div>)}</div>:<div className="flow-empty-state compact"><Inbox/><strong>No asks yet</strong><span>Requests submitted through Slack or email will appear here for review.</span></div>}</div>}
    </div>
  </main>
}


function RowMenu({ children }: { children: ReactNode }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="operations-row-menu" aria-label="Open actions"><MoreHorizontal size={15}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="operations-menu" align="end" sideOffset={5}>{children}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function OperationsMenuItem({ icon, children, onSelect, danger }: { icon: ReactNode; children: ReactNode; onSelect: () => void; danger?: boolean }) { return <DropdownMenu.Item className={danger ? 'danger' : ''} onSelect={onSelect}>{icon}<span>{children}</span></DropdownMenu.Item> }
function relative(value: string) { const delta = Date.now() - new Date(value).getTime(); const minutes = Math.max(0, Math.floor(delta/60000)); if (minutes < 60) return `${minutes || 1}m`; const hours = Math.floor(minutes/60); if (hours < 24) return `${hours}h`; return `${Math.floor(hours/24)}d` }

function DraftsPage({ data, onOpenSidebar, onReload, onNavigate, onResume }: { data: BootstrapData; onOpenSidebar: () => void; onReload: () => Promise<void>; onNavigate: (path: string) => void; onResume: (draft: Draft) => void }) {
  const { t } = useI18n()
  const drafts = data.drafts.filter(item => item.userId === data.viewer.id)
  const [confirm, setConfirm] = useState<{ kind: 'one'; draft: Draft } | { kind: 'all' }>()
  const [deleting, setDeleting] = useState(false)
  const discard = async () => {
    if (!confirm) return
    setDeleting(true)
    try {
      if (confirm.kind === 'all') {
        await deleteAllDrafts()
        drafts.forEach(clearLocalIssueDraft)
      } else {
        await deleteDraft(confirm.draft.id)
        clearLocalIssueDraft(confirm.draft)
      }
      setConfirm(undefined)
      await onReload()
    } finally { setDeleting(false) }
  }
  const open = (draft: Draft) => {
    if (draft.type === 'issue' && !draft.resourceId) { onResume(draft); return }
    const path = draft.resourceId ? resourcePath(data, draft.type, draft.resourceId) : ''
    if (path) onNavigate(path)
  }
  const groups = draftGroups(drafts)
  return <main className="main-panel operations-page drafts-page" aria-label={t('Drafts')}>
    <header className="operations-header drafts-header"><button className="operations-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Menu/></button><h1>{t('Drafts')}</h1>{drafts.length > 0 && <button className="draft-discard-all" aria-label={t('Discard all')} onClick={() => setConfirm({ kind: 'all' })} type="button"><Trash2/></button>}</header>
    {drafts.length === 0 ? <DraftEmpty/> : <div className="draft-groups">{groups.map(group => <section key={group.type}><h2>{t(group.label)}</h2><div className="draft-grid">{group.items.map(draft => <article className={`draft-card is-${draft.type}`} key={draft.id}><button aria-label={t('Edit draft')} className="draft-card-link" onClick={() => open(draft)} type="button"/><header><DraftTypeIcon data={data} draft={draft}/><strong data-i18n-ignore>{draft.title || t('Untitled draft')}</strong><time dateTime={draft.updatedAt} title={new Date(draft.updatedAt).toLocaleString()}>{relative(draft.updatedAt)}</time><button aria-label={t('Discard draft')} className="draft-discard" onClick={() => setConfirm({ kind: 'one', draft })} type="button"><Trash2/></button></header>{draft.type === 'comment' && <div className="draft-context"><MessageCircleQuestion/><span>{t('Commenting on an issue')}</span></div>}{draft.body && <div aria-label={t('Draft content')} className="draft-content" role="document" data-i18n-ignore>{draft.body}</div>}</article>)}</div></section>)}</div>}
    <DraftDiscardDialog all={confirm?.kind === 'all'} deleting={deleting} open={Boolean(confirm)} onCancel={() => !deleting && setConfirm(undefined)} onConfirm={() => void discard()}/>
  </main>
}

function draftGroups(drafts: Draft[]) {
  const definitions = [{ type: 'issue', label: 'Issues' }, { type: 'comment', label: 'Comments' }, { type: 'document', label: 'Documents' }]
  const known = new Set(definitions.map(item => item.type))
  const groups = definitions.map(item => ({ ...item, items: drafts.filter(draft => draft.type === item.type) })).filter(group => group.items.length)
  const other = drafts.filter(draft => !known.has(draft.type))
  return other.length ? [...groups, { type: 'other', label: 'Other', items: other }] : groups
}

function DraftTypeIcon({ data, draft }: { data: BootstrapData; draft: Draft }) {
  if (draft.type === 'comment') { const issue=data.issues.find(item=>item.id===draft.resourceId); if(issue)return <StatusIcon state={issue.state} size={14}/> }
  if (draft.type === 'issue') return <svg aria-hidden="true" className="draft-type-icon" viewBox="0 0 14 14"><circle cx="7" cy="7" r="6" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="1.4 1.74" strokeDashoffset=".65"/><circle cx="7" cy="7" r="2" fill="none" stroke="currentColor" strokeWidth="4" strokeDasharray="12.189 24.379" strokeDashoffset="12.189" transform="rotate(-90 7 7)"/></svg>
  return <FilePenLine className="draft-type-icon"/>
}

function DraftDiscardDialog({ all, deleting, onCancel, onConfirm, open }: { all: boolean; deleting: boolean; onCancel: () => void; onConfirm: () => void; open: boolean }) {
  const { t } = useI18n()
  return <Dialog open={open} onOpenChange={value => { if (!value) onCancel() }}><DialogContent aria-describedby="draft-discard-description" className="draft-discard-dialog"><DialogTitle>{t(all ? 'Discard all drafts?' : 'Discard this draft?')}</DialogTitle><p id="draft-discard-description">{t(all ? 'All your drafts will be deleted.' : 'Your draft will be deleted.')}</p><footer><button disabled={deleting} onClick={onCancel} type="button">{t('Cancel')}</button><button autoFocus className="danger" disabled={deleting} onClick={onConfirm} type="button">{t(deleting ? 'Discarding…' : all ? 'Discard all' : 'Discard')}</button></footer></DialogContent></Dialog>
}

function DraftEmpty() {
  const { t } = useI18n()
  return <div className="draft-empty"><DraftEmptyIllustration/><span>{t('No active drafts')}</span></div>
}

function DraftEmptyIllustration() {
  return <svg aria-hidden="true" viewBox="0 0 132 130" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M10.4 99.7c-1.8-1.1-2.8-2.5-2.8-4V91l53-27.2c3.8-1.9 9.3-1.9 13 0L123.8 89v4.8c0 1.5-1 3-2.8 4l-49.8 29.1a15.3 15.3 0 0 1-14 0L10.4 99.7Z" fill="var(--draft-bg)" stroke="var(--draft-line-faint)" strokeWidth="1.5"/><path d="M10.4 94.7c-3.8-2.2-3.8-5.8 0-8L60.2 58c3.8-2.2 10-2.2 13.9 0l47 27.1c3.7 2.2 3.7 5.8 0 8l-49.9 28.7c-3.8 2.3-10 2.3-13.8 0l-47-27Z" fill="var(--draft-bg)" stroke="var(--draft-line-muted)" strokeWidth="1.5"/><path d="M10.4 85.3c-1.8-1-2.8-2.5-2.8-4v-4.6l53-27.2c3.8-2 9.3-2 13 0l50.2 25.2v4.8c0 1.4-1 2.9-2.8 4l-49.8 29a15.3 15.3 0 0 1-14 0L10.4 85.4Z" fill="var(--draft-bg)" stroke="var(--draft-line-muted)" strokeWidth="1.5"/><path d="M10.4 80.4c-3.8-2.2-3.8-5.8 0-8l49.8-28.7c3.8-2.3 10-2.3 13.9 0l47 27c3.7 2.3 3.7 5.9 0 8l-49.9 28.8c-3.8 2.2-10 2.2-13.8 0l-47-27.1Z" fill="var(--draft-bg)" stroke="var(--draft-line)" strokeWidth="1.5"/><path d="M32 82.5 76.2 57M39.8 87 84 61.5m-36.4 30L91.8 66M55.3 96 83 80" stroke="var(--draft-line-faint)" strokeWidth="1.5" strokeLinecap="round"/><path d="M10.3 45c-1.5-1.5-2.1-3.2-1.8-4.6l1.3-4.5L68 23.3c4-.8 9.4.6 12.5 3.4l42 37.3-1.3 4.6c-.4 1.5-1.7 2.6-3.7 3.2L61.9 87a15.3 15.3 0 0 1-13.5-3.6L10.3 45Z" fill="var(--draft-bg)" stroke="var(--draft-line)" strokeWidth="1.5"/><path d="M11.5 40.2c-3-3.1-2.2-6.6 2.1-7.7l55.5-14.9c4.3-1.1 10.3.5 13.4 3.6L121 59.5c3 3.2 2.2 6.6-2.1 7.8L63.3 82a15.3 15.3 0 0 1-13.4-3.6L11.5 40.2Z" fill="var(--draft-bg)" stroke="var(--draft-line-strong)" strokeWidth="1.5"/><path d="m27.2 40.6 49.3-13.2M33.5 47l49.3-13.2M40 53.3l49.3-13.2m-43 19.6L77 51.5" stroke="var(--draft-line-strong)" strokeWidth="1.5" strokeLinecap="round"/><path fillRule="evenodd" clipRule="evenodd" d="M128.1 2.6a5.5 5.5 0 0 1 0 7.8l-33 33A19.4 19.4 0 0 1 82.6 49c-.5 0-1-.4-.9-1 .3-4.6 2.3-9 5.6-12.4l33-33a5.5 5.5 0 0 1 7.8 0Z" fill="var(--draft-bg)" stroke="var(--draft-line)" strokeWidth="1.5"/></svg>
}

function clearLocalIssueDraft(draft: Draft) {
  if (draft.type !== 'issue') return
  const teamId = typeof draft.metadata?.teamId === 'string' ? draft.metadata.teamId : ''
  if (!teamId) return
  try { localStorage.removeItem(`flow:create-issue-draft:${teamId}`) } catch { /* Storage cleanup is best-effort. */ }
}

function resourcePath(data: BootstrapData, type: string, id: string) { if (type === 'issue') { const value=data.issues.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/issue/${value.identifier}` : '' } if (type === 'project') { const value=data.projects.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/project/${value.slugId}/overview` : '' } if (type === 'document') { const value=data.documents.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/document/${value.slugId}` : '' } if(type==='initiative'){const value=data.initiatives.find(x=>x.id===id);return value?`/${data.workspace.urlKey}/initiative/${value.slugId}/overview`:''} if(type==='customer'){const value=data.customers.find(x=>x.id===id);return value?`/${data.workspace.urlKey}/customer/${operationSlug(value.name)}-${value.id.slice(-12)}`:''} if(type==='release'){const value=data.releases.find(item=>item.id===id);const pipeline=value?data.releasePipelines.find(item=>item.id===value.pipelineId):undefined;return value&&pipeline?releasePath(data.workspace.urlKey,pipeline.slugId,value.slugId):''} if(type==='release_pipeline'){const value=data.releasePipelines.find(item=>item.id===id);return value?releasePipelinePath(data.workspace.urlKey,value.slugId):''} if(type==='view'){const value=data.savedViews.find(x=>x.id===id);if(!value)return '';const team=value.scope==='team'?data.teams.find(x=>x.id===value.teamId):undefined;const root=`/${data.workspace.urlKey}`;if((value.resource??'issues')==='projects')return team?`${root}/team/${team.key}/projects/view/${encodeURIComponent(id)}`:`${root}/projects/view/${encodeURIComponent(id)}`;return team?`${root}/team/${team.key}/view/${encodeURIComponent(id)}`:`${root}/view/${encodeURIComponent(id)}`} return '' }
function operationSlug(value:string){return value.normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]+/gu,'-').replace(/^-+|-+$/g,'').slice(0,80)||'item'}

function AskDecisionDialog({ value, onClose, onSubmit }: { value: { ask: Ask; decision: 'approved'|'rejected' }; onClose: () => void; onSubmit: (note: string) => Promise<void> }) {
  const [note,setNote]=useState('')
  const [saving,setSaving]=useState(false)
  const approve=value.decision==='approved'
  const submit=async()=>{setSaving(true);try{await onSubmit(note.trim())}catch(error){toast.error(error instanceof Error?error.message:'Could not update ask')}finally{setSaving(false)}}
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog operations-decision-dialog"><DialogTitle>{approve?'Approve ask':'Reject ask'}</DialogTitle><div className="operations-decision-summary"><span className={`operations-ask-status ${value.ask.status}`}><Clock3/></span><div><strong>{value.ask.title}</strong><small>{value.ask.requester.displayName} · {value.ask.source}</small></div></div><label><span>Decision note <small>(optional)</small></span><textarea autoFocus value={note} onChange={event=>setNote(event.target.value)} placeholder={approve?'Add context for the created issue…':'Explain why this request was rejected…'}/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className={approve?'primary':'danger'} disabled={saving} onClick={()=>void submit()}>{saving?'Saving…':approve?'Approve and create issue':'Reject ask'}</button></footer></DialogContent></Dialog>
}

function CreateAskDialog({ data, onClose, onCreated }: { data: BootstrapData; onClose: () => void; onCreated: () => Promise<void> }) {
  const [name,setName]=useState(''); const [description,setDescription]=useState(''); const [saving,setSaving]=useState(false); const [teamId,setTeamId]=useState(data.teams[0]?.id??'')
  const [templateId,setTemplateId]=useState('')
  const templates=data.issueTemplates.filter(item=>item.teamId===teamId)
  const submit=async()=>{setSaving(true);try{await createAsk({title:name,body:description,teamId,templateId:templateId||undefined});await onCreated()}catch(error){toast.error(error instanceof Error?error.message:'Could not save')}finally{setSaving(false)}}
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog"><DialogTitle>Create ask</DialogTitle><label><span>Request title</span><input autoFocus value={name} onChange={e=>setName(e.target.value)} placeholder="What do you need?"/></label><div className="operations-dialog-fields"><label><span>Team</span><select value={teamId} onChange={e=>{setTeamId(e.target.value);setTemplateId('')}}>{data.teams.map(team=><option value={team.id} key={team.id}>{team.name}</option>)}</select></label><label><span>Issue template</span><select value={templateId} onChange={e=>setTemplateId(e.target.value)}><option value="">No template</option>{templates.map(template=><option value={template.id} key={template.id}>{template.name}</option>)}</select></label></div><label><span>Description</span><textarea value={description} onChange={e=>setDescription(e.target.value)} placeholder="Add details…"/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className="primary" disabled={!name.trim()||saving} onClick={()=>void submit()}>{saving?'Saving…':'Create'}</button></footer></DialogContent></Dialog>
}
