import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, Clock3, FilePenLine, GitPullRequest, Inbox, Mail, Menu, MessageCircleQuestion, MoreHorizontal, Plus, Trash2, UserRound, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import { ReleasesPage } from '@/components/releases/releases-page'
import { createAsk, decideAsk, deleteAllDrafts, deleteAsk, deleteDraft } from '@/lib/api'
import type { Ask, BootstrapData, Draft } from '@/types/flow'
import type { ReleasePipelineTab, ReleaseRouteTab } from '@/lib/app-routes'
import { customerPath, documentPath, initiativePath, issuePath, newLoopPath, projectPath, releasePath, releasePipelinePath, reviewPath, settingsPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import { clearComposerDraft, readLocalComposerDrafts } from '@/lib/composer-drafts'
import { StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'

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
  const localDrafts = readLocalIssueDrafts(data)
  const localComposerDrafts = readLocalComposerDrafts(data.viewer.id)
  const localTeamIds = new Set(localDrafts.map(item => typeof item.metadata?.teamId === 'string' ? item.metadata.teamId : ''))
  const localComposerKeys = new Set(localComposerDrafts.map(item => `${item.type}:${item.resourceId}`))
  const drafts = [...data.drafts.filter(item => item.userId === data.viewer.id && isVisibleDraft(item) && !(item.type === 'issue' && localTeamIds.has(typeof item.metadata?.teamId === 'string' ? item.metadata.teamId : '')) && !localComposerKeys.has(`${item.type}:${item.resourceId ?? ''}`)), ...localDrafts, ...localComposerDrafts]
  const [confirm, setConfirm] = useState<{ kind: 'one'; draft: Draft } | { kind: 'all' }>()
  const [deleting, setDeleting] = useState(false)
  const discard = async () => {
    if (!confirm) return
    setDeleting(true)
    try {
      if (confirm.kind === 'all') {
        await deleteAllDrafts()
        drafts.forEach(clearLocalDraft)
      } else {
        const remoteId = typeof confirm.draft.metadata?.remoteId === 'string' ? confirm.draft.metadata.remoteId : ''
        if (!confirm.draft.id.startsWith('local:')) await deleteDraft(confirm.draft.id)
        else if (remoteId) await deleteDraft(remoteId)
        clearLocalDraft(confirm.draft)
      }
      setConfirm(undefined)
      await onReload()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not discard draft'))
    } finally { setDeleting(false) }
  }
  const open = (draft: Draft) => {
    if (draft.type === 'issue' && !draft.resourceId) { onResume(draft); return }
    if (draft.type === 'loop' && !draft.resourceId) { onNavigate(`${newLoopPath(data.workspace.urlKey)}?draftId=${encodeURIComponent(draft.id)}`); return }
    const path = draft.resourceId ? resourcePath(data, draft) : ''
    if (path) onNavigate(path)
  }
  const groups = draftGroups(drafts)
  return <main className="main-panel operations-page drafts-page" aria-label={t('Drafts')}>
    <header className="operations-header drafts-header"><button className="operations-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Menu/></button><h2>{t('Drafts')}</h2>{drafts.length > 0 && <button className="draft-discard-all" aria-label={t('Discard all')} onClick={() => setConfirm({ kind: 'all' })} type="button"><Trash2/></button>}</header>
    {drafts.length === 0 ? <DraftEmpty/> : <div className="draft-groups">{groups.map(group => <section key={group.type}><h2>{t(group.label)}</h2><div className="draft-grid">{group.items.map(draft => { const href = draftHref(data, draft); return <article className={`draft-card is-${draft.type}`} key={draft.id}>{href ? <a aria-label={t('Edit draft')} className="draft-card-link" href={href} onClick={event => { event.preventDefault(); open(draft) }}/> : <button aria-label={t('Edit draft')} className="draft-card-link" onClick={() => open(draft)} type="button"/>}<header><DraftTypeIcon data={data} draft={draft}/><strong data-i18n-ignore>{draftTitle(data, draft) || t('Untitled draft')}</strong><time dateTime={draft.updatedAt} title={new Date(draft.updatedAt).toLocaleString()}>{relative(draft.updatedAt)}</time><button aria-label={t('Discard draft')} className="draft-discard" onClick={() => setConfirm({ kind: 'one', draft })} type="button"><Trash2/></button></header>{draft.type === 'loop' && <div className="draft-loop-scope"><ViewGlyph color="currentColor" icon="Team"/><span>{draft.metadata?.level === 'team' ? t('Team') : t('Workspace')}</span></div>}{(draft.type === 'comment' || draft.type === 'project_update' || draft.type === 'initiative_update' || draft.type === 'customer_need' || draft.type === 'pull_request_comment') && <div className="draft-context"><DraftContextIcon data={data} draft={draft}/><span>{t(draftContextLabel(draft))}</span></div>}{draft.type !== 'loop' && draft.body && <div aria-label={t('Draft content')} className="draft-content" role="document" data-i18n-ignore>{draft.body}</div>}</article>})}</div></section>)}</div>}
    <DraftDiscardDialog all={confirm?.kind === 'all'} deleting={deleting} open={Boolean(confirm)} onCancel={() => !deleting && setConfirm(undefined)} onConfirm={() => void discard()}/>
  </main>
}

function draftGroups(drafts: Draft[]) {
  const definitions = [
    { type: 'issue', label: 'Issues' },
    { type: 'loop', label: 'Loops' },
    { type: 'comment', label: 'Comments' },
    { type: 'project_update', label: 'Project updates' },
    { type: 'initiative_update', label: 'Initiative updates' },
    { type: 'customer_need', label: 'Customer requests' },
    { type: 'pull_request_comment', label: 'Pull request comments' },
    { type: 'document', label: 'Documents' },
  ]
  const known = new Set(definitions.map(item => item.type))
  const groups = definitions.map(item => ({ ...item, items: drafts.filter(draft => draft.type === item.type) })).filter(group => group.items.length)
  const other = drafts.filter(draft => !known.has(draft.type))
  return other.length ? [...groups, { type: 'other', label: 'Other', items: other }] : groups
}

function isVisibleDraft(draft: Draft) {
  const draftName = typeof draft.metadata?.name === 'string' ? draft.metadata.name : ''
  const instructions = typeof draft.metadata?.instructions === 'string' ? draft.metadata.instructions : ''
  return Boolean(draft.title.trim() || draft.body.trim() || draftName.trim() || instructions.trim())
}

function draftHref(data: BootstrapData, draft: Draft) {
  if (draft.type === 'loop' && !draft.resourceId) return `${newLoopPath(data.workspace.urlKey)}?draftId=${encodeURIComponent(draft.id)}`
  return draft.resourceId ? resourcePath(data, draft) : ''
}

function DraftTypeIcon({ data, draft }: { data: BootstrapData; draft: Draft }) {
  if (draft.type === 'comment') {
    const parentType = typeof draft.metadata?.resourceType === 'string' ? draft.metadata.resourceType : 'issue'
    if (parentType === 'issue') { const issue=data.issues.find(item=>item.id===draft.resourceId); if(issue)return <StatusIcon state={issue.state} size={14}/> }
    if (parentType === 'project') { const project=data.projects.find(item=>item.id===draft.resourceId); if(project)return <ViewGlyph color={project.color} icon="Project" className="draft-type-icon"/> }
    if (parentType === 'initiative') { const initiative=data.initiatives.find(item=>item.id===draft.resourceId); if(initiative)return <ViewGlyph color={initiative.color} icon="Initiative" className="draft-type-icon"/> }
    if (parentType === 'review') return <GitPullRequest className="draft-type-icon"/>
  }
  if (draft.type === 'project_update') { const project=data.projects.find(item=>item.id===draft.resourceId); if(project)return <ViewGlyph color={project.color} icon="Project" className="draft-type-icon"/> }
  if (draft.type === 'initiative_update') { const initiative=data.initiatives.find(item=>item.id===draft.resourceId); if(initiative)return <ViewGlyph color={initiative.color} icon="Initiative" className="draft-type-icon"/> }
  if (draft.type === 'customer_need') return <UserRound className="draft-type-icon"/>
  if (draft.type === 'pull_request_comment') return <GitPullRequest className="draft-type-icon"/>
  if (draft.type === 'issue') return <svg aria-hidden="true" className="draft-type-icon" viewBox="0 0 14 14"><circle cx="7" cy="7" r="6" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="1.4 1.74" strokeDashoffset=".65"/><circle cx="7" cy="7" r="2" fill="none" stroke="currentColor" strokeWidth="4" strokeDasharray="12.189 24.379" strokeDashoffset="12.189" transform="rotate(-90 7 7)"/></svg>
  if (draft.type === 'loop') return <ViewGlyph className="draft-type-icon" color="#697482" icon="Automation" />
  return <FilePenLine className="draft-type-icon"/>
}

function DraftContextIcon({ data, draft }: { data: BootstrapData; draft: Draft }) {
  const parentType = draft.type === 'comment' && typeof draft.metadata?.resourceType === 'string' ? draft.metadata.resourceType : draft.type
  if (parentType === 'issue') { const issue = data.issues.find(item => item.id === draft.resourceId); if (issue) return <StatusIcon state={issue.state} size={14}/> }
  if (parentType === 'project' || parentType === 'project_update') { const project = data.projects.find(item => item.id === draft.resourceId); if (project) return <ViewGlyph color={project.color} icon="Project"/> }
  if (parentType === 'initiative' || parentType === 'initiative_update') { const initiative = data.initiatives.find(item => item.id === draft.resourceId); if (initiative) return <ViewGlyph color={initiative.color} icon="Initiative"/> }
  if (parentType === 'review' || parentType === 'pull_request_comment') return <GitPullRequest/>
  return <MessageCircleQuestion/>
}

function draftContextLabel(draft: Draft) {
  if (draft.type === 'project_update') return 'Writing a project update'
  if (draft.type === 'initiative_update') return 'Writing an initiative update'
  if (draft.type === 'customer_need') return 'Writing a customer request'
  if (draft.type === 'pull_request_comment') return 'Commenting on a pull request'
  const parentType = typeof draft.metadata?.resourceType === 'string' ? draft.metadata.resourceType : 'issue'
  return parentType === 'project' ? 'Commenting on a project' : parentType === 'initiative' ? 'Commenting on an initiative' : parentType === 'document' ? 'Commenting on a document' : parentType === 'review' ? 'Commenting on a pull request' : 'Commenting on an issue'
}

function draftTitle(data: BootstrapData, draft: Draft) {
  if (draft.title.trim()) return draft.title
  const parentType = draft.type === 'comment' && typeof draft.metadata?.resourceType === 'string' ? draft.metadata.resourceType : draft.type === 'project_update' ? 'project' : draft.type === 'initiative_update' ? 'initiative' : draft.type === 'customer_need' ? 'customer' : draft.type === 'pull_request_comment' ? 'review' : ''
  if (parentType === 'project') return data.projects.find(item => item.id === draft.resourceId)?.name ?? ''
  if (parentType === 'initiative') return data.initiatives.find(item => item.id === draft.resourceId)?.name ?? ''
  if (parentType === 'customer') return data.customers.find(item => item.id === draft.resourceId)?.name ?? ''
  if (parentType === 'review') return data.reviews.find(item => item.id === draft.resourceId || item.slugId === draft.resourceId)?.title ?? ''
  if (parentType === 'issue') return data.issues.find(item => item.id === draft.resourceId)?.title ?? ''
  return ''
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

function clearLocalDraft(draft: Draft) {
  clearLocalIssueDraft(draft)
  if ((draft.type === 'comment' || draft.type === 'project_update' || draft.type === 'initiative_update') && draft.resourceId) clearComposerDraft(draft.type, draft.resourceId)
}

function readLocalIssueDrafts(data: BootstrapData): Draft[] {
  if (typeof localStorage === 'undefined') return []
  const remoteByTeam = new Map<string, Draft>()
  for (const item of data.drafts) {
    if (item.userId !== data.viewer.id || item.type !== 'issue') continue
    const teamId = typeof item.metadata?.teamId === 'string' ? item.metadata.teamId : ''
    const current = remoteByTeam.get(teamId)
    if (!current || current.updatedAt.localeCompare(item.updatedAt) < 0) remoteByTeam.set(teamId, item)
  }
  const drafts: Draft[] = []
  try {
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index)
      if (!key?.startsWith('flow:create-issue-draft:')) continue
      const teamId = key.slice('flow:create-issue-draft:'.length)
      if (!teamId || !data.teams.some(team => team.id === teamId)) continue
      const value = JSON.parse(localStorage.getItem(key) ?? 'null') as Record<string, unknown> | null
      if (!value || typeof value !== 'object') continue
      const description = value.description && typeof value.description === 'object' ? value.description as Record<string, unknown> : undefined
      const title = typeof value.title === 'string' ? value.title : ''
      const body = typeof description?.markdown === 'string' ? description.markdown : ''
      if (!title.trim() && !body.trim()) continue
      const updatedAt = typeof value.updatedAt === 'string' ? value.updatedAt : new Date().toISOString()
      const remote = remoteByTeam.get(teamId)
      if (remote && Date.parse(remote.updatedAt) >= Date.parse(updatedAt)) continue
      drafts.push({
        id: `local:${teamId}`,
        userId: data.viewer.id,
        type: 'issue',
        title,
        body,
        metadata: { ...value, teamId },
        createdAt: updatedAt,
        updatedAt,
      })
    }
  } catch { /* Local draft discovery is best-effort in private browsing. */ }
  return drafts
}

function resourcePath(data: BootstrapData, draft: Draft) {
  const id = draft.resourceId ?? ''
  const rawType = draft.type === 'comment' && typeof draft.metadata?.resourceType === 'string' ? draft.metadata.resourceType : draft.type
  const targetType = rawType === 'project_update' ? 'project' : rawType === 'initiative_update' ? 'initiative' : rawType === 'customer_need' ? 'customer' : rawType === 'pull_request_comment' ? 'review' : rawType
  if (targetType === 'issue') {
    const value = data.issues.find(item => item.id === id)
    return value ? issuePath(data.workspace.urlKey, value) : ''
  }
  if (targetType === 'project') {
    const value = data.projects.find(item => item.id === id)
    return value ? projectPath(data.workspace.urlKey, value, draft.type === 'project_update' || rawType === 'project' ? 'activity' : 'overview') : ''
  }
  if (targetType === 'document') {
    const value = data.documents.find(item => item.id === id)
    return value ? documentPath(data.workspace.urlKey, value) : ''
  }
  if (targetType === 'initiative') {
    const value = data.initiatives.find(item => item.id === id)
    return value ? initiativePath(data.workspace.urlKey, value, draft.type === 'initiative_update' || rawType === 'initiative' ? 'activity' : 'overview') : ''
  }
  if (targetType === 'customer') {
    const value = data.customers.find(item => item.id === id)
    return value ? customerPath(data.workspace.urlKey, value) : ''
  }
  if (targetType === 'release') {
    const value = data.releases.find(item => item.id === id)
    const pipeline = value ? data.releasePipelines.find(item => item.id === value.pipelineId) : undefined
    return value && pipeline ? releasePath(data.workspace.urlKey, pipeline.slugId, value.slugId) : ''
  }
  if (targetType === 'release_pipeline') {
    const value = data.releasePipelines.find(item => item.id === id)
    return value ? releasePipelinePath(data.workspace.urlKey, value.slugId) : ''
  }
  if (targetType === 'view') {
    const value = data.savedViews.find(item => item.id === id)
    if (!value) return ''
    const team = value.scope === 'team' ? data.teams.find(item => item.id === value.teamId) : undefined
    const root = `/${data.workspace.urlKey}`
    if ((value.resource ?? 'issues') === 'projects') return team ? `${root}/team/${team.key}/projects/view/${encodeURIComponent(id)}` : `${root}/projects/view/${encodeURIComponent(id)}`
    return team ? `${root}/team/${team.key}/view/${encodeURIComponent(id)}` : `${root}/view/${encodeURIComponent(id)}`
  }
  if (targetType === 'review') {
    const value = data.reviews.find(item => item.id === id || item.slugId === id)
    return value ? reviewPath(data.workspace.urlKey, value) : ''
  }
  return ''
}

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
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog"><DialogTitle>Create ask</DialogTitle><label><span>Request title</span><input autoFocus value={name} onChange={e=>setName(e.target.value)} placeholder="What do you need?"/></label><div className="operations-dialog-fields"><label><span>Team</span><SelectControl label="Team" value={teamId} onChange={value=>{setTeamId(value);setTemplateId('')}} options={data.teams.map(team=>({value:team.id,label:team.name,entityName:true,icon:<TeamIcon team={team} size={14}/> }))}/></label><label><span>Issue template</span><SelectControl label="Issue template" value={templateId} onChange={setTemplateId} options={[{value:'',label:'No template'},...templates.map(template=>({value:template.id,label:template.name,entityName:true}))]}/></label></div><label><span>Description</span><textarea value={description} onChange={e=>setDescription(e.target.value)} placeholder="Add details…"/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className="primary" disabled={!name.trim()||saving} onClick={()=>void submit()}>{saving?'Saving…':'Create'}</button></footer></DialogContent></Dialog>
}
