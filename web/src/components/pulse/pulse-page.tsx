import { Layers3, Menu, Sparkles, SquarePen } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { pulsePath, pulseViewPath, type PulseRouteView } from '@/lib/app-routes'
import type { BootstrapData, InitiativeUpdate, Project, ProjectUpdate, SavedView, SavedViewMutationInput, UserSettings } from '@/types/flow'
import { PulseComposer } from './pulse-composer'
import { PulseNewViewEditor, PulseSubscriptionMenu, type PulseCadence, type PulseViewDraft } from './pulse-menus'
import { PulseFilterChips, PulseFilterMenu, PulseMatchSummary } from './pulse-filters'
import { buildPulseFeed, pulseConfigFromView, pulseViewMutation, type PulseViewConfig } from './pulse-model'
import { PulseUpdateCard } from './pulse-update-card'
import './pulse.css'

type Props = {
  data: BootstrapData
  view: PulseRouteView
  viewId?: string
  onNavigateView: (view: PulseRouteView) => void
  onNavigateSavedView: (viewId: string) => void
  onOpenSidebar?: () => void
  onCreateSavedView: (input: SavedViewMutationInput) => Promise<SavedView>
  onUpdateSavedView: (id: string, input: SavedViewMutationInput) => Promise<SavedView>
  onDeleteSavedView: (view: SavedView) => Promise<void>
  onUpdateUserSettings: (settings: UserSettings) => Promise<UserSettings>
  onCreateProject: (id: string, input: { body: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<ProjectUpdate>
  onUpdateProject: (projectId: string, updateId: string, input: { body?: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<ProjectUpdate>
  onDeleteProject: (projectId: string, updateId: string) => Promise<void>
  onCommentProject: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onReactProject: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onUploadProjectAttachment:(projectId:string,updateId:string,file:File)=>Promise<ProjectUpdate>
  onDeleteProjectAttachment:(projectId:string,updateId:string,attachmentId:string)=>Promise<ProjectUpdate>
  onCreateInitiative: (id: string, input: { body: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdateInitiative: (initiativeId: string, updateId: string, input: { body?: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<InitiativeUpdate>
  onDeleteInitiative: (initiativeId: string, updateId: string) => Promise<void>
  onCommentInitiative: (initiativeId: string, updateId: string, body: string) => Promise<InitiativeUpdate>
  onReactInitiative: (initiativeId: string, updateId: string, emoji: string) => Promise<InitiativeUpdate>
  onUploadInitiativeAttachment:(initiativeId:string,updateId:string,file:File)=>Promise<InitiativeUpdate>
  onDeleteInitiativeAttachment:(initiativeId:string,updateId:string,attachmentId:string)=>Promise<InitiativeUpdate>
}

const blankDraft=():PulseViewDraft=>({name:'',icon:'CustomView',color:'#8a8f98',filters:[],match:'all'})

export function PulsePage(props: Props) {
  const { data, view, viewId, onNavigateView } = props
  const [composerOpen, setComposerOpen] = useState(false)
  const [creatingView, setCreatingView] = useState(false)
  const [savingView,setSavingView]=useState(false)
  const [draft, setDraft] = useState<PulseViewDraft>(blankDraft)
  const pulseViews=data.savedViews.filter(saved=>saved.resource==='pulse'&&saved.scope==='personal'&&saved.ownerId===data.viewer.id)
  const activeSavedView=pulseViews.find(saved=>saved.id===props.viewId)
  const [activeConfig,setActiveConfig]=useState<PulseViewConfig>(()=>pulseConfigFromView(activeSavedView))
  useEffect(()=>setActiveConfig(pulseConfigFromView(activeSavedView)),[activeSavedView])
  useEffect(()=>{if(viewId&&!activeSavedView)onNavigateView('following')},[activeSavedView,onNavigateView,viewId])
  const feed = useMemo(() => buildPulseFeed(data, activeSavedView ? 'all' : view, activeSavedView ? activeConfig : {filters:[],match:'all'}), [activeConfig, activeSavedView, data, view])
  const userSettings=data.userSettings[data.viewer.id]
  const cadence:PulseCadence=userSettings?.pulseSchedule??'never'
  const changeCadence=async(next:PulseCadence)=>{if(!userSettings)return;await props.onUpdateUserSettings({...userSettings,pulseSchedule:next})}
  const saveView=async()=>{const name=draft.name.trim();if(!name||savingView)return;setSavingView(true);try{const created=await props.onCreateSavedView({name,icon:draft.icon,color:draft.color,resource:'pulse',scope:'personal',ownerId:data.viewer.id,view:'all',...pulseViewMutation(draft)});setCreatingView(false);setDraft(blankDraft());props.onNavigateSavedView(created.id)}finally{setSavingView(false)}}
  const updateActiveConfig=(next:PulseViewConfig)=>{setActiveConfig(next);if(activeSavedView)void props.onUpdateSavedView(activeSavedView.id,pulseViewMutation(next))}
  const navigate=(next:PulseRouteView)=>{setCreatingView(false);props.onNavigateView(next)}

  return <main className="main-panel pulse-page">
    <header className="pulse-header"><button aria-label="Open workspace sidebar" className="pulse-mobile-menu" onClick={props.onOpenSidebar} type="button"><Menu size={16}/></button><h1>Pulse</h1><span/><PulseSubscriptionMenu cadence={cadence} onChange={next=>void changeCadence(next)}/></header>
    <div className="pulse-toolbar">
      <nav aria-label="Pulse views">
        {([['following', 'For me'], ['popular', 'Popular'], ['all', 'Recent']] as const).map(([id, label]) => <a aria-current={!activeSavedView && view === id ? 'page' : undefined} href={pulsePath(data.workspace.urlKey, id)} key={id} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); navigate(id) }}>{label}</a>)}
        {pulseViews.map(saved => <div aria-current={activeSavedView?.id===saved.id?'page':undefined} className="pulse-saved-tab" key={saved.id}><a href={pulseViewPath(data.workspace.urlKey,saved.id)} onClick={event=>{if(event.metaKey||event.ctrlKey||event.shiftKey)return;event.preventDefault();props.onNavigateSavedView(saved.id)}}><ViewGlyph color={saved.color??'#8a8f98'} icon={saved.icon??'CustomView'}/><span>{saved.name}</span></a><button aria-label={`Delete view ${saved.name}`} onClick={()=>{void props.onDeleteSavedView(saved);if(activeSavedView?.id===saved.id)navigate('following')}} type="button">×</button></div>)}
        {!creatingView && <button aria-label="Add new view" className="pulse-add-view" onClick={() => setCreatingView(true)} type="button"><Layers3 size={13}/></button>}
      </nav>
      {creatingView && <PulseNewViewEditor data={data} draft={draft} onCancel={()=>{setCreatingView(false);setDraft(blankDraft())}} onChange={setDraft} onSave={()=>void saveView()}/>}
      {activeSavedView&&!creatingView&&<div className="pulse-active-filters"><PulseFilterMenu data={data} filters={activeConfig.filters} match={activeConfig.match} onChange={filters=>updateActiveConfig({...activeConfig,filters})} onMatchChange={match=>updateActiveConfig({...activeConfig,match})}/><PulseMatchSummary count={activeConfig.filters.length} match={activeConfig.match}/><PulseFilterChips data={data} filters={activeConfig.filters} onChange={filters=>updateActiveConfig({...activeConfig,filters})}/></div>}
    </div>
    <section className="pulse-content">
      {feed.length === 0 ? <PulseEmptyState copy={emptyCopy(activeSavedView, view)} onCreate={() => setComposerOpen(true)}/> : <div className="pulse-feed"><div className="pulse-feed-actions"><button onClick={() => setComposerOpen(true)} type="button"><SquarePen size={13}/>New update</button></div>{feed.map(item => <PulseUpdateCard {...props} item={item} key={item.id} viewerId={data.viewer.id} workspaceSlug={data.workspace.urlKey}/>)}</div>}
    </section>
    <PulseComposer initiatives={data.initiatives} onCreateInitiative={props.onCreateInitiative} onCreateProject={props.onCreateProject} onUploadInitiativeAttachment={props.onUploadInitiativeAttachment} onUploadProjectAttachment={props.onUploadProjectAttachment} onOpenChange={setComposerOpen} open={composerOpen} projects={data.projects}/>
  </main>
}

function PulseEmptyState({ copy, onCreate }: { copy: string; onCreate: () => void }) { return <div className="pulse-empty"><div className="pulse-empty-icon"><i/><i/><span><Sparkles size={17}/></span></div><strong>Pulse</strong><p>{copy}</p><div><button onClick={onCreate} type="button">New update</button><a href="https://github.com/leozhengliu-pixel/flow/blob/main/docs/pulse-page-modules.md" rel="noreferrer" target="_blank">Documentation</a></div></div> }
function emptyCopy(savedView: SavedView | undefined, view: PulseRouteView) { if(savedView)return'Updates matching this personal feed will show here.';if(view==='following')return'Updates from initiatives and projects that you’re a part of or subscribed to will show here.';if(view==='popular')return'Popular project and initiative updates from your workspace will show here.';return'Updates from initiatives and projects in your workspace will show here.' }
