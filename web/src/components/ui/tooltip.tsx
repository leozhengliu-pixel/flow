import * as React from 'react'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'
import { cn } from '@/lib/utils'
import { isElevatedTrigger, tooltipShortcutKeys } from './tooltip-shortcut'

/**
 * The app-wide tooltip, measured against the reference app: 450ms hover delay, then instant tooltips for 400ms
 * after one closes (moving along a toolbar), 8px from the trigger, no arrow, closes without an exit animation.
 * One surface for every tooltip: label, then the shortcut as boxed keys ("G then I" puts a plain "then" between
 * steps). Tooltips over elevated surfaces (a composer card, a popover) use the lighter elevated palette: pass
 * `elevated`, or mark an ancestor of the trigger with `data-tooltip-elevated`.
 */
export const TOOLTIP_DELAY = 450
export const TOOLTIP_SKIP_DELAY = 400
export const TOOLTIP_OFFSET = 8

export function TooltipProvider({ delayDuration = TOOLTIP_DELAY, skipDelayDuration = TOOLTIP_SKIP_DELAY, ...props }: TooltipPrimitive.TooltipProviderProps) {
  return <TooltipPrimitive.Provider delayDuration={delayDuration} skipDelayDuration={skipDelayDuration} {...props}/>
}
export const TooltipRoot = TooltipPrimitive.Root
export const TooltipTrigger = TooltipPrimitive.Trigger

export function TooltipContent({
  className,
  side = 'bottom',
  sideOffset = TOOLTIP_OFFSET,
  children,
  ...props
}: TooltipPrimitive.TooltipContentProps) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content data-flow-motion="tooltip"
        className={cn('flow-tooltip-content', className)}
        collisionPadding={8}
        side={side}
        sideOffset={sideOffset}
        {...props}
      >
        {children}
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  )
}

export interface FlowTooltipProps {
  label?: React.ReactNode
  shortcut?: React.ReactNode
  side?: TooltipPrimitive.TooltipContentProps['side']
  align?: TooltipPrimitive.TooltipContentProps['align']
  /** Keeps the tooltip closed, e.g. while the control's own menu is open. */
  disabled?: boolean
  /** Extra class on the tooltip surface, e.g. `flow-tooltip-content--card` for rich content. */
  contentClassName?: string
  /** Gap between the trigger and the tooltip (8px by default). */
  sideOffset?: number
  /** Lighter palette for tooltips over elevated surfaces; also inferred from a `data-tooltip-elevated` ancestor. */
  elevated?: boolean
  children: React.ReactElement
}

/** The shortcut part of a tooltip: one boxed key per key, a plain "then" between sequence steps. */
export function TooltipShortcut({ value }: { value: React.ReactNode }) {
  if (typeof value !== 'string') return <span className="flow-tooltip-shortcut">{value}</span>
  return <span className="flow-tooltip-shortcut">{tooltipShortcutKeys(value).map((step, index) => <React.Fragment key={index}>
    {index > 0 && <span className="flow-tooltip-then">then</span>}
    {step.map((key, keyIndex) => <kbd className="flow-tooltip-key" key={keyIndex}>{key}</kbd>)}
  </React.Fragment>)}</span>
}

/** Label + shortcut, for surfaces that render their own tooltip content element. */
export function TooltipBody({ label, shortcut }: { label?: React.ReactNode; shortcut?: React.ReactNode }) {
  return <>
    {label != null && label !== '' && <span className="flow-tooltip-copy">{label}</span>}
    {shortcut ? <TooltipShortcut value={shortcut}/> : null}
  </>
}

/** Convenience wrapper for icon buttons and compact controls. */
export function FlowTooltip({ label, shortcut, children, side = 'bottom', align = 'center', disabled = false, contentClassName, sideOffset, elevated }: FlowTooltipProps) {
  const [open, setOpen] = React.useState(false)
  const [inferredElevated, setInferredElevated] = React.useState(false)
  const triggerRef = React.useRef<HTMLButtonElement>(null)
  React.useEffect(() => { if (disabled) setOpen(false) }, [disabled])
  if (!label && !shortcut) return children
  return (
    <TooltipRoot open={open && !disabled} onOpenChange={next => {
      if (next) setInferredElevated(isElevatedTrigger(triggerRef.current))
      setOpen(next && !disabled)
    }}>
      <TooltipTrigger asChild ref={triggerRef}>{children}</TooltipTrigger>
      <TooltipContent align={align} className={cn((elevated ?? inferredElevated) && 'flow-tooltip-content--elevated', contentClassName)} side={side} sideOffset={sideOffset}>
        <TooltipBody label={label} shortcut={shortcut}/>
      </TooltipContent>
    </TooltipRoot>
  )
}

/** FlowTooltip for components that may render outside the app's TooltipProvider (shared bars, tests). */
export function ScopedFlowTooltip(props: FlowTooltipProps) {
  return <TooltipProvider><FlowTooltip {...props}/></TooltipProvider>
}
