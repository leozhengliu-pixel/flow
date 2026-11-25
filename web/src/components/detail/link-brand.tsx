import { IntegrationBrandIcon } from '@/components/settings/integration-brand-icon'

export type LinkBrand = 'github' | 'slack' | 'figma'

/** The service a link points at, when it is one the Resources list draws an icon for. */
export function linkBrand(value: string): LinkBrand | null {
  let host = ''
  try { host = new URL(value).hostname.toLowerCase().replace(/^www\./, '') } catch { return null }
  if (host === 'github.com') return 'github'
  if (host === 'slack.com' || host.endsWith('.slack.com')) return 'slack'
  if (host === 'figma.com') return 'figma'
  return null
}

/** The title a link would have been given with none: owner/repo#number, Figma, Message from Slack or the host. */
export function defaultLinkTitle(value: string): string {
  try {
    const url = new URL(value)
    const parts = url.pathname.split('/').filter(Boolean)
    const brand = linkBrand(value)
    if (brand === 'github' && parts.length >= 4 && (parts[2] === 'issues' || parts[2] === 'pull')) return `${parts[0]}/${parts[1]}#${parts[3]}`
    if (brand === 'figma') return 'Figma'
    if (brand === 'slack' && parts[0] === 'archives') return 'Message from Slack'
    return url.host
  } catch { return 'Link' }
}

/** The mark of a Figma file: five rounded shapes in the service's four colours. */
function FigmaMark({ size }: { size: number }) {
  return (
    <svg aria-hidden="true" height={size} viewBox="0 0 16 16" width={size}>
      <path d="M5.33 1h2.67v4.33H5.33a2.17 2.17 0 0 1 0-4.33Z" fill="#F24E1E"/>
      <path d="M8 1h2.67a2.17 2.17 0 0 1 0 4.33H8V1Z" fill="#FF7262"/>
      <path d="M5.33 5.33H8v4.34H5.33a2.17 2.17 0 0 1 0-4.34Z" fill="#A259FF"/>
      <circle cx="10.67" cy="7.5" fill="#1ABCFE" r="2.17"/>
      <path d="M5.33 9.67H8v2.16a2.17 2.17 0 1 1-2.67-2.16Z" fill="#0ACF83"/>
    </svg>
  )
}

export function LinkBrandIcon({ brand, size = 16 }: { brand: LinkBrand; size?: number }) {
  if (brand === 'figma') return <FigmaMark size={size}/>
  return <IntegrationBrandIcon provider={brand} size={size}/>
}
