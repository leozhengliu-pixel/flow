import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Command } from 'cmdk'
import {
  Clipboard,
  ArrowRight,
  Box,
  FileText,
  FolderKanban,
  GitPullRequest,
  Hand,
  LifeBuoy,
  Inbox,
  Layers3,
  Lightbulb,
  Maximize2,
  Plus,
  Search,
  SquareDot,
  Tag,
  Trash2,
  UserRound,
} from 'lucide-react'

import { Check, ChevronRight, Minus } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { StatusIcon } from '@/components/issue/issue-icons'
import { useIssueSearch } from '@/components/issue/use-issue-search'
import { PulseIcon } from '@/components/pulse/pulse-icon'
import { ReleasesIcon } from '@/components/releases/release-icons'
import { searchWorkspace } from '@/lib/api'
import { ActionRegistry } from '@/lib/action-registry'
import { sortActionGroups } from '@/lib/action-groups'
import { useSelectedModels } from '@/lib/selected-models-store'
import { useAllowedActionGroups } from '@/hooks/use-action-groups-for-selection'
import { useI18n } from '@/i18n/i18n'
import { translatedKeywords } from '@/lib/search-match'
import { roleAllowed } from '@/lib/settings-access'
import type { AgentSession, BootstrapData, Customer, SearchResult } from '@/types/flow'
import { agentSessionUnread, formatAgentHistoryTime, groupAgentHistory } from '@/components/agent/agent-read-state'
import { CustomerLogo } from '@/components/customer/customer-logo'
import { useCommandContext } from './command-context'
import { useDisplayCommands } from './display-commands'
import { documentCommands, type DocumentCommandHost } from './document-commands'
import { GLOBAL_DOCUMENT_PAGES } from './document-command-pages'
import { GLOBAL_RELEASE_PAGES, releaseCommands, type ReleaseCommandHost } from './release-commands'
import { contextChip, useContextCommands, type CommandPage, type ContextCommandHandlers, type PageOption } from './context-commands'
import './command-menu.css'
import { AgentCursorGlyph, AgentHistoryGlyph } from '@/components/ui/agent-glyph'
import { usePhoneLayout } from '@/hooks/use-phone-layout'

/** A Settings › Security "who can" permission for the viewer; without workspace data the command stays listed. */
function viewerAllowed(data: BootstrapData | undefined, key: 'templatePermission' | 'teamCreatePermission') {
  return !data || roleAllowed(data.viewerRole, data.workspaceSettings?.[key])
}

type CommandAction = {
  id: string
  group: string
  label: string
  icon: React.ReactNode
  shortcut?: string[]
  keywords?: string
  /** Breadcrumb label (Display options › Grouping › Customer), each part translated. */
  path?: string[]
  run: () => void
}

export function CommandMenu({
  open,
  onOpenChange,
  onCreateIssue,
  onCreateIssueFullscreen,
  onCreateIssueTemplate,
  onCreateLabel,
  onCreateProject,
  onCreateProjectTemplate,
  onCreateView,
  onCreateInitiative,
  onSearchWorkspace,
  onNavigateInbox,
  onNavigateMyIssues,
  onNavigateProjects,
  onNavigateRecentlyDeletedProjects,
  onNavigateInitiatives,
  onNavigateViews,
  onNavigateMembers,
  onNavigateSettings,
  onReportPerformance,
  onCreateTeam,
  onNavigateCustomers,
  onGoToCustomers,
  onOpenCustomer,
  onNavigateAgent,
  onAskAgent,
  onAskAgentQuery,
  agentSessions,
  onOpenAgentSession,
  onNavigateReviews,
  onNavigatePulse,
  pulseToggle,
  onOpenResult,
  data,
  initialCustomerPicker = false,
  initialDocumentPicker = false,
  initialIssuePicker = false,
  initialReleasePicker = false,
  documentHost,
  releaseHost,
  ...contextHandlers
}: {
  open: boolean
  onOpenChange: (value: boolean) => void
  onCreateIssue: () => void
  /** "Create issue in fullscreen…" (V): the same composer opened expanded. */
  onCreateIssueFullscreen?: () => void
  onCreateIssueTemplate: () => void
  /** "Create new label…": opens the issue label settings with the create row focused. */
  onCreateLabel?: () => void
  onCreateProject: () => void
  onCreateProjectTemplate?: () => void
  onCreateView: () => void
  onCreateInitiative: () => void
  onSearchWorkspace: () => void
  onNavigateInbox: () => void
  onNavigateMyIssues: () => void
  onNavigateProjects: () => void
  onNavigateRecentlyDeletedProjects?: () => void
  onNavigateInitiatives: () => void
  onNavigateViews: () => void
  onNavigateMembers: () => void
  onNavigateSettings?: () => void
  onReportPerformance?: () => void
  /** "Create a team…", listed when Settings › Security "Team creation" allows the viewer. */
  onCreateTeam?: () => void
  onNavigateCustomers: () => void
  /** "Go to customers" (G then Q); omitted when customer requests are off or for guests, which also hides the other customer commands. */
  onGoToCustomers?: () => void
  /** "Open customer…" (O then Q) picks a customer from a nested page. */
  onOpenCustomer?: (customer: Customer) => void
  onNavigateAgent: () => void
  /** "Ask Flow" (⌘J): open the floating agent chat; omitted where the shortcut does nothing. */
  onAskAgent?: () => void
  /** "Ask Flow" with the typed question (Tab): runs it in the floating chat with the current page as context, or in the page chat on the Agent page. */
  onAskAgentQuery?: (question: string) => void
  /** The viewer's agent chats for "Open past agent chat…"; the entry needs `onOpenAgentSession` too. */
  agentSessions?: AgentSession[]
  onOpenAgentSession?: (session: AgentSession) => void
  onNavigateReviews?: () => void
  /** "Go to pulse" (G then F); omitted when Pulse is off or for guests. */
  onNavigatePulse?: () => void
  /** Admins: "Enable Pulse" / "Disable Pulse". */
  pulseToggle?: { enabled: boolean; run: () => void }
  onOpenResult: (result: SearchResult) => void
  /** Workspace data for context actions (issue page, peek pane, selection). */
  data?: BootstrapData
  /** Open straight on the "Open customer…" picker (the O then Q shortcut). */
  initialCustomerPicker?: boolean
  /** Open straight on the "Open document…" picker (the O then D shortcut). */
  initialDocumentPicker?: boolean
  initialIssuePicker?: boolean
  /** Open straight on the "Create new release…" pipeline picker (the N then R shortcut). */
  initialReleasePicker?: boolean
  /** Navigation and the New release composer for the release commands; omitted where releases are unavailable. */
  releaseHost?: ReleaseCommandHost
  /** Reload and navigation for document writes; defaults to router navigation without a reload. */
  documentHost?: DocumentCommandHost
} & ContextCommandHandlers) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const [query, setQuery] = useState('')
  const [pages, setPages] = useState<CommandPage[]>(() => initialIssuePicker ? [{ id: 'issueOpen', label: 'Open issue…' }] : initialDocumentPicker ? [{ id: 'documentOpen', label: 'Open document…' }] : initialReleasePicker ? [{ id: 'releaseCreate', label: 'Create new release…' }] : [])
  const page = pages.at(-1)
  const issuePickerOptions = useIssueSearch(query, data?.issues ?? [], open && page?.id === 'issueOpen')
  const [results, setResults] = useState<SearchResult[]>([])
  const [loading, setLoading] = useState(false)
  // "Open customer…" lists the workspace's customers in place of the root commands.
  const [customerPicker, setCustomerPicker] = useState(Boolean(initialCustomerPicker))
  // "Open past agent chat…" lists the viewer's chats in place of the root commands (the reference app's searchable chat list).
  const [pastChats, setPastChats] = useState(false)
  const pastChatSessions = useMemo(() => [...(agentSessions ?? [])].filter(session => session.slugId).sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt)), [agentSessions])
  const pastChatGroups = useMemo(() => groupAgentHistory(pastChatSessions), [pastChatSessions])
  const location = useLocation()
  const selectedModels = useSelectedModels()
  const allowedActionGroups = useAllowedActionGroups()
  const closeAnd = (work: () => void) => () => { onOpenChange(false); work() }
  const context = useCommandContext()
  const chip = contextChip(context, t)
  const displayCommands = useDisplayCommands(open)
  const navigate = useNavigate()
  const host = useMemo<DocumentCommandHost>(() => documentHost ?? { reload: async () => undefined, navigate: path => navigate(path) }, [documentHost, navigate])
  const issueCommands = useContextCommands({ context, data, page, query, handlers: contextHandlers, close: () => onOpenChange(false), t })
  const documents = documentCommands({ context, data, page, query, host, close: () => onOpenChange(false), t, pathname: location.pathname })
  const releases = releaseCommands({ data: releaseHost ? data : undefined, pathname: location.pathname, page, host: releaseHost ?? { navigate: () => undefined, createRelease: () => undefined }, close: () => onOpenChange(false) })
  const releasePage = Boolean(page && GLOBAL_RELEASE_PAGES.has(page.id))
  const issueOpenOptions: PageOption[] = page?.id === 'issueOpen' ? issuePickerOptions.filter(issue => !issue.archivedAt).map(issue => ({
    id: issue.id,
    label: issue.title,
    entity: true,
    keywords: `${issue.identifier} ${issue.team.name}`,
    icon: <StatusIcon state={issue.state} size={16}/>,
    detail: issue.identifier,
    select: closeAnd(() => onOpenResult({ type: 'issue', id: issue.id, identifier: issue.identifier, title: issue.title, score: 0 })),
  })) : []
  const contextCommands = {
    heading: context?.kind === 'document' ? 'Document' : context?.kind === 'release' ? '' : issueCommands.heading,
    actions: context?.kind === 'document' ? documents.actions : context?.kind === 'release' ? context.actions : issueCommands.actions,
    options: page?.id === 'issueOpen' ? issueOpenOptions : documents.options.length ? documents.options : releasePage ? releases.options : context?.kind === 'release' ? (page && context.pageOptions?.(page, query)) || [] : issueCommands.options,
  }
  const phoneSelectionContext = phone && context?.kind === 'issues' && context.source === 'selection'
  const visibleContextActions = phoneSelectionContext
    ? ['ctx-assignee', 'ctx-assign-me', 'ctx-status', 'ctx-priority', 'ctx-project', 'ctx-labels', 'ctx-team', 'ctx-due-date']
      .map(id => contextCommands.actions.find(action => action.id === id))
      .filter((action): action is NonNullable<typeof action> => Boolean(action))
      .map(action => ({
      ...action,
      icon: <PhoneCommandGlyph id={action.id} fallback={action.icon}/>,
      label: ({
        'ctx-priority': 'Set priority…',
        'ctx-project': 'Add to project…',
        'ctx-team': 'Move to a different team…',
        'ctx-assign-me': 'Un-assign from me',
      } as Record<string, string>)[action.id] ?? action.label,
      }))
    : contextCommands.actions
  const openPage = (next: CommandPage) => { setPages(current => [...current, next]); setQuery('') }
  const back = () => { setPages(current => current.slice(0, -1)); setQuery('') }
  // Tab (or the "Ask Flow" hint): the typed question runs in the chat at once; an empty box just opens the chat.
  const askFlow = () => {
    const question = query.trim()
    if (question && onAskAgentQuery) closeAnd(() => onAskAgentQuery(question))()
    else if (onAskAgent) closeAnd(onAskAgent)()
    else closeAnd(onNavigateAgent)()
  }
  const actions: CommandAction[] = [
    { id: 'create-issue', group: 'Issues', label: 'Create new issue…', icon: <Plus/>, shortcut: ['C'], keywords: 'new ticket task', run: closeAnd(onCreateIssue) },
    ...(onCreateIssueFullscreen ? [{ id: 'create-issue-fullscreen', group: 'Issues', label: 'Create issue in fullscreen…', icon: <Maximize2/>, shortcut: ['V'], keywords: 'new ticket task expanded', run: closeAnd(onCreateIssueFullscreen) }] : []),
    ...(onCreateLabel ? [{ id: 'create-label', group: 'Issues', label: 'Create new label…', icon: <Tag/>, keywords: 'new tag label', run: closeAnd(onCreateLabel) }] : []),
    { id: 'create-project', group: 'Projects', label: 'Create new project…', icon: <Box/>, shortcut: ['N', 'then', 'P'], run: closeAnd(onCreateProject) },
    ...(onCreateProjectTemplate ? [{ id: 'create-project-template', group: 'Projects', label: 'Create new project template…', icon: <FileText/>, run: closeAnd(onCreateProjectTemplate) }] : []),
    { id: 'create-view', group: 'Views', label: 'Create view…', icon: <Layers3/>, run: closeAnd(onCreateView) },
    { id: 'create-initiative', group: 'Initiatives', label: 'Create new initiative', icon: <Lightbulb/>, shortcut: ['N', 'then', 'I'], run: closeAnd(onCreateInitiative) },
    ...(onGoToCustomers ? [{ id: 'create-customer', group: 'Customers', label: 'Create new customer…', icon: <CustomerCommandIcon/>, keywords: 'add create', run: closeAnd(onNavigateCustomers) }] : []),
    ...documents.global.map(action => ({ id: action.id, group: 'Documents', label: action.label, icon: action.icon, shortcut: action.shortcut, keywords: action.keywords, run: () => { if (action.page) openPage(action.page); else void action.run?.() } })),
    ...releases.global.map(action => ({ id: action.id, group: action.group, label: action.label, icon: action.icon, shortcut: action.shortcut, keywords: action.keywords, run: () => { if (action.page) openPage(action.page); else void action.run?.() } })),
    { id: 'search-workspace', group: 'Filter', label: 'Search workspace...', icon: <Search/>, run: closeAnd(onSearchWorkspace) },
    // The reference app lists these only for people the workspace "who can" settings allow.
    ...(viewerAllowed(data, 'templatePermission') ? [{ id: 'issue-template', group: 'Templates', label: 'Create new issue template…', icon: <FileText/>, run: closeAnd(onCreateIssueTemplate) }] : []),
    ...(onCreateTeam && viewerAllowed(data, 'teamCreatePermission') ? [{ id: 'create-team', group: 'Teams', label: 'Create a team…', icon: <Plus/>, keywords: 'new team', run: closeAnd(onCreateTeam) }] : []),
    ...(data && data.viewerRole !== 'guest' ? [{ id: 'view-admins', group: 'Navigation', label: 'View workspace admins', icon: <UserRound/>, keywords: 'admin owner members', run: closeAnd(onNavigateMembers) }] : []),
    { id: 'go-inbox', group: 'Navigation', label: 'Go to Inbox', icon: <Inbox/>, shortcut: ['G', 'then', 'I'], run: closeAnd(onNavigateInbox) },
    { id: 'go-my-issues', group: 'Navigation', label: 'Go to My issues', icon: <SquareDot/>, shortcut: ['G', 'then', 'M'], run: closeAnd(onNavigateMyIssues) },
    { id: 'open-issue', group: 'Navigation', label: 'Open issue…', icon: <SquareDot/>, shortcut: ['O', 'then', 'I'], keywords: 'issue picker ticket find', run: () => openPage({ id: 'issueOpen', label: 'Open issue…' }) },
    { id: 'open-project', group: 'Navigation', label: 'Open project…', icon: <Box/>, shortcut: ['O', 'then', 'P'], keywords: 'project picker', run: closeAnd(onNavigateProjects) },
    { id: 'go-projects', group: 'Navigation', label: 'Go to projects', icon: <ArrowRight/>, shortcut: ['G', 'then', 'P'], run: closeAnd(onNavigateProjects) },
    ...(onNavigateRecentlyDeletedProjects ? [{ id: 'go-deleted-projects', group: 'Navigation', label: 'Go to recently deleted projects', icon: <Trash2/>, keywords: 'trash restore removed', run: closeAnd(onNavigateRecentlyDeletedProjects) }] : []),
    { id: 'go-initiatives', group: 'Navigation', label: 'Go to Initiatives', icon: <Lightbulb/>, run: closeAnd(onNavigateInitiatives) },
    { id: 'go-views', group: 'Navigation', label: 'Go to Views', icon: <Layers3/>, run: closeAnd(onNavigateViews) },
    { id: 'go-members', group: 'Navigation', label: 'Go to Members', icon: <UserRound/>, run: closeAnd(onNavigateMembers) },
    ...(onGoToCustomers ? [
      { id: 'go-customers', group: 'Navigation', label: 'Go to customers', icon: <CustomerCommandIcon/>, shortcut: ['G', 'then', 'Q'], keywords: 'open', run: closeAnd(onGoToCustomers) },
      ...(onOpenCustomer ? [{ id: 'open-customer', group: 'Navigation', label: 'Open customer…', icon: <CustomerCommandIcon/>, shortcut: ['O', 'then', 'Q'], keywords: 'goto', run: () => { setCustomerPicker(true); setQuery('') } }] : []),
    ] : []),
    ...(onAskAgent ? [{ id: 'ask-agent', group: 'Agent chat', label: 'Ask Flow', icon: <AgentCursorGlyph/>, shortcut: ['⌘', 'J'], keywords: 'agent chat ai assistant', run: closeAnd(onAskAgent) }] : []),
    ...(onOpenAgentSession ? [{ id: 'open-past-chat', group: 'Agent chat', label: 'Open past agent chat…', icon: <AgentHistoryGlyph/>, keywords: 'agent chats history conversation recent', run: () => { setPastChats(true); setQuery('') } }] : []),
    { id: 'go-agent', group: 'Agent chat', label: 'Go to Agent', icon: <AgentCursorGlyph/>, shortcut: ['G', 'then', 'J'], run: closeAnd(onNavigateAgent) },
    ...(onNavigatePulse ? [{ id: 'go-pulse', group: 'Navigation', label: 'Go to pulse', icon: <PulseIcon size={14}/>, shortcut: ['G', 'then', 'F'], keywords: 'feed updates', run: closeAnd(onNavigatePulse) }] : []),
    ...(pulseToggle ? [{ id: 'toggle-pulse', group: 'Pulse', label: pulseToggle.enabled ? 'Disable Pulse' : 'Enable Pulse', icon: <PulseIcon size={14}/>, keywords: 'feed updates', run: closeAnd(pulseToggle.run) }] : []),
    ...(onNavigateReviews ? [{ id: 'go-reviews', group: 'Reviews', label: 'Go to Reviews', icon: <GitPullRequest/>, shortcut: ['G', 'then', 'R'], run: closeAnd(onNavigateReviews) }] : []),
    ...(onNavigateSettings ? [{ id: 'touch-optimized', group: 'Settings', label: 'Force UI optimized for touch', icon: <Hand/>, keywords: 'pro touch settings', run: closeAnd(onNavigateSettings) }] : []),
    ...(onReportPerformance ? [
      { id: 'report-performance', group: 'Help', label: 'Report performance problem', icon: <LifeBuoy/>, keywords: 'pro feedback diagnostics', run: closeAnd(onReportPerformance) },
      { id: 'contact-support', group: 'Help', label: 'Contact support', icon: <LifeBuoy/>, keywords: 'pro', run: closeAnd(onReportPerformance) },
      { id: 'whats-new', group: 'Help', label: "What's new", icon: <Lightbulb/>, keywords: 'pro', run: closeAnd(onReportPerformance) },
    ] : []),
    { id: 'copy-url', group: 'Other', label: 'Copy current page link', icon: <Clipboard/>, run: closeAnd(() => void navigator.clipboard.writeText(window.location.href)) },
    ...displayCommands.map(command => ({ id: command.id, group: 'Display options', label: `Display options ${command.path.join(' ')}`, path: ['Display options', ...command.path], icon: <CustomerCommandIcon/>, keywords: `${t('Display options')} ${command.path.map(part => t(part)).join(' ')}`, run: closeAnd(command.run) })),
  ]
  // Register every command once so keyboard, menu and future contextual
  // surfaces dispatch through the same action path.
  const registry = useRef(new ActionRegistry()).current
  actions.forEach(action => registry.register(action))
  contextCommands.actions.forEach(action => registry.register({ ...action, group: contextCommands.heading, run: () => { if (action.page) openPage(action.page); else void action.run?.() } }))

  useEffect(() => {
    if (!open) { setQuery(''); setResults([]); setPages([]); setCustomerPicker(false); setPastChats(false); return }
    if (page || customerPicker || pastChats || !query.trim()) { setResults([]); setLoading(false); return }
    let active = true
    const controller = new AbortController()
    setLoading(true)
    const timer = window.setTimeout(() => {
      searchWorkspace(query.trim(), [], 12, controller.signal)
        .then(response => { if (active) setResults(response.results) })
        .catch(reason => { if (active && !(reason instanceof Error && reason.name === 'AbortError')) setResults([]) })
        .finally(() => { if (active) setLoading(false) })
    }, 140)
    return () => { active = false; window.clearTimeout(timer); controller.abort() }
  }, [customerPicker, open, page, pastChats, query])

  const groupIds = [...new Set(actions.map(action => action.group))]
  const groups = useMemo(
    () =>
      sortActionGroups(groupIds, {
        pathname: location.pathname,
        selectedModels,
        allowedActionGroups: allowedActionGroups.length ? allowedActionGroups : undefined,
      }),
    // groupIds is derived each render; serialize for stable compare
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [groupIds.join("|"), allowedActionGroups, location.pathname, selectedModels],
  )
  const firstSearchGroup = query.trim()
    ? groups.find(group => actions.some(action => action.group === group && `${action.label} ${action.keywords ?? ''}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())))
    : undefined
  // The context vanished (selection cleared, issue closed): nested pages no longer apply.
  useEffect(() => { if (!context) setPages(current => current.filter(item => item.id === 'issueOpen' || GLOBAL_DOCUMENT_PAGES.has(item.id) || GLOBAL_RELEASE_PAGES.has(item.id))) }, [context])
  const onInputKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Backspace' && !query && customerPicker) { event.preventDefault(); setCustomerPicker(false); return }
    if (event.key === 'Backspace' && !query && pastChats) { event.preventDefault(); setPastChats(false); return }
    if (event.key === 'Backspace' && !query && pages.length) { event.preventDefault(); back(); return }
    if (page && !query && !event.metaKey && !event.ctrlKey && !event.altKey && /^\d$/.test(event.key)) {
      const match = contextCommands.options.find(option => option.shortcut === event.key)
      if (match) { event.preventDefault(); selectOption(match); return }
    }
    if (event.key === 'Tab' && !event.shiftKey && !event.nativeEvent.isComposing && !page && !customerPicker && !pastChats) { event.preventDefault(); askFlow() }
  }
  const selectOption = (option: PageOption) => {
    if (option.page) { openPage(option.page); return }
    void option.select?.()
  }
  const optionGroups = contextCommands.options.reduce<{ heading?: string; options: PageOption[] }[]>((groups, option) => {
    const last = groups.at(-1)
    if (last && last.heading === option.group) last.options.push(option)
    else groups.push({ heading: option.group, options: [option] })
    return groups
  }, [])
  // Keep the group registered while requests change; cmdk sorts groups in a deferred layout effect.
  const searchResults = <Command.Group value="workspace-search-results" heading={!loading && query && results.length > 0 ? t('Quick results for "{query}"').replace('{query}', query) : undefined}>
    {!loading && query && results.map(result => <Command.Item key={`${result.type}-${result.id}`} value={`${result.identifier ?? ''} ${result.title} ${result.subtitle ?? ''}`} onSelect={closeAnd(() => onOpenResult(result))}>
      <ResourceIcon result={result}/>
      <span className="command-result-copy">{result.identifier && <small>{result.identifier}</small>}<span>{result.title}</span></span>
      <small>{result.type}</small>
    </Command.Item>)}
  </Command.Group>
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="command-dialog" overlayClassName="command-overlay" onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { if (pastChats) { event.preventDefault(); setPastChats(false); setQuery('') } else if (customerPicker && !initialCustomerPicker) { event.preventDefault(); setCustomerPicker(false); setQuery('') } else if (pages.length) { event.preventDefault(); back() } }}>
      <DialogTitle className="sr-only">Command menu</DialogTitle>
      <Command shouldFilter loop filter={(value, search, keywords) => {
        if (!search.trim()) return 1
        const needle = search.trim().toLocaleLowerCase()
        return `${value} ${keywords ?? ''}`.toLocaleLowerCase().includes(needle) ? 1 : 0
      }}>
        {(chip || page) && <div className="command-context" aria-label={t('Command context')}>
          {chip && (chip.type
            ? <span className="command-context-chip is-typed" title={`${chip.type} ⋅ ${chip.label}`}><span className="command-context-chip__type">{chip.type} ⋅</span> <span data-i18n-ignore={chip.entity || undefined}>{chip.label}</span></span>
            : <span className="command-context-chip" data-i18n-ignore={chip.entity || undefined} title={chip.label}>{chip.label}</span>)}
          {pages.map((item, index) => <span key={`${item.id}-${index}`} className="command-context-page"><ChevronRight aria-hidden="true" size={12}/><span>{t(item.label)}</span></span>)}
        </div>}
        <div className="command-input">
          <Command.Input aria-label="Command menu" placeholder={pastChats ? t('Open past agent chat…') : customerPicker ? t('Open customer…') : page ? t(page.label) : t('Type a command or search...')} autoFocus value={query} onValueChange={setQuery} onKeyDown={onInputKeyDown}/>
          {!page && !customerPicker && !pastChats && <button type="button" onClick={askFlow}><span>Ask Flow</span><kbd>Tab</kbd></button>}
        </div>
        {pastChats ? <Command.List>
          {pastChatGroups.map(group => <Fragment key={group.label}>
            <Command.Group heading={t(group.label)}>
              {group.sessions.map(session => <Command.Item key={session.id} value={`${session.title} ${session.id}`} onSelect={closeAnd(() => onOpenAgentSession?.(session))}>
                <i className="command-chat-dot" aria-hidden="true" data-unread={agentSessionUnread(session) || undefined}/>
                <span className="command-option-label" data-i18n-ignore>{session.title}</span>
                <time className="command-chat-time">{formatAgentHistoryTime(session.updatedAt, t)}</time>
              </Command.Item>)}
            </Command.Group>
          </Fragment>)}
          <Command.Empty>{t('No results found.')}</Command.Empty>
        </Command.List> : customerPicker ? <Command.List>
          <Command.Group heading={t('Customers')}>
            {(data?.customers ?? []).map(customer => <Command.Item key={customer.id} value={`${customer.name} ${customer.domains.join(' ')} ${customer.id}`} onSelect={closeAnd(() => onOpenCustomer?.(customer))}>
              <span className="command-item-icon"><CustomerLogo customer={customer} size={16}/></span>
              <span data-i18n-ignore>{customer.name}</span>
            </Command.Item>)}
          </Command.Group>
          <Command.Empty>{t('No results found.')}</Command.Empty>
        </Command.List> : page ? <Command.List>
          {optionGroups.map(group => <Command.Group key={group.heading ?? page.label} heading={t(group.heading ?? page.label)}>
            {group.options.map(option => <Command.Item key={option.id || 'none'} value={`${option.label} ${option.keywords ?? ''}`} keywords={option.entity ? undefined : translatedKeywords([option.label], t)} forceMount={option.forceMount} onSelect={() => selectOption(option)}>
              {option.checked !== undefined && <span className="command-checkbox" data-checked={option.checked === true ? 'true' : option.checked === 'mixed' ? 'mixed' : 'false'} aria-hidden="true">{option.checked === true ? <Check size={11}/> : option.checked === 'mixed' ? <Minus size={11}/> : null}</span>}
              {option.icon && <span className="command-item-icon">{option.icon}</span>}
              <span className="command-option-label" data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>
              {option.detail && <small className="command-option-detail" data-i18n-ignore>{option.detail}</small>}
              {option.current && <span className="command-option-current" aria-label={t('Current')}><Check size={14}/></span>}
              {option.shortcut && <kbd className="command-option-shortcut">{option.shortcut}</kbd>}
              {option.page && <ChevronRight className="command-option-current" size={14}/>}
            </Command.Item>)}
          </Command.Group>)}
          <Command.Empty>{t('No results found.')}</Command.Empty>
        </Command.List> : <Command.List>
          {loading && <div className="command-loading">Searching...</div>}
          {visibleContextActions.length > 0 && <Command.Group className={phoneSelectionContext ? 'command-context-actions' : undefined} style={phoneSelectionContext ? { height: `${visibleContextActions.length * 46}px` } : undefined} heading={contextCommands.heading ? t(contextCommands.heading) : undefined}>
            {visibleContextActions.map((action, index) => <Command.Item key={action.id} style={phoneSelectionContext ? { position: 'absolute', top: `${index * 46}px`, left: 0, right: 0 } : undefined} value={`${action.label} ${action.keywords ?? ''}`} keywords={translatedKeywords([action.label], t)} onSelect={() => void registry.execute(action.id, { source: 'command-menu' })}>
              <span className="command-item-icon">{action.icon}</span>
              <span>{t(action.label)}</span>
              {action.detail && <span className="command-option-detail" data-i18n-ignore>{action.detail}</span>}
              {action.shortcut && <span className="command-shortcut">{action.shortcut.map((part, index) => <kbd key={index}>{part}</kbd>)}</span>}
              {action.checked && <span className="command-option-current" aria-label={t('Current')}><Check size={14}/></span>}
            </Command.Item>)}
          </Command.Group>}
          {groups.map(group => <Command.Group key={group} heading={group === firstSearchGroup ? undefined : group}>
            {actions.filter(action => action.group === group).map(action => <Command.Item key={action.id} value={`${action.label} ${action.keywords ?? ''}`} keywords={translatedKeywords(action.path ?? [action.label], t)} onSelect={() => void registry.execute(action.id, { source: 'command-menu' })}>
              <span className="command-item-icon">{action.icon}</span>
              {action.path ? <span className="command-path">{action.path.map((part, index) => <span key={index}>{index > 0 && <ChevronRight aria-hidden="true" size={12}/>}{t(part)}</span>)}</span> : <span>{action.label}</span>}
              {action.shortcut && <span className="command-shortcut">{action.shortcut.map((part, index) => part === 'then' ? <small key={index}>then</small> : <kbd key={index}>{part}</kbd>)}</span>}
            </Command.Item>)}
          </Command.Group>)}
          {searchResults}
          <Command.Empty>{loading ? 'Searching...' : 'No results found.'}</Command.Empty>
        </Command.List>}
      </Command>
    </DialogContent>
  </Dialog>
}

/** The reference app's customer glyph (a sheet with a person), used by the customer commands. */
function CustomerCommandIcon() {
  return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path clipRule="evenodd" d="M11.0247 12.3333C13.6728 12.3334 14.6225 13.529 14.9606 14.319C15.112 14.6739 14.806 15 14.4046 15H7.59537C7.18784 14.9997 6.8816 14.6641 7.04464 14.3073C7.40663 13.5172 8.38955 12.3333 11.0247 12.3333Z" fillRule="evenodd"/><path clipRule="evenodd" d="M11 7C12.1045 7 12.9998 7.89543 12.9998 9C12.9998 10.1046 12.1045 11 11 11C9.89553 11 9.00018 10.1046 9.00018 9C9.00018 7.89543 9.89553 7 11 7Z" fillRule="evenodd"/><path clipRule="evenodd" d="M10 4.25V3.75C10 3.05964 9.44036 2.5 8.75 2.5H3.75C3.05964 2.5 2.5 3.05964 2.5 3.75V13.25C2.5 13.3881 2.61193 13.5 2.75 13.5H4.25C4.66421 13.5 5 13.8358 5 14.25C5 14.6642 4.66421 15 4.25 15H2.75C1.7835 15 1 14.2165 1 13.25V3.75C1 2.23122 2.23122 1 3.75 1H8.75C10.2688 1 11.5 2.23122 11.5 3.75V4.25C11.5 4.66421 11.1642 5 10.75 5C10.3358 5 10 4.66421 10 4.25Z" fillRule="evenodd"/><path clipRule="evenodd" d="M7.75 4.25C8.16421 4.25 8.5 4.58579 8.5 5C8.5 5.41421 8.16421 5.75 7.75 5.75H4.75C4.33579 5.75 4 5.41421 4 5C4 4.58579 4.33579 4.25 4.75 4.25H7.75Z" fillRule="evenodd"/><path clipRule="evenodd" d="M6.5 7.25C6.91421 7.25 7.25 7.58579 7.25 8C7.25 8.41421 6.91421 8.75 6.5 8.75H4.75C4.33579 8.75 4 8.41421 4 8C4 7.58579 4.33579 7.25 4.75 7.25H6.5Z" fillRule="evenodd"/><path clipRule="evenodd" d="M6.5 10.25C6.91421 10.25 7.25 10.5858 7.25 11C7.25 11.4142 6.91421 11.75 6.5 11.75H4.75C4.33579 11.75 4 11.4142 4 11C4 10.5858 4.33579 10.25 4.75 10.25H6.5Z" fillRule="evenodd"/></svg>
}

/** Phone command rows use the compact issue-action glyphs rather than the desktop Lucide set. */
function PhoneCommandGlyph({ id, fallback }: { id: string; fallback: React.ReactNode }) {
  const glyphs: Record<string, string[]> = {
    'ctx-assignee': [
      'M12.2616 10.1807C12.5759 9.91122 13.0496 9.94763 13.3192 10.2617L14.8192 12.0117C15.0599 12.2926 15.0599 12.7074 14.8192 12.9883L13.3192 14.7383C13.0496 15.0523 12.5759 15.0888 12.2616 14.8193C11.9476 14.5498 11.9114 14.0761 12.1805 13.7617L12.619 13.25H9.81821C9.31854 13.2504 8.86663 13.5485 8.66978 14.0078L8.43931 14.5459C8.27587 14.926 7.83438 15.1024 7.45396 14.9395C7.0739 14.7761 6.89769 14.3345 7.0604 13.9541L7.29087 13.417C7.72409 12.4061 8.7185 11.7504 9.81821 11.75H12.619L12.1805 11.2383C11.9114 10.924 11.9476 10.4502 12.2616 10.1807Z',
      'M8.57603 8C9.16635 8.00005 9.73859 8.10941 10.2684 8.31152C10.6549 8.45932 10.8493 8.89258 10.702 9.2793C10.5544 9.6662 10.1202 9.86031 9.73325 9.71289C9.37143 9.57489 8.98057 9.50005 8.57603 9.5H7.42563C6.0104 9.50003 4.75811 10.4161 4.32896 11.7646L3.46567 14.4775C3.34008 14.8721 2.91789 15.0902 2.52329 14.9648C2.12863 14.8392 1.9104 14.4171 2.03599 14.0225L2.89927 11.3096C3.52653 9.3386 5.35724 8.00003 7.42563 8H8.57603Z',
      'M7.99985 1C9.51864 1 10.7499 2.23122 10.7499 3.75C10.7499 5.26878 9.51864 6.5 7.99985 6.5C6.4812 6.49985 5.24985 5.26869 5.24985 3.75C5.24985 2.23131 6.4812 1.00015 7.99985 1Z',
    ],
    'ctx-assign-me': [
      'M13.5604 10.4541C13.7236 10.0738 14.1652 9.89774 14.5457 10.0605C14.9262 10.2238 15.1023 10.6653 14.9393 11.0459L14.7088 11.583C14.2755 12.594 13.2814 13.2498 12.1815 13.25H9.38068L9.81916 13.7617C10.0887 14.0762 10.0524 14.5497 9.7381 14.8193C9.42366 15.0887 8.95003 15.0526 8.68048 14.7383L7.18048 12.9883C6.93986 12.7075 6.93989 12.2926 7.18048 12.0117L8.68048 10.2617C8.95004 9.94741 9.42366 9.91125 9.7381 10.1807C10.0524 10.4503 10.0887 10.9238 9.81916 11.2383L9.38068 11.75H12.1815C12.6813 11.7498 13.133 11.4517 13.3299 10.9922L13.5604 10.4541Z',
      'M7.42462 8C7.83884 8 8.17462 8.33579 8.17462 8.75C8.17462 9.16421 7.83884 9.5 7.42462 9.5C6.00944 9.50011 4.75705 10.4161 4.32795 11.7646L3.46466 14.4775C3.33902 14.8722 2.91695 15.0904 2.52228 14.9648C2.12775 14.8392 1.90945 14.4171 2.03498 14.0225L2.89826 11.3096C3.52547 9.33861 5.35628 8.00011 7.42462 8Z',
      'M7.99982 1C9.5186 1 10.7498 2.23122 10.7498 3.75C10.7498 5.26878 9.5186 6.5 7.99982 6.5C6.48119 6.49982 5.24982 5.26867 5.24982 3.75C5.24982 2.23133 6.48119 2.00018 7.99982 1Z',
    ],
    'ctx-status': ['M13.5 8C13.5 4.96243 11.0376 2.5 8 2.5C4.96243 2.5 2.5 4.96243 2.5 8C2.5 11.0376 4.96243 13.5 8 13.5C11.0376 13.5 13.5 11.0376 13.5 8ZM15 8C15 11.866 11.866 15 8 15C4.13401 15 1 11.866 1 8C1 4.13401 4.13401 1 8 1C11.866 1 15 4.13401 15 8ZM12 8C12 10.2091 10.2091 12 8 12V4C10.2091 4 12 5.79086 12 8Z'],
    'ctx-labels': ['M12 11.5V13H5.132v-1.5H12Zm1.5-1.5V6a1.5 1.5 0 0 0-1.346-1.492L12 4.5H5.133a.5.5 0 0 0-.303.103l-.08.076-2.382 2.834a.5.5 0 0 0-.11.234l-.008.087v.331a.5.5 0 0 0 .118.321l2.382 2.835a.5.5 0 0 0 .383.179V13l-.22-.012a2 2 0 0 1-1.16-.54l-.15-.16L1.218 9.45a2 2 0 0 1-.46-1.11L.75 8.165v-.331a2 2 0 0 1 .363-1.147l.106-.14 2.383-2.834a2 2 0 0 1 1.312-.701L5.134 3H12a3 3 0 0 1 3 3v4a3 3 0 0 1-3.002 3v-1.5c.778 0 1.417-.59 1.494-1.347L13.5 10Z','M5.5 8a1 1 0 1 1 2 0 1 1 0 0 1-2 0Z'],
    'ctx-team': ['m11.6 9 .072.005.016.001a.6.6 0 0 1 .313.148l2.887 2.592a.4.4 0 0 1 0 .595l-2.887 2.593A.6.6 0 0 1 11 14.487v-1.114c-4.667-.603-7-1.06-7-1.371 0-.018.01-.036.027-.055a.451.451 0 0 1 .113-.078l.039-.019a2.279 2.279 0 0 1 .31-.115 6.335 6.335 0 0 1 .265-.078l.072-.019.054-.014a17.874 17.874 0 0 1 1.072-.239 40.991 40.991 0 0 1 .69-.13l.043-.008a56.84 56.84 0 0 1 1.51-.254l.432-.067.065-.01.04-.006a82.873 82.873 0 0 1 2.27-.312L11 9.6a.6.6 0 0 1 .6-.6ZM6.782 7.645l.195.114.719.448c.611.38 1.037.984 1.2 1.668-.658.097-1.327.203-1.94.309l-.45.079-.583.109c-.463.09-.866.177-1.2.26l-.193.05-.326.094c-.15.047-.28.094-.392.143-.476.207-.765.419-.807.944L3 12 2.5 12A1.5 1.5 0 0 1 1 10.5c0-.87.42-1.684 1.119-2.19l.154-.103.72-.448a3.768 3.768 0 0 1 3.789-.114Zm6-1 .195.114.719.448A2.702 2.702 0 0 1 14.969 9.5c0 .285-.08.552-.218.78l-2.082-1.87a1.6 1.6 0 0 0-2.662 1.044L10 9.6v.119l-.031.004A3 3 0 0 0 8.76 7.724l-.605-.439.118-.078.72-.448a3.768 3.768 0 0 1 3.789-.114ZM4.969 2a2 2 0 1 1 0 4 2 2 0 0 1 0-4Z'],
    'ctx-due-date': ['M15 5C15 2.79086 13.2091 1 11 1H5C2.79086 1 1 2.79086 1 5V11C1 13.2091 2.79086 15 5 15H6.25C6.66421 15 7 14.6642 7 14.25C7 13.8358 6.66421 13.5 6.25 13.5H5C3.61929 13.5 2.5 12.3807 2.5 11V6H13.5V6.25C13.5 6.66421 13.8358 7 14.25 7C14.6642 7 15 6.66421 15 6.25V5ZM11.5001 8C11.9143 8 12.2501 8.33579 12.2501 8.75V10.75L14.2501 10.75C14.6643 10.75 15.0001 11.0858 15.0001 11.5C15.0001 11.9142 14.6643 12.25 14.2501 12.25L12.2501 12.25V14.25C12.2501 14.6642 11.9143 15 11.5001 15C11.0859 15 10.7501 14.6642 10.7501 14.25V12.25H8.75C8.33579 12.25 8 11.9142 8 11.5C8 11.0858 8.33579 10.75 8.75 10.75L10.7501 10.75V8.75C10.7501 8.33579 11.0859 8 11.5001 8Z'],
  }
  const paths = glyphs[id]
  if (!paths) return fallback
  return <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">{paths.map(path => <path key={path} d={path}/>)}</svg>
}

function ResourceIcon({ result }: { result: SearchResult }) {
  if (result.type === 'issue') return <span className="command-item-icon"><SquareDot/></span>
  if (result.type === 'project') return <span className="command-item-icon"><FolderKanban/></span>
  if (result.type === 'initiative') return <span className="command-item-icon"><Lightbulb/></span>
  if (result.type === 'member') return <span className="command-item-icon"><UserRound/></span>
  if (result.type === 'customer') return <span className="command-item-icon"><CustomerCommandIcon/></span>
  if (result.type === 'release') return <span className="command-item-icon"><ReleasesIcon/></span>
  if (result.type === 'view') return <span className="command-item-icon"><Layers3/></span>
  if (result.type === 'document') return <span className="command-item-icon"><DocumentGlyph color={result.color} icon={result.icon}/></span>
  return <span className="command-item-icon"><FileText/></span>
}
