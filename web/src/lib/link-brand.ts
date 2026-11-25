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
