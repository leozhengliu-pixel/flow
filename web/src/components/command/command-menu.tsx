import { useEffect, useState } from 'react'
import { Command } from 'cmdk'
import {
  Building2, Clipboard, FilePlus2, FileText, Filter, FolderKanban, Inbox, Layers3, Lightbulb,
  ListFilter, Plus, Search, SquareDot, UserRound, UsersRound,
} from 'lucide-react'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { ReleasesIcon } from '@/components/releases/release-icons'
import { searchWorkspace } from '@/lib/api'
import type { SearchResult } from '@/types/flow'

type CommandAction = {
  id: string
  group: string
  label: string
  icon: React.ReactNode
  shortcut?: string[]
  keywords?: string
  run: () => void
}

export function CommandMenu({
  open,
  onOpenChange,
  onCreateIssue,
  onCreateLabel,
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
  onOpenResult,
}: {
  open: boolean
  onOpenChange: (value: boolean) => void
  onCreateIssue: () => void
  onCreateLabel: () => void
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
  onOpenResult: (result: SearchResult) => void
}) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[]>([])
  const [loading, setLoading] = useState(false)
  const closeAnd = (work: () => void) => () => { onOpenChange(false); work() }
  const actions: CommandAction[] = [
    { id: 'create-issue', group: 'Issues', label: 'Create new issue...', icon: <Plus/>, shortcut: ['C'], keywords: 'new ticket task', run: closeAnd(onCreateIssue) },
    { id: 'create-issue-fullscreen', group: 'Issues', label: 'Create issue in fullscreen...', icon: <Plus/>, shortcut: ['V'], keywords: 'new ticket full screen', run: closeAnd(onCreateIssue) },
    { id: 'create-label', group: 'Issues', label: 'Create new label...', icon: <SquareDot/>, run: closeAnd(onCreateLabel) },
    { id: 'create-project', group: 'Projects', label: 'Create new project...', icon: <FolderKanban/>, shortcut: ['N', 'then', 'P'], run: closeAnd(onCreateProject) },
    { id: 'create-document', group: 'Documents', label: 'Create new document in...', icon: <FilePlus2/>, run: closeAnd(onNavigateProjects) },
    { id: 'create-view', group: 'Views', label: 'Create view...', icon: <Layers3/>, run: closeAnd(onCreateView) },
    { id: 'create-initiative', group: 'Initiatives', label: 'Create new initiative', icon: <Lightbulb/>, shortcut: ['N', 'then', 'I'], run: closeAnd(onCreateInitiative) },
    { id: 'customer-request', group: 'Customers', label: 'Add customer request...', icon: <UsersRound/>, shortcut: ['Ctrl', 'R'], run: closeAnd(onNavigateCustomers) },
    { id: 'create-customer', group: 'Customers', label: 'Create new customer...', icon: <UserRound/>, run: closeAnd(onNavigateCustomers) },
    { id: 'search-workspace', group: 'Filter', label: 'Search workspace...', icon: <Search/>, run: closeAnd(onSearchWorkspace) },
    { id: 'find-view', group: 'Filter', label: 'Find in view...', icon: <ListFilter/>, shortcut: ['⌘', 'F'], run: closeAnd(onSearchWorkspace) },
    { id: 'add-filter', group: 'Filter', label: 'Add filter...', icon: <Filter/>, shortcut: ['F', 'F'], run: closeAnd(onSearchWorkspace) },
    { id: 'issue-template', group: 'Templates', label: 'Create new issue template...', icon: <FileText/>, run: closeAnd(onCreateIssue) },
    { id: 'go-inbox', group: 'Navigation', label: 'Go to Inbox', icon: <Inbox/>, shortcut: ['G', 'then', 'I'], run: closeAnd(onNavigateInbox) },
    { id: 'go-my-issues', group: 'Navigation', label: 'Go to My issues', icon: <SquareDot/>, shortcut: ['G', 'then', 'M'], run: closeAnd(onNavigateMyIssues) },
    { id: 'go-projects', group: 'Navigation', label: 'Go to Projects', icon: <FolderKanban/>, run: closeAnd(onNavigateProjects) },
    { id: 'go-initiatives', group: 'Navigation', label: 'Go to Initiatives', icon: <Lightbulb/>, run: closeAnd(onNavigateInitiatives) },
    { id: 'go-views', group: 'Navigation', label: 'Go to Views', icon: <Layers3/>, run: closeAnd(onNavigateViews) },
    { id: 'go-members', group: 'Navigation', label: 'Go to Members', icon: <UserRound/>, run: closeAnd(onNavigateMembers) },
    { id: 'copy-url', group: 'Other', label: 'Copy current page link', icon: <Clipboard/>, run: closeAnd(() => void navigator.clipboard.writeText(window.location.href)) },
  ]

  useEffect(() => {
    if (!open) { setQuery(''); setResults([]); return }
    if (!query.trim()) { setResults([]); setLoading(false); return }
    let active = true
    setLoading(true)
    const timer = window.setTimeout(() => {
      searchWorkspace(query.trim(), [], 12)
        .then(response => { if (active) setResults(response.results) })
        .catch(() => { if (active) setResults([]) })
        .finally(() => { if (active) setLoading(false) })
    }, 140)
    return () => { active = false; window.clearTimeout(timer) }
  }, [open, query])

  const groups = [...new Set(actions.map(action => action.group))]
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="command-dialog" onOpenAutoFocus={event => event.preventDefault()}>
      <DialogTitle className="sr-only">Command menu</DialogTitle>
      <Command shouldFilter loop>
        <div className="command-input">
          <Command.Input aria-label="Command menu" placeholder="Type a command or search..." autoFocus value={query} onValueChange={setQuery}/>
          <button type="button" tabIndex={-1} onClick={() => setQuery('Ask Flow ')}><span>Ask Flow</span><kbd>Tab</kbd></button>
        </div>
        <Command.List>
          {loading && <div className="command-loading">Searching...</div>}
          {!loading && query && results.length > 0 && <Command.Group heading="Search results">
            {results.map(result => <Command.Item key={`${result.type}-${result.id}`} value={`${result.identifier ?? ''} ${result.title} ${result.subtitle ?? ''}`} onSelect={closeAnd(() => onOpenResult(result))}>
              <ResourceIcon result={result}/>
              <span className="command-result-copy">{result.identifier && <small>{result.identifier}</small>}<span>{result.title}</span></span>
              <small>{result.type}</small>
            </Command.Item>)}
          </Command.Group>}
          {groups.map(group => <Command.Group key={group} heading={group}>
            {actions.filter(action => action.group === group).map(action => <Command.Item key={action.id} value={`${action.label} ${action.keywords ?? ''}`} onSelect={action.run}>
              <span className="command-item-icon">{action.icon}</span>
              <span>{action.label}</span>
              {action.shortcut && <span className="command-shortcut">{action.shortcut.map((part, index) => part === 'then' ? <small key={index}>then</small> : <kbd key={index}>{part}</kbd>)}</span>}
            </Command.Item>)}
          </Command.Group>)}
          <Command.Empty>{loading ? 'Searching...' : 'No results found.'}</Command.Empty>
        </Command.List>
      </Command>
    </DialogContent>
  </Dialog>
}

function ResourceIcon({ result }: { result: SearchResult }) {
  if (result.type === 'issue') return <span className="command-item-icon"><SquareDot/></span>
  if (result.type === 'project') return <span className="command-item-icon"><FolderKanban/></span>
  if (result.type === 'initiative') return <span className="command-item-icon"><Lightbulb/></span>
  if (result.type === 'member') return <span className="command-item-icon"><UserRound/></span>
  if (result.type === 'customer') return <span className="command-item-icon"><Building2/></span>
  if (result.type === 'release') return <span className="command-item-icon"><ReleasesIcon/></span>
  if (result.type === 'view') return <span className="command-item-icon"><Layers3/></span>
  return <span className="command-item-icon"><FileText/></span>
}
