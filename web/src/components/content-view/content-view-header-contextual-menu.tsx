import { MoreHorizontal } from 'lucide-react'
import type { ReactNode } from 'react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewHeaderContextualMenuProps {
  children: ReactNode
  ariaLabel?: string
  open?: boolean
  onOpenChange?: (open: boolean) => void
  trigger?: ReactNode
  align?: 'start' | 'center' | 'end'
  className?: string
}

/** LS-0138 — shared header overflow/contextual menu host. */
export function ContentViewHeaderContextualMenu({
  children,
  ariaLabel = 'Open menu',
  open,
  onOpenChange,
  trigger,
  align = 'end',
  className,
}: ContentViewHeaderContextualMenuProps) {
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        {trigger ?? (
          <button
            type="button"
            className={cn(styles.headerAction, className)}
            aria-label={ariaLabel}
            data-content-view-contextual-menu=""
          >
            <MoreHorizontal size={14} />
          </button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align}>{children}</DropdownMenuContent>
    </DropdownMenu>
  )
}
