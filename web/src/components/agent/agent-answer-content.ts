import type { BootstrapData, Issue, Project } from '@/types/flow'
import { splitAgentDraft } from './agent-draft'

/** Shortcode the answer markdown uses for an inline entity chip (parsed by AgentEntityNode). */
export const AGENT_ENTITY_NODE = 'agentEntity'
const SUGGESTIONS_FENCE = 'suggestions'
const MAX_SUGGESTIONS = 3

export type AgentEntityRef = { kind: 'issue'; issue: Issue } | { kind: 'project'; project: Project }

/**
 * Split the model's trailing ```suggestions block (one follow-up request per line) from the reply.
 * An unfinished block, or a fence still being typed at the end of a streaming reply, is hidden too.
 */
export function splitAgentSuggestions(content: string): { prose: string; suggestions: string[] } {
  const { prose, draft } = splitAgentDraft(content, SUGGESTIONS_FENCE)
  const suggestions = (draft ?? '')
    .split('\n')
    .map(line => line.replace(/^\s*(?:[-*•]|\d+[.)])\s+/, '').trim())
    .filter(Boolean)
    .slice(0, MAX_SUGGESTIONS)
  return { prose: stripPartialFence(prose), suggestions }
}

function stripPartialFence(text: string) {
  const lines = text.split('\n')
  const last = lines.at(-1)?.trim() ?? ''
  if (!last.startsWith('`') || !`\`\`\`${SUGGESTIONS_FENCE}`.startsWith(last)) return text
  // Only an opening fence (an even number of fences before it) can start a suggestions block.
  const fencesBefore = lines.slice(0, -1).filter(line => line.trim().startsWith('```')).length
  if (fencesBefore % 2) return text
  return lines.slice(0, -1).join('\n').trim()
}

const IDENTIFIER_PREFIX = /^([A-Z][A-Z0-9]*-\d+)(?=$|[\s:–—-])/

export function findAgentIssue(data: BootstrapData, identifier: string) {
  const key = identifier.toUpperCase()
  return data.issues.find(issue => issue.identifier.toUpperCase() === key)
}

/** A Flow issue (`/…/issue/DEV-1/…`) or project (`/…/project/<slug>/…`) URL on this app, resolved against workspace data. */
export function resolveAgentEntityUrl(url: string, data: BootstrapData): AgentEntityRef | undefined {
  let parsed: URL
  const origin = typeof window !== 'undefined' ? window.location.origin : 'http://flow.local'
  try {
    parsed = new URL(url, origin)
  } catch {
    return undefined
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(url) && parsed.origin !== origin) return undefined
  const issueMatch = parsed.pathname.match(/\/issue\/([A-Za-z][A-Za-z0-9]*-\d+)(?:\/|$)/)
  if (issueMatch) {
    const issue = findAgentIssue(data, issueMatch[1])
    return issue ? { kind: 'issue', issue } : undefined
  }
  const projectMatch = parsed.pathname.match(/\/project\/([^/]+)(?:\/|$)/)
  if (projectMatch) {
    let slug = projectMatch[1]
    try { slug = decodeURIComponent(slug) } catch { /* keep the raw segment */ }
    const project = data.projects.find(item => item.slugId === slug || item.id === slug)
    return project ? { kind: 'project', project } : undefined
  }
  return undefined
}

export function agentEntityById(data: BootstrapData, kind: string, id: string): AgentEntityRef | undefined {
  if (kind === 'issue') {
    const issue = data.issues.find(item => item.id === id)
    return issue ? { kind: 'issue', issue } : undefined
  }
  if (kind === 'project') {
    const project = data.projects.find(item => item.id === id)
    return project ? { kind: 'project', project } : undefined
  }
  return undefined
}

// Code spans and fenced blocks are left untouched.
const CODE = /(```[\s\S]*?(?:```|$)|`[^`\n]*`)/
// Markdown link | <autolink> | bare URL | issue identifier.
const ENTITY_TOKEN = /\[([^\]\n]+)\]\(<?([^)\s>]+)>?(?:\s+"[^"]*")?\)|<(https?:\/\/[^>\s]+)>|(https?:\/\/[^\s<>()[\]]+)|(?<![\w/#.-])([A-Z][A-Z0-9]*-\d+)(?![\w-])/g

/**
 * Turn issue identifiers, links whose text is an identifier ("DEV-1" / "DEV-1 Title") and links to Flow issue or
 * project URLs into inline entity shortcodes. Unknown identifiers stay plain text. Returns the issues referenced, in order.
 */
export function linkAgentEntities(markdown: string, data?: BootstrapData): { markdown: string; issues: Issue[] } {
  if (!data || !markdown) return { markdown, issues: [] }
  const issues: Issue[] = []
  const chip = (ref: AgentEntityRef) => {
    if (ref.kind === 'issue' && !issues.includes(ref.issue)) issues.push(ref.issue)
    const id = ref.kind === 'issue' ? ref.issue.id : ref.project.id
    const label = ref.kind === 'issue' ? ref.issue.identifier : ref.project.name
    return `[${AGENT_ENTITY_NODE} kind="${ref.kind}" id="${shortcodeValue(id)}" label="${shortcodeValue(label)}"]`
  }
  const linked = markdown.split(CODE).map((segment, index) => index % 2 ? segment : segment.replace(ENTITY_TOKEN, (match: string, linkText?: string, linkUrl?: string, autolink?: string, bareUrl?: string, identifier?: string, offset?: number, source?: string) => {
    if (linkText !== undefined && linkUrl !== undefined) {
      if (offset && source?.[offset - 1] === '!') return match
      const textId = linkText.trim().match(IDENTIFIER_PREFIX)?.[1]
      const textIssue = textId ? findAgentIssue(data, textId) : undefined
      if (textIssue) return chip({ kind: 'issue', issue: textIssue })
      const ref = resolveAgentEntityUrl(linkUrl, data)
      return ref ? chip(ref) : match
    }
    if (autolink) {
      const ref = resolveAgentEntityUrl(autolink, data)
      return ref ? chip(ref) : match
    }
    if (bareUrl) {
      const trailing = bareUrl.match(/[.,;:!?]+$/)?.[0] ?? ''
      const ref = resolveAgentEntityUrl(bareUrl.slice(0, bareUrl.length - trailing.length), data)
      return ref ? chip(ref) + trailing : match
    }
    if (identifier) {
      const issue = findAgentIssue(data, identifier)
      return issue ? chip({ kind: 'issue', issue }) : match
    }
    return match
  })).join('')
  return { markdown: linked, issues }
}

function shortcodeValue(value: string) {
  return value.replace(/["\]\n]/g, ' ')
}

/** Everything the answer chrome needs from one assistant reply. */
export function parseAgentAnswer(content: string, data?: BootstrapData) {
  const { prose, suggestions } = splitAgentSuggestions(stripLeakedProgress(content))
  const { markdown, issues } = linkAgentEntities(prose, data)
  return { prose, suggestions, markdown, referencedIssues: issues }
}

/**
 * Some gateways let the model print report_progress payloads ({"title":…,"message":…}) as text; the server turns them
 * into step rows once the turn ends, so hide them while the reply is still streaming (including a partial one).
 */
export function stripLeakedProgress(content: string) {
  let rest = content.trimStart()
  const pattern = /^\{"title":"(?:[^"\\]|\\.)*"(?:,"message":"(?:[^"\\]|\\.)*")?\}\s*/
  for (let match = rest.match(pattern); match; match = rest.match(pattern)) rest = rest.slice(match[0].length)
  if (/^\{"title":/.test(rest) && !rest.includes('}')) return ''
  return rest.length === content.trimStart().length ? content : rest
}

