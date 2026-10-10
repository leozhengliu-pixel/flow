/**
 * What ⌘K acts on. Surfaces register the issue(s) they show (issue page, peek
 * pane, list/board selection) or the project/document page they render; the
 * command menu reads the most specific registration and lists its actions first.
 */
import { useEffect, useId, useRef, useSyncExternalStore } from 'react'
import type { FlowDocument, Issue, IssueRelationType, IssueUpdateInput, Project } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'

/** A context issue: the full record when the surface has one, otherwise the row it shows. */
export type CommandIssueRef = Pick<Issue, 'id' | 'identifier' | 'title'> & Partial<Issue>

export interface IssueCommandContext {
  kind: 'issues'
  /** `selection` (list/board multi-select) wins over an open issue (`detail`). */
  source: 'selection' | 'detail'
  issues: CommandIssueRef[]
  /** Single-issue surfaces route updates through their own (optimistic, autosaving) handler. */
  onUpdate?: (input: IssueUpdateInput) => Promise<unknown>
  onDelete?: () => Promise<void>
  onRelation?: (type: IssueRelationType, relatedIssueId: string) => Promise<void>
}

export interface ProjectCommandContext {
  kind: 'project'
  project: Project
  onUpdate: (input: ProjectMutationInput) => Promise<unknown>
}

export interface DocumentCommandContext {
  kind: 'document'
  document: FlowDocument
  /** A multi-selection (list rows): commands then apply to each of them; `document` is the first. */
  documents?: FlowDocument[]
}

export type CommandContext = IssueCommandContext | ProjectCommandContext | DocumentCommandContext

type Entry = { key: string; order: number; read: () => CommandContext }

const PRIORITY: Record<string, number> = { selection: 3, detail: 2, project: 1, document: 1 }
const entries = new Map<string, Entry>()
const listeners = new Set<() => void>()
let sequence = 0
let version = 0

function emit() { version += 1; for (const listener of listeners) listener() }
function priority(context: CommandContext) { return PRIORITY[context.kind === 'issues' ? context.source : context.kind] ?? 0 }

/** The active context: highest priority first, then the most recently registered. */
export function getCommandContext(): CommandContext | undefined {
  let best: { context: CommandContext; rank: number; order: number } | undefined
  for (const entry of entries.values()) {
    const context = entry.read()
    if (context.kind === 'issues' && !context.issues.length) continue
    const rank = priority(context)
    if (!best || rank > best.rank || (rank === best.rank && entry.order > best.order)) best = { context, rank, order: entry.order }
  }
  return best?.context
}

export function subscribeCommandContext(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** Re-renders when a registration is added, removed or its identity changes. */
export function useCommandContext() {
  useSyncExternalStore(subscribeCommandContext, () => version, () => version)
  return getCommandContext()
}

/** Test helper: drop every registration. */
export function resetCommandContext() { entries.clear(); emit() }

function identity(context: CommandContext | undefined) {
  if (!context) return ''
  if (context.kind === 'issues') return `issues:${context.source}:${context.issues.map(issue => issue.id).join(',')}`
  if (context.kind === 'project') return `project:${context.project.id}`
  return `document:${(context.documents ?? [context.document]).map(item => item.id).join(',')}`
}

/**
 * Expose `context` to ⌘K while the calling component is mounted. Handlers and
 * records are read lazily, so passing fresh closures every render is fine.
 */
export function useRegisterCommandContext(context: CommandContext | undefined) {
  const key = useId()
  const latest = useRef(context)
  latest.current = context
  const id = identity(context)
  useEffect(() => {
    if (!id) return
    entries.set(key, { key, order: ++sequence, read: () => latest.current ?? { kind: 'issues', source: 'detail', issues: [] } })
    emit()
    return () => { entries.delete(key); emit() }
  }, [id, key])
}

/** Window event that opens ⌘K (App listens); surfaces register a context first to scope it ("Actions" buttons). */
export const OPEN_COMMAND_MENU_EVENT = 'flow:open-command-menu'
export function openCommandMenu() { window.dispatchEvent(new Event(OPEN_COMMAND_MENU_EVENT)) }
