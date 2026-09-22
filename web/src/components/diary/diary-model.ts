/**
 * Diary entries are personal documents marked with icon "Diary" and
 * contentData.diaryDate (ISO YYYY-MM-DD). Reuses /api/documents CRUD.
 */
import type { FlowDocument, User } from '@/types/flow'

export const DIARY_ICON = 'Diary'

export interface DiaryEntry {
  id: string
  date: string
  title: string
  content: string
  contentState?: string
  contentData?: Record<string, unknown>
  updatedAt: string
  createdAt: string
  document: FlowDocument
}

export function isDiaryDocument(document: FlowDocument, viewer?: User): boolean {
  if (document.icon !== DIARY_ICON) return false
  if (viewer && document.creator?.id && document.creator.id !== viewer.id) return false
  return true
}

export function diaryDateOf(document: FlowDocument): string {
  const raw = document.contentData?.diaryDate
  if (typeof raw === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(raw)) return raw
  return document.createdAt.slice(0, 10)
}

export function toDiaryEntry(document: FlowDocument): DiaryEntry {
  return {
    id: document.id,
    date: diaryDateOf(document),
    title: document.title,
    content: document.content ?? '',
    contentState: document.contentState,
    contentData: document.contentData,
    updatedAt: document.updatedAt,
    createdAt: document.createdAt,
    document,
  }
}

export function isoDate(date = new Date()): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

export function formatDiaryHeading(iso: string, locale?: string): string {
  const [y, m, d] = iso.split('-').map(Number)
  const date = new Date(y, (m ?? 1) - 1, d ?? 1)
  return date.toLocaleDateString(locale, { weekday: 'long', month: 'long', day: 'numeric' })
}

/** ISO week key YYYY-Www for grouping (UTC-safe via local date parts). */
export function weekKey(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number)
  const date = new Date(y, (m ?? 1) - 1, d ?? 1)
  const target = new Date(date.valueOf())
  const dayNr = (date.getDay() + 6) % 7
  target.setDate(target.getDate() - dayNr + 3)
  const firstThursday = new Date(target.getFullYear(), 0, 4)
  const week =
    1 +
    Math.round(
      ((target.getTime() - firstThursday.getTime()) / 86_400_000 -
        3 +
        ((firstThursday.getDay() + 6) % 7)) /
        7,
    )
  return `${target.getFullYear()}-W${String(week).padStart(2, '0')}`
}

export function weekLabel(key: string): string {
  const week = key.split('-W')[1]
  return week ? `Week ${Number(week)}` : key
}

export function groupDiaryByWeek(entries: DiaryEntry[]): Array<{ key: string; label: string; entries: DiaryEntry[] }> {
  const sorted = [...entries].sort((a, b) => b.date.localeCompare(a.date) || b.updatedAt.localeCompare(a.updatedAt))
  const groups = new Map<string, DiaryEntry[]>()
  for (const entry of sorted) {
    const key = weekKey(entry.date)
    const bucket = groups.get(key) ?? []
    bucket.push(entry)
    groups.set(key, bucket)
  }
  return [...groups.entries()].map(([key, weekEntries]) => ({
    key,
    label: weekLabel(key),
    entries: weekEntries,
  }))
}

export function isEmptyDiaryContent(content: string): boolean {
  return !content.replace(/[#>*_\-\s]/g, '').trim()
}

export function diaryDocumentTitle(dateIso: string): string {
  return formatDiaryHeading(dateIso)
}

export function diaryCreatePayload(dateIso = isoDate()) {
  return {
    title: diaryDocumentTitle(dateIso),
    icon: DIARY_ICON,
    content: '',
    contentData: { diaryDate: dateIso, kind: 'diary' } as Record<string, unknown>,
  }
}
