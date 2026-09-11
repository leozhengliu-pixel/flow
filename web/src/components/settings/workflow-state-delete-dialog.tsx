import { useState } from 'react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { PropertyMenu } from '@/components/property/property-menu'
import { StatusIcon } from '@/components/issue/issue-icons'
import { deleteWorkflowState, updateWorkflowState } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { WorkflowState } from '@/types/flow'
import './workflow-state-delete-dialog.css'

export function WorkflowStateDeleteDialog({teamId,state,states,usage,loading,loadError,onRetry,onClose,onDeleted,onDefaultChanged}:{teamId:string;state:WorkflowState;states:WorkflowState[];usage?:number;loading:boolean;loadError?:string;onRetry:()=>void;onClose:()=>void;onDeleted:()=>Promise<void>;onDefaultChanged:()=>Promise<void>}) {
  const {t}=useI18n()
  const [replacement,setReplacement]=useState('')
  const [saving,setSaving]=useState<'default'|'delete'>()
  const [error,setError]=useState<string>()
  const choices=states.filter(item=>item.id!==state.id&&!item.reserved)
  const lastOfType=states.filter(item=>item.type===state.type&&!item.reserved).length<=1
  const canDelete=!state.default&&!state.reserved&&!lastOfType&&!loading&&!loadError&&usage!==undefined&&choices.some(item=>item.id===replacement)
  const save=async(action:'default'|'delete')=>{
    if(saving||!replacement||(action==='delete'&&!canDelete))return
    setSaving(action)
    setError(undefined)
    try {
      if(action==='default') {await updateWorkflowState(teamId,replacement,{default:true});await onDefaultChanged()}
      else {await deleteWorkflowState(teamId,state.id,replacement);await onDeleted()}
    }catch(reason){setError(reason instanceof Error?reason.message:t('Could not delete status'))}
    finally{setSaving(undefined)}
  }
  return <Dialog open onOpenChange={open=>{if(!open&&!saving)onClose()}}><DialogContent className="workflow-state-delete-dialog" closeLabel={t('Close')} aria-describedby="workflow-state-delete-description" onEscapeKeyDown={event=>{if(saving)event.preventDefault()}} onInteractOutside={event=>{if(saving)event.preventDefault()}}>
    <DialogTitle>{t('Delete issue status')}</DialogTitle>
    <p id="workflow-state-delete-description">{t('Delete the status')} <strong data-i18n-ignore>{state.name}</strong>{t(' and move its issues to a replacement status.')}</p>
    {loading&&<p role="status">{t('Loading issue count…')}</p>}
    {loadError&&<div role="alert"><p>{t('Could not load issue count')}: {t(loadError)}</p><button type="button" disabled={loading} onClick={onRetry}>{t('Try again')}</button></div>}
    {usage!==undefined&&<p>{t(usage===1?'{count} issue will be moved.':'{count} issues will be moved.').replace('{count}',usage.toLocaleString())}</p>}
    {state.reserved&&<p role="alert">{t('Reserved statuses cannot be deleted.')}</p>}
    {lastOfType&&<p role="alert">{t("You can't delete the last status of a type.")}</p>}
    {state.default&&<p role="alert">{t('Change the default status before deleting this status.')}</p>}
    <PropertyMenu label={t('Replacement status')} ariaLabel={t('Replacement status')} icon={choices.some(item=>item.id===replacement)?<StatusIcon state={choices.find(item=>item.id===replacement)!}/>:<StatusIcon state={state}/>} valueIsEntityName value={choices.find(item=>item.id===replacement)?.name} selectedId={replacement} options={choices.map(item=>({id:item.id,label:item.name,i18nIgnore:true,icon:<StatusIcon state={item}/>}))} searchPlaceholder={t('Search statuses…')} onChange={id=>{if(!saving)setReplacement(id)}}/>
    {state.default&&!state.reserved&&<button type="button" disabled={!replacement||Boolean(saving)} onClick={()=>void save('default')}>{t(saving==='default'?'Changing default status…':'Set replacement as default')}</button>}
    {error&&<p role="alert">{t(error)}</p>}
    <footer><button type="button" disabled={Boolean(saving)} onClick={onClose}>{t('Cancel')}</button><button className="danger" type="button" disabled={!canDelete||Boolean(saving)} onClick={()=>void save('delete')}>{t(saving==='delete'?'Deleting…':'Delete status')}</button></footer>
  </DialogContent></Dialog>
}
