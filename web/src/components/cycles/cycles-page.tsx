import { Menu } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { Fragment, useMemo } from 'react'
import { SidebarLeftIcon } from '@/components/ui/view-action-icons'
import { FlowFavoriteFilledIcon, FlowFavoriteIcon } from '@/components/issue/flow-header-icons'
import { CycleTimelineCard, CycleCooldownIcon } from './cycle-timeline'

import type { Cycle, CycleMutationInput, CycleSettingsMutationInput, Issue, Team, TeamSettings } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { CycleActions } from './cycle-menus'
import { cooldownWeeksBetween, cycleListOrder, cycleStats, formatCycleDay } from './cycle-model'

import './cycles.css'
import './cycles-bundle-parity.css'
import './cycle-graph.css'
import './cycle-timeline.css'

export function CyclesPage({cycles,issues,settings,team,estimateSettings,onOpen,onUpdateCycle,onStartCycle,onUpdateSettings,onReload,onOpenSidebar}:{cycles:Cycle[];issues:Issue[];settings:{enabled:boolean;durationWeeks:number;cooldownWeeks:number;upcomingCount:number;favoriteView?:boolean};team:Team;estimateSettings?:TeamSettings;onOpen:(cycle:Cycle)=>void;onUpdateCycle:(cycle:Cycle,input:CycleMutationInput)=>Promise<unknown>;onStartCycle:(cycle:Cycle)=>Promise<unknown>;onUpdateSettings:(input:CycleSettingsMutationInput)=>Promise<unknown>;onReload:()=>Promise<void>;onOpenSidebar:()=>void}){
  const {t}=useI18n()
  const visible=useMemo(()=>cycleListOrder(cycles),[cycles])
  const teamBreadcrumb=<span className="cycle-team-breadcrumb" data-i18n-ignore><span className="cycle-team-breadcrumb__label"><TeamIcon team={team} size={16}/><span>{team.name}</span></span><span aria-hidden="true" className="cycle-team-breadcrumb__separator">›</span></span>
  const earliestUpcoming=[...visible].filter(item=>item.status==='upcoming').sort((a,b)=>a.startsAt.localeCompare(b.startsAt))[0]?.id
  return <main className="main-panel flow-cycles-page"><header className="flow-cycles-header">
    <button className="cycles-mobile-menu" aria-label={t('Open sidebar')} data-sidebar-trigger onClick={onOpenSidebar}><Menu className="cycle-desktop-glyph"/><SidebarLeftIcon className="cycle-phone-glyph"/></button>
    {teamBreadcrumb}<h1>{t('Cycles')}</h1>
    <button className="cycle-top-icon" role="switch" aria-label={t(settings.favoriteView?'Remove from favorites':'Add to favorites')} aria-checked={Boolean(settings.favoriteView)} onClick={()=>void onUpdateSettings({favoriteView:!settings.favoriteView})}>
      <span className="cycle-desktop-glyph"><CycleFavoriteIcon filled={Boolean(settings.favoriteView)}/></span><span className="cycle-phone-glyph">{settings.favoriteView?<FlowFavoriteFilledIcon/>:<FlowFavoriteIcon/>}</span>
    </button>
  </header>{!settings.enabled?<div className="cycle-education flow-cycles-empty-education"><CycleEmptyMark/><strong>{t('Cycles')}</strong><p>{t('Cycles are time-based intervals to help your team focus on a predefined set of work.')}</p><p>{t('Track capacity, scope, and progress as issues are added, and as the cycle progresses.')}</p><div><button type="button" onClick={()=>void onUpdateSettings({enabled:true})}>{t('Enable cycles')}</button><a href="https://flow.app/docs/cycles" target="_blank" rel="noreferrer">{t('Documentation')}</a></div></div>:!visible.length?<div className="flow-cycles-empty"><strong>{t('This team has no cycles.')}</strong></div>:<div className="flow-cycle-list">{visible.map((cycle,index)=>{
    const earlier=visible[index+1],cooldown=earlier?cooldownWeeksBetween(earlier,cycle):0
    return <Fragment key={cycle.id}><CycleDirectoryRow cycle={cycle} cycles={cycles} issues={issues} estimateSettings={estimateSettings} derivedStatus={cycle.status==='upcoming'&&cycle.id!==earliestUpcoming?'planned':cycle.status} onOpen={()=>onOpen(cycle)} onUpdate={input=>onUpdateCycle(cycle,input)} onStart={()=>onStartCycle(cycle)} onReload={onReload}/>{cooldown>0&&<div className="flow-cycle-cooldown"><CycleCooldownIcon/><span>{t(cooldown===1?'{count} week cooldown':'{count} weeks cooldown').replace('{count}',String(cooldown))}</span>{earlier&&<span className="cycle-timeline-date">{formatCycleDay(earlier.endsAt).month}<br/>{formatCycleDay(earlier.endsAt).day}</span>}</div>}</Fragment>
  })}</div>}</main>
}

function CycleEmptyMark(){return <svg className="cycle-empty-mark" viewBox="0 0 123 108" aria-hidden="true"><path className="cycle-empty-shadow" d="M52 36.19 83.47 50.5c.87.4 2.33-2.65 2.82-2v5.75a3.5 3.5 0 0 1-.79 2.13c-.5.65-1.24 1.2-2.11 1.58l-31.46 13.8a7.77 7.77 0 0 1-6.03.06 5.51 5.51 0 0 1-2.22-1.6 3.52 3.52 0 0 1-.8-2.2V39.9c0-.78.28-1.55.82-2.22a5.54 5.54 0 0 1 2.25-1.6 7.8 7.8 0 0 1 6.07.1Z"/><path className="cycle-empty-play" d="M52 30.19 83.47 44.5c.87.4 1.59.95 2.08 1.6.5.66.75 1.4.74 2.15a3.5 3.5 0 0 1-.79 2.13c-.5.65-1.24 1.2-2.11 1.58l-31.46 13.8a7.77 7.77 0 0 1-6.03.06 5.51 5.51 0 0 1-2.22-1.6 3.52 3.52 0 0 1-.8-2.2V33.9c0-.78.28-1.55.82-2.22a5.54 5.54 0 0 1 2.25-1.6 7.8 7.8 0 0 1 6.07.1Z"/><circle className="cycle-empty-orbit" cx="61.5" cy="54" r="53"/></svg>}

function CycleDirectoryRow({cycle,cycles,issues,estimateSettings,derivedStatus,onOpen,onUpdate,onStart,onReload}:{cycle:Cycle;cycles:Cycle[];issues:Issue[];estimateSettings?:TeamSettings;derivedStatus:string;onOpen:()=>void;onUpdate:(input:CycleMutationInput)=>Promise<unknown>;onStart:()=>Promise<unknown>;onReload:()=>Promise<void>}){
  const stats=cycleStats(cycle,issues,estimateSettings)
  return <CycleTimelineCard cycle={cycle} issues={issues} stats={stats} derivedStatus={derivedStatus} onOpen={onOpen} className="flow-cycle-row" actions={<CycleActions cycle={cycle} cycles={cycles} onUpdate={onUpdate} onStart={onStart} onReload={onReload} canonicalPath={location.pathname.replace(/\/cycles$/,`/cycle/${cycle.number}`)} capacityUnit={stats.estimates?'points':'issues'}/>}/>
}

function CycleFavoriteIcon({filled=false}:{filled?:boolean}){return <svg aria-hidden="true" viewBox="0 0 16 16"><path fill={filled?'currentColor':'none'} d="M10.5193 4.98997 9.46118 2.01693A1.55 1.55 0 0 0 8.00081 1c-.684 0-1.27.4-1.45553 1.00374L5.48248 4.98997H2.55536c-.668 0-1.252.405-1.46122 1.0174-.2.595-.012 1.24.46605 1.6376l2.52526 2.07761-1.06174 3.15629c-.225.67.02 1.39.561 1.782.54.39 1.27.388 1.81-.005L8.00053 12.8223l2.58457 1.8791c.54.393 1.27.395 1.8102.004.541-.392.786-1.112.5619-1.782l-1.0616-3.1573 2.5284-2.07807c.477-.4.662-.944.476-1.533a1.55 1.55 0 0 0-1.4521-1.1641l-2.9286-.00003Z"/><path d="M10.5193 4.98997 9.46118 2.01693A1.55 1.55 0 0 0 8.00081 1c-.684 0-1.27.4-1.45553 1.00374L5.48248 4.98997H2.55536c-.668 0-1.252.405-1.46122 1.0174-.2.595-.012 1.24.46605 1.6376l2.52526 2.07761-1.06174 3.15629c-.225.67.02 1.39.561 1.782.54.39 1.27.388 1.8102-.005L8.00053 12.8223l2.58457 1.8791c.54.393 1.27.395 1.8102.004.541-.392.786-1.112.5619-1.782l-1.0616-3.1573 2.5284-2.07807c.477-.4.662-.944.476-1.533a1.55 1.55 0 0 0-1.4521-1.1641l-2.9286-.00003Z" fill="none" stroke="currentColor" strokeWidth=".9"/></svg>}
