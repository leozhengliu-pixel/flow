import { Command } from 'cmdk'
import { useState } from 'react'
import { PhoneMenu } from '@/components/ui/phone-menu'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { SettingsGearIcon } from '@/components/releases/release-icons'
import { ConnectorBrandIcon } from '@/components/settings/connector-brand-icons'
import { useI18n } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import { PhoneHelpGlyph } from './phone-help-glyphs'
import './phone-help-menu.css'

function openExternal(url: string) {
  window.open(url, '_blank', 'noopener,noreferrer')
}

export function PhoneHelpMenu({ open, onOpenChange, onSettings, onShortcuts }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSettings: () => void
  onShortcuts: () => void
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const repository = 'https://github.com/leozhengliu-pixel/flow'
  const sendFlare = () => {
    const url = new URL(`${repository}/issues/new`)
    url.searchParams.set('title', t('Support diagnostics'))
    url.searchParams.set('body', `${t('Browser:')} ${navigator.userAgent}\n${t('Page:')} ${location.pathname}\n\n${t('Describe the problem:')}\n`)
    openExternal(url.toString())
  }
  const entries = [
    { id: 'docs', label: 'Docs', icon: <ViewGlyph icon="Book" color="var(--help-icon-color)"/>, run: () => openExternal(`${repository}/tree/main/docs`) },
    { id: 'contact', label: 'Contact us', icon: <ViewGlyph icon="ChatLine" color="var(--help-icon-color)"/>, run: () => openExternal(`${repository}/issues/new/choose`) },
    { id: 'send-flare', label: 'Send flare', icon: <SettingsGearIcon/>, run: sendFlare },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: <PhoneHelpGlyph name="keyboard"/>, run: onShortcuts },
    { id: 'status', label: 'Flow status', icon: <PhoneHelpGlyph name="status"/>, run: () => openExternal(`${location.origin}/api/health`) },
    { id: 'download-apps', label: 'Download apps', icon: <PhoneHelpGlyph name="devices"/>, run: () => openExternal(`${repository}/releases`) },
    { id: 'settings', label: 'Settings', icon: <SettingsGearIcon/>, run: onSettings },
    { id: 'slack', label: 'Slack community', icon: <ConnectorBrandIcon name="Slack Personal"/>, run: () => toast.info(t('No Slack community is configured for this workspace.')) },
    { id: 'changelog', label: 'Full changelog', icon: <ViewGlyph icon="BookOpen" color="var(--help-icon-color)"/>, run: () => openExternal(`${repository}/blob/main/CHANGELOG.md`) },
  ].filter(entry => t(entry.label).toLocaleLowerCase().includes(query.toLocaleLowerCase()))
  return <PhoneMenu className="help-phone-menu" heading={t('Help')} label={t('Help')} open={open} onOpenChange={value => { setQuery(''); onOpenChange(value) }}>
    <Command label={t('Help with...')} shouldFilter={false}>
      <div className="phone-menu-search"><div className="help-phone-input"><Command.Input aria-label={t('Help with...')} placeholder={t('Help with...')} value={query} onValueChange={setQuery}/></div></div>
      <Command.List>
        <div className="help-phone-rows" style={{ height: entries.length * 46 + (query ? 0 : 30) }}>
          {!query && <div className="help-phone-news-heading" style={{ top: 8 * 46 }}>{t('What’s new')}</div>}
          {entries.map((entry, index) => <Command.Item key={entry.id} value={entry.label} data-help-entry={entry.id} style={{ top: index * 46 + (!query && entry.id === 'changelog' ? 30 : 0) }} onSelect={() => { onOpenChange(false); setQuery(''); entry.run() }}>
            {entry.icon}<span>{t(entry.label)}</span>
          </Command.Item>)}
        </div>
        <Command.Empty>{t('No results')}</Command.Empty>
      </Command.List>
    </Command>
  </PhoneMenu>
}
