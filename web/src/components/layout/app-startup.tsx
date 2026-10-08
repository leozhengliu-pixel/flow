import { FlowLogo } from '@/components/ui/flow-logo'
import { useI18n } from '@/i18n/i18n'

// Keep the geometry and mark in sync with the pre-JavaScript shell in index.html.
export function AppStartup() {
  const { t } = useI18n()
  return <div className="flow-startup" role="status" aria-live="polite" aria-busy="true">
    <div className="flow-startup__content">
      <span className="flow-startup__pulse" aria-hidden="true" />
      <FlowLogo className="flow-startup__mark"/>
      <span className="flow-startup__accessible">{t('Loading…')}</span>
      <span className="flow-startup__text" aria-hidden="true">{t('Loading…')}</span>
    </div>
  </div>
}
