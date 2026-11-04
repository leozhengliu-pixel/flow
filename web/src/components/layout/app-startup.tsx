import { useId, useState, type CSSProperties } from 'react'
import { useI18n } from '@/i18n/i18n'

// Keep the geometry and mark in sync with the pre-JavaScript shell in index.html: a blank sidebar column, the main
// card and a loading mark at the card centre, drawn while authentication and bootstrap are pending.
export function AppStartup() {
  const { t } = useI18n()
  const mask = useId()
  // Keep the hint's clock when React replaces the initial HTML or remounts during bootstrap.
  const [hintStyle] = useState(() => {
    const ready = (performance.getEntriesByType?.('navigation')[0] as PerformanceNavigationTiming | undefined)?.domContentLoadedEventStart
    return ready ? { '--flow-startup-hint-delay': `${8800 - (performance.now() - ready)}ms` } as CSSProperties : undefined
  })
  return <div className="flow-startup" role="status" aria-live="polite" aria-busy="true">
    <div className="flow-startup__rail" />
    <div className="flow-startup__card">
      <svg className="flow-startup__disc" viewBox="4 4 40 40" fill="currentColor" aria-hidden="true"><mask id={mask} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48"><rect width="48" height="48" fill="#fff"/><path d="M0 33C11 33 14 13 24 13C31 13 33 26 37 26C40.5 26 42.5 20 48 17" fill="none" stroke="#000" strokeWidth="5"/></mask><rect x="4" y="4" width="40" height="40" rx="10" mask={`url(#${mask})`}/></svg>
      <span className="flow-startup__text" style={hintStyle} aria-hidden="true">{t('Loading your workspace...')}</span>
    </div>
    <span className="flow-startup__accessible">{t('Loading…')}</span>
  </div>
}
