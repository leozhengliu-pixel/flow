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
]
const resourceOptions: Array<{ id: SearchTab; label: string }> = [
  ...tabs,
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
  const [filterOpen, setFilterOpen] = useState(false)
  const [displayOpen, setDisplayOpen] = useState(false)
  const [ordering, setOrdering] = useState<'relevance'|'updated'>('relevance')
  const [showDetails, setShowDetails] = useState(true)
  const requestRef = useRef(0)
  const toolsRef = useRef<HTMLDivElement>(null)

  const types = useMemo<SearchResourceType[]>(() => tab === 'all' ? [] : [tab], [tab])
  const results = useMemo(() => ordering === 'updated' ? [...response.results].sort((left, right) => (right.updatedAt ?? '').localeCompare(left.updatedAt ?? '')) : response.results, [ordering, response.results])
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
  useEffect(() => {
    if (!filterOpen && !displayOpen) return
    const close = (event: PointerEvent | KeyboardEvent) => {
      if (event instanceof KeyboardEvent ? event.key === 'Escape' : !toolsRef.current?.contains(event.target as Node)) { setFilterOpen(false); setDisplayOpen(false) }
    }
    document.addEventListener('pointerdown', close)
    document.addEventListener('keydown', close)
    return () => { document.removeEventListener('pointerdown', close); document.removeEventListener('keydown', close) }
  }, [displayOpen, filterOpen])

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
        aria-label="Search issues, projects, and documents…"
        placeholder="Search issues, projects, and documents…"
        value={draft}
        onChange={event => setDraft(event.target.value)}
        onKeyDown={event => {
          if (event.key === 'Enter') {
            event.preventDefault()
            if (query === draft.trim() && results[activeIndex]) choose(results[activeIndex])
            else runSearch()
          }
          if (event.key === 'ArrowDown' && results.length) { event.preventDefault(); setActiveIndex(index => Math.min(index + 1, results.length - 1)) }
          if (event.key === 'ArrowUp' && results.length) { event.preventDefault(); setActiveIndex(index => Math.max(index - 1, 0)) }
          if (event.key === 'Escape' && draft) { event.preventDefault(); setDraft(''); setQuery('') }
        }}
      />
      {draft && <button className="workspace-search-clear" type="button" aria-label="Clear search" onClick={() => { setDraft(''); setQuery('') }}><X size={14}/></button>}
    </header>
    <div className="workspace-search-toolbar">
      <nav aria-label="Search resource type">
        {tabs.map(item => <button key={item.id} type="button" className={`ui-pill ${item.id === tab ? 'active' : ''}`} onClick={() => setTab(item.id)}>{item.label}</button>)}
      </nav>
      <div className="workspace-search-tools" ref={toolsRef}>
        <button className="ui-pill" type="button" aria-label="Add filter" aria-expanded={filterOpen} title="Add filter" onClick={() => { setFilterOpen(value => !value); setDisplayOpen(false) }}><FilterIcon/></button>
        <button className="ui-pill" type="button" aria-label="Display options" aria-expanded={displayOpen} title="Display options" onClick={() => { setDisplayOpen(value => !value); setFilterOpen(false) }}><DisplayIcon/></button>
        {filterOpen && <div aria-label="Search filters" className="workspace-search-menu is-filter" role="menu"><strong>Resource type</strong>{resourceOptions.map(item=><button aria-checked={tab===item.id} key={item.id} onClick={()=>{setTab(item.id);setFilterOpen(false)}} role="menuitemradio">{item.label}{tab===item.id&&<span>✓</span>}</button>)}</div>}
        {displayOpen && <div aria-label="Search display options" className="workspace-search-menu is-display" role="menu"><strong>Ordering</strong><button aria-checked={ordering==='relevance'} onClick={()=>setOrdering('relevance')} role="menuitemradio">Relevance{ordering==='relevance'&&<span>✓</span>}</button><button aria-checked={ordering==='updated'} onClick={()=>setOrdering('updated')} role="menuitemradio">Last updated{ordering==='updated'&&<span>✓</span>}</button><hr/><button aria-checked={showDetails} onClick={()=>setShowDetails(value=>!value)} role="menuitemcheckbox">Show details{showDetails&&<span>✓</span>}</button></div>}
      </div>
    </div>
    <section className="workspace-search-content" aria-live="polite">
      {query&&Object.values(facets).some(values=>values.length>0)&&<div className="workspace-search-facets" aria-label="Search facets">{Object.entries(facets).flatMap(([key,values])=>values.slice(0,3).map(value=><span key={`${key}-${value.value}`}>{value.label}<small>{value.count}</small></span>))}</div>}
      {!query && !loading && <RecentSearches history={response.history} onSearch={runSearch} onClear={async () => { await clearSearchHistory(); setResponse(current => ({ ...current, history: [] })) }}/>} 
      {!query && !loading && !response.history.length && !results.length && <SearchEmpty/>}
      {loading && <SearchLoading/>}
      {error && <div className="workspace-search-state"><strong>Search unavailable</strong><span>{error}</span><button type="button" onClick={() => setRetry(value => value + 1)}>Try again</button></div>}
      {!loading && !error && query && response.results.length === 0 && <div className="workspace-search-state"><Search size={20}/><strong>No results found</strong><span>Try a different search term.</span></div>}
      {!loading && !error && results.length > 0 && <div className="workspace-search-results">
        <h2>{query ? 'Search results' : 'Recently viewed'}</h2>
        {results.map((result, index) => <button
          type="button"
          key={`${result.type}-${result.id}`}
          className={activeIndex === index ? 'active' : ''}
          onMouseEnter={() => setActiveIndex(index)}
          onClick={() => choose(result)}
        >
          <SearchResultIcon result={result}/>
          <span className="workspace-search-result-copy">
            <strong>{result.identifier && <small>{result.identifier}</small>}{result.title}</strong>
            {showDetails && (result.subtitle || result.email) && <span>{result.subtitle || result.email}</span>}
          </span>
          <time>{relativeTime(result.updatedAt)}</time>
        </button>)}
      </div>}
    </section>
  </main>
}

function SearchEmpty() {
  return <div className="workspace-search-empty"><SearchEmptyIllustration/><div><strong>Search</strong><span>Find issues, projects, initiatives, and documents</span></div></div>
}

function SearchEmptyIllustration() {
  return <svg aria-label="No search results illustration" className="workspace-search-empty-illustration" fill="none" viewBox="0 0 156 72">
    <g fill="currentColor" opacity=".08">{[4,32,60,88,116,144].flatMap((x,index)=>[8,36,64].map((y,row)=><rect height={index===2&&row===1?16:10} key={`${x}-${y}`} rx="2" width={index===2&&row===1?16:10} x={x-5} y={y-5}/>))}</g>
    <circle cx="78" cy="36" fill="var(--theme-surface-2)" r="20" stroke="var(--theme-border-strong)"/>
    <circle cx="75" cy="33" r="8" stroke="var(--theme-text-secondary)" strokeWidth="2"/><path d="m81 39 7 7" stroke="var(--theme-text-secondary)" strokeLinecap="round" strokeWidth="2"/>
  </svg>
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
