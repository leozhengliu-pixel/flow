import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { Check } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import type { CodingPlanStep } from '@/types/coding-sessions'
import { currentPlanStep } from './coding-session-stream-model'
import './coding-session-stream.css'

const STATUS_PREFIX: Record<CodingPlanStep['status'], string> = { pending: 'Pending: ', inProgress: 'In progress: ', completed: 'Completed: ', canceled: 'Canceled: ' }

/** When the plan last changed: the session's update time at first, then the moment a new plan arrived. */
function usePlanUpdatedAt(plan: readonly CodingPlanStep[], updatedAt: string) {
  const key = JSON.stringify(plan)
  const now = useCurrentTimeMs()
  const [state, setState] = useState({ key, at: updatedAt })
  if (state.key !== key) {
    const next = { key, at: new Date(now).toISOString() }
    setState(next)
    return next.at
  }
  return state.at
}

/** The collapsed step text; when it overflows, hovering scrolls it as a marquee. */
function MarqueeText({ text }: { text: string }) {
  const box = useRef<HTMLSpanElement>(null)
  const inner = useRef<HTMLSpanElement>(null)
  const [overflow, setOverflow] = useState<number>()
  useLayoutEffect(() => {
    const outer = box.current, content = inner.current
    if (!outer || !content) return
    const measure = () => setOverflow(content.scrollWidth > outer.clientWidth + 1 ? content.scrollWidth : undefined)
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(outer)
    return () => observer.disconnect()
  }, [text])
  return <span ref={box} className="cs-marquee" data-overflow={overflow ? '' : undefined} style={overflow ? { ['--cs-marquee-duration' as string]: `${Math.max(3, (overflow + 8) / 30)}s` } : undefined}>
    <span className="cs-marquee-track">
      <span ref={inner} className="cs-marquee-text" data-i18n-ignore>{text}</span>
      {overflow && <span className="cs-marquee-text" aria-hidden="true" data-i18n-ignore>{text}</span>}
    </span>
  </span>
}

/**
 * The agent's plan above the composer: a pill with "3/7" and the current step, which expands into the step list
 * ("Plan · Updated 2min ago"). Clicking elsewhere in the agent panel folds it, and it folds when the session completes.
 */
export function CodingPlanPill({ plan, updatedAt, complete }: { plan?: CodingPlanStep[]; updatedAt: string; complete: boolean }) {
  const { t, formatRelative } = useI18n()
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLOListElement>(null)
  const steps = plan ?? []
  const planUpdatedAt = usePlanUpdatedAt(steps, updatedAt)
  const current = currentPlanStep(steps)
  // The plan folds when the session completes.
  const [wasComplete, setWasComplete] = useState(complete)
  if (complete !== wasComplete) {
    setWasComplete(complete)
    if (complete) setOpen(false)
  }
  useEffect(() => {
    if (!open) return
    const panel = root.current?.closest('.coding-agent-panel') ?? document
    const onDown = (event: Event) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    panel.addEventListener('pointerdown', onDown)
    return () => panel.removeEventListener('pointerdown', onDown)
  }, [open])
  useLayoutEffect(() => {
    if (!open || !current) return
    list.current?.children[current.index]?.scrollIntoView?.({ block: 'nearest' })
  }, [open, current?.index]) // eslint-disable-line react-hooks/exhaustive-deps -- follow the current step only
  if (!current) return null
  const position = `${current.index + 1}/${steps.length}`
  const relative = Number.isNaN(Date.parse(planUpdatedAt)) ? '' : formatRelative(planUpdatedAt)
  return <div ref={root} className="cs-plan" data-open={open || undefined}>
    <button type="button" className="cs-plan-header" aria-expanded={open}
      aria-label={open ? t('Collapse steps') : t('Expand steps, step {current} of {total}: {step}').replace('{current}', String(current.index + 1)).replace('{total}', String(steps.length)).replace('{step}', current.step.content)}
      onClick={() => setOpen(value => !value)}>
      {open
        ? <span className="cs-plan-heading" key="open"><strong>{t('Plan')}</strong>{relative && <span className="cs-plan-updated">{t('Updated {time}').replace('{time}', relative)}</span>}</span>
        : <span className="cs-plan-heading" key="closed"><span className="cs-plan-position">{position}</span><MarqueeText text={current.step.content}/></span>}
    </button>
    {open && <ol ref={list} className="cs-plan-list" aria-label={t('Plan')}>
      {steps.map((step, index) => <li key={index} data-status={step.status} aria-current={index === current.index ? 'step' : undefined}>
        <span className="cs-plan-index" aria-hidden="true">{step.status === 'completed' ? <Check size={12}/> : index + 1}</span>
        <span className="cs-plan-text"><span className="sr-only">{t(STATUS_PREFIX[step.status] ?? STATUS_PREFIX.pending)}</span><span className={step.status === 'inProgress' ? 'cs-plan-shimmer' : undefined} data-i18n-ignore>{step.content}</span></span>
      </li>)}
    </ol>}
  </div>
}
