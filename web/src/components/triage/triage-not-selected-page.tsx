import { Plus } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { useDesktopLayout } from '@/hooks/use-desktop-layout'
import './triage.css'

export type TriageNotSelectedPageProps = {
  /** Number of issues waiting in triage (unsnoozed). */
  issueCount: number
  /** Opens the create dialog for the team in its triage status. */
  onCreate?: () => void
}

/**
 * Right pane when nothing is selected: the triage illustration, "N issues to triage"
 * and a round "Create triage issue" button.
 */
export function TriageNotSelectedPage({ issueCount, onCreate }: TriageNotSelectedPageProps) {
  const { t } = useI18n()
  const desktop = useDesktopLayout()
  const label = issueCount <= 0 ? t(desktop ? 'Nothing to triage' : 'No issues to triage') : issueCount === 1 ? t('1 issue to triage') : t('{count} issues to triage').replace('{count}', () => String(issueCount))
  return (
    <div className="flow-triage-not-selected" data-triage-empty={issueCount <= 0 ? '' : undefined} data-triage-not-selected={issueCount > 0 ? '' : undefined}>
      <div className="flow-triage-not-selected__body">
        {desktop ? <TriageRingsIllustration /> : <TriageIllustration />}
        <h2>{label}</h2>
        {onCreate ? (
          <button className="flow-triage-not-selected__cta" type="button" onClick={onCreate}>
            <Plus size={14} />
            {t('Create triage issue')}
          </button>
        ) : null}
      </div>
    </div>
  )
}

export function TriageEmptyPage({ onCreate }: { onCreate?: () => void }) {
  return <TriageNotSelectedPage issueCount={0} onCreate={onCreate} />
}

/** Grey outlined brackets and arrows, the reference app's 132×84 triage illustration. */
export function TriageIllustration() {
  return <svg className="flow-triage-illustration" viewBox="0 0 132 84" fill="none" aria-hidden="true">
    <path d="M30 6H12a6 6 0 0 0-6 6v60a6 6 0 0 0 6 6h18M102 6h18a6 6 0 0 1 6 6v60a6 6 0 0 1-6 6h-18" stroke="var(--triage-illustration-outer)" strokeWidth="1.5" strokeLinecap="round"/>
    <path d="M40 30h40m0 0-8-8m8 8-8 8M92 54H52m0 0 8-8m-8 8 8 8" stroke="var(--triage-illustration-inner)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
}


/** Dashed concentric rings around two crossing arrows: the desktop triage illustration (105×66). */
export function TriageRingsIllustration() {
  return <svg className="flow-triage-illustration flow-triage-illustration--rings" viewBox="0 0 105 66" fill="none" aria-hidden="true">
    <ellipse cx="52.5" cy="33" rx="51.7" ry="32.1" stroke="var(--triage-illustration-outer)" strokeWidth="1" strokeDasharray="2 2"/>
    <ellipse cx="52.5" cy="33" rx="48.4" ry="29.4" stroke="var(--triage-illustration-outer)" strokeWidth="1" strokeDasharray="3 2"/>
    <ellipse cx="52.5" cy="33" rx="43.5" ry="25.7" stroke="var(--triage-illustration-inner)" strokeWidth="1" strokeDasharray="3 2"/>
    <path d="M28 41h14M42 41l9-5.5M51 35.5l8-4M66 25h-6.5M66 25v7M59.5 25l-8 3" stroke="var(--triage-illustration-inner)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
}
