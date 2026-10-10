import { createContext, useContext } from 'react'

/** LS-0654 stub — Web has no Electron traffic lights; insets stay 0 until desktop shell. */
export type WindowControlsInsets = { left: number; right: number }

export const ZERO_INSETS: WindowControlsInsets = { left: 0, right: 0 }

export const WindowControlsInsetsContext = createContext<WindowControlsInsets>(ZERO_INSETS)

export function useWindowControlsInsets() {
  return useContext(WindowControlsInsetsContext)
}
