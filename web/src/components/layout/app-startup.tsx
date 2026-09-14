import { useI18n } from '@/i18n/i18n'

// Keep the geometry and mark in sync with the pre-JavaScript shell in index.html.
export function AppStartup() {
  const { t } = useI18n()
  return <div className="flow-startup" role="status" aria-live="polite" aria-busy="true">
    <div className="flow-startup__content">
      <span className="flow-startup__pulse" aria-hidden="true" />
      <svg className="flow-startup__mark" viewBox="0 0 48 48" fill="currentColor" aria-hidden="true">
        <path d="M12 8a4 4 0 0 0-4 4v24a4 4 0 0 0 8 0v-6h13a4 4 0 0 0 0-8H16v-6h20a4 4 0 0 0 0-8H12Z" />
        <circle cx="35" cy="36" r="4" />
      </svg>
      <span className="flow-startup__accessible">{t('Loading…')}</span>
      <span className="flow-startup__text" aria-hidden="true">{t('Loading…')}</span>
    </div>
  </div>
}
