import type { CustomerStatus, CustomerTier } from '@/types/flow'

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
