import { ArrowDown, ArrowUp, Copy, Link2, Search, X } from 'lucide-react'
import type { Issue, IssueRelationType } from '@/types/flow'
import { useRef, useState } from 'react'
import { StatusIcon } from '@/components/issue/issue-icons'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'

const relationMeta:Record<IssueRelationType,{label:string;search:string;icon:typeof Link2}>={related:{label:'Related to',search:'Search issues…',icon:Link2},blocks:{label:'Blocking',search:'Search issues…',icon:ArrowUp},blocked_by:{label:'Blocked by',search:'Search issues…',icon:ArrowDown},duplicate:{label:'Duplicate of',search:'Search issues…',icon:Copy},parent_of:{label:'Parent of',search:'Search issues…',icon:ArrowUp},sub_issue_of:{label:'Sub-issue of',search:'Search issues…',icon:ArrowDown}}
export function RelationPicker({open,onOpenChange,type,issueId,issues,onSelect}:{open:boolean;onOpenChange:(v:boolean)=>void;type:IssueRelationType;issueId:string;issues:Issue[];onSelect:(id:string)=>void|Promise<void>}){
  const[saving,setSaving]=useState(false),[error,setError]=useState('')
  const root=useRef<HTMLDivElement>(null)
  const options=issues.filter(issue=>issue.id!==issueId).map(issue=>({id:issue.id,label:issue.title,keywords:issue.identifier,issue,disabled:saving}))
  const choose=async(issue:Issue)=>{if(saving)return;setSaving(true);setError('');try{await onSelect(issue.id);onOpenChange(false)}catch(reason){setError(reason instanceof Error?reason.message:'Relation could not be added')}finally{setSaving(false)}}
  const command=usePropertyCommand({closeOnSelect:false,open,options,onOpenChange,onSelect:option=>choose(option.issue)})
  useDismissibleLayer({open,refs:[root],onDismiss:()=>onOpenChange(false)})
  if(!open)return null
  const Icon=relationMeta[type].icon
  return <div className="relation-command-surface" role="dialog" aria-label={relationMeta[type].label} ref={root} onKeyDown={command.onKeyDown}><header><Icon size={15}/><strong>{relationMeta[type].label}</strong><button type="button" aria-label="Close" onClick={()=>onOpenChange(false)}><X size={14}/></button></header><div className="relation-command-search"><Search size={14}/><input ref={command.inputRef} role="searchbox" aria-label={relationMeta[type].search} placeholder={relationMeta[type].search} value={command.query} onChange={event=>command.onQueryChange(event.target.value)}/></div>{error&&<div className="relation-error" role="alert">{error}</div>}<div className="relation-command-results" role="listbox">{command.filteredOptions.map(option=><button type="button" role="option" aria-selected={option.id===command.activeId} disabled={saving} key={option.id} onMouseEnter={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)}><StatusIcon state={option.issue.state}/><span>{option.issue.identifier}</span><strong>{option.issue.title}</strong></button>)}{!command.filteredOptions.length&&<div className="core-property-empty">No issues found</div>}</div></div>
}
