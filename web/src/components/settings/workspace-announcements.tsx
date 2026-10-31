import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { updateWorkspaceSettings } from '@/lib/api'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import { SettingsRow, SettingsToggle } from './settings-primitives'

export function WorkspaceAnnouncements({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const current = data.settings?.onboardingAnnouncement as { enabled?: boolean; title?: string; message?: string } | undefined
  const [open, setOpen] = useState(false)
  const [enabled, setEnabled] = useState(Boolean(current?.enabled))
  const [title, setTitle] = useState(current?.title ?? '')
  const [message, setMessage] = useState(current?.message ?? '')
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      await updateWorkspaceSettings({ ...data.settings, onboardingAnnouncement: { enabled, title: title.trim(), message: message.trim() } })
      await onReload()
      setOpen(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save announcements'))
    } finally { setBusy(false) }
  }
  return <>
    <SettingsRow title={t('Announcements')} control>
      <button type="button" className="workspace-announcements-open" aria-label={t('Configure announcements')} onClick={() => { setEnabled(Boolean(current?.enabled)); setTitle(current?.title ?? ''); setMessage(current?.message ?? ''); setOpen(true) }}>{t(current?.enabled ? 'Enabled' : 'Disabled')}<ChevronRight size={14}/></button>
    </SettingsRow>
    <Dialog open={open} onOpenChange={setOpen}><DialogContent className="personal-dialog workspace-announcements-dialog"><DialogTitle>{t('Announcements')}</DialogTitle>
      <p>{t('Send an announcement to new members when they join the workspace.')}</p>
      <label className="workspace-announcements-toggle">{t('Enable announcements')}<SettingsToggle label={t('Enable announcements')} checked={enabled} onChange={setEnabled}/></label>
      <label>{t('Title')}<input className="settings-input" aria-label={t('Announcement title')} value={title} maxLength={200} onChange={event => setTitle(event.target.value)}/></label>
      <label>{t('Message')}<textarea className="settings-textarea" aria-label={t('Announcement message')} value={message} maxLength={4000} rows={6} onChange={event => setMessage(event.target.value)}/></label>
      <footer><button type="button" className="settings-action" onClick={() => setOpen(false)} disabled={busy}>{t('Cancel')}</button><button type="button" className="settings-action primary" onClick={() => void save()} disabled={busy || (enabled && (!title.trim() || !message.trim()))}>{t(busy ? 'Saving…' : 'Save')}</button></footer>
    </DialogContent></Dialog>
  </>
}
