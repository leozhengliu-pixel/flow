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
  children: React.ReactElement
}

/** Convenience wrapper for icon buttons and compact controls. */
export function FlowTooltip({ label, shortcut, children, side = 'bottom', align = 'center' }: FlowTooltipProps) {
  if (!label && !shortcut) return children
  return (
    <TooltipRoot>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent align={align} side={side}>
        <span className="flow-tooltip-copy">{label}</span>
        {shortcut ? <kbd className="flow-tooltip-shortcut">{shortcut}</kbd> : null}
      </TooltipContent>
    </TooltipRoot>
  )
}
