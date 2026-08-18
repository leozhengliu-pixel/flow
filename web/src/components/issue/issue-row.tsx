import { format } from 'date-fns'
import { Archive, Copy, Link2, Trash2 } from 'lucide-react'
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu'
import { PriorityIcon, StatusIcon } from './issue-icons'
import type { Issue } from '@/types/flow'
export function Avatar({name}:{name:string}) {
  const initials = name.split(/\s|@/).filter(Boolean).slice(0,2).map(part => part[0]).join('').toUpperCase()
  const script = /[\u3400-\u9fff\uf900-\ufaff]/.test(initials) ? 'cjk' : 'latin'
  return <span className="avatar" aria-label={name} data-script={script}>{initials}</span>
}
export function IssueRow({issue,selected,onSelect,onOpen}:{issue:Issue;selected?:boolean;onSelect:(id:string)=>void;onOpen:(issue:Issue)=>void}){return <ContextMenu><ContextMenuTrigger asChild><button className={`issue-row ${selected?'selected':''}`} onClick={()=>onOpen(issue)}><span className="issue-select" onClick={e=>{e.stopPropagation();onSelect(issue.id)}}><span className={`checkbox ${selected?'checked':''}`}/></span><PriorityIcon priority={issue.priority}/><StatusIcon state={issue.state}/><span className="issue-identifier">{issue.identifier}</span><span className="issue-title">{issue.title}</span><span className="issue-labels">{issue.labels.slice(0,2).map(label=><span className="label-chip" key={label.id}><i style={{background:label.color}}/>{label.name}</span>)}</span>{issue.assignee?<Avatar name={issue.assignee.displayName}/>:<span/>}<time>{format(new Date(issue.createdAt),'MMM d')}</time></button></ContextMenuTrigger><ContextMenuContent><ContextMenuItem><Link2 size={14}/>Open issue</ContextMenuItem><ContextMenuItem><Copy size={14}/>Copy issue link</ContextMenuItem><ContextMenuSeparator/><ContextMenuItem><Archive size={14}/>Archive</ContextMenuItem><ContextMenuItem className="danger"><Trash2 size={14}/>Delete</ContextMenuItem></ContextMenuContent></ContextMenu>}
