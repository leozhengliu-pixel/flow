import type { Customer } from '@/types/flow'
import './customer-logo-pile.css'

export type CustomerLogoPileProps = {
  customers: Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>>
  maxVisible?: number
  overlap?: number
  size?: number
  stableWidth?: boolean
  appendNoCustomer?: boolean
  className?: string
}

/** Overlapping customer logos (LS-0195 Initiative stack / LS-0166 precursor). */
export function CustomerLogoPile({
  customers,
  maxVisible = 3,
  overlap = 40,
  size = 18,
  stableWidth = false,
  appendNoCustomer = false,
  className,
}: CustomerLogoPileProps) {
  const visible = customers.slice(0, maxVisible)
  const overflow = Math.max(0, customers.length - maxVisible)
  const slotCount = visible.length + (appendNoCustomer ? 1 : 0) + (overflow > 0 ? 1 : 0)
  const step = size * (1 - overlap / 100)
  const width = stableWidth
    ? Math.max(size, size + step * Math.max(0, maxVisible - 1 + (appendNoCustomer ? 1 : 0)))
    : slotCount <= 1
      ? size
      : size + step * (slotCount - 1)

  if (!slotCount) return null

  return (
    <div
      aria-label={customers.length ? `${customers.length} customers` : 'No customers'}
      className={`customer-logo-pile${className ? ` ${className}` : ''}`}
      style={{ width, height: size }}
    >
      {visible.map((customer, index) => (
        <span
          className="customer-logo-pile__mark"
          key={customer.id}
          style={{ left: index * step, width: size, height: size, zIndex: index + 1 }}
          title={customer.name}
        >
          {customer.logoUrl ? (
            <img alt="" src={customer.logoUrl} />
          ) : (
            <span aria-hidden="true">{customer.name.slice(0, 1).toUpperCase()}</span>
          )}
        </span>
      ))}
      {appendNoCustomer && (
        <span
          className="customer-logo-pile__mark is-empty"
          style={{ left: visible.length * step, width: size, height: size, zIndex: visible.length + 1 }}
          title="No customer"
        >
          <span aria-hidden="true">–</span>
        </span>
      )}
      {overflow > 0 && (
        <span
          className="customer-logo-pile__mark is-overflow"
          style={{
            left: (visible.length + (appendNoCustomer ? 1 : 0)) * step,
            width: size,
            height: size,
            zIndex: visible.length + 2,
          }}
          title={`+${overflow} more`}
        >
          <span aria-hidden="true">+{overflow}</span>
        </span>
      )}
    </div>
  )
}
