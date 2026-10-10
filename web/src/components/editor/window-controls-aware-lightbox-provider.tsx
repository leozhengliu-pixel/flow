import { useMemo, type ReactNode } from 'react'
import {
  WindowControlsInsetsContext,
  ZERO_INSETS,
  type WindowControlsInsets,
} from './window-controls-insets-context'

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
