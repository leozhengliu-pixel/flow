import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceSearchPage } from './workspace-search-page'
import { SearchMenus } from './search-menus'
import { readSearchState, searchFilterAST, writeSearchState } from './search-state'
import { semanticSearch, searchWorkspace } from '@/lib/api'
import type { SearchResult, SemanticSearchResponse } from '@/types/flow'
import { viewer } from '@/test/fixtures'

vi.mock('@/lib/api',()=>({searchWorkspace:vi.fn(),semanticSearch:vi.fn(),clearSearchHistory:vi.fn()}))
const result:SearchResult={id:'issue-1',type:'issue',identifier:'FLOW-1',title:'General',score:1,state:{id:'state_review',name:'Review',type:'started',color:'#ffcc00',position:1}}
const semantic=(results:SearchResult[]):SemanticSearchResponse=>({results:results.map(item=>({...item,semanticScore:item.score,matchedTerms:[]})),facets:{},nextCursor:'',hasMore:false,total:results.length})
function RouterState(){const location=useLocation();const navigate=useNavigate();return <><output aria-label="Search URL">{location.search}</output><button type="button" onClick={()=>navigate(-1)}>Back</button></>}
function setup(url='/test/search',onOpenResult=vi.fn()) {
  const view=render(<I18nProvider><MemoryRouter initialEntries={[url]}><WorkspaceSearchPage onOpenResult={onOpenResult} getResultHref={item=>`/test/issue/${item.identifier}`} users={[viewer]}/><RouterState/></MemoryRouter></I18nProvider>)
  return {...view,onOpenResult}
}
beforeEach(()=>{vi.clearAllMocks();localStorage.clear();vi.mocked(searchWorkspace).mockResolvedValue({results:[result],history:[],recent:[]});vi.mocked(semanticSearch).mockResolvedValue(semantic([result]))})

describe('workspace search',()=>{
  it('treats whitespace as recents and ignores aborted in-flight search errors',async()=>{
    setup('/test/search?q=%20%20')
    await waitFor(()=>expect(searchWorkspace).toHaveBeenCalled())
    expect(semanticSearch).not.toHaveBeenCalled()
    expect(searchWorkspace).toHaveBeenLastCalledWith('',[],expect.objectContaining({limit:40}),expect.any(AbortSignal))
    const firstSignal=vi.mocked(searchWorkspace).mock.calls[0][3]!
    const abortError=Object.assign(new Error('The user aborted a request.'),{name:'AbortError'})
    vi.mocked(semanticSearch).mockRejectedValueOnce(abortError)
    const input=screen.getByRole('textbox')
    fireEvent.change(input,{target:{value:'Flow'}})
    fireEvent.keyDown(input,{key:'Enter'})
    await waitFor(()=>expect(semanticSearch).toHaveBeenCalled())
    expect(firstSignal.aborted).toBe(true)
    expect(screen.queryByText('Search unavailable')).not.toBeInTheDocument()
    expect(semanticSearch).toHaveBeenLastCalledWith('Flow',[],expect.objectContaining({limit:40}),expect.any(AbortSignal))
  })

  it('keeps recents after clearing whitespace-only input',async()=>{
    setup('/test/search?q=%20%20')
    await screen.findByRole('link',{name:/General/})
    const input=screen.getByRole('textbox')
    fireEvent.change(input,{target:{value:'   '}})
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    fireEvent.keyDown(input,{key:'Escape'})
    expect(await screen.findByRole('link',{name:/General/})).toBeInTheDocument()
    expect(screen.queryByText('Search unavailable')).not.toBeInTheDocument()
  })

  it('does not open stale recent results while a newly submitted search is pending',async()=>{
    let resolve!: (value:SemanticSearchResponse)=>void
    vi.mocked(semanticSearch).mockImplementation(()=>new Promise(done=>{resolve=done}))
    const {onOpenResult}=setup()
    await screen.findByRole('link',{name:/General/})
    const input=screen.getByRole('textbox')
    fireEvent.keyDown(input,{key:'Enter',isComposing:true})
    fireEvent.keyDown(input,{key:'Enter',keyCode:229})
    expect(onOpenResult).not.toHaveBeenCalled()
    fireEvent.change(input,{target:{value:'flow'}})
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByText('Press Enter to search')).toHaveAttribute('role','status')
    fireEvent.keyDown(input,{key:'Enter',isComposing:true})
    expect(semanticSearch).not.toHaveBeenCalled()
    fireEvent.keyDown(input,{key:'Enter'})
    await waitFor(()=>expect(semanticSearch).toHaveBeenCalledTimes(1))
    fireEvent.keyDown(input,{key:'Enter'})
    expect(onOpenResult).not.toHaveBeenCalled()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Search URL')).toHaveTextContent('q=flow')
    expect(screen.getByLabelText('Search URL')).toHaveTextContent('includeArchived=true')
    expect(document.title).toBe('Search: flow')
    await act(async()=>resolve(semantic([{...result,title:'Current result'}])))
    const current=await screen.findByRole('link',{name:/Current result/})
    expect(current).toHaveAttribute('href','/test/issue/FLOW-1')
    fireEvent.keyDown(input,{key:'Enter'})
    expect(onOpenResult).toHaveBeenCalledTimes(1)
    expect(onOpenResult).toHaveBeenCalledWith(expect.objectContaining({title:'Current result'}))
  })

  it('restores query/type/display/filters from URL, aborts stale requests and restores Back',async()=>{
    const filters=[{id:'status',field:'statusType',operator:'is',value:'started'}]
    const params=new URLSearchParams({q:'initial',type:'project',includeArchived:'false',showId:'false',sort:'createdAt',filters:JSON.stringify(filters),match:'or'})
    const {unmount}=setup(`/test/search?${params}`)
    await waitFor(()=>expect(semanticSearch).toHaveBeenCalled())
    expect(screen.getByRole('textbox')).toHaveValue('initial')
    expect(semanticSearch).toHaveBeenLastCalledWith('initial',['project'],expect.objectContaining({includeArchived:false,sort:'createdAt',filter:{or:[{field:'statusType',operator:'is',values:['started']}]}}),expect.any(AbortSignal))
    await screen.findByRole('link',{name:/General/})
    expect(screen.queryByText('FLOW-1')).not.toBeInTheDocument()
    const firstSignal=vi.mocked(semanticSearch).mock.calls[0][3]!
    fireEvent.click(screen.getByRole('button',{name:'Issues'}))
    await waitFor(()=>expect(semanticSearch).toHaveBeenLastCalledWith('initial',['issue'],expect.any(Object),expect.any(AbortSignal)))
    expect(firstSignal.aborted).toBe(true)
    fireEvent.click(screen.getByRole('button',{name:'Back'}))
    await waitFor(()=>expect(screen.getByRole('button',{name:'Projects'})).toHaveAttribute('aria-pressed','true'))
    const lastSignal=vi.mocked(semanticSearch).mock.calls.at(-1)![3]!
    unmount()
    expect(lastSignal.aborted).toBe(true)
  })

  it('renders real links and status glyphs while keeping entity names untranslated',async()=>{
    localStorage.setItem('flow:locale','zh-CN')
    vi.mocked(semanticSearch).mockResolvedValue(semantic([
      result,
      {id:'issue-2',type:'issue',title:'No state',score:1,statusType:'started',statusName:'Review',color:'#ffcc00'},
      {id:'project-1',type:'project',title:'Roadmap',score:1,icon:'Project',color:'#5e6ad2'},
      {id:'doc-1',type:'document',title:'Spec',score:1,subtitle:'Platform'},
      {id:'doc-2',type:'document',title:'Notes',score:1,subtitle:'Document'},
    ]))
    const {onOpenResult}=setup('/test/search?q=flow')
    const link=await screen.findByRole('link',{name:/General/})
    expect(link).toHaveAttribute('href','/test/issue/FLOW-1')
    expect(link.querySelector('.workspace-search-result-icon svg path')).toHaveAttribute('fill','#ffcc00')
    expect(screen.getByText('General')).toBeInTheDocument()
    expect(link.querySelector('[data-i18n-ignore]')).not.toBeNull()
    expect(screen.getByRole('link',{name:/No state/}).querySelector('.workspace-search-result-icon svg path')).toHaveAttribute('fill','#ffcc00')
    expect(screen.getByRole('link',{name:/Roadmap/}).querySelector('use')).toHaveAttribute('href','#Project')
    expect(screen.getByRole('link',{name:/Spec/})).toHaveTextContent('Platform')
    expect(screen.getByRole('link',{name:/Notes/})).not.toHaveTextContent('Document')
    fireEvent.click(link,{ctrlKey:true})
    expect(onOpenResult).not.toHaveBeenCalled()
    fireEvent.click(link)
    expect(onOpenResult).toHaveBeenCalledWith(expect.objectContaining(result))
  })

  it('opens functional filter and display menus in portals',async()=>{
    setup('/test/search?q=flow')
    await screen.findByRole('link')
    fireEvent.pointerDown(screen.getByRole('button',{name:'Add filter'}),{button:0,ctrlKey:false})
    await screen.findByRole('menuitem',{name:'Advanced filter'})
    for(const name of ['Status type','Assignee / Lead','Creator','Updated date','Created date']) expect(screen.getByRole('menuitem',{name})).toBeInTheDocument()
    fireEvent.keyDown(screen.getByRole('menu'),{key:'Escape'})
    fireEvent.pointerDown(screen.getByRole('button',{name:'Display options'}),{button:0,ctrlKey:false})
    const archived=await screen.findByRole('menuitemcheckbox',{name:'Include archived'})
    fireEvent.click(archived)
    await waitFor(()=>expect(semanticSearch).toHaveBeenLastCalledWith('flow',[],expect.objectContaining({includeArchived:false}),expect.any(AbortSignal)))
    expect(screen.getByLabelText('Search URL')).toHaveTextContent('includeArchived=false')
  })

  it('applies advanced AND/OR conditions through the shared status picker',async()=>{
    const onChange=vi.fn()
    render(<I18nProvider><SearchMenus state={readSearchState(new URLSearchParams())} onChange={onChange} users={[viewer]}/></I18nProvider>)
    fireEvent.pointerDown(screen.getByRole('button',{name:'Add filter'}),{button:0,ctrlKey:false})
    fireEvent.click(await screen.findByRole('menuitem',{name:'Advanced filter'}))
    const dialog=await screen.findByRole('dialog',{name:'Advanced filter'})
    fireEvent.click(dialog.querySelector('footer button')!)
    fireEvent.click(screen.getByRole('combobox',{name:'Status type'}))
    fireEvent.click(await screen.findByRole('option',{name:'Started'}))
    fireEvent.click(screen.getByRole('button',{name:'Apply filters'}))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({filters:[expect.objectContaining({field:'statusType',operator:'is',value:'started'})]}))
    expect(searchFilterAST(onChange.mock.calls[0][0])).toEqual({and:[{field:'statusType',operator:'is',values:['started']}]})
  })

  it('applies a person filter from the shared picker without translating the person name',async()=>{
    const onChange=vi.fn()
    render(<I18nProvider><SearchMenus state={readSearchState(new URLSearchParams())} onChange={onChange} users={[viewer]}/></I18nProvider>)
    fireEvent.pointerDown(screen.getByRole('button',{name:'Add filter'}),{button:0,ctrlKey:false})
    const trigger=await screen.findByRole('menuitem',{name:'Assignee / Lead'})
    fireEvent.click(trigger)
    const option=await screen.findByRole('option',{name:new RegExp(viewer.displayName)})
    fireEvent.click(option)
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({filters:[expect.objectContaining({field:'assigneeId',value:viewer.id})]}))
  })

  it('applies a date from the shared calendar inside a filter submenu',async()=>{
    const onChange=vi.fn()
    render(<I18nProvider><SearchMenus state={readSearchState(new URLSearchParams())} onChange={onChange}/></I18nProvider>)
    fireEvent.pointerDown(screen.getByRole('button',{name:'Add filter'}),{button:0,ctrlKey:false})
    fireEvent.click(await screen.findByRole('menuitem',{name:'Updated date'}))
    fireEvent.click(await screen.findByRole('button',{name:'Updated date'}))
    const today=new Date()
    const day=[...document.querySelectorAll('.date-time-grid button')].find(button=>button.textContent===String(today.getDate())&&!button.hasAttribute('data-outside'))
    fireEvent.click(day!)
    const value=`${today.getFullYear()}-${String(today.getMonth()+1).padStart(2,'0')}-${String(today.getDate()).padStart(2,'0')}`
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({filters:[expect.objectContaining({field:'updatedAt',operator:'after',value})]}))
  })

  it('loads the next server page and aborts its pending request when the query changes',async()=>{
    vi.mocked(semanticSearch).mockResolvedValueOnce({...semantic([result]),nextCursor:'next-page',hasMore:true,total:2})
    let resolve!: (value:SemanticSearchResponse)=>void
    vi.mocked(semanticSearch).mockImplementationOnce(()=>new Promise(done=>{resolve=done}))
    setup('/test/search?q=first')
    fireEvent.click(await screen.findByRole('button',{name:'Load more'}))
    await waitFor(()=>expect(semanticSearch).toHaveBeenCalledTimes(2))
    const call=vi.mocked(semanticSearch).mock.calls[1]
    expect(call[2]).toMatchObject({cursor:'next-page'})
    const input=screen.getByRole('textbox')
    fireEvent.change(input,{target:{value:'new'}})
    fireEvent.keyDown(input,{key:'Enter'})
    await waitFor(()=>expect(call[3]?.aborted).toBe(true))
    await act(async()=>resolve(semantic([{...result,id:'old-page',title:'Stale page'}])))
    expect(screen.queryByText('Stale page')).not.toBeInTheDocument()
  })

  it('round trips complete search state and rejects malformed shared filters',()=>{
    const state=readSearchState(new URLSearchParams({q:'x',type:'issue',filters:JSON.stringify([{id:'date',field:'updatedAt',operator:'before',value:'2026-09-10'}]),match:'or',includeArchived:'false',showId:'false',sort:'title'}))
    expect(readSearchState(writeSearchState(state))).toEqual(state)
    expect(readSearchState(new URLSearchParams({filters:'[{}]',type:'missing',sort:'invalid'}))).toMatchObject({filters:[],tab:'all',order:'relevance'})
  })

  it('renders exact Chinese search and calendar copy without partial English substitutions',async()=>{
    localStorage.setItem('flow:locale','zh-CN')
    vi.mocked(searchWorkspace).mockResolvedValue({results:[],history:[],recent:[]})
    setup()
    const input=screen.getByRole('textbox',{name:'搜索事项、项目和文档…'})
    expect(input).toHaveAttribute('placeholder','搜索事项、项目和文档…')
    expect(await screen.findByText('查找事项、项目、目标和文档')).toBeInTheDocument()
    expect(screen.getByRole('img',{name:'搜索空状态插图'})).toBeInTheDocument()
    fireEvent.change(input,{target:{value:'Flow'}})
    expect(screen.getByText('按 Enter 搜索')).toHaveAttribute('role','status')
    fireEvent.pointerDown(screen.getByRole('button',{name:'添加筛选条件'}),{button:0,ctrlKey:false})
    expect(await screen.findByRole('menu')).toHaveAttribute('aria-label','搜索筛选条件')
    fireEvent.click(screen.getByRole('menuitem',{name:'更新日期'}))
    expect(await screen.findByRole('combobox',{name:'筛选运算符'})).toHaveTextContent('晚于')
    fireEvent.click(screen.getByRole('button',{name:'更新日期'}))
    expect(await screen.findByRole('button',{name:'上个月'})).toBeInTheDocument()
    expect(screen.getByRole('button',{name:'下个月'})).toBeInTheDocument()
    expect(screen.getByText(new Date().toLocaleDateString('zh-CN',{year:'numeric',month:'long'}))).toBeInTheDocument()
    expect(document.querySelector('.date-time-weekdays')).toHaveTextContent('一二三四五六日')
    expect(document.body.textContent).not.toContain('Find issues')
    expect(document.body.textContent).not.toContain('after')
  })
})
