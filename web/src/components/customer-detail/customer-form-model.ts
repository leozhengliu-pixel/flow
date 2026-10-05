/** Linear caps customer revenue and size at the largest 32-bit integer. */
export const CUSTOMER_NUMBER_MAX = 2147483647

export type CustomerDraftErrors = Partial<Record<'name' | 'annualRevenue' | 'size' | 'logo', string>> & { domains?: string[]; domainsAll?: string }

/** Linear's customer form validation (required name, 32-bit numbers, valid unique domains). */
export function validateCustomerDraft(draft: { name: string; annualRevenue: string; size: string; domains: string[] }, t: (value: string) => string = value => value): CustomerDraftErrors {
  const errors: CustomerDraftErrors = {}
  if (!draft.name.trim()) errors.name = t('Name is a required field')
  if (draft.annualRevenue && Number(draft.annualRevenue) > CUSTOMER_NUMBER_MAX) errors.annualRevenue = t('Annual revenue must be less than or equal to 2147483647')
  if (draft.size && Number(draft.size) > CUSTOMER_NUMBER_MAX) errors.size = t('Size must be less than or equal to 2147483647')
  const domainErrors = draft.domains.map(domain => domain.trim() && !validDomain(domain) ? t('Please enter a valid domain') : '')
  if (domainErrors.some(Boolean)) errors.domains = domainErrors
  const domains = draft.domains.map(normalizeDomain).filter(Boolean)
  if (new Set(domains).size !== domains.length) errors.domainsAll = t('All domains must be unique')
  return errors
}

export function hasCustomerDraftErrors(errors: CustomerDraftErrors) {
  return Boolean(errors.name || errors.annualRevenue || errors.size || errors.domainsAll || errors.domains?.some(Boolean))
}
function validDomain(value: string) {
  const trimmed = value.trim()
  try {
    const url = new URL(trimmed.includes('://') ? trimmed : `https://${trimmed}`)
    return /^[a-z0-9-]+(\.[a-z0-9-]+)+$/i.test(url.hostname)
  } catch { return false }
}
export function normalizeDomain(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  try { return new URL(trimmed.includes('://') ? trimmed : `https://${trimmed}`).hostname.toLowerCase() }
  catch { return trimmed.toLowerCase() }
}
