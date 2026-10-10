export type EmbedProvider = 'youtube' | 'loom' | 'descript' | 'tella' | 'twitter' | 'figma' | 'miro' | 'github' | 'gitlab'

/**
 * A pasted link a rich-text field can show in place of a bare URL: an iframe player / board, a link card (Figma and X, whose
 * Open Graph title / description / image the API's link-preview endpoint fetches) or a file card (GitHub and GitLab files,
 * whose lines the API's file-preview proxy fetches).
 */
export type EmbedSource = {
  provider: EmbedProvider
  embedUrl: string
  kind?: 'iframe' | 'link' | 'card'
  /** The paste hint's wording for this provider ("Embed video", "Embed post", ...). Passed through t() when shown. */
  label?: string
  /** A fixed iframe height in px (otherwise `aspect` decides): `video` is 5:3 and `tall` 4:3, as in Linear. */
  height?: number
  aspect?: 'video' | 'tall'
}

/** How a saved embed node is drawn, by provider (the node only stores src / embedUrl / provider). */
export type EmbedLayout = Required<Pick<EmbedSource, 'kind' | 'label'>> & Pick<EmbedSource, 'height' | 'aspect'>

export const EMBED_LAYOUTS: Record<EmbedProvider, EmbedLayout> = {
  youtube: { kind: 'iframe', label: 'Embed video', aspect: 'video' },
  loom: { kind: 'iframe', label: 'Embed video', aspect: 'video' },
  descript: { kind: 'iframe', label: 'Embed video', aspect: 'video' },
  tella: { kind: 'iframe', label: 'Embed video', aspect: 'video' },
  twitter: { kind: 'link', label: 'Embed post' },
  figma: { kind: 'link', label: 'Embed preview' },
  miro: { kind: 'iframe', label: 'Embed board', aspect: 'tall' },
  github: { kind: 'card', label: 'Embed file preview' },
  gitlab: { kind: 'card', label: 'Embed file preview' },
}

/** A link that is safe to put in an href: embeds are saved with whatever `src` they had, so only http(s) is opened. */
export function safeEmbedHref(src: string) {
  return /^https?:\/\//i.test(src.trim()) ? src.trim() : undefined
}

/** The layout for a stored provider name (unknown providers from newer content fall back to a video-shaped player). */
export function embedLayoutFor(provider: string): EmbedLayout {
  return EMBED_LAYOUTS[provider as EmbedProvider] ?? EMBED_LAYOUTS.youtube
}

/** The brand shown on a link card until (or without) the page's own title. */
export const LINK_CARD_SITE: Partial<Record<EmbedProvider, string>> = { figma: 'Figma', twitter: 'X' }

function source(provider: EmbedProvider, embedUrl: string): EmbedSource {
  return { provider, embedUrl, ...EMBED_LAYOUTS[provider] }
}

function parse(url: string) {
  try {
    const parsed = new URL(url.trim())
    return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed : undefined
  } catch {
    return undefined
  }
}

const VIDEO_ID = /^[\w-]{6,64}$/
const FIGMA_KINDS = new Set(['file', 'design', 'proto', 'board', 'slides'])

/** A repository file link split into its parts (the ref is the first path segment after /blob/; the server resolves refs with slashes). */
export type RepoFile = { provider: 'github' | 'gitlab'; owner: string; repo: string; ref: string; path: string; startLine?: number; endLine?: number }
/** @deprecated kept for callers that only handle GitHub; see {@link RepoFile}. */
export type GitHubFile = Omit<RepoFile, 'provider'>

function decodeSegments(pathname: string) {
  return pathname.split('/').filter(Boolean).map(part => { try { return decodeURIComponent(part) } catch { return part } })
}

/** #L10, #L10-L20 (GitHub, optionally with columns) and #L10-20 (GitLab). */
function lineRange(hash: string) {
  const lines = /^#L(\d+)(?:C\d+)?(?:-L?(\d+)(?:C\d+)?)?$/.exec(hash)
  const startLine = lines ? Number(lines[1]) : undefined
  const endLine = lines?.[2] ? Number(lines[2]) : undefined
  return { startLine, endLine }
}

export function githubFileForUrl(url: string): GitHubFile | undefined {
  const parsed = parse(url)
  if (!parsed || parsed.hostname.toLowerCase().replace(/^www\./, '') !== 'github.com') return undefined
  const segments = decodeSegments(parsed.pathname)
  if (segments.length < 5 || segments[2] !== 'blob') return undefined
  const [owner, repo, , ref, ...rest] = segments
  return { owner, repo, ref, path: rest.join('/'), ...lineRange(parsed.hash) }
}

/** A GitLab file link (gitlab.com or a self-hosted instance): `<host>/<group>/<project>/-/blob/<ref>/<path>`. */
export function gitlabFileForUrl(url: string): RepoFile | undefined {
  const parsed = parse(url)
  if (!parsed || parsed.protocol !== 'https:' && !/^(localhost|127\.0\.0\.1|\[::1\])$/.test(parsed.hostname)) return undefined
  const marker = parsed.pathname.indexOf('/-/blob/')
  if (marker < 0) return undefined
  const project = decodeSegments(parsed.pathname.slice(0, marker))
  const [ref, ...rest] = decodeSegments(parsed.pathname.slice(marker + '/-/blob/'.length))
  if (project.length < 2 || !ref || !rest.length) return undefined
  const repo = project.pop() as string
  return { provider: 'gitlab', owner: project.join('/'), repo, ref, path: rest.join('/'), ...lineRange(parsed.hash) }
}

/** The repository file a GitHub or GitLab blob link points at. */
export function repoFileForUrl(url: string, provider?: string): RepoFile | undefined {
  if (provider !== 'gitlab') {
    const github = githubFileForUrl(url)
    if (github) return { provider: 'github', ...github }
  }
  return provider === 'github' ? undefined : gitlabFileForUrl(url)
}

/**
 * The embeddable form of a URL: YouTube, Loom, Descript, Tella and Miro players, X post and Figma link cards, and GitHub / GitLab
 * file cards.
 * Anything else stays a link, including Slack links and GitHub pull requests / issues / commits and GitLab merge requests
 * (those become review mentions instead).
 */
export function embedSourceForUrl(url: string): EmbedSource | undefined {
  const parsed = parse(url)
  if (!parsed) return undefined
  const host = parsed.hostname.toLowerCase().replace(/^www\./, '')
  const segments = parsed.pathname.split('/').filter(Boolean)
  if (host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com') {
    const id = segments[0] === 'watch' ? parsed.searchParams.get('v') : segments[0] === 'shorts' || segments[0] === 'embed' || segments[0] === 'live' ? segments[1] : undefined
    return id && VIDEO_ID.test(id) ? source('youtube', `https://www.youtube-nocookie.com/embed/${id}`) : undefined
  }
  if (host === 'youtu.be') return segments[0] && VIDEO_ID.test(segments[0]) ? source('youtube', `https://www.youtube-nocookie.com/embed/${segments[0]}`) : undefined
  if (host === 'loom.com') return segments[0] === 'share' && segments[1] && VIDEO_ID.test(segments[1]) ? source('loom', `https://www.loom.com/embed/${segments[1]}`) : undefined
  if (host === 'share.descript.com') return segments[0] === 'view' && segments[1] && VIDEO_ID.test(segments[1]) ? source('descript', `https://share.descript.com/embed/${segments[1]}`) : undefined
  if (host === 'tella.tv') {
    // /video/<slug>-<id>: the id is the last dash-separated part.
    const id = segments[0] === 'video' && segments[1] ? segments[1].split('-').pop() : undefined
    return id && VIDEO_ID.test(id) ? source('tella', `https://www.tella.tv/video/${id}/embed`) : undefined
  }
  if (host === 'x.com' || host === 'twitter.com' || host === 'mobile.twitter.com') {
    const id = segments.length >= 3 && segments[1] === 'status' ? segments[2] : undefined
    return id && /^\d{1,25}$/.test(id) ? source('twitter', parsed.href) : undefined
  }
  if (host === 'figma.com') return FIGMA_KINDS.has(segments[0]) && segments[1] ? source('figma', parsed.href) : undefined
  if (host === 'miro.com') return segments[0] === 'app' && segments[1] === 'board' && segments[2] && /^[\w=-]{6,}$/.test(segments[2]) ? source('miro', `https://miro.com/app/embed/${segments[2]}/`) : undefined
  if (host === 'github.com') return githubFileForUrl(url) ? source('github', parsed.href) : undefined
  if (gitlabFileForUrl(url)) return source('gitlab', parsed.href)
  return undefined
}
