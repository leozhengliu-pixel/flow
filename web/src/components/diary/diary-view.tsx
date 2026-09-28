/**
 * LS-0200 DiaryView — week-grouped personal diary entries backed by documents.
 */
import { CalendarDays, Plus } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { createDocument, deleteDocument, listDocuments, updateDocument } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import {
  diaryCreatePayload,
  formatDiaryHeading,
  groupDiaryByWeek,
  isDiaryDocument,
  isEmptyDiaryContent,
  toDiaryEntry,
  type DiaryEntry,
} from './diary-model'
import './diary.css'

export function DiaryView({
  data,
  onReload,
}: {
  data: BootstrapData
  onReload?: () => Promise<void>
}) {
  const { t } = useI18n()
  const [entries, setEntries] = useState<DiaryEntry[]>(() =>
    data.documents.filter(doc => isDiaryDocument(doc, data.viewer)).map(toDiaryEntry),
  )
  const [busy, setBusy] = useState(false)
  const [drafts, setDrafts] = useState<Record<string, string>>({})

  const refresh = useCallback(async () => {
    try {
      const docs = await listDocuments({ archived: 'false' })
      setEntries(docs.filter(doc => isDiaryDocument(doc, data.viewer)).map(toDiaryEntry))
    } catch {
      setEntries(data.documents.filter(doc => isDiaryDocument(doc, data.viewer)).map(toDiaryEntry))
    }
  }, [data.documents, data.viewer])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const weeks = useMemo(() => groupDiaryByWeek(entries), [entries])

  const createEntry = async () => {
    if (busy) return
    setBusy(true)
    try {
      const created = await createDocument(diaryCreatePayload())
      setEntries(current => [toDiaryEntry(created), ...current.filter(item => item.id !== created.id)])
      setDrafts(current => ({ ...current, [created.id]: created.content ?? '' }))
      await onReload?.()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not create diary entry'))
    } finally {
      setBusy(false)
    }
  }

  const persistEntry = async (entry: DiaryEntry, content: string) => {
    if (content === entry.content) return
    if (isEmptyDiaryContent(content)) {
      try {
        await deleteDocument(entry.id)
        setEntries(current => current.filter(item => item.id !== entry.id))
        setDrafts(current => {
          const next = { ...current }
          delete next[entry.id]
          return next
        })
        await onReload?.()
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t('Could not delete empty diary entry'))
      }
      return
    }
    try {
      const updated = await updateDocument(entry.id, {
        content,
        contentData: { ...(entry.contentData ?? {}), diaryDate: entry.date, kind: 'diary' },
      })
      setEntries(current => current.map(item => (item.id === entry.id ? toDiaryEntry(updated) : item)))
      await onReload?.()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save diary entry'))
    }
  }

  return (
    <section aria-label={t('My diary')} className="diary-page">
      <header className="diary-page__header">
        <div>
          <h2>{t('My diary')}</h2>
          <p>{t('Capture meeting notes and decisions in one place.')}</p>
        </div>
        <button
          aria-label={t('New entry')}
          className="diary-page__new"
          disabled={busy}
          onClick={() => void createEntry()}
          type="button"
        >
          <Plus size={14} />
          {t('New entry')}
        </button>
      </header>

      {!weeks.length && (
        <div className="diary-empty">
          <CalendarDays size={22} aria-hidden="true" />
          <strong>{t('No entries yet')}</strong>
          <p>{t('Start a diary entry for today to keep decisions and meeting notes together.')}</p>
        </div>
      )}

      {weeks.map(week => (
        <div className="diary-week" key={week.key}>
          <div className="diary-week__label">{week.label}</div>
          {week.entries.map(entry => {
            const value = drafts[entry.id] ?? entry.content
            return (
              <article className="diary-entry" key={entry.id}>
                <div className="diary-entry__date">
                  <CalendarDays size={14} aria-hidden="true" />
                  <strong>{formatDiaryHeading(entry.date)}</strong>
                </div>
                <div className="diary-entry__editor">
                  <textarea
                    aria-label={t('Diary entry')}
                    placeholder={t('Write today’s notes…')}
                    value={value}
                    onChange={event =>
                      setDrafts(current => ({ ...current, [entry.id]: event.target.value }))
                    }
                    onBlur={() => void persistEntry(entry, value)}
                  />
                </div>
              </article>
            )
          })}
        </div>
      ))}
    </section>
  )
}

export default DiaryView
