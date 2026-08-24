import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { BookOpen, ChevronDown, CircleCheck, CircleHelp, Download, FileClock, GitPullRequest, GripVertical, History, Keyboard, MessageCircle, MessageCircleQuestion, MoreHorizontal, Plus, Rocket, Search, Settings, Star, Trash2, X } from 'lucide-react'
import { useEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { MembersIcon as FlowMembersIcon, SlackIcon as FlowSlackIcon } from '@/components/issue/issue-icons'
import { WorkspaceMenu } from '@/components/workspace/workspace-menu'
import { agentPath, asksPath, customersPath, draftsPath, inboxPath, initiativesPath, membersPath, myIssuesPath, projectsPath, pulsePath, releasePipelinesPath, reviewsPath, teamCyclesPath, teamHomePath, teamIssuesPath, teamProjectsPath, teamsPath, teamViewsPath, upcomingCyclePath, workspaceLibraryPath, workspaceViewsPath } from '@/lib/app-routes'
import type { AccountBootstrap, BootstrapData, Team, Workspace } from '@/types/flow'

import './sidebar.css'

export type PageId = 'inbox' | 'search' | 'pulse' | 'reviews' | 'my-issues' | 'workspace-issues' | 'team-issues' | 'cycles' | 'cycle-detail' | 'projects' | 'views' | 'project-detail' | 'issue-detail' | 'initiatives' | 'initiative-detail' | 'members' | 'customers' | 'teams' | 'new-team' | 'drafts' | 'agent' | 'releases' | 'asks' | 'library' | 'document-detail' | 'customer-detail'

type SidebarEntry = 'inbox' | 'reviews' | 'myIssues' | 'pulse' | 'drafts' | 'agent' | 'initiatives' | 'projects' | 'views' | 'members' | 'customers' | 'teams'
type SidebarVisibility = 'always' | 'badged' | 'never'
type SidebarPreferences = Record<SidebarEntry, SidebarVisibility>
type SidebarGroup = 'personal' | 'workspace'
type SidebarOrder = Record<SidebarGroup, SidebarEntry[]>

const defaultPersonalOrder: SidebarEntry[] = ['inbox', 'reviews', 'myIssues', 'pulse', 'drafts', 'agent']
const defaultWorkspaceOrder: SidebarEntry[] = ['initiatives', 'projects', 'views', 'members', 'customers', 'teams']

const defaultPreferences: SidebarPreferences = {
  inbox: 'always', reviews: 'always', myIssues: 'always', pulse: 'always', drafts: 'badged', agent: 'always',
  initiatives: 'always', projects: 'always', views: 'always', members: 'never', customers: 'never', teams: 'never',
}

export function Sidebar({ account, data, page, open = false, onOpenChange, onSearch, onCreate, onOpenSettings, onSwitchWorkspace, onCreateWorkspace, onLogout }: {
  account: AccountBootstrap; data: BootstrapData; page: PageId | 'not-found'; open?: boolean; onOpenChange?: (open: boolean) => void; onSearch: () => void; onCreate: () => void; onOpenSettings: (page?: 'workspace'|'members') => void
  onSwitchWorkspace: (workspace: Workspace) => void; onCreateWorkspace: () => void; onLogout: () => Promise<void>
}) {
  const close = () => onOpenChange?.(false)
  const workspaceSlug = data.workspace.urlKey
  const [customizeOpen, setCustomizeOpen] = useState(false)
  const [planOpen, setPlanOpen] = useState(false)
  const [preferences, setPreferences] = useState(readSidebarPreferences)
  const [sidebarOrder, setSidebarOrder] = useState(readSidebarOrder)
  const [dismissedTry, setDismissedTry] = useState<string[]>(readDismissedTry)
  const [navOverflowing, setNavOverflowing] = useState(false)
  const navRef = useRef<HTMLElement>(null)
  const featureEnabled = (feature: string) => data.workspaceSettings.featureFlags[feature] !== false

  useEffect(() => persistPreference('flow.sidebar.preferences', preferences), [preferences])
  useEffect(() => persistPreference('flow.sidebar.order', sidebarOrder), [sidebarOrder])
  useEffect(() => persistPreference('flow.sidebar.dismissed-try', dismissedTry), [dismissedTry])
  useEffect(() => {
    const nav = navRef.current
    if (!nav) return
    const update = () => setNavOverflowing(nav.scrollHeight > nav.clientHeight + 1)
    const observer = new ResizeObserver(update)
    const mutationObserver = new MutationObserver(update)
    observer.observe(nav)
    mutationObserver.observe(nav, { childList: true, subtree: true })
    update()
    window.addEventListener('resize', update)
    return () => { observer.disconnect(); mutationObserver.disconnect(); window.removeEventListener('resize', update) }
  }, [dismissedTry, preferences, sidebarOrder])

  const activeWorkspaceEntry: SidebarEntry | undefined = page === 'members' ? 'members' : page === 'customers' || page === 'customer-detail' ? 'customers' : page === 'teams' || page === 'new-team' ? 'teams' : undefined
  const show = (entry: SidebarEntry) => preferences[entry] === 'always' || entry === activeWorkspaceEntry
  const available = (entry: SidebarEntry) => (entry !== 'initiatives' || featureEnabled('initiatives'))
    && (entry !== 'customers' || featureEnabled('customer-requests'))
  const hiddenWorkspaceEntries = sidebarOrder.workspace.filter(entry => available(entry) && !show(entry) && (data.viewerRole !== 'guest' || !['initiatives','views','customers'].includes(entry)))
  const inboxUnread = data.notifications.filter(item => item.recipientId === data.viewer.id && !item.readAt && !item.archivedAt && !item.deletedAt && (!item.snoozedUntil || new Date(item.snoozedUntil) <= new Date())).length
  const dismissTry = (id: string) => setDismissedTry(current => [...new Set([...current, id])])
  const reorderSidebar = (group: SidebarGroup, active: SidebarEntry, target: SidebarEntry) => {
    setSidebarOrder(current => ({ ...current, [group]: reorderEntries(current[group], active, target) }))
  }

  const personalNavigation: Record<SidebarEntry, ReactNode> = {
    inbox: <Nav badge={inboxUnread} icon={<FlowIcon name="Inbox"/>} label="Inbox" to={inboxPath(workspaceSlug)} onClick={close}/>,
    reviews: <Nav badge={data.reviews.filter(item=>item.status!=='merged'&&item.status!=='closed'&&item.reviewerIds.includes(data.viewer.id)).length} active={page==='reviews'} icon={<GitPullRequest/>} label="Reviews" to={reviewsPath(workspaceSlug)} onClick={close}/>,
    myIssues: <Nav active={page === 'issue-detail'} icon={<FlowIcon name="MyIssues"/>} label="My issues" to={myIssuesPath(workspaceSlug, 'activity')} onClick={close}/>,
    pulse: featureEnabled('pulse') ? <Nav icon={<PulseIcon/>} label="Pulse" to={pulsePath(workspaceSlug)} onClick={close}/> : null,
    drafts: <Nav badge={data.drafts.length} active={page === 'drafts'} icon={<DraftIcon/>} label="Drafts" to={draftsPath(workspaceSlug)} onClick={close}/>,
    agent: featureEnabled('ai') ? <Nav icon={<AgentIcon/>} label="Agent" to={agentPath(workspaceSlug)} onClick={close}/> : null,
    initiatives: null, projects: null, views: null, members: null, customers: null, teams: null,
  }
  const workspaceNavigation: Record<SidebarEntry, ReactNode> = {
    initiatives: data.viewerRole === 'guest' || !featureEnabled('initiatives') ? null : <Nav active={page === 'initiative-detail'} icon={<FlowIcon name="Initiative"/>} label="Initiatives" to={initiativesPath(workspaceSlug)} onClick={close}/>,
    projects: <Nav active={page === 'project-detail'} icon={<FlowIcon name="Project"/>} label="Projects" to={projectsPath(workspaceSlug)} onClick={close}/>,
    views: data.viewerRole === 'guest' ? null : <Nav icon={<FlowIcon name="CustomView"/>} label="Views" to={workspaceViewsPath(workspaceSlug)} onClick={close}/>,
    members: <Nav active={page === 'members'} icon={<MembersIcon/>} label="Members" to={membersPath(workspaceSlug)} onClick={close}/>,
    customers: data.viewerRole === 'guest' || !featureEnabled('customer-requests') ? null : <Nav active={page === 'customers'} icon={<CustomersIcon/>} label="Customers" to={customersPath(workspaceSlug)} onClick={close}/>,
    teams: <Nav active={page === 'teams'} icon={<FlowIcon name="Team"/>} label="Teams" to={teamsPath(workspaceSlug)} onClick={close}/>,
    inbox: null, reviews: null, myIssues: null, pulse: null, drafts: null, agent: null,
  }

  return <>
    <button className={`sidebar-scrim ${open ? 'open' : ''}`} type="button" aria-label="Close sidebar" onClick={close}/>
    <aside className={`sidebar ${open ? 'open' : ''}`} aria-label="Workspace navigation">
      <div className="workspace-row">
        <WorkspaceMenu account={account} data={data} onSettings={onOpenSettings} onSwitch={onSwitchWorkspace} onCreate={onCreateWorkspace} onLogout={onLogout}/>
        <button className="sidebar-top-action" type="button" aria-label="Search workspace" title="Search workspace" onClick={onSearch}><FlowIcon name="Search"/></button>
        <button className="sidebar-top-action sidebar-compose" type="button" aria-label="Create new issue" title="Create new issue" onClick={onCreate}><ComposeIcon/></button>
      </div>

      <nav ref={navRef} className="sidebar-nav">
        <div className="sidebar-primary-links">
          {sidebarOrder.personal.map(entry => show(entry) ? <span className="sidebar-ordered-entry" key={entry}>{personalNavigation[entry]}</span> : null)}
        </div>

        <Section label="Workspace" storageKey="workspace">
          {sidebarOrder.workspace.map(entry => show(entry) ? <span className="sidebar-ordered-entry" key={entry}>{workspaceNavigation[entry]}</span> : null)}
          <MoreMenu entries={hiddenWorkspaceEntries} onCustomize={() => setCustomizeOpen(true)} workspaceSlug={workspaceSlug} releases={featureEnabled('releases')} asks={featureEnabled('asks')}/>
        </Section>

        <Section label="Library" storageKey="library">
          {data.favorites.length > 0 && <Nav active={page === 'library' && location.pathname.endsWith('/favorites')} icon={<Star/>} label="Favorites" to={workspaceLibraryPath(workspaceSlug, 'favorites')} onClick={close}/>}
          <Nav active={page === 'library' && location.pathname.endsWith('/recent')} icon={<History/>} label="Recently viewed" to={workspaceLibraryPath(workspaceSlug, 'recent')} onClick={close}/>
        </Section>

        <Section label="Your teams" storageKey="teams" action={<NavLink className="section-action" aria-label="Join a team" title="Join a team" to={teamsPath(workspaceSlug)} onClick={close}><Plus/></NavLink>}>
          <div className="sidebar-team-list">
            {data.teams.map(team => <TeamNavigation
              key={team.id}
              cyclesEnabled={Boolean(data.cycleSettings[team.id]?.enabled)}
              upcoming={data.cycles.some(cycle=>cycle.teamId===team.id&&cycle.status==='upcoming')}
              team={team}
              workspaceSlug={workspaceSlug}
              page={page}
              onNavigate={close}
            />)}
          </div>
        </Section>

        <Section label="Try" storageKey="try">
          {data.viewerRole === 'admin' && !dismissedTry.includes('invite') && <TryItem icon={<Plus/>} label="Invite people" to={`${membersPath(workspaceSlug)}?invite=1`} onClick={close} onDismiss={() => dismissTry('invite')}/>}
          {!dismissedTry.includes('cycles') && data.teams[0] ? <TryItem icon={<CycleIcon/>} label="Cycles" to={teamCyclesPath(workspaceSlug, data.teams[0].key)} onClick={close} onDismiss={() => dismissTry('cycles')}/> : null}
          {!dismissedTry.includes('github') && <TryItem icon={<GitHubIcon/>} label="Connect GitHub" onClick={() => toast.info('GitHub is not connected in this workspace.')} onDismiss={() => dismissTry('github')}/>}
        </Section>
      </nav>

      <footer className="sidebar-footer">
        <HelpMenu onSettings={() => onOpenSettings('workspace')}/>
        {!navOverflowing && <button className="plan-pill" type="button" aria-label="Free plan" title="Your workspace is on a free plan" onClick={() => setPlanOpen(true)}><UpgradeIcon/><span>Free plan</span></button>}
      </footer>
    </aside>

    <SidebarCustomization open={customizeOpen} onOpenChange={setCustomizeOpen} preferences={preferences} order={sidebarOrder} onChange={setPreferences} onReorder={reorderSidebar}/>
    <UpgradeDialog open={planOpen} onOpenChange={setPlanOpen}/>
  </>
}

function Section({ label, children, action, storageKey }: { label: string; children: ReactNode; action?: ReactNode; storageKey: string }) {
  const [expanded, setExpanded] = useState(() => readExpandedSection(storageKey))
  useEffect(() => persistPreference(`flow.sidebar.section.${storageKey}`, expanded), [expanded, storageKey])
  return <section className={`nav-section sidebar-section-${storageKey}`} data-expanded={expanded}>
    <div className="section-heading">
      <button className="section-label" type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}><span>{label}</span><ChevronDown/></button>
      {action}
    </div>
    {expanded ? <div className="section-content">{children}</div> : null}
  </section>
}

function TeamNavigation({ team, workspaceSlug, page, cyclesEnabled, upcoming, onNavigate }: { team: Team; workspaceSlug: string; page: PageId | 'not-found'; cyclesEnabled: boolean; upcoming:boolean; onNavigate: () => void }) {
  const [expanded, setExpanded] = useState(() => readExpandedSection(`team.${team.id}`))
  useEffect(() => persistPreference(`flow.sidebar.section.team.${team.id}`, expanded), [expanded, team.id])
  const overviewPath = teamHomePath(workspaceSlug, team.key)
  const onOverview = typeof location !== 'undefined' && location.pathname === overviewPath
  return <div className="sidebar-team" data-expanded={expanded}>
    <div className="team-heading">
      <button type="button" className="team-title" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>
        <FlowIcon name="Team" style={{ color: team.color }}/><strong>{team.name}</strong><span className={`team-disclosure ${expanded ? 'expanded' : ''}`}><TeamDisclosureIcon/></span>
      </button>
    </div>
    {expanded && <div className="team-links">
      <Nav active={onOverview} icon={<FlowIcon name="Home"/>} label="Home" to={overviewPath} onClick={onNavigate}/>
      <Nav active={page === 'team-issues' && !onOverview} icon={<IssuesIcon/>} label="Issues" to={teamIssuesPath(workspaceSlug, team.key)} onClick={onNavigate}/>
      {cyclesEnabled && <Nav active={page === 'cycles' || page === 'cycle-detail'} icon={<CycleIcon/>} label="Cycles" to={teamCyclesPath(workspaceSlug, team.key)} onClick={onNavigate}/>}
      {cyclesEnabled&&upcoming&&<Nav icon={<span/>} label="Upcoming" to={upcomingCyclePath(workspaceSlug,team.key)} onClick={onNavigate}/>}
      <Nav icon={<FlowIcon name="Project"/>} label="Projects" to={teamProjectsPath(workspaceSlug, team.key)} onClick={onNavigate}/>
      <Nav icon={<FlowIcon name="CustomView"/>} label="Views" to={teamViewsPath(workspaceSlug, team.key)} onClick={onNavigate}/>
    </div>}
  </div>
}

function MoreMenu({ entries, onCustomize, workspaceSlug, releases, asks }: { entries: SidebarEntry[]; onCustomize: () => void; workspaceSlug: string; releases: boolean; asks: boolean }) {
  const items: Partial<Record<SidebarEntry, { label: string; icon: ReactElement; onSelect?: () => void; to?: string }>> = {
    initiatives: { label: 'Initiatives', icon: <FlowIcon name="Initiative"/>, to: initiativesPath(workspaceSlug) },
    projects: { label: 'Projects', icon: <FlowIcon name="Project"/>, to: projectsPath(workspaceSlug) },
    views: { label: 'Views', icon: <FlowIcon name="CustomView"/>, to: workspaceViewsPath(workspaceSlug) },
    members: { label: 'Members', icon: <MembersIcon/>, to: membersPath(workspaceSlug) },
    customers: { label: 'Customers', icon: <CustomersIcon/>, to: customersPath(workspaceSlug) },
    teams: { label: 'Teams', icon: <FlowIcon name="Team"/>, to: teamsPath(workspaceSlug) },
  }
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild><button className="nav-item sidebar-more-trigger" type="button" aria-label="Show more links"><MoreHorizontal/><span>More</span></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content className="sidebar-popover sidebar-more-menu" side="bottom" align="start" sideOffset={6.5}>
      {entries.map(entry => {
        const item = items[entry]
        if (!item) return null
        if (item.to) return <DropdownMenu.Item key={entry} asChild><NavLink to={item.to}>{item.icon}<span>{item.label}</span></NavLink></DropdownMenu.Item>
        return <DropdownMenu.Item key={entry} onSelect={item.onSelect}>{item.icon}<span>{item.label}</span></DropdownMenu.Item>
      })}
      {releases&&<DropdownMenu.Item asChild><NavLink to={releasePipelinesPath(workspaceSlug)}><Rocket/><span>Releases</span></NavLink></DropdownMenu.Item>}
      {asks&&<DropdownMenu.Item asChild><NavLink to={asksPath(workspaceSlug)}><MessageCircleQuestion/><span>Asks</span></NavLink></DropdownMenu.Item>}
      <DropdownMenu.Item asChild><NavLink to={workspaceLibraryPath(workspaceSlug,'deleted')}><Trash2/><span>Recently deleted</span></NavLink></DropdownMenu.Item>
      <DropdownMenu.Item asChild><NavLink to={workspaceLibraryPath(workspaceSlug,'audit-log')}><FileClock/><span>Audit log</span></NavLink></DropdownMenu.Item>
      <DropdownMenu.Separator/>
      <DropdownMenu.Item onSelect={onCustomize}><CustomizeIcon/><span>Customize sidebar</span></DropdownMenu.Item>
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function TryItem({ icon, label, onClick, onDismiss, to }: { icon: ReactNode; label: string; onClick?: () => void; onDismiss: () => void; to?: string }) {
  const content = <>{icon}<span>{label}</span></>
  return <div className="try-item">
    {to ? <NavLink to={to} onClick={onClick}>{content}</NavLink> : <button type="button" onClick={onClick}>{content}</button>}
    <button className="try-dismiss" type="button" aria-label={`Dismiss ${label}`} onClick={onDismiss}><X/></button>
  </div>
}

function Nav({ icon, label, badge, active, onClick, to }: { icon: ReactElement; label: string; badge?: number; active?: boolean; onClick?: () => void; to?: string }) {
  const content = <><span className="nav-icon">{icon}</span><span className="nav-label">{label}</span>{badge ? <span className="nav-unread" aria-label={`${badge} unread`}>{badge > 99 ? '99+' : badge}</span> : null}</>
  if (!to) return <button type="button" className={`nav-item ${active ? 'active' : ''}`} onClick={onClick}>{content}</button>
  return <NavLink end={false} className={({ isActive }) => `nav-item ${active || isActive ? 'active' : ''}`} to={to} onClick={onClick}>{content}</NavLink>
}

function SidebarCustomization({ open, onOpenChange, preferences, order, onChange, onReorder }: { open: boolean; onOpenChange: (open: boolean) => void; preferences: SidebarPreferences; order: SidebarOrder; onChange: (preferences: SidebarPreferences) => void; onReorder: (group: SidebarGroup, active: SidebarEntry, target: SidebarEntry) => void }) {
  const personal: Record<SidebarEntry, [string, ReactElement]> = { inbox: ['Inbox', <FlowIcon key="inbox" name="Inbox"/>], reviews: ['Reviews', <GitPullRequest key="reviews"/>], myIssues: ['My issues', <FlowIcon key="my-issues" name="MyIssues"/>], pulse: ['Pulse', <PulseIcon key="pulse"/>], drafts: ['Drafts', <DraftIcon key="drafts"/>], agent: ['Agent', <AgentIcon key="agent"/>], initiatives: ['', <></>], projects: ['', <></>], views: ['', <></>], members: ['', <></>], customers: ['', <></>], teams: ['', <></>] }
  const workspace: Record<SidebarEntry, [string, ReactElement]> = { initiatives: ['Initiatives', <FlowIcon key="initiatives" name="Initiative"/>], projects: ['Projects', <FlowIcon key="projects" name="Project"/>], views: ['Views', <FlowIcon key="views" name="CustomView"/>], members: ['Members', <MembersIcon key="members"/>], customers: ['Customers', <CustomersIcon key="customers"/>], teams: ['Teams', <FlowIcon key="teams" name="Team"/>], inbox: ['', <></>], reviews: ['', <></>], myIssues: ['', <></>], pulse: ['', <></>], drafts: ['', <></>], agent: ['', <></>] }
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="sidebar-customize-dialog">
    <DialogTitle>Customize sidebar</DialogTitle>
    <label className="sidebar-badge-style"><span>Default badge style</span><span className="badge-preview">1</span><select aria-label="Default badge style" defaultValue="count"><option value="count">Count</option><option value="dot">Dot</option></select></label>
    <CustomizationGroup group="personal" label="Personal" entries={order.personal.map(id => [id, ...personal[id]])} preferences={preferences} onChange={onChange} onReorder={onReorder}/>
    <CustomizationGroup group="workspace" label="Workspace" entries={order.workspace.map(id => [id, ...workspace[id]])} preferences={preferences} onChange={onChange} onReorder={onReorder}/>
  </DialogContent></Dialog>
}

function CustomizationGroup({ group, label, entries, preferences, onChange, onReorder }: { group: SidebarGroup; label: string; entries: Array<[SidebarEntry, string, ReactElement]>; preferences: SidebarPreferences; onChange: (preferences: SidebarPreferences) => void; onReorder: (group: SidebarGroup, active: SidebarEntry, target: SidebarEntry) => void }) {
  const draggingRef = useRef<SidebarEntry | null>(null)
  const [dragging, setDragging] = useState<SidebarEntry | null>(null)
  const [keyboardDragging, setKeyboardDragging] = useState<SidebarEntry | null>(null)
  const finishDragging = () => { draggingRef.current = null; setDragging(null) }
  const moveToPointer = (event: React.PointerEvent<HTMLButtonElement>) => {
    const active = draggingRef.current
    if (!active) return
    const targetRow = document.elementFromPoint(event.clientX, event.clientY)?.closest<HTMLElement>('[data-sidebar-customize-entry]')
    if (!targetRow || targetRow.dataset.sidebarCustomizeGroup !== group) return
    const target = targetRow.dataset.sidebarCustomizeEntry as SidebarEntry
    if (target !== active) onReorder(group, active, target)
  }
  const handleKeyboard = (event: React.KeyboardEvent<HTMLButtonElement>, id: SidebarEntry) => {
    if (event.key === ' ' || event.key === 'Enter') {
      event.preventDefault()
      setKeyboardDragging(current => current === id ? null : id)
      return
    }
    if (event.key === 'Escape' && keyboardDragging === id) { event.preventDefault(); setKeyboardDragging(null); return }
    if (keyboardDragging !== id || (event.key !== 'ArrowUp' && event.key !== 'ArrowDown')) return
    event.preventDefault()
    const index = entries.findIndex(([entry]) => entry === id)
    const targetIndex = event.key === 'ArrowUp' ? index - 1 : index + 1
    const target = entries[targetIndex]?.[0]
    if (target) onReorder(group, id, target)
  }
  return <section className="sidebar-customize-group"><h3>{label}</h3><div>{entries.map(([id, name, icon]) => <div
    className="sidebar-customize-row"
    data-dragging={dragging === id || keyboardDragging === id}
    data-sidebar-customize-entry={id}
    data-sidebar-customize-group={group}
    key={id}
    role="button"
    tabIndex={-1}
    aria-label={name}
    aria-roledescription="sortable"
  >
    <button
      className="customize-drag-handle"
      type="button"
      aria-label={`Reorder ${name}`}
      aria-pressed={keyboardDragging === id}
      title={`Drag to reorder ${name}`}
      onBlur={() => setKeyboardDragging(null)}
      onKeyDown={event => handleKeyboard(event, id)}
      onPointerDown={event => {
        if (event.button !== 0) return
        event.preventDefault()
        event.currentTarget.focus()
        event.currentTarget.setPointerCapture(event.pointerId)
        draggingRef.current = id
        setDragging(id)
      }}
      onPointerMove={moveToPointer}
      onPointerUp={finishDragging}
      onPointerCancel={finishDragging}
    ><GripVertical/></button><span className="customize-row-icon">{icon}</span><span>{name}</span>
    <select aria-label={`${name} visibility`} value={preferences[id]} onChange={event => onChange({ ...preferences, [id]: event.target.value as SidebarVisibility })}>
      <option value="always">Always show</option><option value="badged">Show when badged</option><option value="never">Don't show</option>
    </select>
  </div>)}</div></section>
}

function HelpMenu({ onSettings }: { onSettings: () => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="sidebar-help" type="button" aria-label="Open Help menu"><CircleHelp/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content className="sidebar-popover sidebar-help-menu" side="top" align="start" sideOffset={4}>
      <DropdownMenu.Item onSelect={() => openExternal('https://flow.app/docs')}><Search/>Search for help…</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => openExternal('https://flow.app/docs')}><BookOpen/>Docs</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => openExternal('https://flow.app/contact')}><MessageCircle/>Contact us</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: '?', shiftKey: true }))}><Keyboard/>Keyboard shortcuts<kbd className="menu-end">⌘ /</kbd></DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => openExternal('https://flowstatus.com')}><CircleCheck/>Flow status</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => openExternal('https://flow.app/download')}><Download/>Download apps</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={onSettings}><Settings/>Settings<kbd className="menu-end">G then S</kbd></DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => openExternal('https://flow.app/join-slack')}><SlackIcon/>Slack community</DropdownMenu.Item>
      <DropdownMenu.Label className="sidebar-help-label">What’s new</DropdownMenu.Label>
      <DropdownMenu.Item className="sidebar-news-item" onSelect={() => openExternal('https://flow.app/changelog')}><NewsDot/>Team initiatives</DropdownMenu.Item>
      <DropdownMenu.Item className="sidebar-news-item" onSelect={() => openExternal('https://flow.app/changelog')}><NewsDot/>Coding sessions on mobile</DropdownMenu.Item>
      <DropdownMenu.Item className="sidebar-news-item" onSelect={() => openExternal('https://flow.app/changelog')}><NewsDot/>Full changelog</DropdownMenu.Item>
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function UpgradeDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const rows = [['Used 0 of 250 free issues', 'Unlimited issues'], ['Only 2 teams', '3 additional teams'], ['10MB file limit', 'Upload any file'], ['Admin roles', 'Admin roles']]
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="sidebar-upgrade-dialog"><DialogTitle className="sr-only">Your workspace is on the free plan</DialogTitle>
    <UpgradeIcon/><h2>Your workspace is on the free plan</h2><p>Upgrade to keep creating issues and access more features.</p>
    <div className="upgrade-columns"><div><strong>Current plan</strong><span>Free</span></div><div><strong>Basic plan</strong><span>$10 per user/month, billed yearly</span></div></div>
    <div className="upgrade-table">{rows.map(([free, basic]) => <div key={free}><span><i>×</i>{free}</span><span><i>✓</i>{basic}</span></div>)}</div>
    <footer><button type="button" onClick={() => window.open('https://flow.app/pricing', '_blank')}>See all plans</button><button className="upgrade-primary" type="button" onClick={() => window.open('https://flow.app/pricing', '_blank')}>Upgrade to Basic</button></footer>
  </DialogContent></Dialog>
}

function FlowIcon({ name, style }: { name: string; style?: CSSProperties }) { return <svg className="flow-sprite-icon" viewBox="0 0 16 16" aria-hidden="true" style={style}><use href={`/flow-core-icons.svg#${name}`}/></svg> }
function PulseIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M9.35 1.35 3 8.55h4.1l-.45 6.1L13 7.45H8.9l.45-6.1Z" fill="currentColor"/></svg> }
function AgentIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m2.2 2.15 11.15 4.7-4.7 1.35-1.35 4.7-5.1-10.75Z" fill="none" stroke="currentColor" strokeWidth="1.35" strokeLinejoin="round"/></svg> }
function ComposeIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M7.25 1C7.66414 1 7.99988 1.33589 8 1.75C8 2.16421 7.66421 2.5 7.25 2.5H4.75C3.50745 2.5 2.50012 3.50744 2.5 4.75V11.25C2.5 12.4926 3.50736 13.5 4.75 13.5H11.25C12.4926 13.5 13.5 12.4926 13.5 11.25V8.75C13.5001 8.33589 13.8359 8 14.25 8C14.6641 8 14.9999 8.33589 15 8.75V11.25C15 13.3211 13.3211 15 11.25 15H4.75C2.67893 15 1 13.3211 1 11.25V4.75C1.00012 2.67905 2.67899 1 4.75 1H7.25Z"/><path fillRule="evenodd" clipRule="evenodd" d="M13.4326 1.26953C13.7913.910937 14.3728.910883 14.7314 1.26953C15.0897 1.6282 15.0899 2.20981 14.7314 2.56836L9.2373 8.06152C8.68101 8.6177 7.94043 8.95161 7.15527 9C7.06754 9.0052 6.99468 8.93248 7 8.84473C7.04847 8.05961 7.38232 7.31897 7.93848 6.7627L13.4326 1.26953Z"/></svg> }
function IssuesIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2.5" width="8.5" height="8.5" rx="2" fill="none" stroke="currentColor" strokeWidth="1.4"/><path d="M6 13.5h5.5a2 2 0 0 0 2-2V6" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"/></svg> }
function CycleIcon() { return <FlowIcon name="Refresh"/> }
function GitHubIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.4a6.75 6.75 0 0 0-2.13 13.15c.34.06.46-.14.46-.32v-1.3c-1.88.41-2.28-.8-2.28-.8-.31-.78-.75-.98-.75-.98-.61-.42.05-.41.05-.41.68.05 1.03.69 1.03.69.6 1.03 1.57.73 1.95.56.06-.43.24-.73.43-.9-1.5-.17-3.08-.75-3.08-3.34 0-.74.26-1.34.69-1.81-.07-.17-.3-.86.07-1.79 0 0 .57-.18 1.86.69A6.4 6.4 0 0 1 8 4.61c.58 0 1.15.08 1.69.23 1.29-.87 1.85-.69 1.85-.69.37.93.14 1.62.07 1.79.43.47.69 1.07.69 1.81 0 2.6-1.58 3.16-3.09 3.33.24.21.46.63.46 1.27v1.88c0 .18.12.39.46.32A6.75 6.75 0 0 0 8 1.4Z" fill="currentColor"/></svg> }
function MembersIcon() { return <FlowMembersIcon/> }
function CustomersIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2.5h7a2 2 0 0 1 2 2v7H5a2 2 0 0 1-2-2v-7Z" fill="none" stroke="currentColor" strokeWidth="1.3"/><path d="M6 5.5h4M6 8h3M12 5h1a1 1 0 0 1 1 1v7H7" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/></svg> }
function DraftIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2.5h8a1.5 1.5 0 0 1 1.5 1.5v8A1.5 1.5 0 0 1 11 13.5H3A1.5 1.5 0 0 1 1.5 12V4A1.5 1.5 0 0 1 3 2.5Z" fill="none" stroke="currentColor" strokeWidth="1.3"/><path d="m5 10 1-2.5 3.2-3.2 1.5 1.5L7.5 9 5 10Z" fill="currentColor"/></svg> }
function CustomizeIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M10.5 2.5h3v3M13.2 2.8 8.7 7.3M7 3H3.5A1.5 1.5 0 0 0 2 4.5v8A1.5 1.5 0 0 0 3.5 14h8a1.5 1.5 0 0 0 1.5-1.5V9" fill="none" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" strokeLinejoin="round"/></svg> }
function UpgradeIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" strokeWidth="1.5"/><path d="m5.5 8 2.5-2.5L10.5 8M8 5.7v4.8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg> }
function TeamDisclosureIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M7.00194 10.6239C6.66861 10.8183 6.25 10.5779 6.25 10.192V5.80802C6.25 5.42212 6.66861 5.18169 7.00194 5.37613L10.7596 7.56811C11.0904 7.76105 11.0904 8.23895 10.7596 8.43189L7.00194 10.6239Z"/></svg> }
function SlackIcon() { return <FlowSlackIcon className="slack-icon"/> }
function NewsDot() { return <span className="news-dot" aria-hidden="true"/> }

function readSidebarPreferences(): SidebarPreferences { try { return { ...defaultPreferences, ...JSON.parse(localStorage.getItem('flow.sidebar.preferences') ?? '{}') } } catch { return defaultPreferences } }
function readSidebarOrder(): SidebarOrder {
  try {
    const stored = JSON.parse(localStorage.getItem('flow.sidebar.order') ?? '{}') as Partial<SidebarOrder>
    return { personal: normalizeOrder(stored.personal, defaultPersonalOrder), workspace: normalizeOrder(stored.workspace, defaultWorkspaceOrder) }
  } catch { return { personal: [...defaultPersonalOrder], workspace: [...defaultWorkspaceOrder] } }
}
function normalizeOrder(stored: SidebarEntry[] | undefined, defaults: SidebarEntry[]) {
  const allowed = new Set(defaults)
  const valid = Array.isArray(stored) ? stored.filter((entry, index) => allowed.has(entry) && stored.indexOf(entry) === index) : []
  const merged=[...valid, ...defaults.filter(entry => !valid.includes(entry))]
  if(defaults===defaultPersonalOrder)return ['inbox','reviews',...merged.filter(entry=>entry!=='inbox'&&entry!=='reviews')] as SidebarEntry[]
  return merged
}
function reorderEntries(entries: SidebarEntry[], active: SidebarEntry, target: SidebarEntry) {
  const sourceIndex = entries.indexOf(active)
  const targetIndex = entries.indexOf(target)
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return entries
  const reordered = [...entries]
  reordered.splice(sourceIndex, 1)
  reordered.splice(targetIndex, 0, active)
  return reordered
}
function readDismissedTry(): string[] { try { const value = JSON.parse(localStorage.getItem('flow.sidebar.dismissed-try') ?? '[]'); return Array.isArray(value) ? value : [] } catch { return [] } }
function readExpandedSection(key: string) { try { return localStorage.getItem(`flow.sidebar.section.${key}`) !== 'false' } catch { return true } }
function persistPreference(key: string, value: unknown) { try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* Preferences remain in memory. */ } }
function openExternal(url: string) { window.open(url, '_blank', 'noopener,noreferrer') }
