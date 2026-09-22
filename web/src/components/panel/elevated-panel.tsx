/**
 * LS-0236 ElevatedPanel — elevation 0/-1/1/2/3 panel primitive over design tokens.
 */
import {
  forwardRef,
  type CSSProperties,
  type HTMLAttributes,
  type ReactNode,
} from 'react'
import { cn } from '@/lib/utils'
import './elevated-panel.css'

export type ElevatedPanelElevation = -1 | 0 | 1 | 2 | 3

export type ElevatedPanelProps = HTMLAttributes<HTMLDivElement> & {
  children?: ReactNode
  /** 0=base · -1=sub · 1 (default) · 2/3 raised */
  elevation?: ElevatedPanelElevation
  disableBorder?: boolean
  disableShadow?: boolean
  highlightBorder?: boolean
  /** Skip elevated theme wrapper attribute (still applies panel styles). */
  skipElevation?: boolean
  /** Border token role when border is enabled. */
  border?: 'faint' | 'default' | 'strong'
}

export const ElevatedPanel = forwardRef<HTMLDivElement, ElevatedPanelProps>(
  function ElevatedPanel(
    {
      children,
      className,
      style,
      elevation = 1,
      disableBorder = false,
      disableShadow = false,
      highlightBorder = false,
      skipElevation = false,
      border = 'faint',
      ...rest
    },
    ref,
  ) {
    const panelStyle = {
      ...style,
    } as CSSProperties

    return (
      <div
        ref={ref}
        className={cn(
          'flow-elevated-panel',
          !disableBorder && elevation !== 0 && `flow-elevated-panel--border-${border}`,
          !!elevation && !disableShadow && `flow-elevated-panel--shadow-${elevation}`,
          highlightBorder && 'flow-elevated-panel--highlight',
          className,
        )}
        data-elevated-panel=""
        data-elevation={skipElevation ? undefined : elevation}
        data-highlight-border={highlightBorder ? 'true' : undefined}
        style={panelStyle}
        {...rest}
      >
        {children}
      </div>
    )
  },
)

export default ElevatedPanel
