import type { CSSProperties } from 'react'
import { CustomerLogo, type CustomerLogoCustomer } from './customer-logo'
import './customer-logo-pile.css'

export type CustomerLogoPileProps = {
  customers: CustomerLogoCustomer[]
  maxVisible?: number
  /** Percentage of each logo covered by the next one (Linear: 40). */
  overlap?: number
  size?: number
  /** Reserve the width of `maxVisible` logos so rows line up. */
  stableWidth?: boolean
  /** Append the "No customer" logo (also shown when the list is empty and this is set). */
  appendNoCustomer?: boolean
  className?: string
}

/**
 * Linear's CustomerLogoPile: up to `maxVisible` overlapping customer logos, the
 * first on top, each ringed by a 1px border; no overflow counter.
 */
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
  const showDefault = appendNoCustomer
  const total = visible.length + (showDefault ? 1 : 0)
  if (!total) return null
  const shift = size * (overlap / 100)
  const minWidth = stableWidth ? maxVisible * size - (maxVisible - 1) * shift - maxVisible : undefined
  const itemStyle = (index: number): CSSProperties => ({ width: size, height: size, zIndex: total - index, marginRight: index === total - 1 ? 0 : -shift })
  const names = customers.map(customer => customer.name).join(', ')
  return (
    <div
      aria-label={customers.length ? `${customers.length} customers` : 'No customers'}
      className={`customer-logo-pile${className ? ` ${className}` : ''}`}
      role="img"
      style={minWidth !== undefined ? { minWidth } : undefined}
      title={names || undefined}
    >
      {visible.map((customer, index) => (
        <span className="customer-logo-pile__item" key={customer.id} style={itemStyle(index)}>
          <CustomerLogo customer={customer} radius={2} size={size}/>
        </span>
      ))}
      {showDefault && (
        <span className="customer-logo-pile__item is-empty" style={itemStyle(total - 1)} title="No customer">
          <CustomerLogo customer={null} radius={2} size={size}/>
        </span>
      )}
    </div>
  )
}
