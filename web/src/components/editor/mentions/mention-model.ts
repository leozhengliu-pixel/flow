import type { JSONContent } from '@tiptap/core'
import { userMentionPattern } from '@/components/agent/agent-answer-content'
import { peekAgentRecord } from '@/components/agent/agent-entity-fetch'
import {
  AGENT_ENTITY_KINDS,
  agentEntityKey,
  agentEntityLabel,
  agentEntityPath,
  agentTeamKeys,
  findAgentIssue,
  isAgentIdentifier,
  parseAgentEntityUrl,
  resolveAgentEntity,
  type AgentEntity,
  type AgentEntityKind,
  type AgentEntityTarget,
} from '@/components/agent/agent-entity-refs'
import type { BootstrapData, Issue, Project } from '@/types/flow'

/**
 * One inline reference node for every rich-text surface (`mention`): `mentionType` is the resource kind
 * (issue, project, initiative, document, user, team, cycle, label, milestone, customer, release, view, review, update
 * or comment), `id` the resource's key, `label` its text (identifier for issues, name / title otherwise), `href` its
 * in-app path and `title` the issue title. Chips resolve against live workspace data, so a renamed issue shows its
 * new title; the stored label is only the fallback (and what markdown / plain text carry).
 */
export type MentionAttrs = { id: string; label: string; href: string; title: string; mentionType: string }

export const MENTION_KINDS = AGENT_ENTITY_KINDS

/** The kind a node names. Older nodes carry `entity` (a hydrated URL) or nothing: those fall back to the kind their href names. */
export function mentionKind(attrs: Partial<MentionAttrs>, data?: BootstrapData): AgentEntityKind | undefined {
  const type = String(attrs.mentionType ?? 'user')
  if ((MENTION_KINDS as readonly string[]).includes(type)) return type as AgentEntityKind
  if (data && attrs.href) return parseAgentEntityUrl(attrs.href, data, attrs.label)?.kind
  return undefined
}

/** The target a node names, in the shape the entity chips and the by-id fetch cache resolve. */
export function mentionTarget(attrs: Partial<MentionAttrs>, data?: BootstrapData): AgentEntityTarget | undefined {
  const kind = mentionKind(attrs, data)
  if (!kind) return undefined
  const id = String(attrs.id ?? '')
  const href = attrs.href ? String(attrs.href) : undefined
  // An old hydrated URL mention keeps its key in the URL, not in `id`.
  if (!id && data && href) return parseAgentEntityUrl(href, data, attrs.label)
  return { kind, id, label: String(attrs.label ?? ''), ...(href ? { href } : {}) }
}

function relativeHref(href: string) {
  try {
    const url = new URL(href, typeof window !== 'undefined' ? window.location.origin : 'http://flow.local')
    return `${url.pathname}${url.search}${url.hash}`
  } catch {
    return href
  }
}

/** The node attributes for a resolved entity: its key, label and in-app path (plus the issue title). */
export function mentionAttrsForEntity(data: BootstrapData | undefined, entity: AgentEntity): MentionAttrs {
  const kind = entity.kind === 'link' ? entity.icon : entity.kind
  return {
    mentionType: kind,
    id: agentEntityKey(entity),
    label: agentEntityLabel(entity),
    href: entity.kind === 'user' || !data ? '' : agentEntityPath(data, entity),
    title: entity.kind === 'issue' ? entity.issue.title : '',
  }
}

/** Node attributes for a target found in a URL or identifier; the entity (when held or fetched) sharpens label and title. */
export function mentionAttrsForTarget(data: BootstrapData, target: AgentEntityTarget, pathOverride?: string): MentionAttrs {
  const entity = resolveMentionEntity(data, target)
  if (entity) {
    const attrs = mentionAttrsForEntity(data, entity)
    return pathOverride && entity.kind === 'link' ? { ...attrs, href: pathOverride } : attrs
  }
  return {
    mentionType: target.kind,
    id: target.id,
    label: target.label,
    href: pathOverride ?? (target.href ? relativeHref(target.href) : ''),
    title: '',
  }
}

/** The entity a target names: workspace data first, then the issue / project the by-id cache already fetched. */
export function resolveMentionEntity(data: BootstrapData, target: AgentEntityTarget): AgentEntity | undefined {
  const held = resolveAgentEntity(data, target)
  if (held) return held
  if (target.kind !== 'issue' && target.kind !== 'project') return undefined
  const record = peekAgentRecord(data.workspace.urlKey, target.kind, target.id)
  if (!record) return undefined
  return target.kind === 'issue' ? { kind: 'issue', issue: record as Issue } : { kind: 'project', project: record as Project }
}

function escapeLabel(value: string) {
  return value.replace(/[[\]\\]/g, match => `\\${match}`).replace(/\n/g, ' ')
}

function escapeUrl(value: string) {
  return value.replace(/[()\s]/g, match => encodeURIComponent(match))
}

/** What markdown and plain-text projections carry: people as `@name`, everything else as `[label](path)` (just the label without a path). */
export function mentionMarkdown(attrs: Partial<MentionAttrs>) {
  const label = String(attrs.label ?? '')
  if ((attrs.mentionType ?? 'user') === 'user') return `@${label}`
  return attrs.href ? `[${escapeLabel(label)}](${escapeUrl(String(attrs.href))})` : label
}

/** Plain text of a mention (copy as text, search, notification previews). */
export function mentionText(attrs: Partial<MentionAttrs>) {
  const label = String(attrs.label ?? '')
  return (attrs.mentionType ?? 'user') === 'user' ? `@${label}` : label
}

const GITHUB_PULL = /^https:\/\/(?:www\.)?github\.com\/([^/\s]+)\/([^/\s]+)\/pull\/(\d+)(?:[/?#].*)?$/i

function reviewUrlKey(url: string) {
  return url.trim().replace(/[?#].*$/, '').replace(/\/(?:files|commits|checks)?\/?$/, '').replace(/\/+$/, '').toLowerCase()
}

/**
 * A pull / merge request URL on GitHub or GitLab (or any provider URL a held review carries) that matches a review the
 * workspace holds: it becomes that review's mention, as Linear does for GitHub pull requests.
 */
export function reviewTargetForPullUrl(data: BootstrapData, url: string): AgentEntityTarget | undefined {
  if (!/^https?:\/\//i.test(url)) return undefined
  const match = url.match(GITHUB_PULL)
  const key = reviewUrlKey(url)
  const review = (data.reviews ?? []).find(item => {
    if (match && item.number === Number(match[3]) && item.repositoryOwner.toLowerCase() === match[1].toLowerCase() && item.repositoryName.toLowerCase() === match[2].toLowerCase()) return true
    return Boolean(item.url) && reviewUrlKey(item.url) === key
  })
  return review ? { kind: 'review', id: review.id, label: review.title } : undefined
}

function normalizeLabel(value: string) {
  return value.replace(/[​⁠]/g, '').replace(/\s+/g, ' ').trim().toLowerCase()
}

const IDENTIFIER_TEXT = /^([A-Za-z][A-Za-z0-9]*-\d+)(?:\s|$|[:–—-])/

/**
 * Whether a link reads as a bare reference to the resource it points at (its text is the URL, the identifier or the
 * resource's own title), which is when it becomes a mention. A link with its own text ("the rollout plan") stays a link.
 */
export function linkReadsAsReference(data: BootstrapData, target: AgentEntityTarget, href: string, text: string): boolean {
  const label = normalizeLabel(text)
  if (!label) return true
  let decoded = href
  try { decoded = decodeURIComponent(href) } catch { /* keep the raw href */ }
  const candidates = new Set([href, decoded, relativeHref(href), safeDecode(relativeHref(href))].map(normalizeLabel))
  if (candidates.has(label)) return true
  if (/^https?:\/\//i.test(text.trim()) || text.trim().startsWith('/')) return Boolean([...candidates].some(item => item.endsWith(label) || label.endsWith(item)))
  if (target.kind === 'issue') {
    const identifier = text.trim().match(IDENTIFIER_TEXT)?.[1]
    if (identifier && identifier.toUpperCase() === (findAgentIssue(data, target.id)?.identifier ?? target.id).toUpperCase()) return true
    if (identifier && identifier.toUpperCase() === target.label.toUpperCase()) return true
  }
  const entity = resolveMentionEntity(data, target)
  if (entity) {
    if (label === normalizeLabel(agentEntityLabel(entity))) return true
    if (entity.kind === 'issue' && (label === normalizeLabel(entity.issue.title) || label === normalizeLabel(`${entity.issue.identifier} ${entity.issue.title}`))) return true
  }
  if (target.kind === 'update' || target.kind === 'comment') return label === normalizeLabel(target.label)
  // An unheld project's label is the link's own text (the slug when bare): a title must wait for the fetch.
  if (target.kind === 'project') return false
  return label === normalizeLabel(target.label) && target.label !== ''
}

function safeDecode(value: string) {
  try { return decodeURIComponent(value) } catch { return value }
}

/** The target a link names, when it names a workspace resource and reads as a bare reference to it. */
export function linkMentionTarget(data: BootstrapData, href: string, text: string, anyText = false): AgentEntityTarget | undefined {
  const target = reviewTargetForPullUrl(data, href) ?? parseAgentEntityUrl(href, data, text)
  if (!target) return undefined
  return anyText || linkReadsAsReference(data, target, href, text) ? target : undefined
}

export type MentionConversionOptions = {
  /** Also turn bare Flow URLs, team-key identifiers and @names found in plain text into mentions. */
  text?: boolean
  /**
   * Convert every link to a Flow resource whatever its text says (markdown an agent or an API client wrote), not only links
   * that read as a bare reference (the URL, the identifier or the resource's title, which is what a person's editor keeps).
   */
  anyText?: boolean
}

export type MentionConversion = { content: JSONContent; changed: boolean; unresolved: AgentEntityTarget[] }

function mentionNode(attrs: MentionAttrs): JSONContent {
  return { type: 'mention', attrs: { ...attrs } }
}

const URL_IN_TEXT = /(?:https?:\/\/[^\s<>()[\]]+|\/[\w.~%-]+\/(?:issue|project|initiative|document|team|view|review|customer|profiles|pipeline|issue-label|project-label|initiative-label)\/[^\s<>()[\]]+)/g
const IDENTIFIER_IN_TEXT = /(?<![\w/#.@-])([A-Za-z][A-Za-z0-9]*-\d+)(?![\w-])/g

type Span = { start: number; end: number; attrs: MentionAttrs }

function textSpans(data: BootstrapData, text: string, unresolved: AgentEntityTarget[]): Span[] {
  const spans: Span[] = []
  const free = (start: number, end: number) => spans.every(span => end <= span.start || start >= span.end)
  const keys = agentTeamKeys(data)
  for (const match of text.matchAll(URL_IN_TEXT)) {
    const start = match.index ?? 0
    const trailing = match[0].match(/[.,;:!?]+$/)?.[0] ?? ''
    const url = match[0].slice(0, match[0].length - trailing.length)
    const target = reviewTargetForPullUrl(data, url) ?? parseAgentEntityUrl(url, data)
    if (!target || target.kind === 'update' || target.kind === 'comment') continue
    if (!free(start, start + url.length)) continue
    if (!resolveMentionEntity(data, target)) unresolved.push(target)
    spans.push({ start, end: start + url.length, attrs: mentionAttrsForTarget(data, target, target.kind === 'review' ? undefined : relativeHref(url)) })
  }
  for (const match of text.matchAll(IDENTIFIER_IN_TEXT)) {
    const start = match.index ?? 0
    const end = start + match[1].length
    const key = match[1].toUpperCase()
    if (!keys.has(key.replace(/-\d+$/, '')) || !free(start, end)) continue
    const issue = findAgentIssue(data, key)
    const target: AgentEntityTarget = issue ? { kind: 'issue', id: issue.id, label: issue.identifier } : { kind: 'issue', id: key, label: key }
    if (!issue && !resolveMentionEntity(data, target)) unresolved.push(target)
    spans.push({ start, end, attrs: mentionAttrsForTarget(data, target) })
  }
  const people = userMentionPattern(data)
  if (people) {
    for (const match of text.matchAll(people.pattern)) {
      const user = people.byName.get(match[2].toLowerCase())
      if (!user) continue
      const start = (match.index ?? 0) + match[1].length
      const end = start + 1 + match[2].length
      if (free(start, end)) spans.push({ start, end, attrs: mentionAttrsForEntity(data, { kind: 'user', user }) })
    }
  }
  return spans.sort((a, b) => a.start - b.start)
}

type Run = { nodes: JSONContent[]; href?: string }

/**
 * Turns the references in rich-text JSON into mention nodes: links to Flow resources whose text is the URL, the
 * identifier or the resource's own title, and (with `text`) bare Flow URLs, team-key identifiers and @names. Code,
 * code blocks and links with their own text are left alone. `unresolved` lists the issues / projects the client does
 * not hold (paged workspaces): callers fetch them by id and convert again.
 */
export function convertMentionLinks(content: JSONContent, data: BootstrapData | undefined, options: MentionConversionOptions = {}): MentionConversion {
  const unresolved: AgentEntityTarget[] = []
  if (!data) return { content, changed: false, unresolved }
  const needsFetch = (target: AgentEntityTarget) => (target.kind === 'issue' || target.kind === 'project') && !resolveMentionEntity(data, target)
  const convertChildren = (children: JSONContent[], parent: JSONContent): JSONContent[] | undefined => {
    if (parent.type === 'codeBlock' || parent.type === 'diagram') return undefined
    let changed = false
    const output: JSONContent[] = []
    const runs: Run[] = []
    for (const child of children) {
      const link = child.type === 'text' ? child.marks?.find(mark => mark.type === 'link') : undefined
      const href = link ? String(link.attrs?.href ?? '') : undefined
      const last = runs.at(-1)
      if (child.type === 'text' && href !== undefined && last?.href === href) last.nodes.push(child)
      else runs.push({ nodes: [child], href })
    }
    for (const run of runs) {
      const [first] = run.nodes
      if (run.href !== undefined) {
        const text = run.nodes.map(node => node.text ?? '').join('')
        const target = run.href ? linkMentionTarget(data, run.href, text, options.anyText) : undefined
        if (target) {
          if (needsFetch(target)) unresolved.push(target)
          output.push(mentionNode(mentionAttrsForTarget(data, target, target.kind === 'review' ? undefined : relativeHref(run.href))))
          changed = true
          continue
        }
        // A link with its own text to an issue / project the client does not hold may still name it: fetch before deciding.
        const pending = run.href ? parseAgentEntityUrl(run.href, data, text) : undefined
        if (pending && needsFetch(pending)) unresolved.push(pending)
        output.push(...run.nodes)
        continue
      }
      if (first.type === 'text') {
        const text = first.text ?? ''
        const spans = options.text && !first.marks?.some(mark => mark.type === 'code') ? textSpans(data, text, unresolved) : []
        if (!spans.length) { output.push(first); continue }
        let cursor = 0
        for (const span of spans) {
          if (span.start < cursor) continue
          if (span.start > cursor) output.push({ ...first, text: text.slice(cursor, span.start) })
          output.push(mentionNode(span.attrs))
          cursor = span.end
        }
        if (cursor < text.length) output.push({ ...first, text: text.slice(cursor) })
        changed = true
        continue
      }
      if (first.content?.length) {
        const nested = convertChildren(first.content, first)
        if (nested) { output.push({ ...first, content: nested }); changed = true } else output.push(first)
        continue
      }
      output.push(first)
    }
    return changed ? output : undefined
  }
  const next = content.content?.length ? convertChildren(content.content, content) : undefined
  return next ? { content: { ...content, content: next }, changed: true, unresolved } : { content, changed: false, unresolved }
}

/** Matches typed `ABC-123` against the workspace's team keys. */
export function typedIdentifierTarget(data: BootstrapData, identifier: string): AgentEntityTarget | undefined {
  if (!isAgentIdentifier(data, identifier)) return undefined
  const key = identifier.toUpperCase()
  const issue = findAgentIssue(data, key)
  return issue ? { kind: 'issue', id: issue.id, label: issue.identifier } : { kind: 'issue', id: key, label: key }
}
