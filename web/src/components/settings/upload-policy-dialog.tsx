import { useState } from 'react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import type { WorkspaceSettings } from '@/types/flow'
import { SettingsToggle } from './settings-primitives'

export function UploadPolicyDialog({settings,onSave,onClose}:{settings:WorkspaceSettings;onSave:(patch:Partial<WorkspaceSettings>)=>Promise<void>;onClose:()=>void}) {
  const {t}=useI18n()
  const [enabled,setEnabled]=useState(Boolean(settings.restrictFileUploads))
  const [extensions,setExtensions]=useState((settings.allowedFileExtensions??[]).join(', '))
  const [saving,setSaving]=useState(false)
  const [error,setError]=useState('')
  const save=async()=>{const values=[...new Set(extensions.split(/[\s,]+/).map(value=>value.toLowerCase().replace(/^\./,'')).filter(Boolean))];if(values.length>64||values.some(value=>!/^[a-z0-9]{1,16}$/.test(value))){setError(t('Enter file extensions separated by commas'));return}setSaving(true);try{await onSave({restrictFileUploads:enabled,allowedFileExtensions:values});onClose()}catch(reason){setError(reason instanceof Error?reason.message:t('Could not save policy'))}finally{setSaving(false)}}
  return <Dialog open onOpenChange={open=>!open&&onClose()}><DialogContent className="personal-dialog"><DialogTitle>{t('Restrict file uploads')}</DialogTitle><div className="settings-upload-policy"><label><span>{t('Restrict file uploads')}</span><SettingsToggle label={t('Restrict file uploads')} checked={enabled} onChange={setEnabled}/></label><p>{t('Images and videos remain allowed. Other files must match an allowed extension.')}</p><label>{t('Allowed file extensions')}<input aria-label={t('Allowed file extensions')} disabled={!enabled} placeholder="pdf, docx, xlsx, txt" value={extensions} onChange={event=>setExtensions(event.target.value)}/></label>{error&&<p role="alert">{error}</p>}<footer><button type="button" className="settings-action" onClick={onClose}>{t('Cancel')}</button><button type="button" className="settings-action primary" disabled={saving} onClick={()=>void save()}>{t('Save')}</button></footer></div></DialogContent></Dialog>
}
