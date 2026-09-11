import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Building2, Layers3, Lightbulb, Menu, Search, UserRound, X } from 'lucide-react'

import { DocumentGlyph } from '@/components/documents/document-icon'
import { ReleasesIcon } from '@/components/releases/release-icons'

import { clearSearchHistory, searchWorkspace, semanticSearch } from '@/lib/api'
import type { SearchHistoryEntry, SearchResourceType, SearchResponse, SearchResult, User } from '@/types/flow'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { useI18n } from '@/i18n/i18n'
import { readSearchState, writeSearchState, searchFilterAST, type SearchTab, type SearchPageState } from './search-state'
import { SearchMenus, SearchFilterChips } from './search-menus'

import './workspace-search-page.css'

const tabs: Array<{ id: SearchTab; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'issue', label: 'Issues' },
  { id: 'project', label: 'Projects' },
  { id: 'initiative', label: 'Initiatives' },
  { id: 'document', label: 'Documents' },
]
export function WorkspaceSearchPage({ onOpenSidebar, onOpenResult, getResultHref, users }: {
  onOpenSidebar?: () => void
  onOpenResult: (result: SearchResult) => void
  getResultHref: (result: SearchResult) => string
  users?: User[]
}) {
  const {t,locale}=useI18n()
  const [params,setParams]=useSearchParams()
  const state=useMemo(()=>readSearchState(params),[params])
  const {query,tab}=state
  const [draft, setDraft] = useState(query)
  const [response, setResponse] = useState<SearchResponse>({ results: [], history: [], recent: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>()
  const [activeIndex, setActiveIndex] = useState(0)
  const [retry, setRetry] = useState(0)
  const requestRef = useRef(0)
  const [completedRequest,setCompletedRequest]=useState('')
  const [nextCursor,setNextCursor]=useState('')
  const [loadingMore,setLoadingMore]=useState(false)
  const [moreError,setMoreError]=useState<string>()
  const moreController=useRef<AbortController | null>(null)
  const resultListRef=useRef<HTMLDivElement>(null)
  const options=useMemo(()=>({sort:state.order,includeArchived:state.includeArchived,filter:searchFilterAST(state)}),[state])
  const requestKey=JSON.stringify([query,tab,options])
  const currentResults=completedRequest===requestKey&&!loading&&!error
  const inputPending=draft.trim()!==query
  const updateState=(next:SearchPageState)=>setParams(writeSearchState(next))

  const types = useMemo<SearchResourceType[]>(() => tab === 'all' ? [] : [tab], [tab])
  const results = response.results
  useEffect(()=>setDraft(query),[query])
  useEffect(()=>{ document.title=query?`${t('Search')}: ${query}`:t('Search') },[query,t])
  useEffect(()=>{resultListRef.current?.querySelector<HTMLElement>(`[data-result-index="${activeIndex}"]`)?.scrollIntoView({block:'nearest'})},[activeIndex])
  useEffect(() => {
    const request = ++requestRef.current
    const controller=new AbortController()
    moreController.current?.abort()
    setLoadingMore(false)
    setMoreError(undefined)
    setNextCursor('')
    setLoading(true)
    setError(undefined)
    const operation=query ? semanticSearch(query,types,options,controller.signal).then(result=>({results:result.results,history:[],recent:[],nextCursor:result.hasMore?result.nextCursor:''})) : searchWorkspace(query, types,options,controller.signal).then(result=>({...result,nextCursor:''}))
    operation
      .then(result => { if (!controller.signal.aborted&&request === requestRef.current) { setResponse(result);setNextCursor(result.nextCursor);setCompletedRequest(requestKey); setActiveIndex(0) } })
      .catch(reason => { if (!controller.signal.aborted&&request === requestRef.current) setError(reason instanceof Error ? reason.message : 'Search failed') })
      .finally(() => { if (!controller.signal.aborted&&request === requestRef.current) setLoading(false) })
    return ()=>{controller.abort();moreController.current?.abort()}
  }, [query, retry, types, options,requestKey])

  const runSearch = (value = draft) => {
    const next = value.trim()
    setDraft(next)
    updateState({...state,query:next})
  }
  const choose = (result: SearchResult) => onOpenResult(result)
  const loadMore=async()=>{
    if(!currentResults||!nextCursor||loadingMore)return
    const request=requestRef.current
    const controller=new AbortController()
    moreController.current=controller
    setLoadingMore(true)
    setMoreError(undefined)
    try{
      const page=await semanticSearch(query,types,{...options,cursor:nextCursor},controller.signal)
      if(controller.signal.aborted||request!==requestRef.current)return
      setResponse(previous=>{const keys=new Set(previous.results.map(item=>`${item.type}:${item.id}`));return {...previous,results:[...previous.results,...page.results.filter(item=>!keys.has(`${item.type}:${item.id}`))]}})
      setNextCursor(page.hasMore?page.nextCursor:'')
    }catch(reason){if(!controller.signal.aborted)setMoreError(reason instanceof Error?reason.message:t('Search failed'))}
    finally{if(!controller.signal.aborted)setLoadingMore(false)}
  }

  return <main className="main-panel workspace-search-page">
    <header className="workspace-search-header">
      <button className="workspace-search-mobile-menu" type="button" aria-label={t('Open sidebar')} data-sidebar-trigger onClick={onOpenSidebar}><Menu size={15}/></button>
      <Search size={15}/>
      <input
        autoFocus
        aria-label={t('Search issues, projects, and documents…')}
        placeholder={t('Search issues, projects, and documents…')}
        aria-busy={loading}
        value={draft}
        onChange={event => setDraft(event.target.value)}
        onKeyDown={event => {
          if (event.key === 'Enter') {
            event.preventDefault()
            if (!inputPending && currentResults && results[activeIndex]) choose(results[activeIndex])
            else if(inputPending) runSearch()
          }
          if (event.key === 'ArrowDown' && currentResults && results.length) { event.preventDefault(); setActiveIndex(index => Math.min(index + 1, results.length - 1)) }
          if (event.key === 'ArrowUp' && currentResults && results.length) { event.preventDefault(); setActiveIndex(index => Math.max(index - 1, 0)) }
          if (event.key === 'Escape' && draft) { event.preventDefault(); runSearch('') }
        }}
      />
      {draft && <button className="workspace-search-clear" type="button" aria-label={t('Clear search')} onClick={() => runSearch('')}><X size={14}/></button>}
    </header>
    <div className="workspace-search-toolbar">
      <nav aria-label={t('Search resource type')}>
        {tabs.map(item => <button key={item.id} type="button" aria-pressed={item.id===tab} className={`ui-pill ${item.id === tab ? 'active' : ''}`} onClick={() => updateState({...state,tab:item.id})}>{t(item.label)}</button>)}
      </nav>
      <div className="workspace-search-tools"><SearchMenus state={state} users={users} onChange={updateState}/></div>
    </div>
    <section className="workspace-search-content" aria-live="polite">
      {state.filters.length>0&&<SearchFilterChips state={state} onChange={updateState}/>}
      {inputPending&&<div className="workspace-search-pending" role="status">{t('Press Enter to search')}</div>}
      {!query && currentResults && !inputPending && <RecentSearches history={response.history} onSearch={runSearch} onClear={async () => { try{await clearSearchHistory(); setResponse(current => ({ ...current, history: [] }))}catch(reason){setError(reason instanceof Error?reason.message:t('Could not clear search history'))} }}/>} 
      {!query && currentResults && !inputPending && !response.history.length && !results.length && <SearchEmpty/>}
      {loading && <SearchLoading/>}
      {error && <div className="workspace-search-state"><strong>{t('Search unavailable')}</strong><span>{t(error)}</span><button type="button" onClick={() => setRetry(value => value + 1)}>{t('Try again')}</button></div>}
      {currentResults && !inputPending && query && response.results.length === 0 && <div className="workspace-search-state"><Search size={20}/><strong>{t('No results found')}</strong><span>{t('Try a different search term.')}</span></div>}
      {currentResults && !inputPending && results.length > 0 && <div className="workspace-search-results" ref={resultListRef}>
        <h2>{t(query ? 'Search results' : 'Recently viewed')}</h2>
        {results.map((result, index) => <a
          href={getResultHref(result)}
          data-result-index={index}
          key={`${result.type}-${result.id}`}
          className={activeIndex === index ? 'active' : ''}
          onMouseEnter={() => setActiveIndex(index)}
          onClick={event => {if(event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;event.preventDefault();choose(result)}}
        >
          <SearchResultIcon result={result}/>
          <span className="workspace-search-result-copy" data-i18n-ignore>
            <strong>{state.showId && result.identifier && <small>{result.identifier}</small>}{result.title}</strong>
            {(result.subtitle || result.email) && <span>{result.subtitle || result.email}</span>}
          </span>
          <time>{relativeTime(result.updatedAt,locale)}</time>
        </a>)}
        {moreError&&<p role="alert">{t(moreError)}</p>}
        {nextCursor&&<button type="button" className="workspace-search-more" disabled={loadingMore} onClick={()=>void loadMore()}>{t(loadingMore?'Loading...':moreError?'Try again':'Load more')}</button>}
      </div>}
    </section>
  </main>
}

function SearchEmpty() {
  const {t}=useI18n()
  return <div className="workspace-search-empty"><SearchEmptyIllustration/><div><strong>{t('Search')}</strong><span>{t('Find issues, projects, initiatives, and documents')}</span></div></div>
}

function SearchEmptyIllustration() {
  const {t}=useI18n()
  return <svg role="img" aria-label={t('No search results illustration')} className="workspace-search-empty-illustration" fill="none" viewBox="0 0 156 72">
    <g fill="currentColor" opacity=".08">{[4,32,60,88,116,144].flatMap((x,index)=>[8,36,64].map((y,row)=><rect height={index===2&&row===1?16:10} key={`${x}-${y}`} rx="2" width={index===2&&row===1?16:10} x={x-5} y={y-5}/>))}</g>
    <circle cx="78" cy="36" fill="var(--theme-surface-2)" r="20" stroke="var(--theme-border-strong)"/>
    <circle cx="75" cy="33" r="8" stroke="var(--theme-text-secondary)" strokeWidth="2"/><path d="m81 39 7 7" stroke="var(--theme-text-secondary)" strokeLinecap="round" strokeWidth="2"/>
  </svg>
}

function RecentSearches({ history, onSearch, onClear }: { history: SearchHistoryEntry[]; onSearch: (query: string) => void; onClear: () => void }) {
  const {t}=useI18n()
  if (!history.length) return null
  return <div className="workspace-recent-searches">
    <h2>{t('Recent searches')}</h2>
    {history.map(item => <button type="button" key={item.query} onClick={() => onSearch(item.query)}><Search size={13}/><span data-i18n-ignore>{item.query}</span></button>)}
    <button className="workspace-clear-history" type="button" onClick={onClear}><X size={13}/><span>{t('Clear History')}</span></button>
  </div>
}

function SearchResultIcon({ result }: { result: SearchResult }) {
  const style = result.color ? { color: result.color } : undefined
  if (result.type === 'issue') return <span className="workspace-search-result-icon issue" style={style}>{result.state?<StatusIcon state={result.state}/>:<Search size={15}/>}</span>
  if (result.type === 'project') return <span className="workspace-search-result-icon" style={style}>{result.icon?<ViewGlyph icon={normalizeProjectIcon(result.icon)} color={result.color}/>:<ProjectIcon/>}</span>
  if (result.type === 'initiative') return <span className="workspace-search-result-icon" style={style}><Lightbulb/></span>
  if (result.type === 'member') return <span className="workspace-search-result-icon"><UserRound/></span>
  if (result.type === 'customer') return <span className="workspace-search-result-icon"><Building2/></span>
  if (result.type === 'release') return <span className="workspace-search-result-icon"><ReleasesIcon/></span>
  if (result.type === 'view') return <span className="workspace-search-result-icon" style={style}><Layers3/></span>
  return <span className="workspace-search-result-icon"><DocumentGlyph color={result.color} icon={result.icon}/></span>
}

function SearchLoading() {
  return <div className="workspace-search-loading">{Array.from({ length: 5 }, (_, index) => <span key={index}/>)}</div>
}

function relativeTime(value?: string,locale?:string) {
  if (!value) return ''
  const elapsed = Date.now() - new Date(value).getTime()
  const minutes = Math.floor(elapsed / 60_000)
  if(locale==='zh-CN') {
    const formatter=new Intl.RelativeTimeFormat(locale,{numeric:'auto'})
    if(minutes<1)return formatter.format(0,'second')
    if(minutes<60)return formatter.format(-minutes,'minute')
    if(minutes<1440)return formatter.format(-Math.floor(minutes/60),'hour')
    if(minutes<10080)return formatter.format(-Math.floor(minutes/1440),'day')
  }
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(new Date(value))
}
