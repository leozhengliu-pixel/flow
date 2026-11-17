import { useState, type CSSProperties } from 'react'
import { AlertCircle, RotateCcw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { FlowLogo } from '@/components/ui/flow-logo'
import { useI18n } from '@/i18n/i18n'

/** The startup frame when the server cannot be reached (the app keeps retrying; the button tries at once). */
export function AppConnectionError({ retry, title = "Can't connect to Flow", detail = 'Flow will keep trying to reach the server.', action = 'Retry' }: { retry: () => void; title?: string; detail?: string; action?: string }) {
  const { t } = useI18n()
  return <div className="flow-startup" role="alert">
    <div className="flow-startup__rail" />
    <div className="flow-startup__card">
      <div className="state-fill">
        <AlertCircle size={24}/>
        <strong>{t(title)}</strong>
        <span>{t(detail)}</span>
        <Button variant="outline" onClick={retry}><RotateCcw size={13}/>{t(action)}</Button>
      </div>
    </div>
  </div>
}

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
