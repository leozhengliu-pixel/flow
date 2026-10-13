import type { CSSProperties, HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { useOptionalTranslate } from '@/i18n/i18n'
import { LOADING_CARET_DELAY, LOADING_FOOTER_CARET_DELAY, useLoadingState } from './loading-state'
import './loading-caret.css'

/**
 * Loading states of issue lists and issue pages, matching the reference app:
 * no skeletons and no "Loading…" text. The content area stays blank (or keeps the
 * previous content, fading it out after 500ms) and a blinking text-cursor caret
 * appears after a delay. Fast loads therefore never flash anything.
 */

type LoadingCaretProps = {
  /** Milliseconds before the caret becomes visible. */
  delay?: number
  /** Absolutely placed at the top-left of the nearest positioned ancestor (14px down, 10px in). */
  positioned?: boolean
  /** With `positioned`, flush with the left edge instead of 10px in. */
  noPadding?: boolean
  /** Accessible name of the loading status. */
  label?: string
  className?: string
}

export function LoadingCaret({ delay = LOADING_CARET_DELAY, positioned = false, noPadding = false, label, className }: LoadingCaretProps) {
  const t = useOptionalTranslate()
  const style = delay === LOADING_CARET_DELAY ? undefined : { animationDelay: `${delay}ms` } as CSSProperties
  return <div className={cn('flow-loading-caret', positioned && 'is-positioned', positioned && noPadding && 'is-flush', className)} data-loading-caret="" role="status" aria-label={t(label ?? 'Loading…')} style={style}/>
}

type LoadingContainerProps = Omit<HTMLAttributes<HTMLDivElement>, 'children'> & {
  loading: boolean
  /** Keep the content visible (the caret still shows) instead of fading it out. */
  keepContentVisible?: boolean
  noPadding?: boolean
  label?: string
  children?: ReactNode
}

/**
 * A positioned frame around content that may be loading. While loading, the
 * content becomes inert and fades out after 500ms (100ms ease-in); the caret
 * shows from 1s. Loaded content appears at once.
 */
export function LoadingContainer({ loading, keepContentVisible = false, noPadding = false, label, className, children, ...props }: LoadingContainerProps) {
  const active = useLoadingState(loading)
  const hidden = active && !keepContentVisible
  return <div className={cn('flow-loading-frame', className)} aria-busy={active || undefined} {...props}>
    {active ? <LoadingCaret positioned noPadding={noPadding} label={label}/> : null}
    <div className={cn('flow-loading-content', hidden && 'is-loading')} inert={active || undefined}>{children}</div>
  </div>
}

/** The footer of a list while its next page loads: a 50px row with the caret 24px in, after 350ms. */
export function LoadingListFooter({ label, className }: { label?: string; className?: string }) {
  return <div className={cn('flow-loading-footer', className)}><LoadingCaret delay={LOADING_FOOTER_CARET_DELAY} label={label}/></div>
}
