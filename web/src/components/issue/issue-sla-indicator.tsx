import { AlarmClock, AlarmClockCheck, AlarmClockOff, AlarmClockMinus, CircleAlert, Pause } from 'lucide-react'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { useOptionalTranslate } from '@/i18n/i18n'
import { getSlaState, SLA_STATE_LABELS, type SlaState } from '@/lib/issue-sla'
import { slaDisplayText, slaTooltipText } from '@/lib/issue-sla-copy'
import type { IssueSLA } from '@/types/flow'

import './issue-sla-indicator.css'

export function IssueSLAIndicator({ sla, ruleName, compact = false, focusable = true, tooltip = true }: { sla: IssueSLA; ruleName?: string; compact?: boolean; focusable?: boolean; tooltip?: boolean }) {
  const t = useOptionalTranslate()
  const now = useCurrentTimeMs(sla.completedAt || sla.status === 'completed' || sla.status === 'removed' ? 0 : 30_000)
  const state = getSlaState(sla, now)
  if (state === 'none') return null
  const label = slaDisplayText(sla, now, compact, t)
  const content = <span className="issue-sla" data-status={state} data-i18n-ignore tabIndex={focusable ? 0 : undefined} aria-label={`${t('SLA')}: ${slaDisplayText(sla, now, false, t)}`}>
      <SlaIcon state={state}/>
      <span className="issue-sla__text">{label}</span>
      {ruleName && <span className="sr-only">{ruleName}</span>}
    </span>
  return tooltip ? <FlowTooltip label={<SlaTooltipContent sla={sla} now={now}/>} side="top" align="start">{content}</FlowTooltip> : content
}

export function SlaTooltipContent({ sla, now }: { sla: IssueSLA; now: number }) {
  const t = useOptionalTranslate()
  const state = getSlaState(sla, now)
  return <span className="issue-sla-tooltip" data-status={state}><SlaIcon state={state}/><span><strong>{t(SLA_STATE_LABELS[state])}</strong><small>{slaTooltipText(sla, now, t)}</small></span></span>
}

export function SlaIcon({ state }: { state: SlaState }) {
  const Icon = state === 'breached' ? CircleAlert : state === 'failed' ? AlarmClockOff : state === 'achieved' ? AlarmClockCheck : state === 'paused' ? Pause : state === 'low' ? AlarmClockMinus : AlarmClock
  return <Icon size={14} aria-hidden="true"/>
}
