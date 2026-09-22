import { createContext, useContext, useMemo, type ReactNode } from 'react'

/** LS-0654 stub — Web has no Electron traffic lights; insets stay 0 until desktop shell. */
export type WindowControlsInsets = { left: number; right: number }

const ZERO_INSETS: WindowControlsInsets = { left: 0, right: 0 }

export const WindowControlsInsetsContext = createContext<WindowControlsInsets>(ZERO_INSETS)

export function WindowControlsAwareLightboxProvider({
  children,
  windowControlsInsets,
}: {
  children: ReactNode
  /** Override for tests; production web always uses {0,0}. */
  windowControlsInsets?: WindowControlsInsets
}) {
  const insets = useMemo(() => windowControlsInsets ?? ZERO_INSETS, [windowControlsInsets])
  return <WindowControlsInsetsContext.Provider value={insets}>{children}</WindowControlsInsetsContext.Provider>
}

export function useWindowControlsInsets() {
  return useContext(WindowControlsInsetsContext)
}
