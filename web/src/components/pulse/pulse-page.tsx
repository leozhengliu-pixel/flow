import * as ContextMenu from '@radix-ui/react-context-menu'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Check, ChevronDown, CircleHelp, Menu, Search, SquarePen, Trash2, X } from 'lucide-react'
import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { AddViewIcon } from '@/components/ui/view-action-icons'
import { FlowTooltip } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import { useI18n } from '@/i18n/i18n'
import { updateUserSettings } from '@/lib/api'
import { changePulseSubscription, PULSE_SUBSCRIPTIONS_CHANGED_EVENT } from '@/lib/pulse-subscriptions'
import { pulseNewViewPath, pulsePath, pulseViewPath, type PulseRouteView } from '@/lib/app-routes'
import type { BootstrapData, InitiativeUpdate, Project, ProjectUpdate, PulseItem, SavedView, SavedViewMutationInput } from '@/types/flow'
import { FeedStableLastSeen } from './feed-stable-last-seen'
import { PulseComposer } from './pulse-composer'
import { PulseFilterChips, PulseFilterMenu } from './pulse-filters'
import { PulseIcon } from './pulse-icon'
import { PulseNewViewEditor, PulseSubscriptionMenu } from './pulse-menus'
import { emptyCopy, orderPulseViews, PULSE_TABS, PULSE_VIEW_KEYS } from './pulse-page-model'
import { effectivePulseSchedule, type PulseSchedule, type PulseViewDraft } from './pulse-schedule'
import { feedPostUpdateEnabled, newestPulseItemTime, pulseConfigFromView, pulseGroupLabels, pulseGroupStarts, pulseViewMutation, type PulseGroupKey, weekStartsOnFromSetting, type PulseViewConfig } from './pulse-model'
import { PulseUpdateCard, type PulseCardHandlers } from './pulse-update-card'
import { pulseUnread, usePulseUnread, writePulseLastTab } from './pulse-unread'
import { PulseWelcomeBanner } from './pulse-welcome-banner'
import { recordPulseSeen, usePulseFeed, type PulseFeedState } from './use-pulse-feed'
import { useShowPulseWelcomeBanner } from './use-show-pulse-welcome-banner'
import './pulse.css'
import './pulse-feed.css'

type UpdateInput = { body: string; bodyData?: Record<string, unknown>; health?: Project['health'] }
type Props = {
  data: BootstrapData
  view: PulseRouteView
  viewId?: string
  viewMode?: 'new' | 'edit'
  onNavigate?: (path: string) => void
  onNavigateView: (view: PulseRouteView) => void
  onNavigateSavedView: (viewId: string) => void
  onOpenSidebar?: () => void
  onCreateSavedView: (input: SavedViewMutationInput) => Promise<SavedView>
  onUpdateSavedView: (id: string, input: SavedViewMutationInput) => Promise<SavedView>
  onDeleteSavedView: (view: SavedView) => Promise<void>
  onCreateProject: (id: string, input: UpdateInput) => Promise<ProjectUpdate>
  onUpdateProject: (projectId: string, updateId: string, input: Partial<UpdateInput>) => Promise<ProjectUpdate>
  onDeleteProject: (projectId: string, updateId: string) => Promise<void>
  onCommentProject: (projectId: string, updateId: string, body: string, bodyData?: Record<string, unknown>) => Promise<ProjectUpdate>
  onReactProject: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onUploadProjectAttachment: (projectId: string, updateId: string, file: File) => Promise<ProjectUpdate>
  onDeleteProjectAttachment: (projectId: string, updateId: string, attachmentId: string) => Promise<ProjectUpdate>
  onCreateInitiative: (id: string, input: UpdateInput) => Promise<InitiativeUpdate>
  onUpdateInitiative: (initiativeId: string, updateId: string, input: Partial<UpdateInput>) => Promise<InitiativeUpdate>
  onDeleteInitiative: (initiativeId: string, updateId: string) => Promise<void>
  onCommentInitiative: (initiativeId: string, updateId: string, body: string, bodyData?: Record<string, unknown>) => Promise<InitiativeUpdate>
  onReactInitiative: (initiativeId: string, updateId: string, emoji: string) => Promise<InitiativeUpdate>
  onUploadInitiativeAttachment: (initiativeId: string, updateId: string, file: File) => Promise<InitiativeUpdate>
  onDeleteInitiativeAttachment: (initiativeId: string, updateId: string, attachmentId: string) => Promise<InitiativeUpdate>
  /** Kept for callers that still pass it; Pulse writes settings through the fast path. */
  onUpdateUserSettings?: unknown
}

const INLINE_VIEW_TABS = 5
const INITIAL_RENDER = 6
const RENDER_STEP = 10
const DOCS_URL = 'https://github.com/leozhengliu-pixel/flow/blob/main/docs/pulse.md'
const EMPTY_CONFIG: PulseViewConfig = { filters: [], match: 'all' }
const blankDraft = (): PulseViewDraft => ({ name: '', icon: 'CustomView', color: '#8a8f98', filters: [], match: 'all' })

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || (target instanceof HTMLElement && target.isContentEditable)
}
function hasOpenOverlay() {
  return Boolean(document.querySelector('[role="dialog"], [role="menu"][data-state="open"], [data-radix-popper-content-wrapper]'))
}

/* Custom view tab order (drag to reorder), per workspace. */
function readViewOrder(workspaceKey: string): string[] {
  try { const value = JSON.parse(localStorage.getItem(`flow.pulse.viewOrder:${workspaceKey}`) ?? '[]'); return Array.isArray(value) ? value.filter(item => typeof item === 'string') : [] }
  catch { return [] }
}
function writeViewOrder(workspaceKey: string, order: string[]) {
  try { localStorage.setItem(`flow.pulse.viewOrder:${workspaceKey}`, JSON.stringify(order)) } catch { /* in-memory only */ }
}
function useDebounced<T>(value: T, delay: number) {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => { const timer = setTimeout(() => setDebounced(value), delay); return () => clearTimeout(timer) }, [delay, value])
  return debounced
}

export function PulsePage(props: Props) {
  const { data, view, viewId, viewMode } = props
  const { t } = useI18n()
  const workspaceKey = data.workspace.urlKey
  const navigate = useCallback((path: string) => props.onNavigate ? props.onNavigate(path) : window.history.pushState(null, '', path), [props])
  const pulseViews = useMemo(() => data.savedViews.filter(saved => saved.resource === 'pulse' && saved.scope === 'personal' && saved.ownerId === data.viewer.id), [data.savedViews, data.viewer.id])
  const [viewOrder, setViewOrder] = useState(() => readViewOrder(workspaceKey))
  const orderedViews = useMemo(() => orderPulseViews(pulseViews, viewOrder), [pulseViews, viewOrder])
  const activeSavedView = viewId ? pulseViews.find(saved => saved.id === viewId) : undefined
  const creating = viewMode === 'new'
  const editing = viewMode === 'edit' && Boolean(activeSavedView)
  const [draft, setDraft] = useState<PulseViewDraft>(blankDraft)
  const [savingView, setSavingView] = useState(false)
  const [composerOpen, setComposerOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [quickFilters, setQuickFilters] = useState<PulseViewConfig>(EMPTY_CONFIG)
  const [unsubscribedSources, setUnsubscribedSources] = useState<Set<string>>(() => new Set())
  const [activeItemId, setActiveItemId] = useState<string>()
  const [replyRequest, setReplyRequest] = useState<{ itemId: string; nonce: number }>()
  const contentRef = useRef<HTMLElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const query = useDebounced(search, 250)
  const unread = usePulseUnread(workspaceKey, true)

  // Unknown custom view → back to For me.
  const { onNavigateView } = props
  useEffect(() => { if (viewId && !activeSavedView) onNavigateView('following') }, [activeSavedView, onNavigateView, viewId])
  // Draft follows the route: new → blank, edit → the saved view.
  const draftSource = creating ? 'new' : editing ? activeSavedView?.id : undefined
  useEffect(() => {
    if (draftSource === 'new') setDraft(blankDraft())
    else if (draftSource && activeSavedView) setDraft({ name: activeSavedView.name, icon: activeSavedView.icon ?? 'CustomView', color: activeSavedView.color ?? '#8a8f98', ...pulseConfigFromView(activeSavedView) })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftSource])

  const tab: PulseRouteView = activeSavedView || creating ? 'all' : view
  const draftConfig = useMemo<PulseViewConfig>(() => ({ filters: draft.filters, match: draft.match }), [draft.filters, draft.match])
  // A saved view's own filters (and match) are applied by the server from viewId; the
  // editor's draft is sent explicitly; quick filters narrow either.
  const config = useMemo<PulseViewConfig>(() => creating || editing ? draftConfig : quickFilters, [creating, draftConfig, editing, quickFilters])
  const feed = usePulseFeed({ view: tab, viewId: editing || creating ? undefined : activeSavedView?.id, q: query, config }, { workspaceKey })

  // Remember the tab for the sidebar click target; the stable last-seen lives for this visit.
  useEffect(() => { if (!creating) writePulseLastTab(workspaceKey, activeSavedView ? `view:${activeSavedView.id}` : view) }, [activeSavedView, creating, view, workspaceKey])
  useEffect(() => () => FeedStableLastSeen.clear(), [])
  // Linear's rule: "seen" is the newest update actually loaded in the unfiltered
  // For me feed, recorded once the reader reached its top (or the caught-up
  // line) — never wall-clock now, never from Popular/Recent/custom feeds/search.
  const seenEligible = tab === 'following' && !activeSavedView && !creating && !editing && !query.trim() && !search.trim() && quickFilters.filters.length === 0
  const newestLoadedAt = seenEligible && !feed.loading ? newestPulseItemTime(feed.items) : 0
  const seenTarget = useRef(0)
  const markTopReached = useCallback(() => {
    if (!newestLoadedAt) return
    seenTarget.current = Math.max(seenTarget.current, newestLoadedAt)
    recordPulseSeen(newestLoadedAt)
  }, [newestLoadedAt])
  useEffect(() => {
    // Only what the reader reached is written on leave (StrictMode's mount/unmount
    // probe runs before anything loaded, so it writes nothing).
    const leave = () => { if (seenTarget.current) recordPulseSeen(seenTarget.current, { keepalive: true, force: true }) }
    window.addEventListener('beforeunload', leave)
    return () => { leave(); window.removeEventListener('beforeunload', leave) }
  }, [])
  // Top of the For me feed visible → seen up to its newest loaded update.
  useEffect(() => {
    if (newestLoadedAt && (contentRef.current?.scrollTop ?? 0) < 80) markTopReached()
  }, [markTopReached, newestLoadedAt])

  // Subscription changes from cards, menus and dialogs move the sidebar badge too.
  useEffect(() => {
    const refresh = () => pulseUnread.schedule(workspaceKey)
    window.addEventListener(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, refresh)
    return () => window.removeEventListener(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, refresh)
  }, [workspaceKey])

  const userSettings = data.userSettings[data.viewer.id]
  const schedule = effectivePulseSchedule(userSettings, data.workspaceSettings)
  const changeSchedule = async (next: PulseSchedule) => {
    try { await updateUserSettings({ pulseSchedule: next }, workspaceKey) }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : t('Could not save Pulse schedule')) }
  }
  const showWelcome = useShowPulseWelcomeBanner(data)
  const dismissWelcome = (next: PulseSchedule) => updateUserSettings({ pulseSchedule: next, pulseWelcomeDismissed: true }, workspaceKey)

  const goTab = useCallback((next: PulseRouteView) => { setSearch(''); props.onNavigateView(next) }, [props])
  const goView = useCallback((id: string) => { setSearch(''); props.onNavigateSavedView(id) }, [props])
  const keyedTabs = useMemo(() => [...PULSE_TABS.map(item => ({ key: String(PULSE_TABS.indexOf(item) + 1), go: () => goTab(item.id) })), ...orderedViews.slice(0, PULSE_VIEW_KEYS.length).map((saved, index) => ({ key: PULSE_VIEW_KEYS[index], go: () => goView(saved.id) }))], [goTab, goView, orderedViews])

  // Keyboard: 1–3 built-in tabs, 4–9/0 custom feeds, R replies to the active update.
  const lastKey = useRef({ key: '', at: 0 })
  const hoveredItemId = useRef<string | undefined>(undefined)
  const changeHover = useCallback((itemId: string, hovered: boolean) => {
    if (hovered) hoveredItemId.current = itemId
    else if (hoveredItemId.current === itemId) hoveredItemId.current = undefined
  }, [])
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey || isEditableTarget(event.target) || hasOpenOverlay()) return
      const key = event.key.toLowerCase()
      const previous = lastKey.current
      lastKey.current = { key, at: Date.now() }
      if (previous.key === 'g' && Date.now() - previous.at < 1100) return
      const target = keyedTabs.find(item => item.key === key)
      if (target && !creating && !editing) { event.preventDefault(); target.go(); return }
      if (key === 'r') {
        // The hovered card, else the focused one, else the last focused, else the first.
        const ids = new Set(feed.items.map(item => item.id))
        const focused = document.activeElement instanceof HTMLElement ? document.activeElement.closest('[data-pulse-item]')?.getAttribute('data-pulse-item') ?? undefined : undefined
        const itemId = [hoveredItemId.current, focused, activeItemId].find(id => id && ids.has(id)) ?? feed.items[0]?.id
        if (!itemId) return
        event.preventDefault()
        setReplyRequest(current => ({ itemId, nonce: (current?.nonce ?? 0) + 1 }))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [activeItemId, creating, editing, feed.items, keyedTabs])

  const closeEditor = () => {
    if (creating) navigate(pulsePath(workspaceKey, 'following'))
    else if (activeSavedView) props.onNavigateSavedView(activeSavedView.id)
  }
  const saveView = async () => {
    if (savingView) return
    setSavingView(true)
    try {
      const input = { name: draft.name.trim() || t('All updates'), icon: draft.icon, color: draft.color, resource: 'pulse' as const, scope: 'personal' as const, ownerId: data.viewer.id, view: 'all' as const, ...pulseViewMutation(draft) }
      const saved = editing && activeSavedView ? await props.onUpdateSavedView(activeSavedView.id, input) : await props.onCreateSavedView(input)
      props.onNavigateSavedView(saved.id)
    } finally { setSavingView(false) }
  }
  const duplicateView = async (saved: SavedView) => {
    const created = await props.onCreateSavedView({ name: `${saved.name} ${t('copy')}`, description: saved.description, icon: saved.icon, color: saved.color, resource: 'pulse', scope: 'personal', ownerId: data.viewer.id, view: 'all', ...pulseViewMutation(pulseConfigFromView(saved)) })
    props.onNavigateSavedView(created.id)
  }
  const removeView = async (saved: SavedView) => {
    if (!await confirmAction(t('Delete view?'), { description: saved.name, confirmLabel: t('Delete view'), danger: true })) return
    await props.onDeleteSavedView(saved)
    if (activeSavedView?.id === saved.id) goTab('following')
  }
  const reorderViews = (dragged: string, target: string) => {
    const ids = orderedViews.map(saved => saved.id)
    const from = ids.indexOf(dragged), to = ids.indexOf(target)
    if (from < 0 || to < 0 || from === to) return
    ids.splice(from, 1)
    ids.splice(to, 0, dragged)
    setViewOrder(ids)
    writeViewOrder(workspaceKey, ids)
  }

  const handlers = usePulseCardHandlers(props, feed, tab, setUnsubscribedSources, navigate)
  const initiativesEnabled = workspaceFeatureEnabled(data.workspaceSettings.featureFlags, 'initiatives')
  const filtering = Boolean(query.trim()) || quickFilters.filters.length > 0
  const empty = !feed.loading && !feed.error && feed.items.length === 0
  const showNewButton = feedPostUpdateEnabled(data)

  return <main className="flow-framed-workspace main-panel pulse-page">
    <header className="pulse-header">
      <div className="pulse-header-top">
        <button aria-label={t('Open workspace sidebar')} className="pulse-mobile-menu" data-sidebar-trigger onClick={props.onOpenSidebar} type="button"><Menu size={16}/></button>
        <h2>{t('Pulse')}</h2>
        <PulseHelpPopover/>
        <span className="pulse-header-spacer"/>
        <PulseSubscriptionMenu cadence={schedule} onChange={next => void changeSchedule(next)}/>
        {showNewButton && <FlowTooltip label={t('New update')}><button aria-label={t('New update')} className="pulse-icon-button" onClick={() => setComposerOpen(true)} type="button"><SquarePen size={14}/></button></FlowTooltip>}
      </div>
      <div className="pulse-toolbar">
        <nav aria-label={t('Pulse views')} className="pulse-view-tabs">
          {PULSE_TABS.map((item, index) => {
            const current = !activeSavedView && !creating && view === item.id
            return <FlowTooltip key={item.id} label={t(item.label)} shortcut={String(index + 1)}><a aria-current={current ? 'page' : undefined} className="ui-pill pulse-tab" href={pulsePath(workspaceKey, item.id)} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); goTab(item.id) }}>
              {t(item.label)}{item.id === 'following' && unread > 0 && <i aria-label={t('Unread updates')} className="pulse-tab-dot"/>}
            </a></FlowTooltip>
          })}
          <PulseViewTabs activeId={activeSavedView?.id} onDelete={saved => void removeView(saved)} onDuplicate={saved => void duplicateView(saved)} onEdit={saved => navigate(pulseViewPath(workspaceKey, saved.id, 'edit'))} onFavorite={saved => void props.onUpdateSavedView(saved.id, { favorite: !saved.favorite })} onOpen={goView} onReorder={reorderViews} views={orderedViews} workspaceKey={workspaceKey}/>
          {!creating && <FlowTooltip label={t('Add new view')}><button aria-label={t('Add new view')} className="pulse-add-view" onClick={() => navigate(pulseNewViewPath(workspaceKey))} type="button"><AddViewIcon/></button></FlowTooltip>}
        </nav>
        <div className="pulse-toolbar-actions">
          <label className="pulse-inline-search">
            <Search aria-hidden="true" size={13}/>
            <input aria-label={t('Find in feed…')} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { setSearch(''); event.currentTarget.blur() } }} placeholder={t('Find in feed…')} ref={searchRef} value={search}/>
            {search && <button aria-label={t('Clear search')} onClick={() => setSearch('')} type="button"><X size={12}/></button>}
          </label>
          {!creating && !editing && <PulseFilterMenu align="end" compact data={data} filters={quickFilters.filters} match={quickFilters.match} onChange={filters => setQuickFilters(current => ({ ...current, filters }))} onMatchChange={match => setQuickFilters(current => ({ ...current, match }))}/>}
        </div>
      </div>
      {quickFilters.filters.length > 0 && !creating && !editing && <div className="pulse-active-filters"><PulseFilterChips data={data} filters={quickFilters.filters} onChange={filters => setQuickFilters(current => ({ ...current, filters }))}/><button className="pulse-link-button" onClick={() => setQuickFilters(EMPTY_CONFIG)} type="button">{t('Clear filters')}</button></div>}
    </header>
    {(creating || editing) && <PulseNewViewEditor data={data} draft={draft} onCancel={closeEditor} onChange={setDraft} onSave={() => void saveView()} saving={savingView}/>}
    <section className="pulse-content" onScroll={event => { if (newestLoadedAt && event.currentTarget.scrollTop < 80) markTopReached() }} ref={contentRef}>
      {feed.newItemsCount > 0 && <div className="pulse-new-items-anchor"><button className="pulse-new-items" onClick={() => { void feed.showNewItems().then(() => contentRef.current?.scrollTo?.({ top: 0 })) }} type="button"><span className="pulse-new-items-count">{feed.newItemsCount}</span><span>{feed.newItemsCount === 1 ? t('New update available') : t('New updates available')}</span></button></div>}
      {showWelcome && !creating && !editing && <PulseWelcomeBanner cadence={schedule} containerRef={contentRef} onConfirm={dismissWelcome}/>}
      {feed.loading && feed.items.length === 0 ? <PulseFeedSkeleton/> : feed.error && feed.items.length === 0 ? <div className="pulse-empty"><strong>{t('Could not load Pulse')}</strong><p>{feed.error}</p><div><button onClick={() => void feed.reload()} type="button">{t('Retry')}</button></div></div> : empty ? (
        filtering ? <PulseNoResults onClearFilters={quickFilters.filters.length ? () => setQuickFilters(EMPTY_CONFIG) : undefined} onClearSearch={query.trim() ? () => setSearch('') : undefined} term={query.trim()}/> : <PulseEmptyState copy={emptyCopy(tab, initiativesEnabled, t)} onCreate={() => setComposerOpen(true)}/>
      ) : <PulseFeedList activeReply={replyRequest} containerRef={contentRef} data={data} feed={feed} handlers={handlers} onActivate={setActiveItemId} onCaughtUp={markTopReached} onHoverChange={changeHover} tab={tab} unsubscribedSources={unsubscribedSources}/>}
    </section>
    <PulseComposer initiatives={initiativesEnabled ? data.initiatives : []} onCreateInitiative={async (id, input) => { const update = await props.onCreateInitiative(id, input); void feed.reload(); return update }} onCreateProject={async (id, input) => { const update = await props.onCreateProject(id, input); void feed.reload(); return update }} onOpenChange={setComposerOpen} onUploadInitiativeAttachment={props.onUploadInitiativeAttachment} onUploadProjectAttachment={props.onUploadProjectAttachment} open={composerOpen} projects={data.projects} users={data.users}/>
  </main>
}

function usePulseCardHandlers(props: Props, feed: PulseFeedState, tab: PulseRouteView, setUnsubscribed: (update: (current: Set<string>) => Set<string>) => void, navigate: (path: string) => void): PulseCardHandlers {
  const { t } = useI18n()
  const projectId = (item: PulseItem) => item.kind === 'project' ? item.update.projectId || item.source.id : item.source.id
  const initiativeId = (item: PulseItem) => item.kind === 'initiative' ? item.update.initiativeId || item.source.id : item.source.id
  const run = async <T,>(work: () => Promise<T>, fallback: string): Promise<T> => {
    try { return await work() } catch (cause) { toast.error(cause instanceof Error ? cause.message : t(fallback)); throw cause }
  }
  return {
    onComment: async (item, body, bodyData) => {
      const update = await (item.kind === 'project' ? props.onCommentProject(projectId(item), item.update.id, body, bodyData) : props.onCommentInitiative(initiativeId(item), item.update.id, body, bodyData))
      feed.patchUpdate(item.id, update)
    },
    onReact: async (item, emoji) => {
      const update = await (item.kind === 'project' ? props.onReactProject(projectId(item), item.update.id, emoji) : props.onReactInitiative(initiativeId(item), item.update.id, emoji))
      feed.patchUpdate(item.id, update)
    },
    onEdit: async (item, input) => {
      const update = await (item.kind === 'project' ? props.onUpdateProject(projectId(item), item.update.id, input) : props.onUpdateInitiative(initiativeId(item), item.update.id, input))
      feed.patchUpdate(item.id, update)
    },
    onDelete: async item => {
      await (item.kind === 'project' ? props.onDeleteProject(projectId(item), item.update.id) : props.onDeleteInitiative(initiativeId(item), item.update.id))
      feed.removeItem(item.id)
    },
    onDeleteAttachment: async (item, attachmentId) => {
      const update = await (item.kind === 'project' ? props.onDeleteProjectAttachment(projectId(item), item.update.id, attachmentId) : props.onDeleteInitiativeAttachment(initiativeId(item), item.update.id, attachmentId))
      feed.patchUpdate(item.id, update)
    },
    onToggleSubscription: async (item, subscribe) => {
      const subscribed = await run(() => changePulseSubscription(item.kind, item.source.id, subscribe), 'Could not update subscription')
      feed.setSubscribed(item.source.id, subscribed)
      if (tab === 'following') setUnsubscribed(current => { const next = new Set(current); if (subscribed) next.delete(item.source.id); else next.add(item.source.id); return next })
      pulseUnread.schedule(props.data.workspace.urlKey)
    },
    onToggleTeam: async (teamId, subscribed) => {
      await run(() => changePulseSubscription('team', teamId, subscribed), 'Could not update subscription')
      const team = props.data.teams.find(entry => entry.id === teamId)
      toast(subscribed ? t('Subscribed to team project updates') : t('Unsubscribed from team project updates'), { description: (subscribed ? t('You will now receive all project updates for {name}') : t('You will no longer receive all project updates for {name}')).replace('{name}', team?.name ?? t('this team')) })
      pulseUnread.schedule(props.data.workspace.urlKey)
    },
    onNavigate: navigate,
  }
}

function PulseFeedList({ activeReply, containerRef, data, feed, handlers, onActivate, onCaughtUp, onHoverChange, tab, unsubscribedSources }: {
  activeReply?: { itemId: string; nonce: number }
  onCaughtUp: () => void
  onHoverChange: (itemId: string, hovered: boolean) => void
  containerRef: React.RefObject<HTMLElement | null>
  data: BootstrapData
  feed: PulseFeedState
  handlers: PulseCardHandlers
  onActivate: (itemId: string) => void
  tab: PulseRouteView
  unsubscribedSources: Set<string>
}) {
  const { t } = useI18n()
  const [renderLimit, setRenderLimit] = useState(INITIAL_RENDER)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const firstId = feed.items[0]?.id
  useEffect(() => setRenderLimit(INITIAL_RENDER), [tab, firstId])
  const weekStartsOn = weekStartsOnFromSetting(data.userSettings[data.viewer.id]?.firstDay)
  // Groups are positional over the (bounded, ≤100) loaded list — computed once per list.
  const groups = useMemo(() => tab === 'popular' ? new Map<number, PulseGroupKey>() : pulseGroupStarts(feed.items, new Date(), weekStartsOn), [feed.items, tab, weekStartsOn])
  const visible = feed.items.slice(0, renderLimit)
  const canGrow = renderLimit < feed.items.length || feed.hasMore
  const { hasMore, loadMore, loadingMore } = feed
  const itemCount = feed.items.length
  useEffect(() => {
    const sentinel = sentinelRef.current
    if (!sentinel || !canGrow) return
    const observer = new IntersectionObserver(entries => {
      if (!entries.some(entry => entry.isIntersecting)) return
      setRenderLimit(limit => {
        const next = limit + RENDER_STEP
        if (next >= itemCount && hasMore && !loadingMore) void loadMore()
        return Math.min(next, Math.max(itemCount, INITIAL_RENDER))
      })
    }, { root: containerRef.current, rootMargin: '900px 0px' })
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [canGrow, containerRef, hasMore, itemCount, loadMore, loadingMore])

  return <ol aria-label={t('Pulse feed')} className="pulse-feed">
    {visible.map((item, index) => {
      const group = groups.get(index)
      return <Fragment key={item.id}>
        {item.id === feed.lastSeenFeedItemId && <PulseLastSeenSeparator containerRef={containerRef} firstOfGroup={Boolean(group)} onReached={onCaughtUp}/>}
        {group && <li className="pulse-group-header" role="presentation"><span>{t(pulseGroupLabels[group])}</span><i/></li>}
        <li className="pulse-feed-item">
          <PulseUpdateCard {...handlers} data={data} item={item} onActivate={onActivate} onHoverChange={onHoverChange} replyRequest={activeReply?.itemId === item.id ? activeReply.nonce : undefined} unsubscribed={tab === 'following' && unsubscribedSources.has(item.source.id) && !item.subscribed} users={data.users} view={tab} viewer={data.viewer} viewerRole={data.viewerRole}/>
        </li>
      </Fragment>
    })}
    {canGrow && <li aria-hidden="true" className="pulse-feed-sentinel" role="presentation"><div ref={sentinelRef}/>{loadingMore && <PulseCardSkeleton/>}</li>}
  </ol>
}

/**
 * "Last seen" divider. Visible when the page opens → a static "Last seen"
 * header; otherwise an animated line + check "You’re all caught up" once the
 * reader scrolls past the new items (and that records "seen").
 */
function PulseLastSeenSeparator({ containerRef, firstOfGroup, onReached }: { containerRef: React.RefObject<HTMLElement | null>; firstOfGroup: boolean; onReached: () => void }) {
  const { t } = useI18n()
  const ref = useRef<HTMLLIElement>(null)
  const [initiallyVisible, setInitiallyVisible] = useState<boolean>()
  const [reached, setReached] = useState(false)
  const reachedRef = useRef(onReached)
  reachedRef.current = onReached
  useEffect(() => {
    const element = ref.current
    if (!element) return
    const observer = new IntersectionObserver(([entry]) => {
      setInitiallyVisible(current => current ?? entry.isIntersecting)
      if (entry.isIntersecting) { setReached(true); reachedRef.current() }
    }, { root: containerRef.current, threshold: 0.1 })
    observer.observe(element)
    return () => observer.disconnect()
  }, [containerRef])
  return <li className="pulse-last-seen" data-first-of-group={firstOfGroup || undefined} ref={ref} role="separator">
    {initiallyVisible === false ? <div className="pulse-caught-up" data-reached={reached || undefined}>
      <i className="pulse-caught-up-line"/><span className="pulse-caught-up-check"><Check size={11}/></span><span className="pulse-caught-up-text">{t('You’re all caught up')}</span><i className="pulse-caught-up-line"/>
    </div> : <div className="pulse-last-seen-label"><span>{t('Last seen')}</span><i/></div>}
  </li>
}

function PulseViewTabs({ activeId, views, workspaceKey, onOpen, onEdit, onDuplicate, onFavorite, onDelete, onReorder }: {
  activeId?: string
  views: SavedView[]
  workspaceKey: string
  onOpen: (id: string) => void
  onEdit: (view: SavedView) => void
  onDuplicate: (view: SavedView) => void
  onFavorite: (view: SavedView) => void
  onDelete: (view: SavedView) => void
  onReorder: (dragged: string, target: string) => void
}) {
  const { t } = useI18n()
  const [dragging, setDragging] = useState<string>()
  // Keep the active custom feed inline even when it sits past the overflow.
  const inline = views.slice(0, INLINE_VIEW_TABS)
  const activeOverflow = activeId && !inline.some(saved => saved.id === activeId) ? views.find(saved => saved.id === activeId) : undefined
  if (activeOverflow) inline.push(activeOverflow)
  const overflow = views.filter(saved => !inline.includes(saved))
  const menu = (saved: SavedView, trigger: ReactNode) => <ContextMenu.Root key={saved.id}><ContextMenu.Trigger asChild>{trigger}</ContextMenu.Trigger><ContextMenu.Portal><ContextMenu.Content className="pulse-menu pulse-saved-view-menu" data-flow-motion="floating">
    <ContextMenu.Item onSelect={() => onEdit(saved)}><span>{t('Edit…')}</span></ContextMenu.Item>
    <ContextMenu.Item onSelect={() => onDuplicate(saved)}><span>{t('Duplicate…')}</span></ContextMenu.Item>
    <ContextMenu.Separator/>
    <ContextMenu.Item onSelect={() => onFavorite(saved)}><span>{saved.favorite ? t('Unfavorite') : t('Favorite')}</span></ContextMenu.Item>
    <ContextMenu.Separator/>
    <ContextMenu.Item className="is-danger" onSelect={() => onDelete(saved)}><Trash2/><span>{t('Delete')}</span></ContextMenu.Item>
  </ContextMenu.Content></ContextMenu.Portal></ContextMenu.Root>
  return <>
    {inline.map(saved => {
      const index = views.indexOf(saved)
      const shortcut = PULSE_VIEW_KEYS[index]
      return menu(saved, <a
        aria-current={activeId === saved.id ? 'page' : undefined}
        className="pulse-saved-tab ui-pill"
        data-dragging={dragging === saved.id || undefined}
        draggable
        href={pulseViewPath(workspaceKey, saved.id)}
        onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); onOpen(saved.id) }}
        onDragEnd={() => setDragging(undefined)}
        onDragOver={event => { if (dragging && dragging !== saved.id) event.preventDefault() }}
        onDragStart={event => { setDragging(saved.id); event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', saved.id) }}
        onDrop={event => { event.preventDefault(); const dragged = dragging ?? event.dataTransfer.getData('text/plain'); if (dragged) onReorder(dragged, saved.id); setDragging(undefined) }}
        title={shortcut ? `${saved.name} (${shortcut})` : saved.name}
      ><ViewGlyph color={saved.color ?? '#8a8f98'} icon={saved.icon ?? 'CustomView'}/><span data-i18n-ignore>{saved.name}</span></a>)
    })}
    {overflow.length > 0 && <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="ui-pill pulse-more-views" type="button">{t('{count} more').replace('{count}', String(overflow.length))}<ChevronDown size={12}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="pulse-menu" data-flow-motion="floating" sideOffset={4}>
      <DropdownMenu.Label className="pulse-menu-label">{t('All views')}</DropdownMenu.Label>
      {views.map(saved => <DropdownMenu.Item key={saved.id} onSelect={() => onOpen(saved.id)}><ViewGlyph color={saved.color ?? '#8a8f98'} icon={saved.icon ?? 'CustomView'}/><span data-i18n-ignore>{saved.name}</span>{activeId === saved.id && <Check className="pulse-menu-end" size={13}/>}</DropdownMenu.Item>)}
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}
  </>
}

function PulseHelpPopover() {
  const { t } = useI18n()
  return <Popover.Root><Popover.Trigger asChild><button aria-label={t('What is Pulse?')} className="pulse-help-button" type="button"><CircleHelp size={13}/></button></Popover.Trigger><Popover.Portal><Popover.Content align="start" className="pulse-help-popover" data-flow-motion="floating" sideOffset={6}>
    <div className="pulse-help-icon"><PulseIcon size={16}/></div>
    <strong>{t('What is Pulse?')}</strong>
    <p>{t('Pulse brings together updates from projects, initiatives, and teams so you can follow progress across your workspace.')}</p>
    <a href={DOCS_URL} rel="noreferrer" target="_blank">{t('Documentation')}</a>
  </Popover.Content></Popover.Portal></Popover.Root>
}

function PulseEmptyState({ copy, onCreate }: { copy: string; onCreate: () => void }) {
  const { t } = useI18n()
  return <div className="pulse-empty"><div className="pulse-empty-icon"><i/><i/><span><PulseIcon size={17}/></span></div><strong>{t('Pulse')}</strong><p>{copy}</p><div><button onClick={onCreate} type="button">{t('New update')}</button><a href={DOCS_URL} rel="noreferrer" target="_blank">{t('Documentation')}</a></div></div>
}

function PulseNoResults({ term, onClearSearch, onClearFilters }: { term: string; onClearSearch?: () => void; onClearFilters?: () => void }) {
  const { t } = useI18n()
  return <div className="pulse-empty pulse-no-results">
    <strong>{term ? t('No results found for "{term}"').replace('{term}', term) : t('No results found')}</strong>
    <div>{onClearSearch && <button onClick={onClearSearch} type="button">{t('Clear search')}</button>}{onClearFilters && <button onClick={onClearFilters} type="button">{t('Clear filters')}</button>}</div>
  </div>
}

function PulseCardSkeleton() { return <div aria-hidden="true" className="pulse-card-skeleton"><i/><i/><i/></div> }
function PulseFeedSkeleton() { const { t } = useI18n(); return <div aria-busy="true" aria-label={t('Loading Pulse')} className="pulse-feed pulse-feed-loading"><PulseCardSkeleton/><PulseCardSkeleton/><PulseCardSkeleton/></div> }
