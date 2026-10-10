import { useState } from 'react'
import { toast } from 'sonner'
import { useI18n } from '@/i18n/i18n'
import { createDocumentReminder } from '@/lib/api'

/**
 * State for a document's "Remind me" (⇧H) menu: the quick choices set the
 * reminder directly, "Custom…" opens a date-and-time dialog.
 */
export function useDocumentReminder(documentId: string) {
  const { t } = useI18n()
  const [customOpen, setCustomOpen] = useState(false)
  const remind = async (remindAt: string) => {
    try {
      await createDocumentReminder(documentId, remindAt)
      toast.success(t('Reminder set'))
      return true
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not set reminder'))
      return false
    }
  }
  return { customOpen, remind, setCustomOpen }
}
