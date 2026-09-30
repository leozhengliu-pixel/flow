import { Link } from 'lucide-react'
import { IntegrationBrandIcon } from '@/components/settings/integration-brand-icon'
import { hostOf, providerFor } from './project-resource-link-name'

/** Brand icon for known services, otherwise Linear's chain-link glyph. */
export function ResourceLinkIcon({ url }: { url: string }) {
  const provider = providerFor(hostOf(url))
  if (provider) return <span className="project-resource-link-icon"><IntegrationBrandIcon provider={provider} size={16}/></span>
  return <span className="project-resource-link-icon"><Link size={16} strokeWidth={1.75}/></span>
}

/** Linear's small ↗ shown after every link resource. */
export function ResourceExternalArrow() {
  return (
    <svg aria-hidden="true" className="project-resource-external" fill="currentColor" height="16" viewBox="0 0 16 16" width="16">
      <path d="M11.75 10a.75.75 0 1 1-1.5 0V6.81l-4.72 4.72a.75.75 0 1 1-1.06-1.06l4.72-4.72H6a.75.75 0 0 1 0-1.5h5a.75.75 0 0 1 .75.75z"/>
    </svg>
  )
}
