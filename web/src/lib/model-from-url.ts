/**
 * Parse markdown / href → workspace model refs for hydrate hooks (LS-0738).
 * REST-only; no GraphQL. Complements MentionHydrationPlugin (LS-0407).
 */

import { normalizeEntityType, type WorkspaceEntityType } from '@/store/entity-type'

/** Linear-style markdown mention href: `Issue:uuid` or `Issue:uuid?href=...` */
const MARKDOWN_MENTION_HREF =
  /^([a-zA-Z]+):([a-zA-Z0-9_-]+)(?:\?href=([^\s)]+))?$/

/** Flow / Linear app paths under /{workspace}/… */
const ISSUE_PATH = /(?:^|\/)(?:[\w.-]+)\/issue\/([A-Za-z]+-\d+)(?:\/|$|\?|#)/i
const PROJECT_PATH = /(?:^|\/)(?:[\w.-]+)\/project\/([\w.%+-]+)(?:\/|$|\?|#)/i
const INITIATIVE_PATH = /(?:^|\/)(?:[\w.-]+)\/initiative\/([\w.%+-]+)(?:\/|$|\?|#)/i
const DOCUMENT_PATH = /(?:^|\/)(?:[\w.-]+)\/document\/([\w.%+-]+)(?:\/|$|\?|#)/i
const PROFILE_PATH = /(?:^|\/)(?:[\w.-]+)\/(?:profiles|member)\/([\w.-]+)(?:\/|$|\?|#)/i
const TEAM_PATH = /(?:^|\/)(?:[\w.-]+)\/team\/([\w.-]+)(?:\/|$|\?|#)/i

/** Markdown inline links + bare http(s) / absolute paths. */
const MD_LINK = /\[([^\]]*)\]\(([^)\s]+)\)/g
const BARE_URL = /(?:^|[\s(])((?:https?:\/\/|\/)[^\s<>)"']+)/g

export type ModelRef = {
  type: WorkspaceEntityType | string
  /** UUID preferred; may be identifier / slugId until store resolve. */
  id: string
}

const TYPE_ALIASES: Record<string, WorkspaceEntityType> = {
  Issue: 'issue',
  Project: 'project',
  Initiative: 'initiative',
  Document: 'document',
  User: 'user',
  Team: 'team',
  Cycle: 'cycle',
  AgentSession: 'agentSession',
  Ask: 'ask',
  SavedView: 'savedView',
  WorkflowState: 'state',
  IssueLabel: 'label',
}

function decodeMaybe(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function pathnameOf(href: string): string {
  try {
    const base = typeof window !== 'undefined' ? window.location.origin : 'https://flow.local'
    return new URL(href, base).pathname
  } catch {
    return href
  }
}

/** Parse a single href (app URL or Linear mention href) into a model ref. */
export function getModelRefFromHref(href: string): ModelRef | null {
  if (!href || href === 'streamdown:incomplete-link') return null
  const trimmed = href.trim()

  const mention = trimmed.match(MARKDOWN_MENTION_HREF)
  if (mention) {
    const rawType = mention[1]
    const id = mention[2]
    const type = TYPE_ALIASES[rawType] ?? normalizeEntityType(rawType)
    return { type, id }
  }

  const path = pathnameOf(trimmed)
  let match = path.match(ISSUE_PATH)
  if (match) return { type: 'issue', id: match[1].toUpperCase() }
  match = path.match(PROJECT_PATH)
  if (match) return { type: 'project', id: decodeMaybe(match[1]) }
  match = path.match(INITIATIVE_PATH)
  if (match) return { type: 'initiative', id: decodeMaybe(match[1]) }
  match = path.match(DOCUMENT_PATH)
  if (match) return { type: 'document', id: decodeMaybe(match[1]) }
  match = path.match(PROFILE_PATH)
  if (match) return { type: 'user', id: match[1] }
  match = path.match(TEAM_PATH)
  if (match) return { type: 'team', id: match[1] }
  return null
}

function pushUnique(out: ModelRef[], seen: Set<string>, ref: ModelRef | null) {
  if (!ref?.id) return
  const key = `${ref.type}:${ref.id}`
  if (seen.has(key)) return
  seen.add(key)
  out.push(ref)
}

/**
 * Extract unique `{type,id}` refs from markdown (links, bare URLs, mention hrefs).
 */
export function extractModelsFromMarkdown(markdown: string | null | undefined): ModelRef[] {
  if (!markdown) return []
  const out: ModelRef[] = []
  const seen = new Set<string>()

  for (const match of markdown.matchAll(MD_LINK)) {
    pushUnique(out, seen, getModelRefFromHref(match[2]))
  }
  for (const match of markdown.matchAll(BARE_URL)) {
    pushUnique(out, seen, getModelRefFromHref(match[1]))
  }
  // Standalone mention hrefs (PascalCase or known lowercase) in AI / exported markdown.
  const knownTypes = new Set(Object.values(TYPE_ALIASES))
  for (const match of markdown.matchAll(/\b([A-Za-z]+):([a-zA-Z0-9_-]+)\b/g)) {
    const normalized = normalizeEntityType(match[1])
    if (!TYPE_ALIASES[match[1]] && !knownTypes.has(normalized as WorkspaceEntityType)) continue
    pushUnique(out, seen, getModelRefFromHref(`${match[1]}:${match[2]}`))
  }

  return out
}
