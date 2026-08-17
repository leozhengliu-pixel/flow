import type { Cycle, Issue } from '@/types/flow'
import { cycleStats, formatCycleDay } from './cycle-model'

export function CycleGraph({ cycle, issues, compact = false }: { cycle: Cycle; issues: Issue[]; compact?: boolean }) {
  const stats = cycleStats(cycle, issues)
  const start = formatCycleDay(cycle.startsAt)
  const end = formatCycleDay(cycle.endsAt)
  const startedHeight = Math.max(0, 160 - Math.min(stats.startedPercent, 100) * 1.05)
  const completedHeight = Math.max(0, 160 - Math.min(stats.completedPercent, 100) * 1.05)
  return <div className={`cycle-graph ${compact ? 'is-compact' : ''}`}>
    {!compact && <div className="cycle-graph__legend">
      <Stat color="#77777d" label="Scope" value={stats.scope}/>
      <Stat color="#f2c200" label="Started" value={`${stats.started} · ${stats.startedPercent}%`}/>
      <Stat color="#5e6ad2" label="Completed" value={`${stats.completed} · ${stats.completedPercent}%`}/>
    </div>}
    <svg aria-label={`Cycle progress. ${stats.scope} scope, ${stats.started} started, ${stats.completed} completed.`} role="img" viewBox="0 0 640 210" preserveAspectRatio="none">
      <defs>
        <pattern id={`weekend-${cycle.id}`} width="8" height="8" patternUnits="userSpaceOnUse" patternTransform="rotate(35)"><line x1="0" x2="0" y1="0" y2="8" stroke="#ffffff10" strokeWidth="3"/></pattern>
        <radialGradient id={`started-${cycle.id}`} cx="50%" cy="10%" r="90%"><stop offset="0" stopColor="#f2c200" stopOpacity=".15"/><stop offset="1" stopColor="#f2c200" stopOpacity="0"/></radialGradient>
      </defs>
      <rect x="46" y="23" width="91" height="137" fill={`url(#weekend-${cycle.id})`}/>
      <rect x="451" y="23" width="91" height="137" fill={`url(#weekend-${cycle.id})`}/>
      <path d="M0 22 C130 22 160 19 205 22 C250 28 260 56 310 60 L594 60" fill="none" stroke="#747479" strokeWidth="1.4"/>
      <path d={`M0 160 C35 92 60 ${startedHeight} 110 ${startedHeight} S230 ${startedHeight} 286 ${startedHeight} L594 ${startedHeight}`} fill={`url(#started-${cycle.id})`} stroke="#f2c200" strokeWidth="2"/>
      <path d={`M0 160 C110 160 195 ${completedHeight} 286 ${completedHeight} L594 ${completedHeight}`} fill="none" stroke="#5e6ad2" strokeWidth="2"/>
      <path d="M0 160 L594 22" fill="none" stroke="#6f7cf3" strokeDasharray="4 5" strokeWidth="1.5"/>
      <line x1="0" x2="594" y1="160" y2="160" stroke="#45454a"/>
      <circle cx="286" cy={startedHeight} r="4" fill="#f2c200"/><circle cx="286" cy={completedHeight} r="4" fill="#5e6ad2"/>
      <text x="0" y="187">{start.month} {start.day}</text><text x="594" y="187" textAnchor="end">{end.month} {end.day}</text>
    </svg>
  </div>
}

function Stat({ color, label, value }: { color: string; label: string; value: string | number }) {
  return <div><i style={{ background: color }}/><span>{label}</span><strong>△ {value}</strong></div>
}
