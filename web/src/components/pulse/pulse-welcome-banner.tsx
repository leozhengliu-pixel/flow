import { useEffect, useState, type RefObject } from 'react'
import { toast } from 'sonner'
import { useI18n } from '@/i18n/i18n'
import { PulseIcon } from './pulse-icon'
import { pulseWelcomeBannerCopy } from './use-show-pulse-welcome-banner'
import { PULSE_SCHEDULES, pulseScheduleLabels, type PulseSchedule } from './pulse-schedule'

export type PulseWelcomeBannerProps = {
  containerRef?: RefObject<HTMLElement | null>
  cadence: PulseSchedule
  onConfirm: (cadence: PulseSchedule) => Promise<unknown> | unknown
}

const SCROLL_OFFSET = 236

/**
 * LS-0767 — "Welcome to Pulse" first-run banner: pick the summary schedule, then Confirm.
 */
export function PulseWelcomeBanner({ containerRef, cadence, onConfirm }: PulseWelcomeBannerProps) {
  const { t } = useI18n()
  const copy = pulseWelcomeBannerCopy()
  const [draft, setDraft] = useState<PulseSchedule>(cadence)
  const [busy, setBusy] = useState(false)
  const [opacity, setOpacity] = useState(1)

  useEffect(() => {
    const scroller = containerRef?.current
    if (!scroller) return
    const onScroll = () => {
      const progress = Math.min(1, Math.max(0, scroller.scrollTop / SCROLL_OFFSET))
      setOpacity(Math.max(0.25, 1 - progress * 0.75))
    }
    onScroll()
    scroller.addEventListener('scroll', onScroll, { passive: true })
    return () => scroller.removeEventListener('scroll', onScroll)
  }, [containerRef])

  const confirm = async () => {
    if (busy) return
    setBusy(true)
    try {
      await onConfirm(draft)
      toast.success(t('Pulse schedule saved'), { description: t('You can always edit your preferences in Pulse Settings') })
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t('Could not save Pulse schedule'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="pulse-welcome-banner" data-pulse-welcome-banner="" style={{ opacity }}>
      <div className="pulse-welcome-banner__glyph" aria-hidden><PulseIcon size={26}/></div>
      <h2>{t(copy.title)}</h2>
      <p>{t(copy.body)}</p>
      <div className="pulse-welcome-banner__schedule">
        <strong>{t(copy.scheduleLabel)}</strong>
        <span>{t(copy.scheduleHelp)}</span>
        <div className="pulse-welcome-banner__options" role="radiogroup" aria-label={t(copy.scheduleLabel)}>
          {PULSE_SCHEDULES.map(option => (
            <button key={option} type="button" role="radio" aria-checked={draft === option} data-active={draft === option || undefined} onClick={() => setDraft(option)}>
              {t(pulseScheduleLabels[option])}
            </button>
          ))}
        </div>
        <button type="button" className="pulse-welcome-banner__confirm" disabled={busy} onClick={() => void confirm()}>{t(copy.confirm)}</button>
      </div>
    </div>
  )
}
