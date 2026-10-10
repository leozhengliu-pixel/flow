import { documentDisplayTitle } from '@/components/documents/document-actions'
import type { FlowDocument } from '@/types/flow'

export type LinkProvider = 'github' | 'gitlab' | 'slack' | 'jira'

const BRAND_NAMES: Record<string, string> = {
  'github.com': 'GitHub',
  'gitlab.com': 'GitLab',
  'slack.com': 'Slack',
  'atlassian.net': 'Jira',
  'figma.com': 'Figma',
  'notion.so': 'Notion',
  'notion.site': 'Notion',
  'docs.google.com': 'Google Docs',
  'drive.google.com': 'Google Drive',
  'youtube.com': 'YouTube',
  'loom.com': 'Loom',
  'miro.com': 'Miro',
  'linear.app': 'Linear',
  'vercel.com': 'Vercel',
  'sentry.io': 'Sentry',
}

export function hostOf(url: string) {
  try {
    return new URL(url).hostname.toLowerCase().replace(/^www\./, '')
  } catch {
    return ''
  }
}

function brandFor(host: string) {
  return Object.keys(BRAND_NAMES).find(domain => host === domain || host.endsWith(`.${domain}`))
}

export function providerFor(host: string): LinkProvider | undefined {
  const brand = brandFor(host)
  if (brand === 'github.com') return 'github'
  if (brand === 'gitlab.com') return 'gitlab'
  if (brand === 'slack.com') return 'slack'
  if (brand === 'atlassian.net') return 'jira'
  return undefined
}

/**
 * Linear names an untitled link after its site ("GitHub", "Example") rather
 * than showing the whole URL.
 */
export function resourceLinkName(url: string) {
  const host = hostOf(url)
  if (!host) return url
  const brand = brandFor(host)
  if (brand) return BRAND_NAMES[brand]
  const label = host.split('.').at(-2) ?? host
  return label.charAt(0).toUpperCase() + label.slice(1)
}

/** Title to show for a resource; legacy untitled links stored their URL as the title. */
export function resourceDisplayTitle(resource: { type: string; title: string; url: string }) {
  if (resource.type !== 'link') return resource.title
  const title = resource.title.trim()
  return !title || title === resource.url.trim() ? resourceLinkName(resource.url) : title
}

/** What the server stores as a document resource's title while the document has no title yet. */
const LEGACY_UNTITLED_RESOURCE_TITLE = 'Untitled document'

/**
 * Title on a Resources chip. A document chip shows the live document title (the title stored on the
 * resource row can be empty or stale) and "Untitled" while the document has none.
 */
export function resourceChipTitle(resource: { type: string; title: string; url: string }, document: Pick<FlowDocument, 'title'> | undefined, t: (source: string) => string) {
  if (document) return documentDisplayTitle(document, t)
  if (resource.type === 'document') {
    const title = resource.title.trim()
    return !title || title === LEGACY_UNTITLED_RESOURCE_TITLE ? t('Untitled') : title
  }
  return resourceDisplayTitle(resource)
}
