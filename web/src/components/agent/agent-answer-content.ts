import type { BootstrapData, Issue, User } from '@/types/flow'
import { splitAgentDraft } from './agent-draft'
import { findAgentIssue, isAgentIdentifier, parseAgentEntityUrl, resolveAgentEntity, type AgentEntityTarget } from './agent-entity-refs'

export { findAgentIssue }

/** Shortcode the answer markdown uses for an inline entity chip (parsed by AgentEntityNode). */
export const AGENT_ENTITY_NODE = 'agentEntity'
const SUGGESTIONS_FENCE = 'suggestions'
const MAX_SUGGESTIONS = 3

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

// Code spans and fenced blocks, and entity shortcodes already in the text, are left untouched.
const PROTECTED = /(```[\s\S]*?(?:```|$)|`[^`\n]*`|\[agentEntity [^\]\n]*\])/
// Markdown link | <autolink> | bare URL | issue identifier.
const ENTITY_TOKEN = /\[([^\]\n]+)\]\(<?([^)\s>]+)>?(?:\s+"[^"]*")?\)|<(https?:\/\/[^>\s]+)>|(https?:\/\/[^\s<>()[\]]+)|(?<![\w/#.-])([A-Z][A-Z0-9]*-\d+)(?![\w-])/g

// A markdown link to an app path whose target holds spaces (the model writing a customer's name instead of its slug):
// markdown does not link it, so the spaces are percent-encoded first.
const SPACED_PATH_LINK = /\]\(\s*(\/[^()\n"<>]*?[ \t][^()\n"<>]*?)\s*\)/g

export type LinkAgentEntitiesOptions = {
  /**
   * Link every resource type (documents, people, teams, cycles, labels, milestones, customers, releases, views, reviews,
   * updates and comments) and the issues / projects the client does not hold (paged workspaces, or records created since
   * the page loaded; they are fetched by identifier / id when the chip renders). Without it only the issues
   * and projects workspace data already holds are linked (loop instructions share this helper with their own chips).
   */
  all?: boolean
}

/**
 * Turn issue identifiers, links whose text is an identifier ("DEV-1" / "DEV-1 Title") and links to Flow resource URLs
 * (and, with `all`, @mentions of people) into inline entity shortcodes. Identifiers whose prefix is not a team key stay plain text. Returns the
 * issues referenced (those workspace data holds) and every reference in order. Idempotent: shortcodes are left alone.
 */
export function linkAgentEntities(markdown: string, data?: BootstrapData, options: LinkAgentEntitiesOptions = {}): { markdown: string; issues: Issue[]; references: AgentEntityTarget[] } {
  if (!data || !markdown) return { markdown, issues: [], references: [] }
  const all = options.all ?? false
  const issues: Issue[] = []
  const references: AgentEntityTarget[] = []
  const chip = (target: AgentEntityTarget) => {
    if (!references.some(item => item.kind === target.kind && item.id === target.id)) references.push(target)
    if (target.kind === 'issue') {
      const issue = findAgentIssue(data, target.label)
      if (issue && !issues.includes(issue)) issues.push(issue)
    }
    const href = target.href ? ` href="${shortcodeHref(target.href)}"` : ''
    return `[${AGENT_ENTITY_NODE} kind="${target.kind}" id="${shortcodeValue(target.id)}" label="${shortcodeValue(target.label)}"${href}]`
  }
  const urlTarget = (url: string, linkText?: string): AgentEntityTarget | undefined => {
    const target = parseAgentEntityUrl(url, data, linkText)
    if (!target || all) return target
    return (target.kind === 'issue' || target.kind === 'project') && resolveAgentEntity(data, target) ? target : undefined
  }
  const identifierTarget = (identifier: string): AgentEntityTarget | undefined => {
    const issue = findAgentIssue(data, identifier)
    if (issue) return { kind: 'issue', id: issue.id, label: issue.identifier }
    return all && isAgentIdentifier(data, identifier) ? { kind: 'issue', id: identifier.toUpperCase(), label: identifier.toUpperCase() } : undefined
  }
  const linked = markdown.split(PROTECTED).map((segment, index) => index % 2 ? segment : segment.replace(SPACED_PATH_LINK, (_match: string, path: string) => `](${path.trim().replace(/\s+/g, '%20')})`).replace(ENTITY_TOKEN, (match: string, linkText?: string, linkUrl?: string, autolink?: string, bareUrl?: string, identifier?: string, offset?: number, source?: string) => {
    if (linkText !== undefined && linkUrl !== undefined) {
      if (offset && source?.[offset - 1] === '!') return match
      const textId = linkText.trim().match(IDENTIFIER_PREFIX)?.[1]
      const textTarget = textId ? identifierTarget(textId) : undefined
      if (textTarget) return chip(textTarget)
      const target = urlTarget(linkUrl, linkText)
      return target ? chip(target) : match
    }
    if (autolink) {
      const target = urlTarget(autolink)
      return target ? chip(target) : match
    }
    if (bareUrl) {
      const trailing = bareUrl.match(/[.,;:!?]+$/)?.[0] ?? ''
      const target = urlTarget(bareUrl.slice(0, bareUrl.length - trailing.length))
      return target ? chip(target) + trailing : match
    }
    if (identifier) {
      const target = identifierTarget(identifier)
      return target ? chip(target) : match
    }
    return match
  })).join('')
  const mentions = all ? userMentionPattern(data) : undefined
  const result = mentions
    ? linked.split(PROTECTED).map((segment, index) => index % 2 ? segment : segment.replace(mentions.pattern, (_match: string, lead: string, name: string) => {
      const user = mentions.byName.get(name.toLowerCase())
      return user ? `${lead}${chip({ kind: 'user', id: user.id, label: user.displayName || user.name })}` : _match
    })).join('')
    : linked
  return { markdown: result, issues, references }
}

function userMentionPattern(data: BootstrapData) {
  const byName = new Map<string, User>()
  for (const user of data.users ?? []) {
    if (user.active === false || user.app) continue
    for (const name of [user.displayName, user.name, user.username]) {
      const key = name?.trim().toLowerCase()
      if (key && key.length > 1 && !byName.has(key)) byName.set(key, user)
    }
  }
  if (!byName.size) return undefined
  const names = [...byName.keys()].sort((a, b) => b.length - a.length).map(name => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  return { byName, pattern: new RegExp(`(^|[\\s(])@(${names.join('|')})(?![\\w-])`, 'gi') }
}

function shortcodeValue(value: string) {
  return value.replace(/["\]|\n]/g, ' ')
}

function shortcodeHref(value: string) {
  return value.replace(/"/g, '%22').replace(/\]/g, '%5D').replace(/\s/g, '%20')
}

/** Everything the answer chrome needs from one assistant reply. */
export function parseAgentAnswer(content: string, data?: BootstrapData) {
  const { prose, suggestions } = splitAgentSuggestions(stripLeakedProgress(content))
  const { markdown, issues, references } = linkAgentEntities(prose, data, { all: true })
  return { prose, suggestions, markdown, referencedIssues: issues, references }
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

