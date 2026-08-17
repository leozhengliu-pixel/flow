import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import { Copy, Settings2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import type { Issue, IssueRelationType, IssueUpdateInput, Presence, WorkflowState } from '@/types/flow'
import { IssueOptionsMenu } from '@/components/issue/issue-options-menu'
import { FlowBranchIcon, FlowChevronIcon, FlowFavoriteIcon, FlowIssueIdIcon, FlowNextIcon, FlowPreviousIcon, FlowUrlIcon, FlowWorkIcon } from '@/components/issue/flow-header-icons'

export type IssueSaveState = 'idle' | 'saving' | 'saved' | 'error'

interface IssueHeaderProps {
  issue: Issue; states: WorkflowState[]; presence?: Presence[]; saveState: IssueSaveState; onRetrySave?: () => void
  position: number; total: number; onClose: () => void; onNavigate: (direction: 'next' | 'previous') => void
  onUpdate: (input: IssueUpdateInput) => Promise<void>; onDelete: () => Promise<void>; onRelation: (type: IssueRelationType) => void
}

export function IssueHeader({ issue, states, presence = [], saveState, onRetrySave, position, total, onClose, onNavigate, onUpdate, onDelete, onRelation }: IssueHeaderProps) {
  const [favorite,setFavorite]=useState(false),[working,setWorking]=useState(false)
  const canGoNext=position<total, canGoPrevious=position>1
  useEffect(()=>setFavorite(false),[issue.id])
  useEffect(()=>{const onKey=(event:KeyboardEvent)=>{if(isEditable(event.target))return;if((event.metaKey||event.ctrlKey)&&event.altKey&&event.key.toLowerCase()==='p'){event.preventDefault();void copyPrompt(issue)} };addEventListener('keydown',onKey);return()=>removeEventListener('keydown',onKey)},[issue])
  const startWork=async()=>{if(working)return;const started=states.find(state=>state.type==='started');if(!started){toast.error('No started status is configured');return}if(issue.state.id===started.id){toast('Issue is already in progress');return}setWorking(true);try{await onUpdate({stateId:started.id});toast.success(`Moved ${issue.identifier} to ${started.name}`)}finally{setWorking(false)}}
  return <header className="issue-header">
    <div className="issue-header-context"><button className="issue-breadcrumb" onClick={onClose}><span>My issues</span><span className="issue-breadcrumb-separator" aria-hidden="true">›</span><strong>{issue.identifier}</strong><span>{issue.title}</span></button><button className="issue-header-icon" type="button" role="switch" aria-checked={favorite} aria-label={favorite?'Remove from favorites':'Add to favorites'} onClick={()=>setFavorite(value=>!value)}><FlowFavoriteIcon/></button><IssueOptionsMenu issue={issue} onUpdate={onUpdate} onDelete={onDelete} onRelation={onRelation}/></div>
    {presence.length>0&&<div className="issue-presence" aria-label={`${presence.length} other ${presence.length===1?'person':'people'} viewing`}><span className="issue-presence-dot"/>{presence.slice(0,3).map(item=><span className="issue-presence-avatar" title={`${item.user.displayName} is viewing`} key={item.clientId}>{initials(item.user.displayName)}</span>)}</div>}
    {saveState==='error'?<button type="button" className="save-state error" onClick={onRetrySave}>Save failed · Retry</button>:<span className={`save-state ${saveState}`} role="status" aria-live="polite">{saveState==='saving'?'Saving...':saveState==='saved'?'Saved':''}</span>}
    <div className="issue-sequence" aria-label="Issue navigation"><span><strong>{position}</strong><i>/</i>{total}</span><div className="issue-sequence-buttons"><button type="button" aria-label="Go to next item" disabled={!canGoNext} onClick={()=>onNavigate('next')}><FlowNextIcon/></button><button type="button" aria-label="Go to previous item" disabled={!canGoPrevious} onClick={()=>onNavigate('previous')}><FlowPreviousIcon/></button></div></div>
    <div className="issue-command-strip">
      <CommandButton label="Copy issue URL" onClick={()=>copyText(location.href,'Issue URL copied to clipboard')}><FlowUrlIcon/></CommandButton>
      <CommandButton label="Copy issue ID" onClick={()=>copyText(issue.identifier,'Issue ID copied to clipboard')}><FlowIssueIdIcon/></CommandButton>
      <CommandButton label="Copy branch name" onClick={()=>copyText(branchName(issue),'Branch name copied to clipboard')}><FlowBranchIcon/></CommandButton>
      <div className="issue-work-control"><CommandButton label="Start work on issue" busy={working} onClick={()=>void startWork()}><FlowWorkIcon/></CommandButton><WorkMenu issue={issue}/></div>
    </div>
  </header>
}

function WorkMenu({issue}:{issue:Issue}){const[open,setOpen]=useState(false);useEffect(()=>{let armed=false;const onKey=(event:KeyboardEvent)=>{if(isEditable(event.target)||event.metaKey||event.ctrlKey||event.altKey)return;if(event.key.toLowerCase()==='w'){armed=true;setTimeout(()=>{armed=false},900);return}if(armed&&event.key.toLowerCase()==='o'){event.preventDefault();setOpen(true);armed=false}};addEventListener('keydown',onKey);return()=>removeEventListener('keydown',onKey)},[]);return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button className="issue-command-button" type="button" aria-label="Work on issue"><FlowChevronIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content className="work-menu" side="bottom" align="end" sideOffset={5} collisionPadding={10} onOpenAutoFocus={event=>event.preventDefault()}><Command loop><div className="work-menu-search"><Command.Input aria-label="Work on issue…" placeholder="Work on issue…" autoFocus/><kbd>W</kbd><span>then</span><kbd>O</kbd></div><Command.List><Command.Item onSelect={()=>{void copyPrompt(issue);setOpen(false)}}><Copy size={15}/><span>Copy as prompt</span><span className="work-menu-shortcut"><kbd>⌘</kbd><kbd>⌥</kbd><kbd>P</kbd></span></Command.Item><div className="work-menu-separator"/><Command.Item disabled aria-disabled="true"><Settings2 size={15}/><span>Configure coding tools…</span><small>External integration</small></Command.Item></Command.List></Command></Popover.Content></Popover.Portal></Popover.Root>}

function CommandButton({label,onClick,busy,children}:{label:string;onClick?:()=>void;busy?:boolean;children:React.ReactNode}){return <button className="issue-command-button" type="button" aria-label={label} data-tooltip={label} disabled={busy} onClick={onClick}>{busy?<span className="command-spinner"/>:children}</button>}
async function copyText(text:string,message:string){try{await navigator.clipboard.writeText(text);toast.success(message)}catch{toast.error('Could not write to clipboard')}}
async function copyPrompt(issue:Issue){await copyText(`# ${issue.identifier}: ${issue.title}\n\n${issue.description||'No description provided.'}\n\nIssue URL: ${location.href}`, 'Prompt copied to clipboard')}
function branchName(issue:Issue){
  const owner=(issue.assignee?.email||issue.creator.email).split('@')[0].toLowerCase().replace(/[^a-z0-9]/g,'')||'user'
  const title=issue.title.normalize('NFC').toLowerCase().replace(/[()[\]{}=]/g,'').replace(/[^\p{L}\p{N}_：:-]+/gu,'-').replace(/^-|-$/g,'')
  return `${owner}/${`${issue.identifier.toLowerCase()}-${title||'issue'}`.slice(0,100)}`
}
function isEditable(target:EventTarget|null){return target instanceof HTMLInputElement||target instanceof HTMLTextAreaElement||(target instanceof HTMLElement&&target.isContentEditable)}
function initials(value:string){return value.split(/\s+/).filter(Boolean).map(part=>part[0]).join('').slice(0,2).toUpperCase()||'?'}
