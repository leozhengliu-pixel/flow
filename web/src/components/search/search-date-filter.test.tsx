import { render,screen,fireEvent } from '@testing-library/react'
import { describe,it,expect,vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { SearchDateFilter } from './search-date-filter'
import { parseSearchDateInput } from './search-date-input'
import { searchDateRange,searchDateLabel,searchFilterAST,readSearchState,writeSearchState } from './search-state'
import { setWorkspaceRuntimePreferences } from '@/lib/runtime-preferences'

describe('search date drafts',()=>{
  it('cancels without applying drafted selections',()=>{
    const onApply=vi.fn(),onCancel=vi.fn()
    render(<I18nProvider><SearchDateFilter label="Updated date" onApply={onApply} onCancel={onCancel}/></I18nProvider>)
    expect(screen.getByRole('button',{name:'Apply'})).toBeEnabled()
    fireEvent.click(screen.getAllByRole('gridcell')[10])
    expect(onApply).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
    expect(onCancel).toHaveBeenCalledOnce()
    expect(onApply).not.toHaveBeenCalled()
  })
  it('keeps the weekday divider directly above the date grid',()=>{
    render(<I18nProvider><SearchDateFilter label="Updated date" onApply={vi.fn()} onCancel={vi.fn()}/></I18nProvider>)
    const grid=screen.getAllByRole('grid')[0]
    const weekdays=grid.previousElementSibling
    expect(weekdays).toBeTruthy()
    if (!weekdays) return
    expect(weekdays).toHaveAttribute('data-calendar-weekdays','true')
    expect(weekdays.nextElementSibling).toHaveAttribute('role','grid')
  })
  it('applies the suggested current day without changing it while browsing other months',()=>{
    const onApply=vi.fn(),today=new Date()
    render(<I18nProvider><SearchDateFilter label="Updated date" onApply={onApply} onCancel={vi.fn()}/></I18nProvider>)
    fireEvent.click(screen.getByRole('button',{name:'Next month'}))
    fireEvent.click(screen.getByRole('button',{name:'Apply'}))
    const value=`${today.getFullYear()}-${String(today.getMonth()+1).padStart(2,'0')}-${String(today.getDate()).padStart(2,'0')}`
    expect(onApply).toHaveBeenCalledWith(value,'after','day')
  })
  it.each(['on','before','after'] as const)('commits %s only on Apply',operator=>{
    const onApply=vi.fn()
    render(<I18nProvider><SearchDateFilter label="Updated date" value="2026-11-02" onApply={onApply} onCancel={vi.fn()}/></I18nProvider>)
    fireEvent.click(screen.getByRole('radio',{name:operator}))
    fireEvent.click(screen.getByRole('radio',{name:'Quarter'}))
    expect(onApply).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button',{name:'Apply'}))
    expect(onApply).toHaveBeenCalledWith('2026-11-02','on','quarter')
    expect(screen.queryByRole('radiogroup',{name:'Filter operator'})).not.toBeInTheDocument()
    expect(screen.getByText('in')).toBeInTheDocument()
  })
  it.each([['May 2027','month',2027,4],['Q4 2027','quarter',2027,9],['H2 2027','half-year',2027,6],['2027','year',2027,0],['05/20/2027','day',2027,4]] as const)('parses the precision of %s', (text,mode,year,month)=>{
    setWorkspaceRuntimePreferences({fiscalMonth:'January'})
    const parsed=parseSearchDateInput(text)
    expect(parsed?.mode).toBe(mode)
    expect(parsed?.date.getFullYear()).toBe(year)
    expect(parsed?.date.getMonth()).toBe(month)
  })
  it('disables invalid input and ignores an IME confirmation',()=>{
    const onApply=vi.fn()
    render(<I18nProvider><SearchDateFilter label="Updated date" onApply={onApply} onCancel={vi.fn()}/></I18nProvider>)
    const input=screen.getByRole('textbox')
    fireEvent.change(input,{target:{value:'garbage'}})
    expect(screen.getByRole('button',{name:'Apply'})).toBeDisabled()
    fireEvent.change(input,{target:{value:'两周后'}})
    fireEvent.keyDown(input,{key:'Enter',isComposing:true})
    expect(onApply).not.toHaveBeenCalled()
    fireEvent.keyDown(input,{key:'Enter'})
    expect(onApply).toHaveBeenCalledOnce()
  })
})

describe('search date ranges',()=>{
  it.each(['not-a-date','2026-02-30','2026-13-01','2026-01-01T10:00:00'])('drops invalid shared date chips and requests together: %s',value=>{
    const state=readSearchState(new URLSearchParams({filters:JSON.stringify([{id:'invalid',field:'updatedAt',operator:'on',value}])}))
    expect(state.filters).toEqual([])
    expect(searchFilterAST(state)).toBeUndefined()
  })
  it('labels date chips with the chosen precision and locale',()=>{
    setWorkspaceRuntimePreferences({fiscalMonth:'January'})
    expect(searchDateLabel('2026-11-02','month','en-US')).toBe('November 2026')
    expect(searchDateLabel('2026-11-02','month','zh-CN')).toBe('2026年11月')
    expect(searchDateLabel('2026-11-02','quarter','en-US')).toBe('Q4 2026')
    expect(searchDateLabel('2026-11-02','half-year','en-US')).toBe('H2 2026')
    expect(searchDateLabel('2026-11-02','year','en-US')).toBe('2026')
  })
  it.each([['day','2026-11-02','2026-11-03'],['month','2026-11-01','2026-12-01'],['quarter','2026-10-01','2027-01-01'],['half-year','2026-07-01','2027-01-01'],['year','2026-01-01','2027-01-01']] as const)('compiles every %s operator into exact API boundaries', (granularity,start,end)=>{
    setWorkspaceRuntimePreferences({fiscalMonth:'January'})
    const expected=[new Date(`${start}T00:00:00`).toISOString(),new Date(`${end}T00:00:00`).toISOString()]
    expect(searchDateRange('2026-11-02',granularity).map(d=>d.toISOString())).toEqual(expected)
    for(const operator of ['on','before','after'] as const){
      const condition={id:'one',field:'updatedAt' as const,value:'2026-11-02',operator,granularity}
      const state={...readSearchState(new URLSearchParams()),filters:[condition]}
      expect(readSearchState(writeSearchState(state)).filters).toEqual([condition])
      const lower={field:'updatedAt',operator:'gte',values:[expected[0]]},upper={field:'updatedAt',operator:'before',values:[expected[1]]}
      expect(searchFilterAST(state)).toEqual({and:[operator==='on'?{and:[lower,upper]}:operator==='after'?{...lower,values:[expected[1]]}:{...upper,values:[expected[0]]}]})
    }
  })
  it('uses calendar arithmetic across leap days and fiscal years',()=>{
    expect(searchDateRange('2024-02-29','month')[1].getDate()).toBe(1)
    setWorkspaceRuntimePreferences({fiscalMonth:'April'})
    expect(searchDateRange('2026-02-01','year')[0].getFullYear()).toBe(2025)
    expect(searchDateRange('2026-02-01','quarter')[0].getMonth()).toBe(0)
    setWorkspaceRuntimePreferences({fiscalMonth:'January'})
  })
})
