import { ChevronRight, Menu, Star } from 'lucide-react'
import { useMemo } from 'react'

import type { Cycle, CycleMutationInput, CycleSettingsMutationInput, Issue, Team } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { CycleActions } from './cycle-menus'
import { cycleStats, formatCycleDay } from './cycle-model'

import './cycles.css'

export function CyclesPage({cycles,issues,settings,onOpen,onUpdateCycle,onStartCycle,onCompleteCycle,onUpdateSettings,onReload,onOpenSidebar}:{cycles:Cycle[];issues:Issue[];settings:{enabled:boolean;durationWeeks:number;cooldownWeeks:number;upcomingCount:number;favoriteView?:boolean};team:Team;onOpen:(cycle:Cycle)=>void;onUpdateCycle:(cycle:Cycle,input:CycleMutationInput)=>Promise<unknown>;onStartCycle:(cycle:Cycle)=>Promise<unknown>;onCompleteCycle:(cycle:Cycle)=>Promise<unknown>;onUpdateSettings:(input:CycleSettingsMutationInput)=>Promise<unknown>;onReload:()=>Promise<void>;onOpenSidebar:()=>void}){
  const {t}=useI18n()
  const visible=useMemo(()=>[...cycles].sort((a,b)=>b.startsAt.localeCompare(a.startsAt)),[cycles])
  const earliestUpcoming=[...visible].filter(item=>item.status==='upcoming').sort((a,b)=>a.startsAt.localeCompare(b.startsAt))[0]?.id
  return <main className="main-panel linear-cycles-page"><header className="linear-cycles-header"><button className="cycles-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Menu/></button><h1>{t('Cycles')}</h1><button className="cycle-top-icon" role="switch" aria-label={t(settings.favoriteView?'Remove from favorites':'Add to favorites')} aria-checked={Boolean(settings.favoriteView)} onClick={()=>void onUpdateSettings({favoriteView:!settings.favoriteView})}><Star fill={settings.favoriteView?'currentColor':'none'}/></button></header>{!settings.enabled||!visible.length?<div className="linear-cycles-empty"><strong>{t('This team has no cycles.')}</strong></div>:<div className="linear-cycle-list">{visible.map(cycle=><CycleDirectoryRow key={cycle.id} cycle={cycle} issues={issues} derivedStatus={cycle.status==='upcoming'&&cycle.id!==earliestUpcoming?'planned':cycle.status} onOpen={()=>onOpen(cycle)} onUpdate={input=>onUpdateCycle(cycle,input)} onStart={()=>onStartCycle(cycle)} onComplete={()=>onCompleteCycle(cycle)} onReload={onReload}/>)}</div>}</main>
}

function CycleDirectoryRow({cycle,issues,derivedStatus,onOpen,onUpdate,onStart,onComplete,onReload}:{cycle:Cycle;issues:Issue[];derivedStatus:string;onOpen:()=>void;onUpdate:(input:CycleMutationInput)=>Promise<unknown>;onStart:()=>Promise<unknown>;onComplete:()=>Promise<unknown>;onReload:()=>Promise<void>}){
  const {t}=useI18n(),start=formatCycleDay(cycle.startsAt),stats=cycleStats(cycle,issues)
  const metric=cycle.status==='current'?`${weekdays(cycle.endsAt)} ${t('weekdays left')}`:cycle.status==='completed'?`${stats.successPercent}% ${t('success')}`:`${stats.scope} ${t('scope')}`
  return <div className="linear-cycle-row"><button className="linear-cycle-row-link" onClick={onOpen}><span className="linear-cycle-date"><b>{start.month}</b><strong>{start.day}</strong></span><span className="linear-cycle-arrow"><ChevronRight/></span><strong className="linear-cycle-name" data-i18n-ignore>{cycle.name}</strong><span className={`linear-cycle-status is-${derivedStatus}`}>{t(statusText(derivedStatus))}</span><span className="linear-cycle-capacity"><i style={{'--capacity':`${Math.min(100,stats.capacityPercent)*3.6}deg`} as React.CSSProperties}/><b>{stats.capacityPercent}%</b><em>{t('of capacity')}</em></span><span className="linear-cycle-scope">{metric}</span></button><CycleActions cycle={cycle} onUpdate={onUpdate} onStart={onStart} onComplete={onComplete} onReload={onReload} canonicalPath={location.pathname.replace(/\/cycles$/,`/cycle/${cycle.number}`)}/></div>
}

function statusText(status:string){return status==='current'?'Active':status==='completed'?'Completed':status==='planned'?'Planned':'Upcoming'}
function weekdays(end:string){const cursor=new Date(),last=new Date(end);let count=0;cursor.setHours(0,0,0,0);while(cursor<=last){if(cursor.getDay()!==0&&cursor.getDay()!==6)count++;cursor.setDate(cursor.getDate()+1)}return Math.max(0,count)}
