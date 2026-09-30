import { useRef, useState } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronRight, Clock3 } from 'lucide-react'
import { toast } from 'sonner'
import { ReminderChoices } from '@/components/issue/issue-options-menu'
import { ProjectSubmenu } from '@/components/project-detail/project-menu-primitives'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
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

/** Linear's searchable Remind me submenu (An hour from now … Custom…). */
export function DocumentReminderSubmenu({ onCustom, onRemind }: { onCustom: () => void; onRemind: (remindAt: string) => void }) {
  return <ProjectSubmenu label="Remind me" icon={<Clock3 size={16}/>} shortcut="⇧ H" searchable className="project-resource-submenu">
    {close => <ReminderChoices onChoose={date => { close(); onRemind(date.toISOString()) }} onCustom={() => { close(); onCustom() }}/>}
  </ProjectSubmenu>
}

/** The same submenu styled for the document page's own "⋯" menu rows. */
export function DocumentPageReminderSubmenu({ onCustom, onRemind }: { onCustom: () => void; onRemind: (remindAt: string) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const trigger = useRef<HTMLDivElement>(null)
  const close = () => { setOpen(false); trigger.current?.focus() }
  return <DropdownMenu.Sub open={open} onOpenChange={setOpen}>
    <DropdownMenu.SubTrigger ref={trigger}><Clock3 size={14}/><span>{t('Remind me')}</span><ChevronRight size={12}/></DropdownMenu.SubTrigger>
    <DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className="project-action-menu project-action-submenu" data-menu="Remind me" aria-label={t('Remind me')} sideOffset={6} collisionPadding={16} onKeyDown={event => { if ((event.target as HTMLElement).closest('input,[cmdk-root]') && !['ArrowLeft', 'Escape'].includes(event.key)) event.stopPropagation() }}>
      <ReminderChoices onChoose={date => { close(); onRemind(date.toISOString()) }} onCustom={() => { close(); onCustom() }}/>
    </DropdownMenu.SubContent></DropdownMenu.Portal>
  </DropdownMenu.Sub>
}

export function DocumentCustomReminderDialog({ onOpenChange, onRemind, open }: { onOpenChange: (open: boolean) => void; onRemind: (remindAt: string) => Promise<boolean>; open: boolean }) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const [saving, setSaving] = useState(false)
  const valid = Boolean(value) && new Date(value).getTime() > Date.now()
  const submit = async () => {
    if (!valid || saving) return
    setSaving(true)
    const done = await onRemind(new Date(value).toISOString())
    setSaving(false)
    if (done) { setValue(''); onOpenChange(false) }
  }
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="action-dialog" overlayClassName="action-dialog-overlay">
      <DialogTitle>{t('Remind me')}</DialogTitle>
      <div className="action-dialog-date"><DateTimeControl label={t('Reminder time')} min={new Date().toISOString()} mode="datetime" value={value} onChange={setValue}/></div>
      <footer>
        <button onClick={() => onOpenChange(false)} type="button">{t('Cancel')}</button>
        <button className="primary" disabled={!valid || saving} onClick={() => void submit()} type="button">{t('Set reminder')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}
