import * as React from 'react'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'
import { cn } from '@/lib/utils'

/** Shared tooltip primitives. The product tooltip uses a short delayed hover and
 * keeps the delay when moving between adjacent controls. */
export const TooltipProvider = TooltipPrimitive.Provider
export const TooltipRoot = TooltipPrimitive.Root
export const TooltipTrigger = TooltipPrimitive.Trigger

export function TooltipContent({
  className,
  side = 'bottom',
  sideOffset = 6,
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
  /** Extra class on the tooltip surface, e.g. `flow-tooltip-content--title` for multi-line content. */
  contentClassName?: string
  children: React.ReactElement
}

/** Renders a shortcut; key sequences written as "P then S" get Linear's "then" separator. */
export function TooltipShortcut({ value }: { value: React.ReactNode }) {
  if (typeof value !== 'string' || !value.includes(' then ')) return <>{value}</>
  const keys = value.split(' then ')
  return <>{keys.map((key, index) => <React.Fragment key={index}>{index > 0 && <span className="flow-tooltip-then">then</span>}<span>{key}</span></React.Fragment>)}</>
}

/** Convenience wrapper for icon buttons and compact controls. */
export function FlowTooltip({ label, shortcut, children, side = 'bottom', align = 'center', disabled = false, contentClassName }: FlowTooltipProps) {
  const [open, setOpen] = React.useState(false)
  React.useEffect(() => { if (disabled) setOpen(false) }, [disabled])
  if (!label && !shortcut) return children
  return (
    <TooltipRoot open={open && !disabled} onOpenChange={next => setOpen(next && !disabled)}>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent align={align} className={contentClassName} side={side}>
        <span className="flow-tooltip-copy">{label}</span>
        {shortcut ? <kbd className="flow-tooltip-shortcut"><TooltipShortcut value={shortcut}/></kbd> : null}
      </TooltipContent>
    </TooltipRoot>
  )
}
