import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { ParentTeamPicker } from './parent-team-picker'
import { I18nProvider } from '@/i18n/i18n'
import type { Team } from '@/types/flow'

it('searches ancestor names and prevents choosing a parent at the fifth level',()=>{
  localStorage.setItem('flow:locale','en-US')
  const teams:Team[]=Array.from({length:5},(_,i)=>({id:`t${i}`,key:`T${i}`,name:i===0?'Engineering':`Unit ${i}`,color:'#888888'}))
  const settings=Object.fromEntries(teams.slice(1).map((team,i)=>[team.id,{parentTeamId:`t${i}`}]))
  const change=vi.fn()
  render(<I18nProvider><ParentTeamPicker teams={teams} settings={settings} onChange={change}/></I18nProvider>)
  fireEvent.click(screen.getByRole('combobox',{name:'Parent team'}))
  fireEvent.change(screen.getByRole('textbox'),{target:{value:'Engineering'}})
  expect(screen.getAllByRole('option')).toHaveLength(5)
  const deepest=screen.getByRole('option',{name:/Unit 4/})
  expect(deepest).toHaveAttribute('aria-disabled','true')
  fireEvent.click(deepest)
  expect(change).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('option',{name:/Unit 3 T3$/}))
  fireEvent.click(screen.getByRole('button',{name:'Set parent team'}))
  expect(change).toHaveBeenCalledWith('t3')
})

it('lists teams as an indented tree with keys, without a "No parent team" row until a parent is set',()=>{
  localStorage.setItem('flow:locale','en-US')
  const teams:Team[]=[{id:'a',key:'AAA',name:'Alpha',color:'#888888'},{id:'b',key:'BBB',name:'Beta',color:'#888888'}]
  const settings={b:{parentTeamId:'a'}}
  const view=render(<I18nProvider><ParentTeamPicker teams={teams} settings={settings} onChange={vi.fn()}/></I18nProvider>)
  fireEvent.click(screen.getByRole('combobox',{name:'Parent team'}))
  const rows=screen.getAllByRole('option')
  expect(rows.map(row=>row.textContent)).toEqual(['AlphaAAA','BetaBBB'])
  expect(rows[1]).toHaveAttribute('data-indent','1')
  expect(rows[1].querySelector('.property-command-tree-elbow')).not.toBeNull()
  view.unmount()
  render(<I18nProvider><ParentTeamPicker teams={teams} settings={settings} value="a" onChange={vi.fn()}/></I18nProvider>)
  fireEvent.click(screen.getByRole('combobox',{name:'Parent team'}))
  expect(screen.getAllByRole('option')[0]).toHaveTextContent('No parent team')
})
