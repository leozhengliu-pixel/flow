import * as Dialog from '@radix-ui/react-dialog'
import { Bot, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import type { BootstrapData } from '@/types/flow'
import { listApplications, saveApplication, type ApplicationInstallation } from '@/lib/application-agents'
import { useI18n } from '@/i18n/i18n'
import './application-agents.css'

export function ApplicationMembers({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}) {
  const {t}=useI18n()
  const [open,setOpen]=useState(false),[items,setItems]=useState<ApplicationInstallation[]>([]),[error,setError]=useState(''),[busy,setBusy]=useState(false),[secret,setSecret]=useState('')
  const [editing,setEditing]=useState<ApplicationInstallation>(),[clientId,setClientId]=useState(''),[builtin,setBuiltin]=useState(true),[webhook,setWebhook]=useState(''),[teamIds,setTeamIds]=useState<string[]>([]),[scopes,setScopes]=useState(['read','app:mentionable','app:assignable'])
  useEffect(()=>{if(open)void listApplications(data.workspace.urlKey).then(setItems).catch(error=>setError(String(error.message??error)))},[open,data.workspace.urlKey])
  const edit=(item?:ApplicationInstallation)=>{setEditing(item);setClientId(item?.clientId??'');setBuiltin(item?.builtin??true);setWebhook(item?.webhookUrl??'');setTeamIds(item?.teamIds??[]);setScopes(item?.scopes??['read','app:mentionable','app:assignable']);setSecret('');setError('')}
  const save=async(active=true)=>{
    if(busy)return;setBusy(true);setError('')
    try{const result=await saveApplication(data.workspace.urlKey,{...editing,clientId,builtin,webhookUrl:webhook,teamIds,scopes,active});setEditing(result.application);setSecret(result.webhookSecret);setItems(await listApplications(data.workspace.urlKey));await onReload();toast.success(t(active?'Application saved':'Application suspended'))}catch(error){setError(String(error instanceof Error?error.message:error))}finally{setBusy(false)}
  }
  if(data.viewerRole!=='admin'&&data.viewerRole!=='owner')return null
  return <Dialog.Root open={open} onOpenChange={value=>{setOpen(value);if(!value)setSecret('')}}><Dialog.Trigger asChild><button className="application-members-trigger" type="button"><Bot size={14}/>{t('Applications')}</button></Dialog.Trigger><Dialog.Portal><Dialog.Overlay className="application-members-overlay" data-flow-motion="backdrop"/><Dialog.Content className="application-members-dialog" data-flow-motion="dialog" aria-describedby={undefined}><header><Dialog.Title>{t('Application members')}</Dialog.Title><Dialog.Close asChild><button type="button" aria-label={t('Close')}><X size={16}/></button></Dialog.Close></header>
    <div className="application-members-list">{items.map(item=><button type="button" key={item.id} onClick={()=>edit(item)} aria-pressed={editing?.id===item.id}><Bot size={16}/><span>{item.name}</span><small>{t(item.active?'Active':'Suspended')}</small></button>)}<button type="button" onClick={()=>edit()}>{t('Add application')}</button></div>
    <form onSubmit={event=>{event.preventDefault();void save()}}>
      {!editing&&<label>{t('Application type')}<select value={builtin?'builtin':'external'} onChange={event=>setBuiltin(event.target.value==='builtin')}><option value="builtin">Flow Agent</option><option value="external">{t('OAuth application')}</option></select></label>}
      {!builtin&&<><label>{t('OAuth client ID')}<input required disabled={Boolean(editing)} value={clientId} onChange={event=>setClientId(event.target.value)}/></label><label>{t('Webhook URL')}<input type="url" placeholder="https://" value={webhook} onChange={event=>setWebhook(event.target.value)}/></label><fieldset><legend>{t('Permissions')}</legend>{['read','write','app:mentionable','app:assignable'].map(scope=><label key={scope}><input type="checkbox" checked={scopes.includes(scope)} disabled={scope==='read'} onChange={event=>setScopes(current=>event.target.checked?[...current,scope]:current.filter(value=>value!==scope))}/>{t(({read:'Read workspace data',write:'Modify workspace data','app:mentionable':'Allow mentions','app:assignable':'Allow issue delegation'} as Record<string,string>)[scope])}</label>)}</fieldset></>}
      <fieldset><legend>{t('Team access')}</legend>{data.teams.filter(team=>!team.retiredAt).map(team=><label key={team.id}><input type="checkbox" checked={teamIds.includes(team.id)} onChange={event=>setTeamIds(current=>event.target.checked?[...current,team.id]:current.filter(id=>id!==team.id))}/><span data-i18n-ignore>{team.name}</span></label>)}</fieldset>
      {error&&<p role="alert">{error}</p>}{secret&&!builtin&&<label>{t('Webhook signing secret')}<input readOnly value={secret}/></label>}
      <footer>{editing?.active&&<button type="button" disabled={busy} onClick={()=>void save(false)}>{t('Suspend application')}</button>}<button type="submit" disabled={busy||!teamIds.length||!builtin&&!clientId.trim()}>{t(busy?'Saving…':'Save')}</button></footer>
    </form>
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}
