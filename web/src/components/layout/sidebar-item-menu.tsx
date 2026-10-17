import * as ContextMenu from '@radix-ui/react-context-menu'
import { Check, ChevronRight, Plus } from 'lucide-react'
import { useContext, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useI18n } from '@/i18n/i18n'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { SidebarItemMenuContext, copySidebarLink } from './sidebar-item-menu-context'
import {
  sidebarEntryHasBadge,
  sidebarVisibilityOptions,
  type SidebarBadgeMode,
  type SidebarBadgeStyle,
  type SidebarEntry,
  type SidebarVisibility,
} from './sidebar-customization-state'

const VISIBILITY_LABELS: Record<SidebarVisibility, string> = { always: 'Always show', badged: 'Show when badged', never: "Don't show" }
const BADGE_LABELS: Record<Exclude<SidebarBadgeMode, 'default'>, string> = { count: 'Count', dot: 'Dot', none: 'None' }

function MenuIcon({ children }: { children: ReactNode }) {
  return <svg className="sidebar-item-menu-icon" viewBox="0 0 16 16" aria-hidden="true">{children}</svg>
}
const EyeIcon = () => <MenuIcon><path d="M8 4C4.9 4 2.9 6.2 2 8c.9 1.8 2.9 4 6 4s5.1-2.2 6-4c-.9-1.8-2.9-4-6-4Z" /><circle cx="8" cy="8" r="1.8" /></MenuIcon>
const BadgeIcon = () => <MenuIcon><path d="M9.5 3H5a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2V6.5" /><circle cx="12" cy="4" r="1.75" /></MenuIcon>
const CustomizeIcon = () => <MenuIcon><path d="M7 3H5a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2V9" /><path d="m7 9 .3-1.7 5-5a1.1 1.1 0 0 1 1.5 1.5l-5 5L7 9Z" /></MenuIcon>
const MarkReadIcon = () => <MenuIcon><path d="M3 9.5 5 4h6l2 5.5V12H3V9.5Z" /><path d="m6 8 1.5 1.5L10 6.5" /></MenuIcon>

function BadgePreview({ style }: { style: SidebarBadgeStyle }) {
  return <span className={`badge-preview is-${style}`} aria-hidden="true">{style === 'count' ? '1' : ''}</span>
}

export function SidebarItemMenu({ entry, href, children }: { entry?: SidebarEntry; href?: string; children: ReactNode }) {
  const context = useContext(SidebarItemMenuContext)
  const { t } = useI18n()
  const navigate = useNavigate()
  const [filter, setFilter] = useState('')
  if (!context) return <>{children}</>
  const hasBadge = entry !== undefined && sidebarEntryHasBadge(entry)
  const visibility = entry ? context.preferences[entry] : undefined
  const mode: SidebarBadgeMode = (entry && context.badgeModes[entry]) || 'default'
  const defaultLabel = `${t('Default')} (${t(BADGE_LABELS[context.defaultBadgeStyle])})`
  const badgeOptions: { value: SidebarBadgeMode; label: string }[] = [
    { value: 'default', label: defaultLabel },
    { value: 'count', label: t('Count') },
    { value: 'dot', label: t('Dot') },
    { value: 'none', label: t('None') },
  ]
  const needle = filter.trim().toLocaleLowerCase()
  const shownBadgeOptions = badgeOptions.filter(option => !needle || option.label.toLocaleLowerCase().includes(needle))
  const extra = entry === 'inbox'
    ? <ContextMenu.Item onSelect={context.onMarkAllRead}><MarkReadIcon /><span>{t('Mark all as read')}</span></ContextMenu.Item>
    : entry === 'agent'
      ? <ContextMenu.Item onSelect={() => navigate(context.agentHref)}><Plus className="sidebar-item-menu-icon" /><span>{t('New chat')}</span></ContextMenu.Item>
      : undefined
  return (
    <ContextMenu.Root onOpenChange={open => { if (!open) setFilter('') }}>
      <ContextMenu.Trigger asChild><span className="sidebar-item-menu-trigger">{children}</span></ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content data-flow-motion="floating" className="sidebar-popover sidebar-team-menu sidebar-item-menu" collisionPadding={8}>
          {extra && <>{extra}<ContextMenu.Separator /></>}
          {entry && visibility && (
            <ContextMenu.Sub>
              <ContextMenu.SubTrigger><EyeIcon /><span>{t('Visibility')}</span><ChevronRight className="menu-chevron" /></ContextMenu.SubTrigger>
              <ContextMenu.Portal>
                <ContextMenu.SubContent data-flow-motion="floating" className="sidebar-popover sidebar-team-menu sidebar-item-menu sidebar-item-submenu" sideOffset={-7} alignOffset={-7} collisionPadding={8}>
                  <ContextMenu.RadioGroup value={visibility} onValueChange={value => context.setVisibility(entry, value as SidebarVisibility)}>
                    {sidebarVisibilityOptions(entry).map(option => (
                      <ContextMenu.RadioItem key={option} value={option}>
                        <span>{t(VISIBILITY_LABELS[option])}</span>
                        <span className="sidebar-item-menu-end">{visibility === option ? <Check /> : null}</span>
                      </ContextMenu.RadioItem>
                    ))}
                  </ContextMenu.RadioGroup>
                </ContextMenu.SubContent>
              </ContextMenu.Portal>
            </ContextMenu.Sub>
          )}
          {hasBadge && entry && (
            <ContextMenu.Sub>
              <ContextMenu.SubTrigger><BadgeIcon /><span>{t('Badge')}</span><ChevronRight className="menu-chevron" /></ContextMenu.SubTrigger>
              <ContextMenu.Portal>
                <ContextMenu.SubContent data-flow-motion="floating" className="sidebar-popover sidebar-team-menu sidebar-item-menu sidebar-item-submenu" sideOffset={-7} alignOffset={-44} collisionPadding={8}>
                  <div className="sidebar-item-menu-filter">
                    <input
                      aria-label={t('Filter...')}
                      placeholder={t('Filter...')}
                      value={filter}
                      onChange={event => setFilter(event.target.value)}
                      onKeyDown={event => { if (event.key !== 'Escape' && event.key !== 'ArrowLeft') event.stopPropagation() }}
                    />
                  </div>
                  <ContextMenu.RadioGroup value={mode} onValueChange={value => context.setBadgeMode(entry, value as SidebarBadgeMode)}>
                    {shownBadgeOptions.map(option => (
                      <ContextMenu.RadioItem key={option.value} value={option.value}>
                        <span>{option.label}</span>
                        <span className="sidebar-item-menu-end">
                          {mode === option.value ? <Check /> : option.value === 'count' || option.value === 'dot' ? <BadgePreview style={option.value} /> : null}
                        </span>
                      </ContextMenu.RadioItem>
                    ))}
                  </ContextMenu.RadioGroup>
                </ContextMenu.SubContent>
              </ContextMenu.Portal>
            </ContextMenu.Sub>
          )}
          {entry && <ContextMenu.Item onSelect={context.onCustomize}><CustomizeIcon /><span>{t('Customize sidebar')}</span></ContextMenu.Item>}
          {entry && href && <ContextMenu.Separator />}
          {href && (
            <ContextMenu.Item onSelect={() => copySidebarLink(href, t('Link copied to clipboard'))}>
              <ViewGlyph color="currentColor" icon="Link" /><span>{t('Copy link')}</span>
            </ContextMenu.Item>
          )}
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  )
}
