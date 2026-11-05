import { useState, type CSSProperties } from 'react'
import { FlowLogo } from '@/components/ui/flow-logo'
import { useI18n } from '@/i18n/i18n'

// Keep the geometry and mark in sync with the pre-JavaScript shell in index.html: a blank sidebar column, the main
// card and the Flow mark at the card centre, drawn while authentication and bootstrap are pending.
export function AppStartup() {
  const { t } = useI18n()
  // Keep the hint's clock when React replaces the initial HTML or remounts during bootstrap.
  const [hintStyle] = useState(() => {
    const ready = (performance.getEntriesByType?.('navigation')[0] as PerformanceNavigationTiming | undefined)?.domContentLoadedEventStart
    return ready ? { '--flow-startup-hint-delay': `${8800 - (performance.now() - ready)}ms` } as CSSProperties : undefined
  })
  return <div className="flow-startup" role="status" aria-live="polite" aria-busy="true">
    <div className="flow-startup__rail" />
    <div className="flow-startup__card">
      <FlowLogo className="flow-startup__mark"/>
      <span className="flow-startup__text" style={hintStyle} aria-hidden="true">{t('Loading your workspace...')}</span>
    </div>
    <span className="flow-startup__accessible">{t('Loading…')}</span>
  </div>
}
