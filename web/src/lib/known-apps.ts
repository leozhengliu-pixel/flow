import { APP_LOGO_MARKS, type AppLogoKey, type AppLogoMark } from '@/assets/app-logos/logos'

/**
 * Well-known OAuth / MCP clients (Claude, Cursor, VS Code, …) and how a client is recognised, so the consent screen and
 * the application lists can show the app's brand mark instead of the generic application icon.
 *
 * A client is recognised by
 *  1. its redirect URIs: an app-owned web host (`claude.ai`, `chatgpt.com`, `vscode.dev`), the app's custom URL scheme
 *     (`cursor://`, `windsurf://`, `zed://`) or, for editor extensions, the extension id in the authority
 *     (`vscode://saoudrizwan.claude-dev/…` is Cline, not VS Code);
 *  2. otherwise its name (the dynamic client registration `client_name`, the application name or the MCP `clientInfo`
 *     name), case-, space- and punctuation-insensitively: the name's leading words must be one of the app's aliases
 *     ("Claude Code (linear)" and "claude-code" are Claude Code, "Claudette" is nobody).
 *
 * A name alone never borrows a brand for a client that sends its codes somewhere else: when the client has a redirect URI
 * that is neither loopback (CLIs listen on `127.0.0.1`) nor owned by the named app, it is not recognised and keeps the
 * generic icon.
 */
export interface KnownApp {
  id: string
  name: string
  logo: AppLogoKey
  /** Lower-case names; matched against the leading words of a client name. */
  aliases: string[]
  /** Web hosts (and their subdomains) the app's redirect URIs use. */
  hosts?: string[]
  /** Custom URL schemes the app's redirect URIs use. */
  schemes?: string[]
  /** Restricts `schemes` to authorities with one of these prefixes (VS Code's own `vscode.*` extensions). */
  authorityPrefixes?: string[]
  /** Editor-extension ids: a redirect with this authority belongs to the app whatever the editor's scheme. */
  extensionIds?: string[]
}

export const KNOWN_APPS: KnownApp[] = [
  { id: 'claude', name: 'Claude', logo: 'claude', aliases: ['claude', 'claude ai', 'claude desktop', 'claude for desktop', 'anthropic'], hosts: ['claude.ai', 'claude.com', 'anthropic.com'], schemes: ['claude'] },
  { id: 'claude-code', name: 'Claude Code', logo: 'claudeCode', aliases: ['claude code', 'claudecode'] },
  { id: 'chatgpt', name: 'ChatGPT', logo: 'openai', aliases: ['chatgpt', 'openai', 'chat gpt'], hosts: ['chatgpt.com', 'chat.openai.com', 'openai.com'], schemes: ['chatgpt'] },
  { id: 'codex', name: 'Codex', logo: 'openai', aliases: ['codex', 'openai codex', 'codex cli', 'codex mcp client'] },
  { id: 'cursor', name: 'Cursor', logo: 'cursor', aliases: ['cursor'], hosts: ['cursor.com', 'cursor.sh'], schemes: ['cursor'] },
  { id: 'vscode', name: 'Visual Studio Code', logo: 'vscode', aliases: ['visual studio code', 'vs code', 'vscode', 'code oss'], hosts: ['vscode.dev'], schemes: ['vscode', 'vscode-insiders'], authorityPrefixes: ['vscode.'] },
  { id: 'windsurf', name: 'Windsurf', logo: 'windsurf', aliases: ['windsurf', 'codeium'], hosts: ['windsurf.com', 'codeium.com'], schemes: ['windsurf'] },
  { id: 'zed', name: 'Zed', logo: 'zed', aliases: ['zed', 'zed industries', 'zed editor'], hosts: ['zed.dev'], schemes: ['zed'] },
  { id: 'opencode', name: 'OpenCode', logo: 'opencode', aliases: ['opencode', 'open code'] },
  { id: 'raycast', name: 'Raycast', logo: 'raycast', aliases: ['raycast'], hosts: ['raycast.com'], schemes: ['raycast'] },
  { id: 'goose', name: 'goose', logo: 'goose', aliases: ['goose', 'block goose'] },
  { id: 'cline', name: 'Cline', logo: 'cline', aliases: ['cline'], extensionIds: ['saoudrizwan.claude-dev'] },
  { id: 'github-copilot', name: 'GitHub Copilot', logo: 'githubCopilot', aliases: ['github copilot', 'copilot cli', 'copilot coding agent'], extensionIds: ['github.copilot', 'github.copilot-chat'] },
  { id: 'gemini', name: 'Gemini', logo: 'gemini', aliases: ['gemini', 'gemini cli', 'google gemini'], hosts: ['gemini.google.com'] },
  { id: 'warp', name: 'Warp', logo: 'warp', aliases: ['warp', 'warp terminal'], hosts: ['warp.dev'], schemes: ['warp'] },
  { id: 'jetbrains', name: 'JetBrains', logo: 'jetbrains', aliases: ['jetbrains', 'jetbrains ai', 'jetbrains ai assistant'], hosts: ['jetbrains.com'], schemes: ['jetbrains'] },
  { id: 'replit', name: 'Replit', logo: 'replit', aliases: ['replit'], hosts: ['replit.com'] },
  { id: 'v0', name: 'v0', logo: 'v0', aliases: ['v0', 'v0 dev'], hosts: ['v0.dev', 'v0.app'] },
  { id: 'perplexity', name: 'Perplexity', logo: 'perplexity', aliases: ['perplexity'], hosts: ['perplexity.ai'] },
  { id: 'trae', name: 'Trae', logo: 'trae', aliases: ['trae'], hosts: ['trae.ai'], schemes: ['trae'] },
  { id: 'zapier', name: 'Zapier', logo: 'zapier', aliases: ['zapier'], hosts: ['zapier.com'] },
  { id: 'n8n', name: 'n8n', logo: 'n8n', aliases: ['n8n'], hosts: ['n8n.io', 'n8n.cloud'] },
]

export interface KnownAppClient {
  /** client_name / application name / MCP clientInfo name. */
  name?: string | null
  redirectUris?: readonly (string | null | undefined)[] | null
}

export type ResolvedKnownApp = KnownApp & { mark: AppLogoMark }

const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '[::1]', '::1'])

/** Words of a name: lower case, split on anything but letters and digits ("Claude Code (linear)" → claude, code, linear). */
function words(value: string) {
  return value.toLowerCase().normalize('NFKD').replace(/[̀-ͯ]/g, '').split(/[^\p{L}\p{N}]+/u).filter(Boolean)
}

const aliasIndex = KNOWN_APPS.flatMap(app => app.aliases.map(alias => ({ app, words: words(alias) })))
  // Longest alias first: "OpenAI Codex" is Codex, not ChatGPT.
  .sort((a, b) => b.words.length - a.words.length)

function byName(name: string): KnownApp | undefined {
  const tokens = words(name)
  if (!tokens.length) return undefined
  const compact = tokens.join('')
  for (const { app, words: alias } of aliasIndex) {
    if (alias.length <= tokens.length && alias.every((word, index) => tokens[index] === word)) return app
    if (alias.join('') === compact) return app
  }
  return undefined
}

type ParsedRedirect = { kind: 'loopback' } | { kind: 'web'; host: string } | { kind: 'custom'; scheme: string; authority: string } | { kind: 'invalid' }

function parseRedirect(value: string): ParsedRedirect {
  let url: URL
  try { url = new URL(value.trim()) } catch { return { kind: 'invalid' } }
  const scheme = url.protocol.replace(/:$/, '').toLowerCase()
  const host = url.hostname.toLowerCase().replace(/\.$/, '')
  if (scheme === 'http' || scheme === 'https') return LOOPBACK_HOSTS.has(host) ? { kind: 'loopback' } : { kind: 'web', host }
  return { kind: 'custom', scheme, authority: host }
}

function hostOwned(host: string, hosts?: string[]) {
  return Boolean(hosts?.some(owned => host === owned || host.endsWith(`.${owned}`)))
}

/** The app a redirect URI belongs to: an editor extension id first, then an app-owned host or scheme. */
function byRedirect(redirect: ParsedRedirect): KnownApp | undefined {
  if (redirect.kind === 'custom' && redirect.authority) {
    const extension = KNOWN_APPS.find(app => app.extensionIds?.includes(redirect.authority))
    if (extension) return extension
  }
  return KNOWN_APPS.find(app => owns(app, redirect))
}

function owns(app: KnownApp, redirect: ParsedRedirect) {
  if (redirect.kind === 'web') return hostOwned(redirect.host, app.hosts)
  if (redirect.kind !== 'custom') return false
  if (redirect.authority && app.extensionIds?.includes(redirect.authority)) return true
  if (!app.schemes?.includes(redirect.scheme)) return false
  if (!app.authorityPrefixes) return true
  return app.authorityPrefixes.some(prefix => redirect.authority.startsWith(prefix))
}

/** Recognises a well-known client (see the module comment for the rules); `undefined` for any other client. */
export function matchKnownApp(client: KnownAppClient): ResolvedKnownApp | undefined {
  const redirects = (client.redirectUris ?? []).filter((value): value is string => Boolean(value?.trim())).map(parseRedirect)
  let app: KnownApp | undefined
  for (const redirect of redirects) {
    app = byRedirect(redirect)
    if (app) break
  }
  if (!app && client.name) {
    const named = byName(client.name)
    // A name-only match is kept while every redirect stays on this machine or with the named app.
    if (named && redirects.every(redirect => redirect.kind === 'loopback' || owns(named, redirect))) app = named
  }
  return app ? { ...app, mark: APP_LOGO_MARKS[app.logo] } : undefined
}
