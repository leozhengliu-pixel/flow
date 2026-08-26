import * as Popover from '@radix-ui/react-popover'
import { ArrowLeft, ChevronRight, CircleDashed, ExternalLink, Plus, Search } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'

import { useI18n } from '@/i18n/i18n'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { setIssueReleases } from '@/lib/api'
import { releasePath } from '@/lib/app-routes'
import type { BootstrapData, Issue } from '@/types/flow'
import { CheckboxMark } from '@/components/ui/checkbox-mark'

import './issue-release-picker.css'

type View = {kind:'recent'}|{kind:'pipelines'}|{kind:'pipeline';pipelineId:string}

export function IssueReleasePicker({data,issue}:{data:BootstrapData;issue:Issue}){
  const {t,formatDate}=useI18n()
  const [open,setOpen]=useState(false)
  const [view,setView]=useState<View>({kind:'recent'})
  const [saving,setSaving]=useState(false)
  const activeReleases=useMemo(()=>data.releases.filter(item=>!item.archivedAt),[data.releases])
  const currentIds=useMemo(()=>activeReleases.filter(item=>item.issueIds.includes(issue.id)).map(item=>item.id),[activeReleases,issue.id])
  const [selected,setSelected]=useState(currentIds)
  useEffect(()=>setSelected(currentIds),[currentIds])
  useEffect(()=>{const sync=(event:Event)=>{const detail=(event as CustomEvent<{issueId:string;releaseIds:string[]}>).detail;if(detail?.issueId===issue.id)setSelected(detail.releaseIds)};addEventListener('flow:issue-releases',sync);return()=>removeEventListener('flow:issue-releases',sync)},[issue.id])
  const pipelines=data.releasePipelines
  const selectedReleases=activeReleases.filter(item=>selected.includes(item.id))
  const visible=view.kind==='pipeline'?activeReleases.filter(item=>item.pipelineId===view.pipelineId):view.kind==='recent'?[...activeReleases].sort((a,b)=>Number(selected.includes(b.id))-Number(selected.includes(a.id))||b.updatedAt.localeCompare(a.updatedAt)):[]
  const toggle=async(id:string)=>{
    if(saving)return
    const next=selected.includes(id)?selected.filter(value=>value!==id):[...selected,id]
    setSelected(next);setSaving(true)
    try{await setIssueReleases(issue.id,next);dispatchEvent(new CustomEvent('flow:issue-releases',{detail:{issueId:issue.id,releaseIds:next}}))}catch(error){setSelected(selected);toast.error(error instanceof Error?error.message:t('Could not update releases'))}finally{setSaving(false)}
  }
  const changeOpen=(value:boolean)=>{setOpen(value);if(!value)setView({kind:'recent'})}
  const commandOptions:Array<{id:string;label:string;keywords?:string;disabled?:boolean}>=view.kind==='pipelines'?pipelines.map(item=>({id:item.id,label:item.name})):[...visible.map(item=>({id:item.id,label:item.name,keywords:pipelines.find(pipeline=>pipeline.id===item.pipelineId)?.name,disabled:Boolean(item.stageFrozenAt&&!selected.includes(item.id))})),...(view.kind==='recent'?[{id:'__all-pipelines',label:t('All pipelines…')}]:[])]
  const command=usePropertyCommand({closeOnSelect:false,open,options:commandOptions,resetKey:view.kind==='pipeline'?`pipeline:${view.pipelineId}`:view.kind,selectedIds:selected,onOpenChange:setOpen,onSelect:option=>{if(view.kind==='pipelines'){setView({kind:'pipeline',pipelineId:option.id});return}if(option.id==='__all-pipelines'){setView({kind:'pipelines'});return}void toggle(option.id)}})
  const back=()=>setView(view.kind==='pipeline'?{kind:'pipelines'}:{kind:'recent'})
  return <div className="issue-release-property">
    <Popover.Root open={open} onOpenChange={changeOpen}>
      <Popover.Trigger asChild><button className="issue-release-add" aria-label={t('Add to release')}><Plus/></button></Popover.Trigger>
      <Popover.Portal><Popover.Content className="issue-release-picker" align="start" side="bottom" sideOffset={4} collisionPadding={8} onOpenAutoFocus={event=>event.preventDefault()} onKeyDown={command.onKeyDown} onEscapeKeyDown={event=>{if(view.kind==='recent')return;event.preventDefault();back()}}>
        <label className="issue-release-search">{view.kind!=='recent'?<button type="button" aria-label={t('Back')} onClick={back}><ArrowLeft/></button>:<Search/>}<input ref={command.inputRef} role="searchbox" aria-label={t(view.kind==='recent'?'Add to release…':view.kind==='pipelines'?'All pipelines…':'Filter releases…')} placeholder={t(view.kind==='recent'?'Add to release…':view.kind==='pipelines'?'All pipelines…':'Filter releases…')} value={command.query} onChange={event=>command.onQueryChange(event.target.value)}/>{view.kind==='recent'&&<kbd>⌥ R</kbd>}</label>
        <div className="issue-release-results" role="listbox" aria-multiselectable={view.kind!=='pipelines'}>
          {view.kind==='pipelines'?command.filteredOptions.map(option=><button type="button" role="option" aria-selected={command.activeId===option.id} key={option.id} onPointerMove={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)}><CircleDashed/><span data-i18n-ignore>{option.label}</span><ChevronRight/></button>):command.filteredOptions.map(option=>{if(option.id==='__all-pipelines')return <button type="button" role="option" aria-selected={command.activeId===option.id} className="issue-release-all" key={option.id} onPointerMove={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)}><span>{option.label}</span><ChevronRight/></button>;const item=activeReleases.find(value=>value.id===option.id)!,pipeline=pipelines.find(value=>value.id===item.pipelineId),checked=command.isSelected(item.id),disabled=Boolean(item.stageFrozenAt&&!checked);return <button type="button" role="option" aria-selected={command.activeId===item.id} aria-checked={checked} aria-disabled={disabled} disabled={disabled||saving} key={item.id} onPointerMove={()=>command.setActiveId(item.id)} onFocus={()=>command.setActiveId(item.id)} onClick={()=>command.choose(option)}><span className="issue-release-check" role="checkbox" aria-checked={checked}>{checked&&<CheckboxMark/>}</span><span><strong data-i18n-ignore>{item.name}</strong><small><span data-i18n-ignore>{pipeline?.name}</span>{item.releasedAt||item.targetDate?<>{' · '}{formatDate(item.releasedAt||item.targetDate!,{month:'short',day:'numeric'})}</>:null}</small></span></button>})}
          {view.kind!=='pipelines'&&!command.filteredOptions.some(option=>option.id!=='__all-pipelines')&&<p>{t('No releases found')}</p>}{view.kind==='pipelines'&&!command.filteredOptions.length&&<p>{t('No pipelines found')}</p>}
        </div>
      </Popover.Content></Popover.Portal>
    </Popover.Root>
    <div className="issue-release-values">{selectedReleases.map(item=>{const pipeline=pipelines.find(value=>value.id===item.pipelineId);return <div key={item.id}><button className="issue-release-pill" onClick={()=>{setOpen(true);setView({kind:'pipeline',pipelineId:item.pipelineId??''})}}><CircleDashed/><span data-i18n-ignore>{pipeline?.name}</span><strong data-i18n-ignore>{item.name}</strong>{item.releasedAt||item.targetDate?<small>{formatDate(item.releasedAt||item.targetDate!,{month:'short',day:'numeric'})}</small>:null}</button>{pipeline&&<a href={releasePath(data.workspace.urlKey,pipeline.slugId,item.slugId)} aria-label={t('Open release')}><ExternalLink/></a>}</div>})}</div>
  </div>
}
