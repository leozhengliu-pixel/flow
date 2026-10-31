import { Star } from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewHeaderFavoriteActionButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  favorited?: boolean
  favoriteLabel?: string
  unfavoriteLabel?: string
  icon?: ReactNode
}

/** LS-0139 — header favorite star (`FavoriteStarWithAction`). */
export function ContentViewHeaderFavoriteActionButton({
  favorited = false,
  favoriteLabel = 'Add to favorites',
  unfavoriteLabel = 'Remove from favorites',
  icon,
  className,
  ...rest
}: ContentViewHeaderFavoriteActionButtonProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={favorited}
      aria-label={favorited ? unfavoriteLabel : favoriteLabel}
      data-active={favorited || undefined}
      className={cn(styles.headerAction, className)}
      data-content-view-favorite=""
      {...rest}
    >
      {icon ?? <Star size={14} fill={favorited ? 'currentColor' : 'none'} />}
    </button>
  )
}
