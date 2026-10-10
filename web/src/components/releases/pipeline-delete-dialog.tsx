import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { deleteReleasePipeline } from '@/lib/api'
import type { ReleasePipeline } from '@/types/flow'

import './pipeline-settings-list.css'

/**
 * Linear's pipeline delete confirmation: names the pipeline (or counts them),
 * explains the 30-day "Recently deleted pipelines" retention and asks for the
 * pipeline name ("delete" for several) before the destructive button unlocks.
 */
export function PipelineDeleteDialog({ pipelines, onClose, onDeleted, onViewDeleted }: {
  pipelines: ReleasePipeline[]
  onClose: () => void
  onDeleted: () => Promise<void> | void
  /** Opens the recently deleted pipelines list from the success toast. */
  onViewDeleted?: () => void
}) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const single = pipelines.length === 1 ? pipelines[0] : undefined
  const prompt = single ? single.name.trim() : 'delete'
  const confirmed = value.trim() === prompt
  const remove = async () => {
    if (!confirmed || busy) return
    setBusy(true)
    try {
      for (const pipeline of pipelines) await deleteReleasePipeline(pipeline.id)
      await onDeleted()
      const message = single ? t('Pipeline deleted') : t('{count} pipelines deleted').replace('{count}', String(pipelines.length))
      // Long enough to reach the toast's link, like Linear's undo toasts.
      toast.success(message, { duration: 8000, ...(onViewDeleted ? { action: { label: t('View recently deleted pipelines'), onClick: onViewDeleted } } : {}) })
      onClose()
    } catch (error) {
      setBusy(false)
      toast.error(error instanceof Error ? error.message : t('Could not delete release pipeline'))
    }
  }
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
    <DialogContent className="flow-pipeline-confirm-dialog" closeLabel={t('Close')} aria-describedby="flow-pipeline-delete-description">
      <DialogTitle>{single ? withName(t('Delete the pipeline "{name}"?'), <span data-i18n-ignore>{single.name}</span>) : t('Delete {count} pipelines?').replace('{count}', String(pipelines.length))}</DialogTitle>
      <p id="flow-pipeline-delete-description">{t('Deleted pipelines are available in the "Recently deleted pipelines" view for 30 days, before being permanently deleted along with all their releases.')}</p>
      <label className="flow-pipeline-confirm-field">
        <span>{withName(t('Type {name} to confirm'), <strong data-i18n-ignore>{prompt}</strong>)}</span>
        <input autoFocus aria-label={t(single ? 'Pipeline name' : 'Confirmation')} autoComplete="off" spellCheck={false} value={value} onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); void remove() } }}/>
      </label>
      <footer>
        <button type="button" className="flow-pipeline-confirm-button" disabled={busy} onClick={onClose}>{t('Cancel')}</button>
        <button type="button" className="flow-pipeline-confirm-button danger" disabled={!confirmed || busy} onClick={() => void remove()}>{t(busy ? 'Deleting…' : 'Delete')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}

/** Puts a node where "{name}" sits in a translated sentence. */
function withName(text: string, name: ReactNode) {
  const [before, after = ''] = text.split('{name}')
  return <>{before}{name}{after}</>
}
