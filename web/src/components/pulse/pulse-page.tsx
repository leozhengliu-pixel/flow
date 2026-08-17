import { Layers3, Menu, Sparkles, SquarePen } from 'lucide-react'
import { useMemo, useState } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { pulsePath, type PulseRouteView } from '@/lib/app-routes'
import type { BootstrapData, InitiativeUpdate, Project, ProjectUpdate } from '@/types/flow'
import { PulseComposer } from './pulse-composer'
import { PulseNewViewEditor, PulseSubscriptionMenu, type PulseCadence } from './pulse-menus'
import { buildPulseFeed, pulseViewsKey, readPulseViews, type PulseSavedView, type PulseSourceFilter } from './pulse-model'
import { PulseUpdateCard } from './pulse-update-card'
import './pulse.css'

type Props = {
  data: BootstrapData
  view: PulseRouteView
  onNavigateView: (view: PulseRouteView) => void
  onOpenSidebar?: () => void
  onCreateProject: (id: string, input: { body: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onUpdateProject: (projectId: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onDeleteProject: (projectId: string, updateId: string) => Promise<void>
  onCommentProject: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onReactProject: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onCreateInitiative: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdateInitiative: (initiativeId: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onDeleteInitiative: (initiativeId: string, updateId: string) => Promise<void>
  onCommentInitiative: (initiativeId: string, updateId: string, body: string) => Promise<InitiativeUpdate>
  onReactInitiative: (initiativeId: string, updateId: string, emoji: string) => Promise<InitiativeUpdate>
}

export function PulsePage(props: Props) {
  const { data, view } = props
  const [composerOpen, setComposerOpen] = useState(false)
  const [cadence, setCadence] = useStoredCadence(data.workspace.id)
  const [savedViews, setSavedViews] = useState<PulseSavedView[]>(() => readPulseViews(data.workspace.id))
  const [creatingView, setCreatingView] = useState(false)
  const [activeSavedViewId, setActiveSavedViewId] = useState<string>()
  const [draft, setDraft] = useState<Omit<PulseSavedView, 'id'>>({ name: '', icon: 'CustomView', color: '#8a8f98', source: 'all' })
  const activeSavedView = savedViews.find(item => item.id === activeSavedViewId)
  const source: PulseSourceFilter = activeSavedView?.source ?? 'all'
  const feed = useMemo(() => buildPulseFeed(data, activeSavedView ? 'all' : view, source), [activeSavedView, data, source, view])
  const saveViews = (views: PulseSavedView[]) => { setSavedViews(views); localStorage.setItem(pulseViewsKey(data.workspace.id), JSON.stringify(views)) }
  const saveView = () => {
    const name = draft.name.trim()
    if (!name) return
    const created = { ...draft, name, id: `pulse_view_${Date.now()}` }
    saveViews([...savedViews, created]); setActiveSavedViewId(created.id); setCreatingView(false); setDraft({ name: '', icon: 'CustomView', color: '#8a8f98', source: 'all' })
  }
  const navigate = (next: PulseRouteView) => { setActiveSavedViewId(undefined); props.onNavigateView(next) }

  return <main className="main-panel pulse-page">
    <header className="pulse-header"><button aria-label="Open workspace sidebar" className="pulse-mobile-menu" onClick={props.onOpenSidebar} type="button"><Menu size={16}/></button><h1>Pulse</h1><span/><PulseSubscriptionMenu cadence={cadence} onChange={setCadence}/></header>
    <div className="pulse-toolbar">
      <nav aria-label="Pulse views">
        {([['following', 'For me'], ['popular', 'Popular'], ['all', 'Recent']] as const).map(([id, label]) => <a aria-current={!activeSavedView && view === id ? 'page' : undefined} href={pulsePath(data.workspace.urlKey, id)} key={id} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); navigate(id) }}>{label}</a>)}
        {savedViews.map(saved => <div aria-current={activeSavedViewId === saved.id ? 'page' : undefined} className="pulse-saved-tab" key={saved.id} onClick={() => setActiveSavedViewId(saved.id)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setActiveSavedViewId(saved.id) } }} role="button" tabIndex={0}><ViewGlyph color={saved.color} icon={saved.icon}/><span>{saved.name}</span><button aria-label={`Delete view ${saved.name}`} onClick={event => { event.stopPropagation(); saveViews(savedViews.filter(item => item.id !== saved.id)); if (activeSavedViewId === saved.id) setActiveSavedViewId(undefined) }} type="button">×</button></div>)}
        {!creatingView && <button aria-label="Add new view" className="pulse-add-view" onClick={() => setCreatingView(true)} type="button"><Layers3 size={13}/></button>}
      </nav>
      {creatingView && <PulseNewViewEditor draft={draft} onCancel={() => { setCreatingView(false); setDraft({ name: '', icon: 'CustomView', color: '#8a8f98', source: 'all' }) }} onChange={setDraft} onSave={saveView}/>} 
    </div>
    <section className="pulse-content">
      {feed.length === 0 ? <PulseEmptyState copy={emptyCopy(activeSavedView, view)} onCreate={() => setComposerOpen(true)}/> : <div className="pulse-feed"><div className="pulse-feed-actions"><button onClick={() => setComposerOpen(true)} type="button"><SquarePen size={13}/>New update</button></div>{feed.map(item => <PulseUpdateCard {...props} item={item} key={item.id} viewerId={data.viewer.id} workspaceSlug={data.workspace.urlKey}/>)}</div>}
    </section>
    <PulseComposer initiatives={data.initiatives} onCreateInitiative={props.onCreateInitiative} onCreateProject={props.onCreateProject} onOpenChange={setComposerOpen} open={composerOpen} projects={data.projects}/>
  </main>
}

function PulseEmptyState({ copy, onCreate }: { copy: string; onCreate: () => void }) { return <div className="pulse-empty"><div className="pulse-empty-icon"><i/><i/><span><Sparkles size={17}/></span></div><strong>Pulse</strong><p>{copy}</p><div><button onClick={onCreate} type="button">New update</button><a href="https://flow.app/docs/pulse" rel="noreferrer" target="_blank">Documentation</a></div></div> }
function emptyCopy(savedView: PulseSavedView | undefined, view: PulseRouteView) { if (savedView) return `Updates from ${savedView.source === 'all' ? 'initiatives and projects' : savedView.source} in your workspace will show here.`; if (view === 'following') return 'Updates from initiatives and projects that you’re a part of or subscribed to will show here.'; if (view === 'popular') return 'Updates with recent reactions and conversations will show here.'; return 'Updates from initiatives and projects in your workspace will show here.' }
function useStoredCadence(workspaceId: string) { const key = `flow:pulse:${workspaceId}:cadence`; const [value, setValue] = useState<PulseCadence>(() => (localStorage.getItem(key) as PulseCadence) || 'never'); const update = (next: PulseCadence) => { setValue(next); localStorage.setItem(key, next) }; return [value, update] as const }
