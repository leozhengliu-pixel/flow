/**
 * LS-0039 — derive filtered external URLs for agent sessions / application activities.
 * Linear: agentSession.filteredExternalUrls → label + url menu.
 */

export type AgentExternalUrl = {
  url: string
  label: string
}

const LABEL_BY_HOST: Record<string, string> = {
  'github.com': 'GitHub',
  'gitlab.com': 'GitLab',
  'bitbucket.org': 'Bitbucket',
  'dev.azure.com': 'Azure DevOps',
  'linear.app': 'Linear',
  'notion.so': 'Notion',
  'figma.com': 'Figma',
  'vercel.com': 'Vercel',
  'railway.app': 'Railway',
  'console.aws.amazon.com': 'AWS',
}

function hostLabel(hostname: string): string {
  const host = hostname.replace(/^www\./, '').toLowerCase()
  if (LABEL_BY_HOST[host]) return LABEL_BY_HOST[host]
  for (const [key, label] of Object.entries(LABEL_BY_HOST)) {
    if (host === key || host.endsWith(`.${key}`)) return label
  }
  return host
}

export function labelForExternalUrl(url: string, fallback?: string): string {
  if (fallback?.trim()) return fallback.trim()
  try {
    const parsed = new URL(url)
    return hostLabel(parsed.hostname)
  } catch {
    return url
  }
}

/** Dedupe by URL and drop empty / non-http(s) links. */
export function filteredExternalUrls(
  candidates: Array<{ url?: string | null; label?: string | null } | string | null | undefined>,
): AgentExternalUrl[] {
  const seen = new Set<string>()
  const result: AgentExternalUrl[] = []
  for (const item of candidates) {
    const raw = typeof item === 'string' ? item : item?.url
    if (!raw || typeof raw !== 'string') continue
    const url = raw.trim()
    if (!/^https?:\/\//i.test(url)) continue
    const key = url.replace(/\/$/, '').toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    const label =
      typeof item === 'string' ? labelForExternalUrl(url) : labelForExternalUrl(url, item?.label ?? undefined)
    result.push({ url, label })
  }
  return result
}

export function externalUrlsFromActivities(
  activities: Array<{ url?: string; body?: string; type?: string }>,
): AgentExternalUrl[] {
  const fromFields = activities.map(activity => ({ url: activity.url, label: undefined as string | undefined }))
  const fromBodies: Array<{ url: string; label?: string }> = []
  const urlRe = /https?:\/\/[^\s)\]>'"]+/gi
  for (const activity of activities) {
    if (!activity.body) continue
    for (const match of activity.body.matchAll(urlRe)) {
      fromBodies.push({ url: match[0].replace(/[.,;:]+$/, '') })
    }
  }
  return filteredExternalUrls([...fromFields, ...fromBodies])
}
