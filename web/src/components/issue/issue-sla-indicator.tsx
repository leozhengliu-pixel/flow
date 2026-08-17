import { Clock3, Flame } from 'lucide-react'
import { useEffect, useState } from 'react'

import type { IssueSLA } from '@/types/flow'

import './issue-sla-indicator.css'

export function IssueSLAIndicator({ sla, ruleName, compact = false }: { sla: IssueSLA; ruleName?: string; compact?: boolean }) {
  const [, refresh] = useState(0)
  useEffect(() => {
    if (sla.status !== 'active') return
    const timer = window.setInterval(() => refresh(value => value + 1), 30_000)
    return () => window.clearInterval(timer)
  }, [sla.status])
  const label = slaLabel(sla)
  return <span className="issue-sla" data-status={sla.status} title={`${ruleName ?? 'SLA'} · ${label}`}>
    {sla.status === 'breached' ? <Flame size={12}/> : <Clock3 size={12}/>}
    {!compact && ruleName && <span className="issue-sla__rule">{ruleName}</span>}
    <strong>{label}</strong>
  </span>
}

function slaLabel(sla: IssueSLA) {
  if (sla.status === 'breached') return 'Breached'
  if (sla.status === 'completed') return 'Met'
  if (sla.status === 'removed') return 'Removed'
  const minutes = sla.status === 'paused' ? sla.remainingMinutes : Math.ceil((new Date(sla.dueAt).getTime() - Date.now()) / 60_000)
  return `${sla.status === 'paused' ? 'Paused · ' : ''}${formatDuration(Math.max(0, minutes))}`
}

function formatDuration(minutes: number) {
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const remainder = minutes % 60
  if (hours < 24) return remainder ? `${hours}h ${remainder}m` : `${hours}h`
  const days = Math.floor(hours / 24)
  const dayHours = hours % 24
  return dayHours ? `${days}d ${dayHours}h` : `${days}d`
}
