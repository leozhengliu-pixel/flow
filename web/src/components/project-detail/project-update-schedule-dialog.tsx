import * as Dialog from '@radix-ui/react-dialog'
import { useState } from 'react'
import { toast } from 'sonner'
import { SelectControl } from '@/components/ui/select-control'
import { useI18n } from '@/i18n/i18n'
import type { Project, ProjectUpdateSchedule } from '@/types/flow'
import './project-action-menu.css'

import { projectSchedule } from './project-schedule'

export function ProjectUpdateScheduleDialog({ open, onOpenChange, project, onSave, defaultCadenceDays = 0 }: {open:boolean;onOpenChange:(open:boolean)=>void;project:Project;onSave:(schedule:ProjectUpdateSchedule)=>Promise<void>;defaultCadenceDays?:number}) {
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="project-update-schedule"><ScheduleForm key={open ? 'open' : 'closed'} project={project} defaultCadenceDays={defaultCadenceDays} onSave={onSave} onClose={() => onOpenChange(false)}/></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function ScheduleForm({project,onSave,onClose,defaultCadenceDays}:{project:Project;onSave:(schedule:ProjectUpdateSchedule)=>Promise<void>;onClose:()=>void;defaultCadenceDays:number}) {
  const {t} = useI18n()
  const [value,setValue] = useState(() => projectSchedule(project))
  const [saving,setSaving] = useState(false)
  const weekDays = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday']
  const frequencyOptions = [1,7,14,21,28,35,42,49,56].map(days => ({value:String(days),label:t(days === 1 ? 'Every day' : days === 7 ? 'Every week' : 'Every {count} weeks').replace('{count}',String(days / 7))}))
  const hourOptions = Array.from({length:24},(_,hour) => ({value:String(hour),label:`${String(hour).padStart(2,'0')}:00 - ${String((hour + 1) % 24).padStart(2,'0')}:00`}))
  const save = async () => {
    setSaving(true)
    try { await onSave(value); onClose() }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update schedule')) }
    finally { setSaving(false) }
  }
  return <>
    <header><Dialog.Title>{t('Update schedule')}</Dialog.Title><Dialog.Description>{t('Configure how often updates are expected when this project is in progress')}</Dialog.Description></header>
    <div className="project-update-schedule__options" role="radiogroup" aria-label={t('Update schedule')}>
      <label><input type="radio" name="project-update-schedule" checked={value.mode === 'default'} onChange={() => setValue({...value,mode:'default'})}/><span>{t('Default')}<small>{defaultCadenceDays ? t('Every {count} days').replace('{count}',String(defaultCadenceDays)) : t('No expectation for updates')}</small></span></label>
      <label><input type="radio" name="project-update-schedule" checked={value.mode === 'custom'} onChange={() => setValue({...value,mode:'custom'})}/><span>{t('Custom schedule')}</span></label>
      {value.mode === 'custom' && <div className="project-update-schedule__custom"><SelectControl label={t('Frequency')} value={String(value.frequencyDays)} options={frequencyOptions} onChange={frequencyDays => setValue({...value,frequencyDays:Number(frequencyDays)})}/>{value.frequencyDays > 1 && <><span>{t('on')}</span><SelectControl label={t('Day of week')} value={String(value.weekday)} options={weekDays.map((day,index) => ({value:String(index),label:t(day)}))} onChange={weekday => setValue({...value,weekday:Number(weekday)})}/></>}<span>{t('between')}</span><SelectControl label={t('Time range')} value={String(value.hour)} options={hourOptions} onChange={hour => setValue({...value,hour:Number(hour)})}/></div>}
      <label><input type="radio" name="project-update-schedule" checked={value.mode === 'never'} onChange={() => setValue({...value,mode:'never'})}/><span>{t('Never')}</span></label>
    </div>
    <footer><button type="button" onClick={onClose}>{t('Cancel')}</button><button className="is-primary" type="button" disabled={saving} onClick={() => void save()}>{t('Save')}</button></footer>
  </>
}
