import { useState } from 'react'
import { ChevronLeft, ChevronRight, Inbox, MessageCircle, PanelsTopLeft, Search, Zap } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { chatPlatformOf } from '@/lib/chat-platform'
import { getIntegrationCatalogEntry, integrationDisplayName, isInstalledIntegrationConnection, primaryIntegrations, type IntegrationCatalogEntry } from '@/lib/integration-catalog'
import type { BootstrapData } from '@/types/flow'
import { IntegrationBrandIcon } from './integration-brand-icon'
import { FlowProductName } from '@/components/ui/flow-product-name'
import './desktop-settings.css'

export function DesktopIntegrations({ data, onOpen }: { data: BootstrapData; onOpen: (slug: string) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState('slack')
  const primary = primaryIntegrations(chatPlatformOf(data)).filter(item => item.availability === 'supported')
  const intake: IntegrationCatalogEntry = { ...getIntegrationCatalogEntry('slack-asks')!, name: t('Flow Asks for Slack'), connectProvider: 'slack' }
  const catalog = [primary[0], intake, ...primary.slice(1)]
  const normalized = query.trim().toLowerCase()
  const matches = catalog.filter(item => `${integrationDisplayName(item, t)} ${t(item.description)}`.toLowerCase().includes(normalized))
  const installed = primary.filter(item => data.integrationConnections.some(connection => connection.provider === item.connectProvider && isInstalledIntegrationConnection(connection)))
  const featuredOrder = [intake, ...['slack', 'github', 'figma', 'intercom', 'gitlab', 'zapier', 'jira'].map(slug => getIntegrationCatalogEntry(slug)!), ...primary.filter(item => item.connectProvider === 'feishu' || item.connectProvider === 'lark')]
  const groups = normalized
    ? [{ title: 'Search results', items: matches }]
    : ['Essentials', ...new Set(catalog.map(item => item.category))].filter((title, index, all) => all.indexOf(title) === index).map(title => ({ title, items: catalog.filter(item => item.category === title) })).filter(group => group.items.length)
  const featured = featuredOrder.find(item => item.slug === selected) ?? featuredOrder[0]
  const move = (direction: number) => setSelected(featuredOrder[(featuredOrder.indexOf(featured) + direction + featuredOrder.length) % featuredOrder.length].slug)
  const icon = (provider: IntegrationCatalogEntry['connectProvider'], size: number) => provider && <IntegrationBrandIcon provider={provider} size={size}/>;
  const entryIcon = (item: IntegrationCatalogEntry, size: number) => {
    if (item.slug === intake.slug) return <Inbox size={size} aria-hidden="true"/>
    if (item.connectProvider) return icon(item.connectProvider, size)
    const Glyph = item.slug === 'figma' ? PanelsTopLeft : item.slug === 'intercom' ? MessageCircle : Zap
    return <Glyph size={size} aria-hidden="true"/>
  }
  const open = (item: IntegrationCatalogEntry) => onOpen(item.slug === intake.slug ? 'slack' : item.slug)
  return <div className="feature-integrations desktop-integrations"><div className="feature-settings">
    <header className="feature-header"><h1>{t('Integrations')}</h1><p>{t('Enhance your Flow experience with a wide variety of add-ons and integrations')}</p></header>
    <label className="desktop-integration-search"><Search size={16}/><input aria-label={t('Search integrations')} placeholder={t('Search integrations')} value={query} onChange={event => setQuery(event.target.value)}/></label>
    {installed.length > 0 && <section className="desktop-enabled">
      <h2><button type="button" onClick={() => onOpen('enabled')}>{t('Enabled')}</button></h2>
      <div className="desktop-enabled-cards">{installed.map(item => {
        const connection = data.integrationConnections.find(connection => connection.provider === item.connectProvider)!
        const state = connection.status === 'connected' ? 'Enabled' : connection.status === 'error' ? 'Connection failed' : 'Configured'
        return <button type="button" className="desktop-enabled-card" aria-label={`${integrationDisplayName(item, t)} ${t(state)}`} key={item.slug} onClick={() => onOpen(item.slug)}>{icon(item.connectProvider, 32)}<span><strong>{integrationDisplayName(item, t)}</strong><small>{t(state)}</small></span></button>
      })}</div>
    </section>}
    {!normalized && featured && <section className="desktop-integration-discovery" aria-label={t('Discover integrations')}>
      <button type="button" className="desktop-integration-feature" aria-label={`${t(featured.category)} ${integrationDisplayName(featured, t)} ${t(featured.description)}`} onClick={() => open(featured)}>
        <span className="desktop-integration-category">{t(featured.category)}</span>
        <span className="desktop-integration-preview" aria-hidden="true">{entryIcon(featured, 112)}<ChevronRight size={28}/><img src="/email/flow-mark-128.png" alt="" width="112" height="112"/></span>
        <span className="desktop-integration-feature-copy">{entryIcon(featured, 56)}<span><span>{integrationDisplayName(featured, t)}</span><span>{t(featured.description)}</span></span></span>
      </button>
      <div className="desktop-integration-strip">
        <div role="tablist" aria-label={t('Featured integrations')}>{featuredOrder.map((item, index) => <button type="button" key={item.slug} role="tab" tabIndex={item.slug === featured.slug ? 0 : -1} aria-selected={item.slug === featured.slug} onClick={event => { setSelected(item.slug); event.currentTarget.scrollIntoView?.({ block: 'nearest', inline: 'nearest' }) }} onKeyDown={event => {
          const next = event.key === 'ArrowRight' ? (index + 1) % featuredOrder.length : event.key === 'ArrowLeft' ? (index + featuredOrder.length - 1) % featuredOrder.length : event.key === 'Home' ? 0 : event.key === 'End' ? featuredOrder.length - 1 : undefined
          if (next === undefined) return
          event.preventDefault()
          const tab = event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]
          tab?.focus()
          tab?.click()
        }}>{entryIcon(item, 16)}<span>{item.slug === intake.slug ? <><FlowProductName emphasis/> {t('Asks for Slack')}</> : integrationDisplayName(item, t)}</span></button>)}</div>
        <button type="button" aria-label={t('Previous integration')} title={t('Previous integration')} onClick={() => move(-1)}><ChevronLeft size={16}/></button>
        <button type="button" aria-label={t('Next integration')} title={t('Next integration')} onClick={() => move(1)}><ChevronRight size={16}/></button>
      </div>
    </section>}
    <section className="desktop-integration-catalog" aria-label={t('All integrations')}>
      {groups.map(group => <div className="desktop-integration-group" key={group.title}>
        <h3>{t(group.title)}</h3>
        <ul>{group.items.map(item => <li key={item.slug}><button type="button" className="desktop-integration-card" aria-label={`${integrationDisplayName(item, t)} ${t(item.category)}`} onClick={() => open(item)}>
          <span className="desktop-integration-card-head">{entryIcon(item, 36)}<span>{integrationDisplayName(item, t)}</span></span>
          <span className="desktop-integration-card-copy">{t(item.description)}</span>
        </button></li>)}</ul>
      </div>)}
      {!matches.length && <p>{t('No integrations found')}</p>}
    </section>
  </div></div>
}
