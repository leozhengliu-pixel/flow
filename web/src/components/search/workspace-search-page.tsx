import { useEffect, useMemo, useRef, useState } from 'react'
import { Building2, FileText, FolderKanban, Layers3, Lightbulb, Search, UserRound, X } from 'lucide-react'

import { ReleasesIcon } from '@/components/releases/release-icons'

import { clearSearchHistory, searchWorkspace, semanticSearch } from '@/lib/api'
import type { SearchHistoryEntry, SearchResourceType, SearchResponse, SearchResult, SemanticSearchFacet } from '@/types/flow'

import './workspace-search-page.css'
import { DisplayIcon, FilterIcon } from '@/components/ui/view-action-icons'

type SearchTab = 'all' | SearchResourceType

const tabs: Array<{ id: SearchTab; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'issue', label: 'Issues' },
  { id: 'project', label: 'Projects' },
  { id: 'initiative', label: 'Initiatives' },
  { id: 'document', label: 'Documents' },
  { id: 'customer', label: 'Customers' },
  { id: 'release', label: 'Releases' },
  { id: 'view', label: 'Views' },
  { id: 'member', label: 'Members' },
]

export function WorkspaceSearchPage({ onOpenSidebar, onOpenResult }: {
  onOpenSidebar?: () => void
  onOpenResult: (result: SearchResult) => void
}) {
  const [draft, setDraft] = useState('')
  const [query, setQuery] = useState('')
  const [tab, setTab] = useState<SearchTab>('all')
  const [response, setResponse] = useState<SearchResponse>({ results: [], history: [], recent: [] })
  const [facets,setFacets]=useState<Record<string,SemanticSearchFacet[]>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>()
  const [activeIndex, setActiveIndex] = useState(0)
  const [retry, setRetry] = useState(0)
  const requestRef = useRef(0)

  const types = useMemo<SearchResourceType[]>(() => tab === 'all' ? [] : [tab], [tab])
  useEffect(() => {
    const request = ++requestRef.current
    setLoading(true)
    setError(undefined)
    const operation=query ? semanticSearch(query,types).then(result=>({results:result.results,history:[],recent:[],facets:result.facets})) : searchWorkspace(query, types).then(result=>({...result,facets:{}}))
    operation
      .then(result => { if (request === requestRef.current) { setResponse(result);setFacets(result.facets); setActiveIndex(0) } })
      .catch(reason => { if (request === requestRef.current) setError(reason instanceof Error ? reason.message : 'Search failed') })
      .finally(() => { if (request === requestRef.current) setLoading(false) })
  }, [query, retry, types])

  const runSearch = (value = draft) => {
    const next = value.trim()
    setDraft(next)
    setQuery(next)
  }
  const choose = (result: SearchResult) => onOpenResult(result)

  return <main className="main-panel workspace-search-page">
    <header className="workspace-search-header">
      <button className="workspace-search-mobile-menu" type="button" aria-label="Open sidebar" onClick={onOpenSidebar}>☰</button>
      <Search size={15}/>
      <input
        autoFocus
        aria-label="Search workspace"
        placeholder="Search workspace..."
        value={draft}
        onChange={event => setDraft(event.target.value)}
        onKeyDown={event => {
          if (event.key === 'Enter') {
            event.preventDefault()
            if (query === draft.trim() && response.results[activeIndex]) choose(response.results[activeIndex])
            else runSearch()
          }
          if (event.key === 'ArrowDown' && response.results.length) { event.preventDefault(); setActiveIndex(index => Math.min(index + 1, response.results.length - 1)) }
          if (event.key === 'ArrowUp' && response.results.length) { event.preventDefault(); setActiveIndex(index => Math.max(index - 1, 0)) }
          if (event.key === 'Escape' && draft) { event.preventDefault(); setDraft(''); setQuery('') }
        }}
      />
      {draft && <button className="workspace-search-clear" type="button" aria-label="Clear search" onClick={() => { setDraft(''); setQuery('') }}><X size={14}/></button>}
    </header>
    <div className="workspace-search-toolbar">
      <nav aria-label="Search resource type">
        {tabs.map(item => <button key={item.id} type="button" className={`ui-pill ${item.id === tab ? 'active' : ''}`} onClick={() => setTab(item.id)}>{item.label}</button>)}
      </nav>
      <div className="workspace-search-tools">
        <button className="ui-pill" type="button" aria-label="Add filter" title="Add filter"><FilterIcon/></button>
        <button className="ui-pill" type="button" aria-label="Display options" title="Display options"><DisplayIcon/></button>
      </div>
    </div>
    <section className="workspace-search-content" aria-live="polite">
      {query&&Object.values(facets).some(values=>values.length>0)&&<div className="workspace-search-facets" aria-label="Search facets">{Object.entries(facets).flatMap(([key,values])=>values.slice(0,3).map(value=><span key={`${key}-${value.value}`}>{value.label}<small>{value.count}</small></span>))}</div>}
      {!query && !loading && <RecentSearches history={response.history} onSearch={runSearch} onClear={async () => { await clearSearchHistory(); setResponse(current => ({ ...current, history: [] })) }}/>} 
      {loading && <SearchLoading/>}
      {error && <div className="workspace-search-state"><strong>Search unavailable</strong><span>{error}</span><button type="button" onClick={() => setRetry(value => value + 1)}>Try again</button></div>}
      {!loading && !error && query && response.results.length === 0 && <div className="workspace-search-state"><Search size={20}/><strong>No results found</strong><span>Try a different search term.</span></div>}
      {!loading && !error && response.results.length > 0 && <div className="workspace-search-results">
        <h2>{query ? 'Search results' : 'Recently viewed'}</h2>
        {response.results.map((result, index) => <button
          type="button"
          key={`${result.type}-${result.id}`}
          className={activeIndex === index ? 'active' : ''}
          onMouseEnter={() => setActiveIndex(index)}
          onClick={() => choose(result)}
        >
          <SearchResultIcon result={result}/>
          <span className="workspace-search-result-copy">
            <strong>{result.identifier && <small>{result.identifier}</small>}{result.title}</strong>
            {(result.subtitle || result.email) && <span>{result.subtitle || result.email}</span>}
          </span>
          <time>{relativeTime(result.updatedAt)}</time>
        </button>)}
      </div>}
    </section>
  </main>
}

function RecentSearches({ history, onSearch, onClear }: { history: SearchHistoryEntry[]; onSearch: (query: string) => void; onClear: () => void }) {
  if (!history.length) return null
  return <div className="workspace-recent-searches">
    <h2>Recent searches</h2>
    {history.map(item => <button type="button" key={item.query} onClick={() => onSearch(item.query)}><Search size={13}/><span>{item.query}</span></button>)}
    <button className="workspace-clear-history" type="button" onClick={onClear}><X size={13}/><span>Clear History</span></button>
  </div>
}

function SearchResultIcon({ result }: { result: SearchResult }) {
  const style = result.color ? { color: result.color } : undefined
  if (result.type === 'issue') return <span className="workspace-search-result-icon issue" style={style}><span/></span>
  if (result.type === 'project') return <span className="workspace-search-result-icon" style={style}><FolderKanban/></span>
  if (result.type === 'initiative') return <span className="workspace-search-result-icon" style={style}><Lightbulb/></span>
  if (result.type === 'member') return <span className="workspace-search-result-icon"><UserRound/></span>
  if (result.type === 'customer') return <span className="workspace-search-result-icon"><Building2/></span>
  if (result.type === 'release') return <span className="workspace-search-result-icon"><ReleasesIcon/></span>
  if (result.type === 'view') return <span className="workspace-search-result-icon" style={style}><Layers3/></span>
  return <span className="workspace-search-result-icon"><FileText/></span>
}

function SearchLoading() {
  return <div className="workspace-search-loading">{Array.from({ length: 5 }, (_, index) => <span key={index}/>)}</div>
}

function relativeTime(value?: string) {
  if (!value) return ''
  const elapsed = Date.now() - new Date(value).getTime()
  const minutes = Math.floor(elapsed / 60_000)
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(new Date(value))
}
