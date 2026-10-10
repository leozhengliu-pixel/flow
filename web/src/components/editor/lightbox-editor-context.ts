import { createContext, useContext, type MutableRefObject } from 'react'
import { openLightbox, type LightboxItem } from './lightbox-bridge'

export type LightboxEditorContextValue = {
  open: (items: LightboxItem[], index?: number) => void
  close: () => void
  editorInstanceRef: MutableRefObject<unknown>
  activeIndex: number
  items: LightboxItem[]
}

export const LightboxEditorContext = createContext<LightboxEditorContextValue | null>(null)

export function useLightboxEditor() {
  const value = useContext(LightboxEditorContext)
  if (!value) throw new Error('useLightboxEditor must be used within LightboxEditorProvider')
  return value
}

export function useOptionalLightboxEditor() {
  return useContext(LightboxEditorContext)
}

/** Prefer React portal; fall back to legacy DOM overlay when provider is absent. */
export function openDescriptionLightboxViaProvider(src: string, alt = '', siblings?: LightboxItem[]) {
  const items = siblings?.length ? siblings : [{ src, alt }]
  const index = Math.max(0, items.findIndex(item => item.src === src))
  if (openLightbox(items, index === -1 ? 0 : index)) return true
  return false
}
