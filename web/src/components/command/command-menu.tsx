import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Command } from 'cmdk'
import {
  Clipboard,
  FileText,
  FolderKanban,
  GitPullRequest,
  Inbox,
  Layers3,
  Lightbulb,
  Plus,
  Search,
  SquareDot,
  UserRound,
} from 'lucide-react'

import { Check, ChevronRight, Minus } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { PulseIcon } from '@/components/pulse/pulse-icon'
import { ReleasesIcon } from '@/components/releases/release-icons'
import { searchWorkspace } from '@/lib/api'
import { ActionRegistry } from '@/lib/action-registry'
import { sortActionGroups } from '@/lib/action-groups'
import { useSelectedModels } from '@/lib/selected-models-store'
import { useAllowedActionGroups } from '@/hooks/use-action-groups-for-selection'
import { useI18n } from '@/i18n/i18n'
import type { AgentSession, BootstrapData, Customer, SearchResult } from '@/types/flow'
import { agentSessionUnread, formatAgentHistoryTime, groupAgentHistory } from '@/components/agent/agent-read-state'
import { CustomerLogo } from '@/components/customer/customer-logo'
import { useCommandContext } from './command-context'
import { useDisplayCommands } from './display-commands'
import { documentCommands, type DocumentCommandHost } from './document-commands'
import { GLOBAL_DOCUMENT_PAGES } from './document-command-pages'
import { contextChip, useContextCommands, type CommandPage, type ContextCommandHandlers, type PageOption } from './context-commands'
import './command-menu.css'
import { AgentCursorGlyph, AgentHistoryGlyph } from '@/components/ui/agent-glyph'

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
  onCreateIssueTemplate,
  onCreateProject,
  onCreateView,
  onCreateInitiative,
  onSearchWorkspace,
  onNavigateInbox,
  onNavigateMyIssues,
  onNavigateProjects,
  onNavigateInitiatives,
  onNavigateViews,
  onNavigateMembers,
  onNavigateCustomers,
  onGoToCustomers,
  onOpenCustomer,
  onNavigateAgent,
  onAskAgent,
  agentSessions,
  onOpenAgentSession,
  onNavigateReviews,
  onNavigatePulse,
  pulseToggle,
  onOpenResult,
  data,
  initialCustomerPicker = false,
  initialDocumentPicker = false,
  documentHost,
  ...contextHandlers
}: {
  open: boolean
  onOpenChange: (value: boolean) => void
  onCreateIssue: () => void
  onCreateIssueTemplate: () => void
  onCreateProject: () => void
  onCreateView: () => void
  onCreateInitiative: () => void
  onSearchWorkspace: () => void
  onNavigateInbox: () => void
  onNavigateMyIssues: () => void
  onNavigateProjects: () => void
  onNavigateInitiatives: () => void
  onNavigateViews: () => void
  onNavigateMembers: () => void
  onNavigateCustomers: () => void
  /** "Go to customers" (G then Q); omitted when customer requests are off or for guests, which also hides the other customer commands. */
  onGoToCustomers?: () => void
  /** "Open customer…" (O then Q) picks a customer from a nested page. */
  onOpenCustomer?: (customer: Customer) => void
  onNavigateAgent: () => void
  /** "Ask Flow" (⌘J): open the floating agent chat; omitted where the shortcut does nothing. */
  onAskAgent?: () => void
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
  /** Reload and navigation for document writes; defaults to router navigation without a reload. */
  documentHost?: DocumentCommandHost
} & ContextCommandHandlers) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [pages, setPages] = useState<CommandPage[]>(() => initialDocumentPicker ? [{ id: 'documentOpen', label: 'Open document…' }] : [])
  const page = pages.at(-1)
  const [results, setResults] = useState<SearchResult[]>([])
  const [loading, setLoading] = useState(false)
  // "Open customer…" lists the workspace's customers in place of the root commands.
  const [customerPicker, setCustomerPicker] = useState(Boolean(initialCustomerPicker))
  // "Open past agent chat…" lists the viewer's chats in place of the root commands (Linear's searchable chat list).
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
  const contextCommands = {
    heading: context?.kind === 'document' ? 'Document' : issueCommands.heading,
    actions: context?.kind === 'document' ? documents.actions : issueCommands.actions,
    options: documents.options.length ? documents.options : issueCommands.options,
  }
  const openPage = (next: CommandPage) => { setPages(current => [...current, next]); setQuery('') }
  const back = () => { setPages(current => current.slice(0, -1)); setQuery('') }
  const actions: CommandAction[] = [
    { id: 'create-issue', group: 'Issues', label: 'Create new issue...', icon: <Plus/>, shortcut: ['C'], keywords: 'new ticket task', run: closeAnd(onCreateIssue) },
    { id: 'create-project', group: 'Projects', label: 'Create new project...', icon: <FolderKanban/>, shortcut: ['N', 'then', 'P'], run: closeAnd(onCreateProject) },
    { id: 'create-view', group: 'Views', label: 'Create view...', icon: <Layers3/>, run: closeAnd(onCreateView) },
    { id: 'create-initiative', group: 'Initiatives', label: 'Create new initiative', icon: <Lightbulb/>, shortcut: ['N', 'then', 'I'], run: closeAnd(onCreateInitiative) },
    ...(onGoToCustomers ? [{ id: 'create-customer', group: 'Customers', label: 'Create new customer…', icon: <CustomerCommandIcon/>, keywords: 'add create', run: closeAnd(onNavigateCustomers) }] : []),
    ...documents.global.map(action => ({ id: action.id, group: 'Documents', label: action.label, icon: action.icon, shortcut: action.shortcut, keywords: action.keywords, run: () => { if (action.page) openPage(action.page); else void action.run?.() } })),
    { id: 'search-workspace', group: 'Filter', label: 'Search workspace...', icon: <Search/>, run: closeAnd(onSearchWorkspace) },
    { id: 'issue-template', group: 'Templates', label: 'Create new issue template...', icon: <FileText/>, run: closeAnd(onCreateIssueTemplate) },
    { id: 'go-inbox', group: 'Navigation', label: 'Go to Inbox', icon: <Inbox/>, shortcut: ['G', 'then', 'I'], run: closeAnd(onNavigateInbox) },
    { id: 'go-my-issues', group: 'Navigation', label: 'Go to My issues', icon: <SquareDot/>, shortcut: ['G', 'then', 'M'], run: closeAnd(onNavigateMyIssues) },
    { id: 'go-projects', group: 'Navigation', label: 'Go to Projects', icon: <FolderKanban/>, run: closeAnd(onNavigateProjects) },
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
  // The context vanished (selection cleared, issue closed): nested pages no longer apply.
  useEffect(() => { if (!context) setPages(current => current.filter(item => GLOBAL_DOCUMENT_PAGES.has(item.id))) }, [context])
  const onInputKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Backspace' && !query && customerPicker) { event.preventDefault(); setCustomerPicker(false); return }
    if (event.key === 'Backspace' && !query && pastChats) { event.preventDefault(); setPastChats(false); return }
    if (event.key === 'Backspace' && !query && pages.length) { event.preventDefault(); back(); return }
    if (event.key === 'Tab' && !page) { event.preventDefault(); closeAnd(onNavigateAgent)() }
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
  const searchResults = !loading && query && results.length > 0 && <Command.Group heading="Search results">
    {results.map(result => <Command.Item key={`${result.type}-${result.id}`} value={`${result.identifier ?? ''} ${result.title} ${result.subtitle ?? ''}`} onSelect={closeAnd(() => onOpenResult(result))}>
      <ResourceIcon result={result}/>
      <span className="command-result-copy">{result.identifier && <small>{result.identifier}</small>}<span>{result.title}</span></span>
      <small>{result.type}</small>
    </Command.Item>)}
  </Command.Group>
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="command-dialog" overlayClassName="command-overlay" onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { if (pastChats) { event.preventDefault(); setPastChats(false); setQuery('') } else if (customerPicker && !initialCustomerPicker) { event.preventDefault(); setCustomerPicker(false); setQuery('') } else if (pages.length) { event.preventDefault(); back() } }}>
      <DialogTitle className="sr-only">Command menu</DialogTitle>
      <Command shouldFilter loop>
        {(chip || page) && <div className="command-context" aria-label={t('Command context')}>
          {chip && <span className="command-context-chip" data-i18n-ignore={chip.entity || undefined} title={chip.label}>{chip.label}</span>}
          {pages.map((item, index) => <span key={`${item.id}-${index}`} className="command-context-page"><ChevronRight aria-hidden="true" size={12}/><span>{t(item.label)}</span></span>)}
        </div>}
        <div className="command-input">
          <Command.Input aria-label="Command menu" placeholder={pastChats ? t('Open past agent chat…') : customerPicker ? t('Open customer…') : page ? t(page.label) : t('Type a command or search...')} autoFocus value={query} onValueChange={setQuery} onKeyDown={onInputKeyDown}/>
          {!page && !customerPicker && !pastChats && <button type="button" onClick={closeAnd(onNavigateAgent)}><span>Ask Flow</span><kbd>Tab</kbd></button>}
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
            {group.options.map(option => <Command.Item key={option.id || 'none'} value={`${option.label} ${option.keywords ?? ''}`} forceMount={option.forceMount} onSelect={() => selectOption(option)}>
              {option.checked !== undefined && <span className="command-checkbox" data-checked={option.checked === true ? 'true' : option.checked === 'mixed' ? 'mixed' : 'false'} aria-hidden="true">{option.checked === true ? <Check size={11}/> : option.checked === 'mixed' ? <Minus size={11}/> : null}</span>}
              {option.icon && <span className="command-item-icon">{option.icon}</span>}
              <span className="command-option-label" data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>
              {option.detail && <small className="command-option-detail" data-i18n-ignore>{option.detail}</small>}
              {option.current && <span className="command-option-current" aria-label={t('Current')}><Check size={14}/></span>}
              {option.page && <ChevronRight className="command-option-current" size={14}/>}
            </Command.Item>)}
          </Command.Group>)}
          <Command.Empty>{t('No results found.')}</Command.Empty>
        </Command.List> : <Command.List>
          {loading && <div className="command-loading">Searching...</div>}
          {contextCommands.actions.length > 0 && <Command.Group heading={t(contextCommands.heading)}>
            {contextCommands.actions.map(action => <Command.Item key={action.id} value={`${action.label} ${action.keywords ?? ''}`} onSelect={() => void registry.execute(action.id, { source: 'command-menu' })}>
              <span className="command-item-icon">{action.icon}</span>
              <span>{t(action.label)}</span>
              {action.shortcut && <span className="command-shortcut">{action.shortcut.map((part, index) => <kbd key={index}>{part}</kbd>)}</span>}
              {action.checked && <span className="command-option-current" aria-label={t('Current')}><Check size={14}/></span>}
            </Command.Item>)}
          </Command.Group>}
          {searchResults}
          {groups.map(group => <Command.Group key={group} heading={group}>
            {actions.filter(action => action.group === group).map(action => <Command.Item key={action.id} value={`${action.label} ${action.keywords ?? ''}`} onSelect={() => void registry.execute(action.id, { source: 'command-menu' })}>
              <span className="command-item-icon">{action.icon}</span>
              {action.path ? <span className="command-path">{action.path.map((part, index) => <span key={index}>{index > 0 && <ChevronRight aria-hidden="true" size={12}/>}{t(part)}</span>)}</span> : <span>{action.label}</span>}
              {action.shortcut && <span className="command-shortcut">{action.shortcut.map((part, index) => part === 'then' ? <small key={index}>then</small> : <kbd key={index}>{part}</kbd>)}</span>}
            </Command.Item>)}
          </Command.Group>)}
          <Command.Empty>{loading ? 'Searching...' : 'No results found.'}</Command.Empty>
        </Command.List>}
      </Command>
    </DialogContent>
  </Dialog>
}

/** Linear's customer glyph (a sheet with a person), used by the customer commands. */
function CustomerCommandIcon() {
  return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path clipRule="evenodd" d="M11.0247 12.3333C13.6728 12.3334 14.6225 13.529 14.9606 14.319C15.112 14.6739 14.806 15 14.4046 15H7.59537C7.18784 14.9997 6.8816 14.6641 7.04464 14.3073C7.40663 13.5172 8.38955 12.3333 11.0247 12.3333Z" fillRule="evenodd"/><path clipRule="evenodd" d="M11 7C12.1045 7 12.9998 7.89543 12.9998 9C12.9998 10.1046 12.1045 11 11 11C9.89553 11 9.00018 10.1046 9.00018 9C9.00018 7.89543 9.89553 7 11 7Z" fillRule="evenodd"/><path clipRule="evenodd" d="M10 4.25V3.75C10 3.05964 9.44036 2.5 8.75 2.5H3.75C3.05964 2.5 2.5 3.05964 2.5 3.75V13.25C2.5 13.3881 2.61193 13.5 2.75 13.5H4.25C4.66421 13.5 5 13.8358 5 14.25C5 14.6642 4.66421 15 4.25 15H2.75C1.7835 15 1 14.2165 1 13.25V3.75C1 2.23122 2.23122 1 3.75 1H8.75C10.2688 1 11.5 2.23122 11.5 3.75V4.25C11.5 4.66421 11.1642 5 10.75 5C10.3358 5 10 4.66421 10 4.25Z" fillRule="evenodd"/><path clipRule="evenodd" d="M7.75 4.25C8.16421 4.25 8.5 4.58579 8.5 5C8.5 5.41421 8.16421 5.75 7.75 5.75H4.75C4.33579 5.75 4 5.41421 4 5C4 4.58579 4.33579 4.25 4.75 4.25H7.75Z" fillRule="evenodd"/><path clipRule="evenodd" d="M6.5 7.25C6.91421 7.25 7.25 7.58579 7.25 8C7.25 8.41421 6.91421 8.75 6.5 8.75H4.75C4.33579 8.75 4 8.41421 4 8C4 7.58579 4.33579 7.25 4.75 7.25H6.5Z" fillRule="evenodd"/><path clipRule="evenodd" d="M6.5 10.25C6.91421 10.25 7.25 10.5858 7.25 11C7.25 11.4142 6.91421 11.75 6.5 11.75H4.75C4.33579 11.75 4 11.4142 4 11C4 10.5858 4.33579 10.25 4.75 10.25H6.5Z" fillRule="evenodd"/></svg>
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
