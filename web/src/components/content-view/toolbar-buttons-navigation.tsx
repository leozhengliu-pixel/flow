import {
  forwardRef,
  useCallback,
  useRef,
  type HTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

function mergeRefs<T>(...refs: Array<Ref<T> | undefined>) {
  return (value: T | null) => {
    for (const ref of refs) {
      if (!ref) continue
      if (typeof ref === 'function') ref(value)
      else (ref as { current: T | null }).current = value
    }
  }
}

export interface ToolbarButtonsNavigationProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode
  /** CSS selector for focusable toolbar controls. */
  focusSelector?: string
}

/**
 * LS-0610 — arrow-key navigation across toolbar icon buttons
 * (Linear `ToolbarButtonsNavigation`).
 */
export const ToolbarButtonsNavigation = forwardRef<HTMLDivElement, ToolbarButtonsNavigationProps>(
  function ToolbarButtonsNavigation(
    { children, className, focusSelector = 'button:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])', onKeyDown, ...rest },
    ref,
  ) {
    const localRef = useRef<HTMLDivElement>(null)

    const onArrowNavigationKeyDown = useCallback(
      (event: KeyboardEvent<HTMLDivElement>) => {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
        const root = localRef.current
        if (!root) return
        const items = Array.from(root.querySelectorAll<HTMLElement>(focusSelector)).filter(
          el => !el.hasAttribute('disabled') && el.getAttribute('aria-disabled') !== 'true',
        )
        if (!items.length) return
        event.preventDefault()
        const active = document.activeElement as HTMLElement | null
        const index = active ? items.indexOf(active) : -1
        const direction = event.key === 'ArrowLeft' ? -1 : 1
        if (index < 0) {
          ;(direction < 0 ? items[items.length - 1] : items[0])?.focus()
          return
        }
        const next = items[index + direction]
        next?.focus()
      },
      [focusSelector],
    )

    return (
      <div
        {...rest}
        ref={mergeRefs(ref, localRef)}
        className={cn(styles.toolbarNav, className)}
        data-toolbar-buttons-navigation=""
        onKeyDown={event => {
          onKeyDown?.(event)
          if (!event.defaultPrevented) onArrowNavigationKeyDown(event)
        }}
      >
        {children}
      </div>
    )
  },
)
