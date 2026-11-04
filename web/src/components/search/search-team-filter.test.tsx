import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { Team } from '@/types/flow'
import { SearchFilterChips, SearchMenus } from './search-menus'
import { readSearchState, searchFilterAST, teamSelection, writeSearchState } from './search-state'

const teams:Team[]=[{id:'a',name:'Engineering',key:'ENG',color:'var(--text-secondary)'},{id:'b',name:'研发',key:'DEV',color:'var(--text-secondary)'}]

beforeEach(()=>localStorage.clear())

it('round trips exact team sets, negative operators and compound search requests',()=>{
  const state=readSearchState(new URLSearchParams())
  state.match='or'
  state.filters=[{id:'t',field:'team',operator:'notIn',value:teams[0].id,values:teams.map(team=>team.id)},{id:'s',field:'statusType',operator:'is',value:'started'}]
  expect(readSearchState(writeSearchState(state))).toEqual(state)
  expect(searchFilterAST(state)).toEqual({or:[{field:'team',operator:'notIn',values:teams.map(team=>team.id)},{field:'statusType',operator:'is',values:['started']}]})
  expect(teamSelection(state.filters[0],[teams[0].id])).toMatchObject({operator:'isNot',values:[teams[0].id]})
  for(const filter of [{field:'team',operator:'before',value:'x'},{field:'team',operator:'in',value:'',values:['']},{field:'team',operator:'in',value:'x',values:[1]},{field:'team',operator:'in',value:'x',values:'x'},{field:'statusType',operator:'in',value:'started'}]) {
    expect(readSearchState(new URLSearchParams({filters:JSON.stringify([{id:'bad',...filter}])})).filters).toEqual([])
  }
})

it('renders a team icon and untranslated name, edits all operators, and removes the chip',async()=>{
  const onChange=vi.fn()
  const state=readSearchState(new URLSearchParams())
  state.filters=[{id:'t',field:'team',operator:'is',value:teams[0].id}]
  render(<I18nProvider><SearchFilterChips state={state} teams={[{...teams[0],name:'研发'}]} onChange={onChange}/></I18nProvider>)
  expect(screen.getByText('研发')).toHaveAttribute('data-i18n-ignore')
  expect(document.querySelector('[data-filter-chip] svg')).not.toBeNull()
  fireEvent.click(screen.getByRole('button',{name:'Team operator'}))
  for(const name of ['is','is not','is any of','is none of']) expect(await screen.findByRole('menuitemradio',{name})).toBeInTheDocument()
  fireEvent.click(screen.getByRole('menuitemradio',{name:'is none of'}))
  expect(searchFilterAST(onChange.mock.calls.at(-1)![0])).toEqual({and:[{field:'team',operator:'notIn',values:[teams[0].id]}]})
  fireEvent.click(screen.getByRole('button',{name:'Remove Team filter'}))
  expect(onChange).toHaveBeenLastCalledWith({...state,filters:[]})
})

it('offers searchable multi-select teams with keys and translates the chip',async()=>{
  localStorage.setItem('flow:locale','zh-CN')
  const state=readSearchState(new URLSearchParams())
  const onChange=vi.fn()
  const view=render(<I18nProvider><SearchMenus state={state} teams={teams} onChange={onChange}/></I18nProvider>)
  fireEvent.pointerDown(screen.getByRole('button',{name:'添加筛选条件'}),{button:0,ctrlKey:false})
  fireEvent.click(await screen.findByRole('menuitem',{name:'团队'}))
  fireEvent.change(await screen.findByRole('textbox',{name:'搜索团队…'}),{target:{value:teams[0].key}})
  const option=await screen.findByRole('option',{name:new RegExp(teams[0].name)})
  expect(option).toHaveTextContent(teams[0].key)
  fireEvent.click(option)
  const next=onChange.mock.calls.at(-1)![0]
  expect(searchFilterAST(next)).toEqual({and:[{field:'team',operator:'is',values:[teams[0].id]}]})
  view.unmount()
  render(<I18nProvider><SearchFilterChips state={{...next,filters:[teamSelection(next.filters[0],teams.map(team=>team.id))]}} teams={teams} onChange={onChange}/></I18nProvider>)
  expect(screen.getByLabelText('已应用的筛选')).toHaveTextContent('团队')
  expect(screen.getByRole('button',{name:'团队 值'})).toHaveTextContent(`${teams.length} 个团队`)
})

it('keeps the chosen negative operator when editing Team values in advanced filters',async()=>{
  const state=readSearchState(new URLSearchParams())
  state.filters=[{id:'team',field:'team',operator:'notIn',value:teams[0].id,values:teams.map(team=>team.id)}]
  const onChange=vi.fn()
  render(<I18nProvider><SearchMenus state={state} teams={teams} onChange={onChange}/></I18nProvider>)
  fireEvent.pointerDown(screen.getByRole('button',{name:'Add filter'}),{button:0,ctrlKey:false})
  fireEvent.click(await screen.findByRole('menuitem',{name:'Advanced filter'}))
  fireEvent.click(await screen.findByRole('combobox',{name:'Team'}))
  fireEvent.click(await screen.findByRole('option',{name:/Engineering/}))
  fireEvent.click(screen.getByRole('button',{name:'Apply filters'}))
  const next=onChange.mock.calls.at(-1)![0]
  expect(searchFilterAST(next)).toEqual({and:[{field:'team',operator:'notIn',values:[teams[1].id]}]})
  expect(readSearchState(writeSearchState(next))).toEqual(next)
})

it('preserves independent Team conditions when editing the quick filter',async()=>{
  const state=readSearchState(new URLSearchParams())
  state.filters=[{id:'included',field:'team',operator:'is',value:teams[0].id},{id:'excluded',field:'team',operator:'isNot',value:teams[1].id}]
  const onChange=vi.fn()
  render(<I18nProvider><SearchMenus state={state} teams={teams} onChange={onChange}/></I18nProvider>)
  fireEvent.pointerDown(screen.getByRole('button',{name:'Add filter'}),{button:0,ctrlKey:false})
  fireEvent.click(await screen.findByRole('menuitem',{name:'Team'}))
  fireEvent.click(await screen.findByRole('option',{name:/研发/}))
  const next=onChange.mock.calls.at(-1)![0]
  expect(next.filters).toHaveLength(2)
  expect(next.filters[1]).toEqual(state.filters[1])
  expect(searchFilterAST(next)).toEqual({and:[{field:'team',operator:'in',values:teams.map(team=>team.id)},{field:'team',operator:'isNot',values:[teams[1].id]}]})
})
