import type { CSSProperties, SVGProps } from 'react'
import type { CustomerStatus, CustomerTier } from '@/types/flow'
import './customer-status-icon.css'

/** Linear's default customer status colour (`CustomerStatus.getDefaultStatusColor`). */
export const DEFAULT_CUSTOMER_STATUS_COLOR = '#5e6ad2'

/** Finds a customer's status by its stored value (status id or, for older data, the status name). */
export function findCustomerStatus(statuses: readonly CustomerStatus[] | undefined, value: string | undefined) {
  if (!value || !statuses?.length) return undefined
  const key = value.toLowerCase()
  return statuses.find(status => status.id === value) ?? statuses.find(status => status.name.toLowerCase() === key)
}

/** Finds a customer's tier by its stored value (tier id or, for older data, the tier name). */
export function findCustomerTier(tiers: readonly CustomerTier[] | undefined, value: string | undefined) {
  if (!value || !tiers?.length) return undefined
  const key = value.toLowerCase()
  return tiers.find(tier => tier.id === value) ?? tiers.find(tier => tier.name.toLowerCase() === key)
}

/**
 * Linear's CustomerStatusIcon: an 8×8 square with a 1px radius filled with the status colour.
 * `monochrome` draws it in the muted label colour (used for the Status property header icon).
 */
export function CustomerStatusIcon({ color, monochrome, size = 8, className, style }: { color?: string; monochrome?: boolean; size?: number; className?: string; style?: CSSProperties }) {
  const fill = monochrome ? undefined : (color || DEFAULT_CUSTOMER_STATUS_COLOR)
  return <span aria-hidden="true" className={`customer-status-icon${monochrome ? ' is-monochrome' : ''}${className ? ` ${className}` : ''}`} style={{ width: size, height: size, ...(fill ? { background: fill } : {}), ...style }} />
}

/** Linear's customer tier glyph (stacked layers), drawn with `currentColor`. */
export function CustomerTierIcon({ size = 16, ...props }: SVGProps<SVGSVGElement> & { size?: number }) {
  return <svg aria-hidden="true" width={size} height={size} viewBox="0 0 16 16" fill="currentColor" focusable="false" {...props}>
    <path d="M5.12144 5.50007C5.00967 5.4391 4.86993 5.47187 4.79692 5.57618L3.38287 7.59624C3.29738 7.71837 3.33709 7.8877 3.46797 7.95908L7.88028 10.3658C7.9549 10.4065 8.04509 10.4065 8.11971 10.3658L12.5332 7.95845C12.6641 7.88706 12.7038 7.71773 12.6183 7.59561L11.2042 5.57554C11.1312 5.47123 10.9915 5.43846 10.8797 5.49943L8.47885 6.809C8.18037 6.9718 7.81962 6.9718 7.52114 6.809L5.12144 5.50007Z"/>
    <path d="M10.3387 4.33903C10.4242 4.46115 10.3845 4.63048 10.2536 4.70187L8.11971 5.8658C8.04509 5.9065 7.9549 5.9065 7.88028 5.8658L5.74757 4.70251C5.6167 4.63112 5.57699 4.46179 5.66248 4.33967L7.59119 1.58436C7.79024 1.3 8.21137 1.3 8.41042 1.58436L10.3387 4.33903Z"/>
    <path d="M13.4838 8.83212C13.4108 8.72781 13.2711 8.69504 13.1593 8.75601L8.47885 11.309C8.18037 11.4718 7.81962 11.4718 7.52114 11.309L2.84184 8.75665C2.73006 8.69568 2.59033 8.72845 2.51732 8.83275L1.31508 10.5502C1.14636 10.7913 1.22119 11.1251 1.47662 11.2711L7.75274 14.8574C7.90645 14.9453 8.09516 14.9453 8.24887 14.8574L14.525 11.2711C14.7804 11.1251 14.8552 10.7913 14.6865 10.5502L13.4838 8.83212Z"/>
  </svg>
}
