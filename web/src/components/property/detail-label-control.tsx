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

/** Resolve customers linked to an initiative via its projects (name match or request). */
export function customersForInitiative(
  initiative: { projectIds: string[] },
  projects: Array<{ id: string; customers?: string[] }>,
  customers: Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>>,
  requests: Array<{ projectId?: string; customerId: string; archivedAt?: string }>,
): Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>> {
  const projectIdSet = new Set(initiative.projectIds)
  const byName = new Map(customers.map((item) => [item.name.toLowerCase(), item]))
  const seen = new Set<string>()
  const result: Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>> = []

  for (const project of projects) {
    if (!projectIdSet.has(project.id)) continue
    for (const name of project.customers ?? []) {
      const match = byName.get(name.toLowerCase())
      if (match && !seen.has(match.id)) {
        seen.add(match.id)
        result.push(match)
      }
    }
  }
  for (const request of requests) {
    if (!request.projectId || request.archivedAt || !projectIdSet.has(request.projectId)) continue
    if (seen.has(request.customerId)) continue
    const match = customers.find((item) => item.id === request.customerId)
    if (match) {
      seen.add(match.id)
      result.push(match)
    }
  }
  return result
}
