import * as React from 'react'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'
import { cn } from '@/lib/utils'
import { tooltipShortcutKeys, tooltipTone, type TooltipTone } from './tooltip-shortcut'

/**
 * The app-wide tooltip, measured against the reference app: 450ms hover delay, then instant tooltips for 400ms
 * after one closes (moving along a toolbar), 8px from the trigger, no arrow, closes without an exit animation.
 * One surface for every tooltip: label, then the shortcut as boxed keys ("G then I" puts a plain "then" between
 * steps). Tooltips over raised surfaces use a lighter palette, one step per level: `elevated` over the agent
 * composer card, `raised` over dialogs and floating panels. Pass the prop, or mark an ancestor of the trigger with
 * `data-tooltip-elevated` / `data-tooltip-raised`.
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
  /** Lightest palette, for tooltips over the agent composer card; also inferred from a `data-tooltip-elevated` ancestor. */
  elevated?: boolean
  /** Lighter palette for tooltips over dialogs and floating panels; also inferred from a `data-tooltip-raised` ancestor. */
  raised?: boolean
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
export function FlowTooltip({ label, shortcut, children, side = 'bottom', align = 'center', disabled = false, contentClassName, sideOffset, elevated, raised }: FlowTooltipProps) {
  const [open, setOpen] = React.useState(false)
  const [inferredTone, setInferredTone] = React.useState<TooltipTone | undefined>()
  const triggerRef = React.useRef<HTMLButtonElement>(null)
  if (disabled && open) setOpen(false)
  if (!label && !shortcut) return children
  // An explicit prop (even `false`) overrides what the surroundings suggest.
  const tone: TooltipTone | undefined = elevated ? 'elevated' : raised ? 'raised' : elevated === false || raised === false ? undefined : inferredTone
  return (
    <TooltipRoot open={open && !disabled} onOpenChange={next => {
      if (next) setInferredTone(tooltipTone(triggerRef.current))
      setOpen(next && !disabled)
    }}>
      <TooltipTrigger asChild ref={triggerRef}>{children}</TooltipTrigger>
      <TooltipContent align={align} className={cn(tone && `flow-tooltip-content--${tone}`, contentClassName)} side={side} sideOffset={sideOffset}>
        <TooltipBody label={label} shortcut={shortcut}/>
      </TooltipContent>
    </TooltipRoot>
  )
}

/** FlowTooltip for components that may render outside the app's TooltipProvider (shared bars, tests). */
export function ScopedFlowTooltip(props: FlowTooltipProps) {
  return <TooltipProvider><FlowTooltip {...props}/></TooltipProvider>
}
