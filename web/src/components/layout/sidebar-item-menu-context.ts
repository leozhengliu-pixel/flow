import { createContext } from 'react'
import { toast } from '@/lib/toast'
import type { SidebarBadgeMode, SidebarBadgeModes, SidebarBadgeStyle, SidebarEntry, SidebarPreferences, SidebarVisibility } from './sidebar-customization-state'

/** What a sidebar item's right-click menu needs from the sidebar. */
export type SidebarItemMenuValue = {
  preferences: SidebarPreferences
  badgeModes: SidebarBadgeModes
  defaultBadgeStyle: SidebarBadgeStyle
  setVisibility: (entry: SidebarEntry, visibility: SidebarVisibility) => void
  setBadgeMode: (entry: SidebarEntry, mode: SidebarBadgeMode) => void
  onCustomize: () => void
  onMarkAllRead: () => void
  agentHref: string
}

export const SidebarItemMenuContext = createContext<SidebarItemMenuValue | undefined>(undefined)

export function copySidebarLink(href: string, message: string) {
  const url = new URL(href, window.location.origin).href
  void Promise.resolve(navigator.clipboard?.writeText(url)).then(() => toast.success(message)).catch(() => toast.error(message))
}

