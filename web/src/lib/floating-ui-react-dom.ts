import { useFloating as useFloatingBase } from '@floating-ui/react-dom'
import { DESKTOP_LAYOUT_QUERY } from './layout-breakpoints'

// Radix Popper (menus, pickers, popovers, tooltips) positions through this module in place of '@floating-ui/react-dom'; see snapFloatingPositions in vite.config.ts.
export * from '@floating-ui/react-dom'

/**
 * Desktop popups sit on whole CSS pixels: a trigger at x 471.59 opens its menu at 472 and a picker 281.25px down opens at 281,
 * never at the half pixel floating-ui's device-pixel rounding gives at 2x. Rounding the unrounded position here (instead of the
 * rounded transform) keeps the nearest pixel. Touch layouts keep the library's own rounding.
 */
export function useFloating(options?: Parameters<typeof useFloatingBase>[0]): ReturnType<typeof useFloatingBase> {
  const result = useFloatingBase(options)
  const { floatingStyles, isPositioned, x, y } = result
  if (!isPositioned || !floatingStyles.transform || typeof window === 'undefined' || !window.matchMedia(DESKTOP_LAYOUT_QUERY).matches) return result
  return { ...result, floatingStyles: { ...floatingStyles, transform: `translate(${Math.round(x)}px, ${Math.round(y)}px)` } }
}
