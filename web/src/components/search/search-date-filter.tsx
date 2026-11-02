import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { DayPanel, PeriodPanel, type DateMode } from '@/components/projects-page/project-target-date-picker'
import { useI18n } from '@/i18n/i18n'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import type { SearchCondition } from './search-state'
import './search-date-filter.css'

const modes: Array<{value:DateMode;label:string}>=[{value:'day',label:'Day'},{value:'month',label:'Month'},{value:'quarter',label:'Quarter'},{value:'half-year',label:'Half-year'},{value:'year',label:'Year'}]
export function SearchDateFilter({label,value,onApply,onCancel}:{label:string;value?:string;onApply:(value:string,operator:SearchCondition['operator'],granularity:DateMode)=>void;onCancel:()=>void}) {
  const {t,locale}=useI18n()
  const phone=usePhoneLayout()
  const surfaceRef=useRef<HTMLDivElement>(null)
  const [operator,setOperator]=useState<'on'|'before'|'after'>('after')
  const [mode,setMode]=useState<DateMode>('day')
  const [initialDate]=useState(()=>new Date())
  const [date,setDate]=useState(()=>value?new Date(`${value}T00:00:00`):undefined)
  const [cursor,setCursor]=useState(()=>date??initialDate)
  const draft=date??initialDate
  useEffect(()=>{
    if(phone)return
    // Wait for the submenu's focus scope before entering the date form.
    const frame=requestAnimationFrame(()=>surfaceRef.current?.querySelector<HTMLElement>('[role=radio][aria-checked=true]')?.focus({preventScroll:true}))
    return()=>cancelAnimationFrame(frame)
  },[phone])
  const choose=(next:Date)=>{setDate(next);setCursor(next)}
  const keyDown=(event:KeyboardEvent<HTMLDivElement>)=>{
    if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return
    const buttons=Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role=radio]'))
    const current=buttons.indexOf(document.activeElement as HTMLButtonElement)
    const next=event.key==='Home'?0:event.key==='End'?buttons.length-1:(current+(event.key==='ArrowRight'?1:-1)+buttons.length)%buttons.length
    event.preventDefault();buttons[next].click();buttons[next].focus()
  }
  return <div ref={surfaceRef} className="search-date-filter" onKeyDown={event=>{if(!phone||event.key!=='Tab')event.stopPropagation()}}>
    <header><strong>{label}</strong><div role="radiogroup" aria-label={t('Filter operator')} onKeyDown={keyDown}>{(['on','before','after'] as const).map(item=><button type="button" role="radio" aria-checked={operator===item} tabIndex={operator===item?0:-1} key={item} onClick={()=>setOperator(item)}>{t(item)}</button>)}</div></header>
    <div className="search-date-modes" role="radiogroup" aria-label={t('Date precision')} onKeyDown={keyDown}>{modes.map(item=><button type="button" role="radio" aria-checked={mode===item.value} tabIndex={mode===item.value?0:-1} key={item.value} onClick={()=>setMode(item.value)}>{t(item.label)}</button>)}</div>
    {mode==='day'?<DayPanel cursor={cursor} locale={locale} onChangeCursor={setCursor} onChoose={choose} selected={date}/>:<PeriodPanel cursor={cursor} label="Start date" locale={locale} mode={mode} onChoose={choose} selected={draft}/>}
    <footer><button type="button" onClick={onCancel}>{t('Cancel')}</button><button type="button" className="search-date-apply" onClick={()=>onApply(`${draft.getFullYear()}-${String(draft.getMonth()+1).padStart(2,'0')}-${String(draft.getDate()).padStart(2,'0')}`,operator,mode)}>{t('Apply')}</button></footer>
  </div>
}
