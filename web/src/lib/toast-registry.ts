import { useSyncExternalStore, type ReactNode } from 'react'

/**
 * Shared state of the notification (toast) system that is not React rendering: the content model, the
 * navigation hook the Toaster installs, and the registry that powers "Open toast link" (⌘ ⌥ O).
 */
/** "Open toast link" keyboard shortcut, in the reference app's notation. */
export const OPEN_TOAST_LINK_SHORTCUT = '⌘ ⌥ O'

export type ToastKind = 'default' | 'success' | 'error' | 'warning' | 'info' | 'loading'

/** A blue link under the message ("View release", "Undo"). */
export type ToastLink = {
  label: ReactNode
  onClick?: () => void
  /** App path (react-router) or absolute URL (new tab). */
  href?: string
}

/** The entity a toast is about, shown as a chip with its icon and title. Clicking opens it. */
export type ToastEntity = {
  title: ReactNode
  /** Short identifier shown before the title, e.g. "FLO-12". */
  identifier?: string
  icon?: ReactNode
  href?: string
  onClick?: () => void
}

export type ToastContent = {
  kind: ToastKind
  title: ReactNode
  description?: ReactNode
  /** `false` hides the leading icon; a node replaces the per-kind icon. */
  icon?: ReactNode | false
  entities: ToastEntity[]
  links: ToastLink[]
  /** Hides the dismiss button (kept for toasts that must stay until they resolve). */
  closeButton: boolean
}

type Navigate = (to: string) => void
let navigate: Navigate | null = null

/** The Toaster lives under the router; call sites do not, so it hands over its `navigate`. */
export function setToastNavigator(next: Navigate | null) {
  navigate = next
}

export function openToastHref(href: string) {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) window.open(href, '_blank', 'noopener,noreferrer')
  else if (navigate) navigate(href)
  else window.location.assign(href)
}

export function runToastLink(link: Pick<ToastLink, 'onClick' | 'href'>) {
  link.onClick?.()
  if (link.href) openToastHref(link.href)
}

/** The action "Open toast link" performs for a toast: its first link, else its first chip. */
export function primaryToastTarget(content: Pick<ToastContent, 'links' | 'entities'>): (() => void) | undefined {
  const link = content.links[0]
  if (link && (link.onClick || link.href)) return () => runToastLink(link)
  const entity = content.entities.find(item => item.onClick || item.href)
  if (entity) return () => runToastLink(entity)
  return undefined
}

type LinkToast = { id: string | number; open: () => void }
let linkToasts: LinkToast[] = []
const listeners = new Set<() => void>()
const emit = () => listeners.forEach(listener => listener())
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }

/** Registers a mounted toast that can be opened from the keyboard; newest registration wins. */
export function registerToastLink(id: string | number, open: () => void) {
  linkToasts = [...linkToasts.filter(item => item.id !== id), { id, open }]
  emit()
  return () => { linkToasts = linkToasts.filter(item => item.id !== id); emit() }
}

/** Runs the newest toast's link. Returns whether there was one. */
export function openLatestToastLink(): boolean {
  const latest = linkToasts[linkToasts.length - 1]
  if (!latest) return false
  latest.open()
  return true
}

/** Whether `id` is the toast the "Open toast link" shortcut would open (drives the tooltip hint). */
export function useIsLatestLinkToast(id: string | number) {
  return useSyncExternalStore(subscribe, () => linkToasts[linkToasts.length - 1]?.id === id, () => false)
}

export function resetToastRegistry() {
  linkToasts = []
  navigate = null
  emit()
}
