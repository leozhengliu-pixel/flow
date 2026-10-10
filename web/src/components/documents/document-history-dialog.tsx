/**
 * "Restore version for [icon] title": the selected version read-only on the left (with what changed in it highlighted),
 * the versions grouped by day on the right, and a floating pill with Highlight changes / change navigator / Restore.
 */
import { ChevronDown, ChevronUp } from 'lucide-react'
import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Toggle } from '@/components/ui/toggle'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument } from '@/types/flow'

import { DocumentGlyph } from './document-icon'
import { buildHistoryEntries, dayHeadingKind, entryPreview, groupEntriesByDay, type HistoryEntry } from './document-history-model'
import { DocumentJsonView } from './document-json-view'
import './document-history-dialog.css'

export interface DocumentHistoryDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  document: FlowDocument
  /** Restores a stored revision; resolves once the document has been reloaded. */
  onRestore: (revisionId: string) => Promise<void>
}

const authorName = (entry: HistoryEntry) => entry.author?.displayName || entry.author?.name || ''

export function DocumentHistoryDialog({ open, onOpenChange, document, onRestore }: DocumentHistoryDialogProps) {
  const { t, formatDate } = useI18n()
  const entries = useMemo(() => buildHistoryEntries(document), [document])
  const days = useMemo(() => groupEntriesByDay(entries), [entries])
  const [selectedId, setSelectedId] = useState(entries[0]?.id ?? '')
  const [highlight, setHighlight] = useState(true)
  const [activeChange, setActiveChange] = useState(1)
  const [busy, setBusy] = useState(false)
  const [previewEl, setPreviewEl] = useState<HTMLDivElement | null>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const listId = useId()
  const hasHistory = (document.revisions ?? []).length > 0

  // Every time the dialog opens it starts on the current version with highlighting on.
  useEffect(() => {
    if (!open) return
    setSelectedId(entries[0]?.id ?? '')
    setHighlight(true)
    setActiveChange(1)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  // The selected version can disappear when the document reloads (e.g. after a restore).
  const selectedIndex = Math.max(0, entries.findIndex(entry => entry.id === selectedId))
  const selected = entries[selectedIndex] as HistoryEntry | undefined
  const previous = entries[selectedIndex + 1]
  const preview = useMemo(() => (selected ? entryPreview(selected, previous, highlight) : undefined), [selected, previous, highlight])
  const changes = highlight ? preview?.changes ?? 0 : 0

  const select = useCallback((id: string) => {
    setSelectedId(id)
    setActiveChange(1)
  }, [])

  // Keep the active change marked in the preview and scrolled into view.
  useEffect(() => {
    const root = previewEl
    if (!root) return
    root.querySelectorAll('[data-active]').forEach(element => element.removeAttribute('data-active'))
    if (!changes) return
    // Only the outermost element of a change is marked (a removed block also has struck text inside it).
    const targets = [...root.querySelectorAll(`[data-change-id="${activeChange}"]`)].filter(element => !element.parentElement?.closest(`[data-change-id="${activeChange}"]`))
    targets.forEach(element => element.setAttribute('data-active', ''))
    targets[0]?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
  }, [activeChange, changes, preview, previewEl])

  const step = (delta: number) => {
    if (!changes) return
    setActiveChange(value => ((value - 1 + delta + changes) % changes) + 1)
  }

  const onListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const move = event.key === 'ArrowDown' ? 1 : event.key === 'ArrowUp' ? -1 : event.key === 'Home' ? -Infinity : event.key === 'End' ? Infinity : 0
    if (!move) return
    event.preventDefault()
    const next = Math.min(entries.length - 1, Math.max(0, move === Infinity ? entries.length - 1 : move === -Infinity ? 0 : selectedIndex + move))
    select(entries[next].id)
    window.requestAnimationFrame(() => listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: 'nearest' }))
  }

  const restore = async () => {
    if (!selected || selected.current || busy) return
    setBusy(true)
    try {
      await onRestore(selected.id)
      onOpenChange(false)
      toast.success(t('Content has been restored.'), { id: 'document-version-restored' })
    } catch {
      toast.error(t('Could not restore this version.'))
    } finally {
      setBusy(false)
    }
  }

  const longDate = (value: string) => formatDate(value, { year: 'numeric', month: 'short', day: 'numeric' })
  const time = (value: string) => formatDate(value, { hour: 'numeric', minute: '2-digit' })
  const headingFor = (date: Date) => {
    const kind = dayHeadingKind(date)
    if (kind === 'today') return t('Today')
    if (kind === 'yesterday') return t('Yesterday')
    return formatDate(date, { month: 'short', day: 'numeric', ...(date.getFullYear() === new Date().getFullYear() ? {} : { year: 'numeric' }) })
  }
  const counter = t('{current} of {total}').replace('{current}', String(changes ? activeChange : 0)).replace('{total}', String(changes))

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        aria-describedby={undefined}
        className="document-history-dialog"
        closeLabel={t('Close modal dialog')}
        onOpenAutoFocus={event => {
          if (!hasHistory) return
          event.preventDefault()
          listRef.current?.focus()
        }}
      >
        <DialogTitle className="document-history-title">
          <span>{t('Restore version for')}</span>
          <DocumentGlyph document={document}/>
          <strong data-i18n-ignore>{document.title || t('Untitled')}</strong>
        </DialogTitle>
        {hasHistory && selected && preview ? (
          <div className="document-history-panel">
            <section aria-label={t('Version preview')} className="document-history-main">
              <div className="document-history-scroll" ref={setPreviewEl}>
                <span className="document-history-date" data-testid="history-date-chip">
                  <b>{longDate(selected.createdAt)}</b>
                  <span>{time(selected.createdAt)}</span>
                </span>
                <DocumentJsonView className="document-history-doc" doc={preview.doc} emptyLabel={t('Empty document')}/>
              </div>
              <div aria-label={t('Version actions')} className="document-history-pill" role="toolbar">
                {previous && <label className="document-history-highlight">
                  <Toggle checked={highlight} label={t('Highlight changes')} onChange={setHighlight} size="regular"/>
                  <span>{t('Highlight changes')}</span>
                </label>}
                {previous && highlight && (
                  <div className="document-history-navigator">
                    <span aria-live="polite" data-testid="history-change-counter">{changes ? counter : t('No changes')}</span>
                    <button aria-label={t('Previous change')} disabled={!changes} onClick={() => step(-1)} type="button"><ChevronUp size={14}/></button>
                    <button aria-label={t('Next change')} disabled={!changes} onClick={() => step(1)} type="button"><ChevronDown size={14}/></button>
                  </div>
                )}
                <div className="document-history-actions"><button
                  className="document-history-restore"
                  disabled={selected.current || busy}
                  onClick={() => void restore()}
                  title={selected.current ? t('This is the current version, you cannot restore it') : undefined}
                  type="button"
                >{t('Restore version')}</button></div>
              </div>
            </section>
            <nav aria-label={t('Document versions')} className="document-history-versions">
              <div
                aria-activedescendant={`${listId}-${selected.id}`}
                aria-label={t('Document versions')}
                className="document-history-list"
                onKeyDown={onListKeyDown}
                ref={listRef}
                role="listbox"
                tabIndex={0}
              >
                {days.map(day => (
                  <div className="document-history-day" key={day.key} role="group" aria-label={headingFor(day.date)}>
                    <h3>{headingFor(day.date)}</h3>
                    {day.entries.map(entry => (
                      <div
                        aria-selected={entry.id === selected.id}
                        className="document-history-version"
                        data-current={entry.current ? 'true' : undefined}
                        id={`${listId}-${entry.id}`}
                        key={entry.id}
                        onClick={() => select(entry.id)}
                        role="option"
                      >
                        <span className="document-history-version-time">
                          <b>{longDate(entry.createdAt)}</b>
                          <span>{time(entry.createdAt)}</span>
                          {entry.current && <em>{t('Current')}</em>}
                        </span>
                        <span className="document-history-version-author">
                          <UserAvatar avatarUrl={entry.author?.avatarUrl} className="document-history-avatar" name={authorName(entry) || '?'}/>
                          <span data-i18n-ignore>{authorName(entry)}</span>
                        </span>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            </nav>
          </div>
        ) : (
          <div className="document-history-empty">{t('There is no history yet.')}</div>
        )}
      </DialogContent>
    </Dialog>
  )
}
