import * as Popover from '@radix-ui/react-popover'
import { FlowTooltip } from '@/components/ui/tooltip'
import { Command } from 'cmdk'
import { ExternalLink, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from '@/lib/toast'
import type { ActivityEvent, BootstrapData, Issue, IssueRelationType, IssueUpdateInput, Presence, WorkflowState } from '@/types/flow'
import { IssueOptionsMenu, type IssueOptionsActions } from '@/components/issue/issue-options-menu'
import { FlowBranchIcon, FlowChevronIcon, FlowCodingToolsIcon, FlowFavoriteFilledIcon, FlowFavoriteIcon, FlowIssueIdIcon, FlowNextIcon, FlowPreviousIcon, FlowUrlIcon, FlowWorkIcon } from '@/components/issue/flow-header-icons'
import { issuePath, settingsPath } from '@/lib/app-routes'
import { enabledCodingTools, renderCodingPrompt, type CodingTool } from '@/lib/coding-tools'
import { issueBreadcrumbs, type IssueBreadcrumb } from '@/lib/issue-navigation-context'
import { issueViewers } from '@/lib/issue-presence'
import { configuredIssueBranch, copyIssueForWork } from '@/lib/issue-work-actions'
import { useOptionalTranslate } from '@/i18n/i18n'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { ProjectGlyph } from '@/components/views/project-glyph'
import { AgentEntityHover } from '@/components/agent/agent-entity-hover'
import './issue-header.css'

export type IssueSaveState = 'idle' | 'saving' | 'saved' | 'error'

interface IssueHeaderProps {
  issue: Issue; states: WorkflowState[]; presence?: Presence[]; saveState: IssueSaveState; onRetrySave?: () => void
  data: BootstrapData; activities: ActivityEvent[]; issueOptionsActions?: IssueOptionsActions
  /** 1-based position in the list the issue was opened from; `total` 0 when it was opened without a list (no counter). */
  position: number; total: number; onClose: () => void; onNavigate: (direction: 'next' | 'previous') => void
  /** Shows a close button: only previews need one, the full issue page closes with Esc and its breadcrumbs. */
  closable?: boolean
  onNavigateIssue?: (issue: Issue) => void; onNavigateRoot?: () => void
  returnPath?: string
  /** The pages the issue was opened from, oldest first (see issueBreadcrumbs); falls back to `returnPath`. */
  navigationTrail?: string[]
  onUpdate: (input: IssueUpdateInput) => Promise<void>; onDelete: () => Promise<void>; onRelation: (type: IssueRelationType) => void
  /** The reference app's Accept / Decline / duplicate / snooze controls, shown while the issue is in Triage. */
  triageActions?: React.ReactNode
  /** `triage`: the reference app's header beside the triage list (identifier + title, favorite, options, triage actions). */
  variant?: 'page' | 'triage'
}

export function IssueHeader({ issue, presence = [], saveState, onRetrySave, data, activities, issueOptionsActions, position, total, closable = false, onClose, onNavigate, onNavigateRoot, returnPath, navigationTrail, onUpdate, onDelete, onRelation, triageActions, variant = 'page' }: IssueHeaderProps) {
  const t = useOptionalTranslate()
  const favorite=data.favorites.some(item=>item.resourceType==='issue'&&item.resourceId===issue.id)
  const canGoNext=position<total, canGoPrevious=position>1
  const breadcrumbs = issueBreadcrumbs(data, issue, navigationTrail?.length ? navigationTrail : returnPath)
  const viewers = issueViewers(presence, issue.id, data.viewer.id)
  const preferences=data.userSettings?.[data.viewer.id]
  const tools=enabledCodingTools(preferences)
  const buildPrompt=useCallback(()=>renderCodingPrompt(preferences?.codingPromptTemplate,{identifier:issue.identifier,title:issue.title,description:issue.description,branchName:configuredIssueBranch(issue,data),url:location.href}),[preferences?.codingPromptTemplate,issue,data])
  const openInTool=useCallback(async(tool:CodingTool)=>{const url=tool.link(buildPrompt(),preferences??{});if(!url){toast.error('Add a custom link in coding tool settings first');return}window.open(url,/^https?:/i.test(url)?'_blank':'_self','noopener');try{await copyIssueForWork(buildPrompt(),'prompt',issue,data,onUpdate)}catch{/* opening the tool matters more than the clipboard */}},[buildPrompt,preferences,issue,data,onUpdate])
  const copyWork=useCallback(async(kind:'branch'|'prompt')=>{try{const text=kind==='branch'?configuredIssueBranch(issue,data):buildPrompt();await copyIssueForWork(text,kind,issue,data,onUpdate);toast.success(kind==='branch'?'Branch name copied to clipboard':'Prompt copied to clipboard')}catch(error){toast.error(error instanceof Error?error.message:'Could not write to clipboard')}},[issue,data,onUpdate,buildPrompt])
  useEffect(()=>{const onKey=(event:KeyboardEvent)=>{if(isEditable(event.target))return;if((event.metaKey||event.ctrlKey)&&event.altKey&&event.key.toLowerCase()==='p'){event.preventDefault();void copyWork('prompt')} };addEventListener('keydown',onKey);return()=>removeEventListener('keydown',onKey)},[copyWork])
  const triage=variant==='triage'
  // Previews sit beside a list that owns J / K and Esc; only the full page handles them here.
  usePageKeys({enabled:!triage&&!closable,closable,canGoNext,canGoPrevious,onClose,onNavigate})
  const presenceGroup=viewers.length>0?<IssuePresence viewers={viewers} label={t('People viewing this issue')}/>:null
  return <header className={triage?'issue-header issue-header--triage':'issue-header'}>
    <button type="button" data-sidebar-trigger aria-label="Open sidebar" onClick={() => window.dispatchEvent(new Event('flow:open-sidebar'))} />
    <div className="issue-header-context"><nav className="issue-breadcrumb" aria-label="Issue breadcrumb">{!triage&&breadcrumbs.map((crumb,index)=><span className="issue-breadcrumb-segment" key={crumb.href}><BreadcrumbLink crumb={crumb} data={data} onClick={index===breadcrumbs.length-1 && onNavigateRoot ? event=>activateLink(event,onNavigateRoot) : undefined}/><span className="issue-breadcrumb-separator" aria-hidden="true">›</span></span>)}<a className="issue-breadcrumb-current" href={issuePath(data.workspace.urlKey,issue)} aria-current="page"><strong>{issue.identifier}</strong><span>{issue.title}</span></a></nav><HeaderTooltip label={t(favorite?'Unfavorite issue':'Favorite issue')} shortcut="⌥ F"><button className="issue-header-icon" type="button" role="switch" aria-checked={favorite} aria-label={favorite?'Remove from favorites':'Add to favorites'} onClick={()=>void issueOptionsActions?.toggleFavorite()}>{favorite?<FlowFavoriteFilledIcon className="issue-header-favorite-filled"/>:<FlowFavoriteIcon/>}</button></HeaderTooltip><IssueOptionsMenu issue={issue} data={data} activities={activities} actions={issueOptionsActions} favorited={favorite} onUpdate={onUpdate} onDelete={onDelete} onRelation={onRelation}/></div>
    {saveState==='error'?<button type="button" className="save-state error" onClick={onRetrySave}>Save failed · Retry</button>:<span className={`save-state ${saveState}`} role="status" aria-live="polite">{saveState==='saving'?'Saving...':saveState==='saved'?'Saved':''}</span>}
    {triage&&presenceGroup}
    {triageActions}
    {!triage&&(presenceGroup||total>0||closable)&&<div className="issue-header-end">
      {presenceGroup}
      {total>0&&<div className="issue-sequence" role="group" aria-label="Issue navigation"><span className="issue-sequence-count">{position}<span> / {total}</span></span><div className="issue-sequence-buttons"><HeaderTooltip label={t('Navigate down')} shortcut="J"><button className="issue-sequence-down" type="button" aria-label="Navigate down" disabled={!canGoNext} onClick={()=>onNavigate('next')}><FlowNextIcon/></button></HeaderTooltip><HeaderTooltip label={t('Navigate up')} shortcut="K"><button className="issue-sequence-up" type="button" aria-label="Navigate up" disabled={!canGoPrevious} onClick={()=>onNavigate('previous')}><FlowPreviousIcon/></button></HeaderTooltip></div></div>}
      {closable&&<HeaderTooltip label="Close" shortcut="Esc"><button className="issue-header-icon issue-header-close" type="button" aria-label="Close issue" aria-keyshortcuts="Escape" onClick={onClose}><X size={14}/></button></HeaderTooltip>}
    </div>}
    <IssueCommandStrip issue={issue} tools={tools} configurePath={`${settingsPath(data.workspace.urlKey,'code-and-reviews')}/coding-tools`} onCopyWork={kind=>void copyWork(kind)} onOpenTool={tool=>void openInTool(tool)}/>
  </header>
}

/** The reference app's floating copy / work strip under the header's right edge: it overlays the content and stays put while it scrolls. */
function IssueCommandStrip({issue,tools,configurePath,onCopyWork,onOpenTool}:{issue:Issue;tools:CodingTool[];configurePath:string;onCopyWork:(kind:'branch'|'prompt')=>void;onOpenTool:(tool:CodingTool)=>void}){
  const t = useOptionalTranslate()
  return <div className="issue-command-strip"><div className="issue-command-buttons">
    <CommandButton label={t('Copy issue URL')} shortcut="⌘ ⇧ ," onClick={()=>copyText(location.href,'Issue URL copied to clipboard')}><FlowUrlIcon/></CommandButton>
    <CommandButton label={t('Copy issue ID')} shortcut="⌘ ." onClick={()=>copyText(issue.identifier,'Issue ID copied to clipboard')}><FlowIssueIdIcon/></CommandButton>
    <CommandButton label={t('Copy branch name')} shortcut="⌘ ⇧ ." onClick={()=>onCopyWork('branch')}><FlowBranchIcon/></CommandButton>
    <div className="issue-work-control" role="group" aria-label={t('Work on issue')}><CommandButton className="issue-work-prompt" label={t('Copy as prompt')} shortcut="⌘ ⌥ P" onClick={()=>onCopyWork('prompt')}><FlowWorkIcon/></CommandButton><WorkMenu tools={tools} onOpenTool={onOpenTool} onCopyPrompt={()=>onCopyWork('prompt')} configurePath={configurePath}/></div>
  </div></div>
}

function WorkMenu({tools,onOpenTool,onCopyPrompt,configurePath}:{tools:CodingTool[];onOpenTool:(tool:CodingTool)=>void;onCopyPrompt:()=>void;configurePath:string}){
  const t = useOptionalTranslate()
  const[open,setOpen]=useState(false)
  // The reference app highlights a row on hover, or after the arrow keys move through the list; never the first row on open.
  const[keyboard,setKeyboard]=useState(false)
  useEffect(()=>{if(!open)setKeyboard(false)},[open])
  useEffect(()=>{let armed=false;const onKey=(event:KeyboardEvent)=>{if(isEditable(event.target)||event.metaKey||event.ctrlKey||event.altKey)return;if(event.key.toLowerCase()==='w'){armed=true;setTimeout(()=>{armed=false},900);return}if(armed&&event.key.toLowerCase()==='o'){event.preventDefault();setOpen(true);armed=false}};addEventListener('keydown',onKey);return()=>removeEventListener('keydown',onKey)},[])
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <HeaderTooltip label={t('Work on issue')} shortcut="W then O" disabled={open}><Popover.Trigger asChild><button className="issue-command-button issue-work-tools" type="button" aria-label={t('Choose coding tool')}><FlowChevronIcon/></button></Popover.Trigger></HeaderTooltip>
    <Popover.Portal><Popover.Content data-flow-motion="floating" className="work-menu" data-keyboard={keyboard||undefined} side="bottom" align="end" sideOffset={4.5} collisionPadding={10} aria-label="Menu" onOpenAutoFocus={event=>event.preventDefault()}>
      <Command loop onKeyDown={event=>{if(event.key==='ArrowDown'||event.key==='ArrowUp')setKeyboard(true)}}>
        <div className="work-menu-search"><Command.Input aria-label="Work on issue…" placeholder="Work on issue…" autoFocus/><kbd>W</kbd><span>then</span><kbd>O</kbd></div>
        <Command.List>
          {tools.map(tool=><Command.Item key={tool.id} value={`Open in ${tool.name}`} onSelect={()=>{onOpenTool(tool);setOpen(false)}}><ExternalLink/><span className="work-menu-label">Open in {tool.name}</span></Command.Item>)}
          <Command.Item value="Copy as prompt" onSelect={()=>{onCopyPrompt();setOpen(false)}}><FlowWorkIcon/><span className="work-menu-label">{t('Copy as prompt')}</span><span className="work-menu-shortcut" aria-label="Command Option P"><kbd>⌘</kbd><kbd>⌥</kbd><kbd>P</kbd></span></Command.Item>
          <div className="work-menu-separator" role="separator"/>
          <ConfigureCodingToolsItem path={configurePath} label={t('Configure coding tools…')} onDone={()=>setOpen(false)}/>
        </Command.List>
      </Command>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

/** One crumb: team and project crumbs carry their icon; the project opens its hover card, "Projects" and an
 * issue crumb (the issue this one was opened from) show a tooltip, like the reference header. */
function BreadcrumbLink({crumb,data,onClick}:{crumb:IssueBreadcrumb;data:BootstrapData;onClick?:(event:React.MouseEvent<HTMLAnchorElement>)=>void}){
  const t = useOptionalTranslate()
  const link=<a className="issue-breadcrumb-link" href={crumb.href} data-i18n-ignore={crumb.entity || undefined} onClick={onClick}>
    {crumb.team&&<span className="issue-breadcrumb-team-icon" aria-hidden="true"><ViewGlyph color={crumb.team.color} icon={crumb.team.icon || 'Team'}/></span>}
    {crumb.project&&<span className="issue-breadcrumb-project-icon" aria-hidden="true"><ProjectGlyph project={crumb.project}/></span>}
    {crumb.entity?crumb.label:t(crumb.label)}
  </a>
  if(crumb.project)return <AgentEntityHover data={data} entity={{kind:'project',project:crumb.project}} side="bottom" sideOffset={8} alignOffset={4}>{link}</AgentEntityHover>
  if(crumb.tooltip)return <FlowTooltip label={crumb.entity?<span data-i18n-ignore>{crumb.tooltip}</span>:t(crumb.tooltip)}>{link}</FlowTooltip>
  return link
}

function IssuePresence({viewers,label}:{viewers:Presence[];label:string}){
  const shown=viewers.slice(0,3), extra=viewers.length-shown.length
  return <div className="issue-presence" role="group" aria-label={label}>{shown.map(item=><FlowTooltip key={item.user.id} label={<span data-i18n-ignore>{item.user.displayName}</span>}><span className="issue-presence-item"><UserAvatar className="issue-presence-avatar" name={item.user.displayName} avatarUrl={item.user.avatarUrl}/></span></FlowTooltip>)}{extra>0&&<span className="issue-presence-more">+{extra}</span>}</div>
}

/** Full issue page keys: Esc closes (back to the list), J / K walk the list the issue was opened from. */
function usePageKeys(options:{enabled:boolean;closable:boolean;canGoNext:boolean;canGoPrevious:boolean;onClose:()=>void;onNavigate:(direction:'next'|'previous')=>void}){
  const latest=useRef(options)
  useEffect(()=>{latest.current=options})
  const {enabled}=options
  useEffect(()=>{
    if(!enabled)return
    let lastKey='',lastAt=0
    const onKey=(event:KeyboardEvent)=>{
      const key=event.key.toLowerCase(), previous=lastKey, recent=Date.now()-lastAt<1000
      lastKey=key;lastAt=Date.now()
      if(event.defaultPrevented||event.metaKey||event.ctrlKey||event.altKey||event.shiftKey||isEditable(event.target)||overlayOpen())return
      // "G then J" and similar sequences belong to the app's navigation shortcuts.
      if(recent&&previous==='g')return
      const {closable,canGoNext,canGoPrevious,onClose,onNavigate}=latest.current
      if(key==='escape'&&!closable){event.preventDefault();onClose();return}
      if(key==='j'&&canGoNext){event.preventDefault();onNavigate('next');return}
      if(key==='k'&&canGoPrevious){event.preventDefault();onNavigate('previous')}
    }
    addEventListener('keydown',onKey)
    return()=>removeEventListener('keydown',onKey)
  },[enabled])
}

/** Header tooltips are the app-wide tooltip (boxed keys, "then" between steps, 8px below). */
function HeaderTooltip({label,shortcut,disabled,children}:{label:React.ReactNode;shortcut?:string;disabled?:boolean;children:React.ReactElement}){
  return <FlowTooltip label={label} shortcut={shortcut} disabled={disabled}>{children}</FlowTooltip>
}

function CommandButton({label,shortcut,onClick,className,children}:{label:string;shortcut?:string;onClick?:()=>void;className?:string;children:React.ReactNode}){return <HeaderTooltip label={label} shortcut={shortcut}><button className={className?`issue-command-button ${className}`:'issue-command-button'} type="button" aria-label={label} onClick={onClick}>{children}</button></HeaderTooltip>}
async function copyText(text:string,message:string){try{await navigator.clipboard.writeText(text);toast.success(message)}catch{toast.error('Could not write to clipboard')}}
function isEditable(target:EventTarget|null){return target instanceof HTMLInputElement||target instanceof HTMLTextAreaElement||target instanceof HTMLSelectElement||(target instanceof HTMLElement&&target.isContentEditable)}
function overlayOpen(){return Boolean(document.querySelector('[data-radix-popper-content-wrapper],[role=dialog],[role=alertdialog],[role=menu],[role=listbox]'))}
function activateLink(event:React.MouseEvent<HTMLAnchorElement>,action:()=>void){if(event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;event.preventDefault();action()}

/** Mounted only while the work menu is open, so the header renders without a router. */
function ConfigureCodingToolsItem({path,label,onDone}:{path:string;label:string;onDone:()=>void}){const navigate=useNavigate();return <Command.Item value="Configure coding tools…" onSelect={()=>{onDone();navigate(path)}}><FlowCodingToolsIcon/><span className="work-menu-label">{label}</span></Command.Item>}
