import type { ReactNode } from 'react'
import type { Customer } from '@/types/flow'
import { CustomerLogoPile } from '@/components/customer/customer-logo-pile'
import './detail-label-control.css'

export type DetailLabelHost = 'issue' | 'project' | 'initiative'

export type DetailLabelControlProps = {
  host: DetailLabelHost
  children: ReactNode
  /** Contextual change-label action id/label for menus & a11y. */
  changeLabelAction?: string
  isReadOnly?: boolean
  isArchived?: boolean
  /** Initiative path: overlapping customer logos beside the picker. */
  customers?: Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>>
  className?: string
  labelClass?: string
  fontSize?: number | string
}

/**
 * Shared detail-property label host (LS-0195).
 * Wraps existing pickers with readonly/archive gates, contextual changeLabelAction,
 * and Initiative CustomerLogoPile.
 */
export function DetailLabelControl({
  host,
  children,
  changeLabelAction = 'changeLabelAction',
  isReadOnly = false,
  isArchived = false,
  customers,
  className,
  labelClass,
  fontSize,
}: DetailLabelControlProps) {
  const gated = isReadOnly || isArchived
  const showPile = host === 'initiative'

  return (
    <div
      className={[
        'detail-label-control',
        `detail-label-control--${host}`,
        gated ? 'is-readonly' : '',
        isArchived ? 'is-archived' : '',
        className,
        labelClass,
      ]
        .filter(Boolean)
        .join(' ')}
      data-change-label-action={changeLabelAction}
      data-host={host}
      data-readonly={gated || undefined}
      style={fontSize != null ? { fontSize } : undefined}
      tabIndex={gated ? -1 : undefined}
    >
      {showPile && (
        <CustomerLogoPile
          className="detail-label-control__pile"
          customers={customers ?? []}
          maxVisible={3}
          size={18}
        />
      )}
      <div
        aria-disabled={gated || undefined}
        className="detail-label-control__picker"
        // Soft gate: keep chips visible but block interaction when archived/readonly.
        onClickCapture={gated ? (event) => { event.preventDefault(); event.stopPropagation() } : undefined}
        onKeyDownCapture={gated ? (event) => { event.preventDefault(); event.stopPropagation() } : undefined}
      >
        {children}
      </div>
    </div>
  )
}
