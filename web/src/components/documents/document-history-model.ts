/**
 * The versions list of the history dialog.
 *
 * The live document is always the first, "Current" entry (author = the last editor, time = `updatedAt`). Stored revisions
 * follow it, newest first. The server may keep the PRE-update state as a revision (so revisions[0] is the previous
 * state) or a snapshot AFTER the edit (so revisions[0] equals the current document); both work: a revision identical to
 * the live document (same title and content) is dropped so no two rows look "current".
 */
import type { DocumentRevision, FlowDocument, User } from '@/types/flow'
import { diffDocuments, type HistoryDiffResult, type PMNode } from './document-history-diff'
import { documentSourceToDoc } from './document-json-doc'

export interface HistoryEntry {
  /** Stable key: the revision id, or `current` for the live document. */
  id: string
  current: boolean
  title: string
  content: string
  contentData?: Record<string, unknown>
  author: User
  createdAt: string
}

export interface HistoryDay { key: string; date: Date; entries: HistoryEntry[] }

export const CURRENT_ENTRY_ID = 'current'

export function buildHistoryEntries(document: Pick<FlowDocument, 'title' | 'content' | 'contentData' | 'creator' | 'updatedAt' | 'revisions'>): HistoryEntry[] {
  const revisions = [...(document.revisions ?? [])]
  const current: HistoryEntry = {
    id: CURRENT_ENTRY_ID,
    current: true,
    title: document.title,
    content: document.content,
    contentData: document.contentData,
    author: revisions[0]?.author ?? document.creator,
    createdAt: document.updatedAt,
  }
  const previous = revisions
    .filter(revision => !(revision.title === document.title && (revision.content ?? '') === (document.content ?? '')))
    .map((revision: DocumentRevision): HistoryEntry => ({
      id: revision.id,
      current: false,
      title: revision.title,
      content: revision.content,
      contentData: revision.contentData,
      author: revision.author,
      createdAt: revision.createdAt,
    }))
  return [current, ...previous]
}

const dayKey = (value: Date) => `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`

/** Groups entries (already newest first) by local calendar day. */
export function groupEntriesByDay(entries: HistoryEntry[]): HistoryDay[] {
  const days: HistoryDay[] = []
  for (const entry of entries) {
    const date = new Date(entry.createdAt)
    const key = Number.isFinite(date.getTime()) ? dayKey(date) : 'unknown'
    const last = days[days.length - 1]
    if (last && last.key === key) last.entries.push(entry)
    else days.push({ key, date, entries: [entry] })
  }
  return days
}

/** `today` / `yesterday` / `date` for a day heading, relative to `now`. */
export function dayHeadingKind(date: Date, now: Date = new Date()): 'today' | 'yesterday' | 'date' {
  const key = dayKey(date)
  if (key === dayKey(now)) return 'today'
  const yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  return key === dayKey(yesterday) ? 'yesterday' : 'date'
}

/** The preview of one entry; `previous` is the entry before it (older), compared when `highlight` is on. */
export function entryPreview(entry: HistoryEntry, previous: HistoryEntry | undefined, highlight: boolean): HistoryDiffResult {
  const doc: PMNode = documentSourceToDoc(entry)
  if (!highlight || !previous) return { doc, changes: 0 }
  return diffDocuments(documentSourceToDoc(previous), doc)
}
