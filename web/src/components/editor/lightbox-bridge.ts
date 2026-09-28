/** Imperative bridge so TipTap DOM image views can open the React lightbox portal. */

export type LightboxItem = {
  src: string
  alt?: string
  title?: string
}

type OpenHandler = (items: LightboxItem[], index?: number) => void

let openHandler: OpenHandler | null = null

export function registerLightboxOpener(handler: OpenHandler | null) {
  openHandler = handler
}

export function openLightbox(items: LightboxItem[], index = 0) {
  if (!items.length) return false
  if (!openHandler) return false
  openHandler(items, Math.max(0, Math.min(index, items.length - 1)))
  return true
}

export function isLightboxRegistered() {
  return Boolean(openHandler)
}
