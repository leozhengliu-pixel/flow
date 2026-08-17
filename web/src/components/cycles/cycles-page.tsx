import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronRight, Menu, Settings2, Star } from 'lucide-react'
import { useMemo } from 'react'
import type { Cycle, CycleMutationInput, CycleSettingsMutationInput, Issue, Team } from '@/types/flow'
import type { CyclesRouteView } from '@/lib/app-routes'
import { CycleActions } from './cycle-menus'
import { CycleGraph } from './cycle-graph'
import { cycleStats, cycleStatusLabel, formatCycleDay, weekdaysLeft } from './cycle-model'
import './cycles.css'

export function CyclesPage({ cycles, issues, settings, team, view, onViewChange, onOpen, onUpdateCycle, onStartCycle, onCompleteCycle, onUpdateSettings, onOpenSidebar }: {
  cycles: Cycle[]
  issues: Issue[]
  settings: { enabled: boolean; durationWeeks: number; cooldownWeeks: number; upcomingCount: number }
  team: Team
  view: CyclesRouteView
  onViewChange: (view: CyclesRouteView) => void
  onOpen: (cycle: Cycle) => void
  onUpdateCycle: (cycle: Cycle, input: CycleMutationInput) => Promise<unknown>
  onStartCycle: (cycle: Cycle) => Promise<unknown>
  onCompleteCycle: (cycle: Cycle) => Promise<unknown>
  onUpdateSettings: (input: CycleSettingsMutationInput) => Promise<unknown>
  onOpenSidebar: () => void
}) {
  const visible = useMemo(() => [...cycles].filter(cycle => view === 'all' || (view === 'current' ? cycle.status === 'current' : cycle.status === 'upcoming')).sort((a, b) => new Date(b.startsAt).getTime() - new Date(a.startsAt).getTime()), [cycles, view])
  return <main className="main-panel cycles-page">
    <header className="cycles-header">
      <button aria-label="Open sidebar" className="cycles-mobile-menu" onClick={onOpenSidebar} type="button"><Menu size={15}/></button>
      {settings.enabled ? <><span className="cycles-team-mark" style={{ color: team.color }}>{team.icon || team.name[0]}</span><strong>{team.key}</strong><ChevronRight size={13}/><CycleViewMenu view={view} onViewChange={onViewChange}/></> : <strong className="cycles-empty-title">Cycles</strong>}
      <button aria-label="Favorite cycles view" className="cycle-icon-button" type="button"><Star size={16}/></button>
      <CycleSettingsMenu settings={settings} onUpdate={onUpdateSettings}/>
    </header>
    {!settings.enabled || !visible.length ? <div className="cycles-empty">This team has no cycles.</div> : <div className="cycles-list">
      {visible.map(cycle => <CycleRow cycle={cycle} issues={issues} key={cycle.id} onOpen={() => onOpen(cycle)} onUpdate={input => onUpdateCycle(cycle, input)} onStart={() => onStartCycle(cycle)} onComplete={() => onCompleteCycle(cycle)}/>) }
    </div>}
  </main>
}

function CycleViewMenu({ view, onViewChange }: { view: CyclesRouteView; onViewChange: (view: CyclesRouteView) => void }) {
  const label = view === 'all' ? 'All cycles' : view === 'current' ? 'Current cycle' : 'Upcoming cycles'
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="cycles-view-title" type="button">{label}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="cycle-menu" sideOffset={6}>
    {(['all', 'current', 'upcoming'] as CyclesRouteView[]).map(item => <DropdownMenu.Item key={item} onSelect={() => onViewChange(item)}><span>{item === 'all' ? 'All cycles' : item === 'current' ? 'Current cycle' : 'Upcoming cycles'}</span>{item === view && <span className="cycle-menu__check">✓</span>}</DropdownMenu.Item>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function CycleRow({ cycle, issues, onOpen, onUpdate, onStart, onComplete }: { cycle: Cycle; issues: Issue[]; onOpen: () => void; onUpdate: (input: CycleMutationInput) => Promise<unknown>; onStart: () => Promise<unknown>; onComplete: () => Promise<unknown> }) {
  const stats = cycleStats(cycle, issues)
  const start = formatCycleDay(cycle.startsAt)
  return <article className={`cycle-row is-${cycle.status}`}>
    <div className="cycle-row__rail"><span>{start.month}<br/>{start.day}</span><i/></div>
    <div className="cycle-row__body">
      <header>
        <button className="cycle-row__title" onClick={onOpen} type="button"><span className="cycle-play"><ChevronRight size={12}/></span><strong>{cycle.name}</strong></button>
        <span className={`cycle-status is-${cycle.status}`}>{cycleStatusLabel(cycle.status)}</span>
        <span className={`cycle-capacity ${stats.capacityPercent > 110 ? 'is-over' : ''}`}><i style={{ '--capacity': `${Math.min(stats.capacityPercent, 100) * 3.6}deg` } as React.CSSProperties}/><strong>{stats.capacityPercent}%</strong> of capacity</span>
        <span className="cycle-row__metric">△ {cycle.status === 'current' ? `${weekdaysLeft(cycle)} weekdays left` : cycle.status === 'completed' ? `${stats.successPercent}% success` : `${stats.scope} scope`}</span>
        {cycle.status === 'completed' && <span className="cycle-row__metric">△ {stats.completed} completed</span>}
        <CycleActions cycle={cycle} onUpdate={onUpdate} onStart={onStart} onComplete={onComplete}/>
      </header>
      {cycle.status === 'current' && <div className="cycle-row__expanded"><CycleGraph compact cycle={cycle} issues={issues}/><div className="cycle-row__stats"><Stat label="Scope" value={stats.scope} color="#77777d"/><Stat label="Started" value={`${stats.started} · ${stats.startedPercent}%`} color="#f2c200"/><Stat label="Completed" value={`${stats.completed} · ${stats.completedPercent}%`} color="#5e6ad2"/></div></div>}
    </div>
  </article>
}

function Stat({ color, label, value }: { color: string; label: string; value: string | number }) { return <div><i style={{ background: color }}/><span>{label}</span><strong>△ {value}</strong></div> }

function CycleSettingsMenu({ settings, onUpdate }: { settings: { enabled: boolean; durationWeeks: number; cooldownWeeks: number; upcomingCount: number }; onUpdate: (input: CycleSettingsMutationInput) => Promise<unknown> }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Cycle settings" className="cycle-icon-button cycles-settings-trigger" type="button"><Settings2 size={15}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="cycle-menu cycle-settings-menu" sideOffset={5}>
    <DropdownMenu.Label>Cycle settings</DropdownMenu.Label>
    <DropdownMenu.CheckboxItem checked={settings.enabled} onCheckedChange={enabled => void onUpdate({ enabled: Boolean(enabled) })}><span>Enable cycles</span><span className="cycle-menu__check">{settings.enabled && '✓'}</span></DropdownMenu.CheckboxItem>
    <DropdownMenu.Separator/>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><span>Cycle duration</span><small>{settings.durationWeeks} weeks</small><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="cycle-menu" sideOffset={4}>{[1, 2, 3, 4, 6, 8].map(weeks => <DropdownMenu.Item key={weeks} onSelect={() => void onUpdate({ durationWeeks: weeks })}><span>{weeks} {weeks === 1 ? 'week' : 'weeks'}</span>{settings.durationWeeks === weeks && <span className="cycle-menu__check">✓</span>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger><span>Upcoming cycles</span><small>{settings.upcomingCount}</small><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="cycle-menu" sideOffset={4}>{[1, 2, 3, 5, 8, 15].map(count => <DropdownMenu.Item key={count} onSelect={() => void onUpdate({ upcomingCount: count })}><span>{count}</span>{settings.upcomingCount === count && <span className="cycle-menu__check">✓</span>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}
