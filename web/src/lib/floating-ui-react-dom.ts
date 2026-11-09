import { useFloating as useFloatingBase } from '@floating-ui/react-dom'
import { DESKTOP_LAYOUT_QUERY } from './layout-breakpoints'

// Radix Popper (menus, pickers, popovers, tooltips) positions through this module in place of '@floating-ui/react-dom'; see snapFloatingPositions in vite.config.ts.
export * from '@floating-ui/react-dom'

/**
 * Desktop popups sit on whole CSS pixels: a trigger at x 471.59 opens its menu at 472 and a picker 281.25px down opens at 281,
 * never at the half pixel floating-ui's device-pixel rounding gives at 2x. Rounding the unrounded position here (instead of the
 * rounded transform) keeps the nearest pixel. Touch layouts keep the library's own rounding.
 *
 * The reference only snaps its menus and pickers; its panels (display options, document info, subscribers) and the snooze submenu
 * keep the library's device-pixel position, so they land on half pixels (1088.5 / 92.25 opens at 1088.5 / 92.5). A popup opts into that
 * with `data-popper-device-pixels` on its content element.
 */
export function useFloating(options?: Parameters<typeof useFloatingBase>[0]): ReturnType<typeof useFloatingBase> {
  const result = useFloatingBase(options)
  const { floatingStyles, isPositioned, x, y } = result
  if (!isPositioned || !floatingStyles.transform || typeof window === 'undefined' || !window.matchMedia(DESKTOP_LAYOUT_QUERY).matches) return result
  if (result.refs.floating.current?.firstElementChild?.hasAttribute('data-popper-device-pixels')) return result
  return { ...result, floatingStyles: { ...floatingStyles, transform: `translate(${Math.round(x)}px, ${Math.round(y)}px)` } }
}
