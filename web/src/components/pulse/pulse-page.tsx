import * as ContextMenu from '@radix-ui/react-context-menu'
import { Menu, Sparkles, SquarePen, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { pulsePath, pulseViewPath, type PulseRouteView } from '@/lib/app-routes'
import type { BootstrapData, InitiativeUpdate, Project, ProjectUpdate, SavedView, SavedViewMutationInput, UserSettings } from '@/types/flow'
import { PulseComposer } from './pulse-composer'
import { PulseNewViewEditor, PulseSubscriptionMenu, type PulseCadence, type PulseViewDraft } from './pulse-menus'
import { buildPulseFeed, pulseConfigFromView, pulseViewMutation, type PulseViewConfig } from './pulse-model'
import { PulseUpdateCard } from './pulse-update-card'
import { AddViewIcon } from '@/components/ui/view-action-icons'
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
  const [editingView, setEditingView] = useState<SavedView>()
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
  const closeViewEditor=()=>{setCreatingView(false);setEditingView(undefined);setDraft(blankDraft())}
  const openViewEditor=(saved?:SavedView)=>{setEditingView(saved);setDraft(saved?{name:saved.name,icon:saved.icon??'CustomView',color:saved.color??'#8a8f98',...pulseConfigFromView(saved)}:blankDraft());setCreatingView(true)}
  const saveView=async()=>{const name=draft.name.trim()||'All updates';if(savingView)return;setSavingView(true);try{const input={name,icon:draft.icon,color:draft.color,resource:'pulse' as const,scope:'personal' as const,ownerId:data.viewer.id,view:'all' as const,...pulseViewMutation(draft)};const saved=editingView?await props.onUpdateSavedView(editingView.id,input):await props.onCreateSavedView(input);closeViewEditor();props.onNavigateSavedView(saved.id)}finally{setSavingView(false)}}
  const duplicateView=async(saved:SavedView)=>{const config=pulseConfigFromView(saved),created=await props.onCreateSavedView({name:`${saved.name} copy`,description:saved.description,icon:saved.icon,color:saved.color,resource:'pulse',scope:'personal',ownerId:data.viewer.id,view:'all',...pulseViewMutation(config)});props.onNavigateSavedView(created.id)}
  const removeView=async(saved:SavedView)=>{if(!await confirmAction('Delete view?',{description:saved.name,confirmLabel:'Delete view',danger:true}))return;await props.onDeleteSavedView(saved);if(activeSavedView?.id===saved.id)navigate('following')}
  const navigate=(next:PulseRouteView)=>{closeViewEditor();props.onNavigateView(next)}

  return <main className="flow-framed-workspace main-panel pulse-page">
    <header className="pulse-header">
      <div className="pulse-header-top"><button aria-label="Open workspace sidebar" className="pulse-mobile-menu" onClick={props.onOpenSidebar} type="button"><Menu size={16}/></button><h2>Pulse</h2><span/><PulseSubscriptionMenu cadence={cadence} onChange={next=>void changeCadence(next)}/></div>
      <div className="pulse-toolbar">
      <div className="pulse-view-tabs">
        {([['following', 'For me'], ['popular', 'Popular'], ['all', 'Recent']] as const).map(([id, label]) => <a className="ui-pill" aria-current={!activeSavedView && view === id ? 'page' : undefined} href={pulsePath(data.workspace.urlKey, id)} key={id} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); navigate(id) }}>{label}</a>)}
        {pulseViews.map(saved => <ContextMenu.Root key={saved.id}><ContextMenu.Trigger asChild><a aria-current={activeSavedView?.id===saved.id?'page':undefined} aria-label={`View ${saved.name}`} className="pulse-saved-tab ui-pill" data-i18n-ignore href={pulseViewPath(data.workspace.urlKey,saved.id)} onClick={event=>{if(event.metaKey||event.ctrlKey||event.shiftKey)return;event.preventDefault();props.onNavigateSavedView(saved.id)}}><ViewGlyph color={saved.color??'#8a8f98'} icon={saved.icon??'CustomView'}/><span>{saved.name}</span></a></ContextMenu.Trigger><ContextMenu.Portal><ContextMenu.Content className="pulse-menu pulse-saved-view-menu"><ContextMenu.Item onSelect={()=>openViewEditor(saved)}><span>Edit…</span></ContextMenu.Item><ContextMenu.Item onSelect={()=>void duplicateView(saved)}><span>Duplicate…</span></ContextMenu.Item><ContextMenu.Separator/><ContextMenu.Item onSelect={()=>void props.onUpdateSavedView(saved.id,{favorite:!saved.favorite})}><span>{saved.favorite?'Unfavorite':'Favorite'}</span></ContextMenu.Item><ContextMenu.Separator/><ContextMenu.Item className="is-danger" onSelect={()=>void removeView(saved)}><Trash2/><span>Delete</span></ContextMenu.Item></ContextMenu.Content></ContextMenu.Portal></ContextMenu.Root>)}
        {!creatingView && <button aria-label="Add new view" className="pulse-add-view" onClick={() => openViewEditor()} type="button"><AddViewIcon/></button>}
      </div>
      </div>
    </header>
    {creatingView && <PulseNewViewEditor
      data={data}
      draft={draft}
      onCancel={closeViewEditor}
      onChange={setDraft}
      onSave={()=>void saveView()}
    />}
    <section className="pulse-content">
      {feed.length === 0 ? <PulseEmptyState copy={emptyCopy(activeSavedView, view)} onCreate={() => setComposerOpen(true)}/> : <div className="pulse-feed"><div className="pulse-feed-actions"><button onClick={() => setComposerOpen(true)} type="button"><SquarePen size={13}/>New update</button></div>{feed.map(item => <PulseUpdateCard {...props} item={item} key={item.id} viewerId={data.viewer.id} workspaceSlug={data.workspace.urlKey}/>)}</div>}
    </section>
    <PulseComposer initiatives={data.initiatives} onCreateInitiative={props.onCreateInitiative} onCreateProject={props.onCreateProject} onUploadInitiativeAttachment={props.onUploadInitiativeAttachment} onUploadProjectAttachment={props.onUploadProjectAttachment} onOpenChange={setComposerOpen} open={composerOpen} projects={data.projects}/>
  </main>
}

function PulseEmptyState({ copy, onCreate }: { copy: string; onCreate: () => void }) { return <div className="pulse-empty"><div className="pulse-empty-icon"><i/><i/><span><Sparkles size={17}/></span></div><strong>Pulse</strong><p>{copy}</p><div><button onClick={onCreate} type="button">New update</button><a href="https://github.com/leozhengliu-pixel/flow/blob/main/docs/pulse-page-modules.md" rel="noreferrer" target="_blank">Documentation</a></div></div> }
function emptyCopy(savedView: SavedView | undefined, view: PulseRouteView) { if(savedView)return'Updates matching this personal feed will show here.';if(view==='following')return'Updates from initiatives and projects that you’re a part of or subscribed to will show here.';if(view==='popular')return'Popular project and initiative updates from your workspace will show here.';return'Updates from initiatives and projects in your workspace will show here.' }
