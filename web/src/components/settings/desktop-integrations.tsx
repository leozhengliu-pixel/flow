import { useState } from 'react'
import { ChevronLeft, ChevronRight, Search } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { chatPlatformOf } from '@/lib/chat-platform'
import { integrationDisplayName, isInstalledIntegrationConnection, primaryIntegrations, type IntegrationCatalogEntry } from '@/lib/integration-catalog'
import type { BootstrapData } from '@/types/flow'
import { IntegrationBrandIcon } from './integration-brand-icon'
import './desktop-settings.css'

export function DesktopIntegrations({ data, onOpen }: { data: BootstrapData; onOpen: (slug: string) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState('slack')
  const catalog = primaryIntegrations(chatPlatformOf(data)).filter(item => item.availability === 'supported')
  const normalized = query.trim().toLowerCase()
  const matches = catalog.filter(item => `${integrationDisplayName(item, t)} ${t(item.description)}`.toLowerCase().includes(normalized))
  const installed = catalog.filter(item => data.integrationConnections.some(connection => connection.provider === item.connectProvider && isInstalledIntegrationConnection(connection)))
  const featured = catalog.find(item => item.slug === selected) ?? catalog[0]
  const move = (direction: number) => setSelected(catalog[(catalog.indexOf(featured) + direction + catalog.length) % catalog.length].slug)
  const icon = (provider: IntegrationCatalogEntry['connectProvider'], size: number) => provider && <IntegrationBrandIcon provider={provider} size={size}/>;
  return <div className="feature-integrations desktop-integrations"><div className="feature-settings">
    <header className="feature-header"><h1>{t('Integrations')}</h1><p>{t('Enhance your Flow experience with a wide variety of add-ons and integrations')}</p></header>
    <label className="desktop-integration-search"><Search size={16}/><input aria-label={t('Search integrations')} placeholder={t('Search integrations')} value={query} onChange={event => setQuery(event.target.value)}/></label>
    <section className="desktop-enabled">
      <h2><button type="button" onClick={() => onOpen('enabled')}>{t('Enabled')}</button></h2>
      {installed.length ? <div className="desktop-enabled-cards">{installed.map(item => {
        const connection = data.integrationConnections.find(connection => connection.provider === item.connectProvider)!
        const state = connection.status === 'connected' ? 'Enabled' : connection.status === 'error' ? 'Connection failed' : 'Configured'
        return <button type="button" className="desktop-enabled-card" aria-label={`${integrationDisplayName(item, t)} ${t(state)}`} key={item.slug} onClick={() => onOpen(item.slug)}>{icon(item.connectProvider, 32)}<span><strong>{integrationDisplayName(item, t)}</strong><small>{t(state)}</small></span></button>
      })}</div> : <p>{t('No enabled integrations')}</p>}
    </section>
    {!normalized && featured && <section className="desktop-integration-discovery" aria-label={t('Discover integrations')}>
      <button type="button" className="desktop-integration-feature" aria-label={`${t(featured.category)} ${integrationDisplayName(featured, t)} ${t(featured.description)}`} onClick={() => onOpen(featured.slug)}>
        <span className="desktop-integration-category">{t(featured.category)}</span>
        <span className="desktop-integration-preview" aria-hidden="true">{icon(featured.connectProvider, 112)}<ChevronRight size={28}/><img src="/email/flow-mark-128.png" alt="" width="112" height="112"/></span>
        <span className="desktop-integration-feature-copy">{icon(featured.connectProvider, 48)}<span><strong>{integrationDisplayName(featured, t)}</strong><small>{t(featured.description)}</small></span></span>
      </button>
      <div className="desktop-integration-strip" role="tablist" aria-label={t('Featured integrations')}>
        <div>{catalog.map(item => <button type="button" key={item.slug} role="tab" aria-selected={item.slug === featured.slug} onClick={() => setSelected(item.slug)}>{icon(item.connectProvider, 16)}{integrationDisplayName(item, t)}</button>)}</div>
        <button type="button" aria-label={t('Previous integration')} title={t('Previous integration')} onClick={() => move(-1)}><ChevronLeft size={16}/></button>
        <button type="button" aria-label={t('Next integration')} title={t('Next integration')} onClick={() => move(1)}><ChevronRight size={16}/></button>
      </div>
    </section>}
    <section className="desktop-integration-catalog" aria-label={t('All integrations')}>
      <h2>{t(normalized ? 'Search results' : 'All integrations')}</h2>
      <div className="desktop-enabled-cards">{matches.map(item => <button type="button" className="desktop-enabled-card" aria-label={`${integrationDisplayName(item, t)} ${t(item.category)}`} key={item.slug} onClick={() => onOpen(item.slug)}>{icon(item.connectProvider, 32)}<span><strong>{integrationDisplayName(item, t)}</strong><small>{t(item.category)}</small></span></button>)}</div>
      {!matches.length && <p>{t('No integrations found')}</p>}
    </section>
  </div></div>
}
