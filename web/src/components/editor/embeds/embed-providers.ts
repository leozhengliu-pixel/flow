/** A pasted link a rich-text field can show as an inline player instead of a bare URL. */
export type EmbedSource = { provider: 'youtube' | 'loom' | 'descript'; embedUrl: string }

function parse(url: string) {
  try {
    const parsed = new URL(url.trim())
    return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed : undefined
  } catch {
    return undefined
  }
}

const VIDEO_ID = /^[\w-]{6,64}$/

/**
 * The embeddable form of a URL: YouTube, Loom and Descript links (what Linear embeds without an integration).
 * Anything else, including Slack, GitHub and GitLab links, stays a link (pull requests become review mentions).
 */
export function embedSourceForUrl(url: string): EmbedSource | undefined {
  const parsed = parse(url)
  if (!parsed) return undefined
  const host = parsed.hostname.toLowerCase().replace(/^www\./, '')
  const segments = parsed.pathname.split('/').filter(Boolean)
  if (host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com') {
    const id = segments[0] === 'watch' ? parsed.searchParams.get('v') : segments[0] === 'shorts' || segments[0] === 'embed' || segments[0] === 'live' ? segments[1] : undefined
    return id && VIDEO_ID.test(id) ? { provider: 'youtube', embedUrl: `https://www.youtube-nocookie.com/embed/${id}` } : undefined
  }
  if (host === 'youtu.be') return segments[0] && VIDEO_ID.test(segments[0]) ? { provider: 'youtube', embedUrl: `https://www.youtube-nocookie.com/embed/${segments[0]}` } : undefined
  if (host === 'loom.com') return segments[0] === 'share' && segments[1] && VIDEO_ID.test(segments[1]) ? { provider: 'loom', embedUrl: `https://www.loom.com/embed/${segments[1]}` } : undefined
  if (host === 'share.descript.com') return segments[0] === 'view' && segments[1] && VIDEO_ID.test(segments[1]) ? { provider: 'descript', embedUrl: `https://share.descript.com/embed/${segments[1]}` } : undefined
  return undefined
}
