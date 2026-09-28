import { useEffect, useRef, useState, type RefObject } from 'react'
import { Sparkles } from 'lucide-react'
import { toast } from 'sonner'
import type { UserSettings } from '@/types/flow'
import { pulseWelcomeBannerCopy } from './use-show-pulse-welcome-banner'
import type { PulseCadence } from './pulse-menus'

export type PulseWelcomeBannerProps = {
  containerRef?: RefObject<HTMLElement | null>
  cadence: PulseCadence
  workspaceDefault?: string
  onConfirm: (cadence: PulseCadence) => Promise<UserSettings | void> | UserSettings | void
}

const BANNER_HEIGHT = 300
const SCROLL_OFFSET = 236

/**
 * LS-0767 — Welcome to Pulse first-run banner + Confirm schedule.
 */
export function PulseWelcomeBanner({ containerRef, cadence, workspaceDefault, onConfirm }: PulseWelcomeBannerProps) {
  const copy = pulseWelcomeBannerCopy()
  const [draft, setDraft] = useState<PulseCadence>(cadence === 'default' ? 'default' : cadence)
  const [busy, setBusy] = useState(false)
  const [opacity, setOpacity] = useState(1)
  const rootRef = useRef<HTMLDivElement>(null)

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
      toast.success('Pulse schedule saved', {
        description: 'You can always edit your preferences in Pulse Settings',
      })
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : 'Could not save Pulse schedule')
    } finally {
      setBusy(false)
    }
  }

  const options: { id: PulseCadence; label: string }[] = [
    { id: 'default', label: workspaceDefault ? `Workspace default (${workspaceDefault})` : 'Workspace default' },
    { id: 'daily', label: 'Daily' },
    { id: 'weekly', label: 'Weekly' },
    { id: 'never', label: 'Never' },
  ]

  return (
    <div
      ref={rootRef}
      className="pulse-welcome-banner"
      data-pulse-welcome-banner=""
      style={{ opacity, minHeight: BANNER_HEIGHT }}
    >
      <div className="pulse-welcome-banner__glyph" aria-hidden>
        <Sparkles size={28} />
      </div>
      <h2>{copy.title}</h2>
      <p>{copy.body}</p>
      <div className="pulse-welcome-banner__schedule">
        <strong>{copy.scheduleLabel}</strong>
        <span>{copy.scheduleHelp}</span>
        <div className="pulse-welcome-banner__options" role="radiogroup" aria-label={copy.scheduleLabel}>
          {options.map(option => (
            <button
              key={option.id}
              type="button"
              role="radio"
              aria-checked={draft === option.id}
              data-active={draft === option.id || undefined}
              onClick={() => setDraft(option.id)}
            >
              {option.label}
            </button>
          ))}
        </div>
        <button
          type="button"
          className="pulse-welcome-banner__confirm"
          disabled={busy}
          onClick={() => void confirm()}
        >
          {copy.confirm}
        </button>
      </div>
    </div>
  )
}
