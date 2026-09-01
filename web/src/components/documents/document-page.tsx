import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, Check, ChevronDown, Copy, FileText, History, Link2, MessageCircle, MoreHorizontal, Send, SlidersHorizontal, Star, Trash2, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DocumentGlyph, DocumentIconPicker } from '@/components/documents/document-icon'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { Toggle } from '@/components/ui/toggle'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { useI18n } from '@/i18n/i18n'
import { documentPath, issuePath, projectPath, teamDocumentsPath, teamHomePath } from '@/lib/app-routes'
import { addFavorite, addSubscription, createDocumentComment, deleteDocument, deleteDocumentComment, removeFavorite, removeSubscription, restoreDocumentRevision, updateDocument } from '@/lib/api'
import type { BootstrapData, FlowDocument, User } from '@/types/flow'

import './document-page.css'

export function DocumentPage({ data, document, onReload, onBack }: { data: BootstrapData; document: FlowDocument; onReload: () => Promise<void>; onBack: () => void }) {
  const {t}=useI18n()
  const [title,setTitle]=useState(document.title)
  const editorState=document.contentData?JSON.stringify(document.contentData):document.contentState
  const [body,setBody]=useState({value:document.content,state:editorState})
  const [saveState,setSaveState]=useState<'idle'|'saving'|'saved'|'error'>('idle')
  const [historyOpen,setHistoryOpen]=useState(false)
  const [selectedRevisionId,setSelectedRevisionId]=useState(document.revisions[0]?.id??'')
  const [deleteOpen,setDeleteOpen]=useState(false)
  const [editorVersion,setEditorVersion]=useState(0)
  const [commentBody,setCommentBody]=useState('')
  const [replyTo,setReplyTo]=useState<string>()
  const [commentsOpen,setCommentsOpen]=useState(()=>readDocumentViewOption(document.id,'comments',true))
  const [authorNamesOpen,setAuthorNamesOpen]=useState(()=>readDocumentViewOption(document.id,'authors',false))
  const [presence,setPresence]=useState<User[]>([])
  const [copyBusy,setCopyBusy]=useState(false)
  const [favoriteBusy,setFavoriteBusy]=useState(false)
  const [subscriptionBusy,setSubscriptionBusy]=useState(false)
  const pending=useRef<number | undefined>(undefined)
  const editedTriggerRef=useRef<HTMLButtonElement>(null)
  const favorite=data.favorites.some(item=>item.resourceType==='document'&&item.resourceId===document.id) || document.favorite
  const subscribed=data.subscriptions.some(item=>item.resourceType==='document'&&item.resourceId===document.id) || document.subscriberIds.includes(data.viewer.id)
  const collaborators=[...new Map(presence.filter(user=>Boolean(user.id)&&user.id!==data.viewer.id).map(user=>[user.id,user])).values()]
  useEffect(()=>{setTitle(document.title);setBody({value:document.content,state:document.contentData?JSON.stringify(document.contentData):document.contentState})},[document])
  useEffect(()=>{if(historyOpen&&!document.revisions.some(item=>item.id===selectedRevisionId))setSelectedRevisionId(document.revisions[0]?.id??'')},[document.revisions,historyOpen,selectedRevisionId])
  useEffect(()=>()=>window.clearTimeout(pending.current),[])
  const schedule=(input: Parameters<typeof updateDocument>[1])=>{window.clearTimeout(pending.current);setSaveState('saving');pending.current=window.setTimeout(()=>{void updateDocument(document.id,input).then(async()=>{await onReload();setSaveState('saved');window.setTimeout(()=>setSaveState('idle'),900)}).catch(()=>setSaveState('error'))},600)}
  const project=data.projects.find(item=>document.projectIds.includes(item.id))
  const issue=data.issues.find(item=>item.id===document.issueId)
  const team=data.teams.find(item=>document.teamIds.includes(item.id))
  const selectedRevision=document.revisions.find(item=>item.id===selectedRevisionId)
  const selectedRevisionCurrent=document.revisions[0]?.id===selectedRevisionId
  const comments=data.comments[document.id] ?? []
  const defaultDocumentIcon=!document.icon
  const lastRevision=document.revisions[0]
  const lastEditor=lastRevision?.author??document.creator
  const lastEditedAt=lastRevision?.createdAt??document.updatedAt
  const submitComment=async(parentId?:string)=>{const body=commentBody.trim();if(!body)return;await createDocumentComment(document.id,{body,parentId});setCommentBody('');setReplyTo(undefined);await onReload()}
  const openHistory=async()=>{await onReload();setSelectedRevisionId(document.revisions[0]?.id??'');setHistoryOpen(true)}
  const restoreRevision=async()=>{if(!selectedRevision||selectedRevisionCurrent)return;const restored=await restoreDocumentRevision(document.id,selectedRevision.id);setTitle(restored.title);setBody({value:restored.content,state:restored.contentData?JSON.stringify(restored.contentData):restored.contentState});setEditorVersion(value=>value+1);setHistoryOpen(false);await onReload();toast.success('Content has been restored.')}
  const toggleProject=async(id:string)=>{const next=document.projectIds.includes(id)?document.projectIds.filter(value=>value!==id):[...document.projectIds,id];await updateDocument(document.id,{projectIds:next});await onReload()}
  const copyDocumentURL=async()=>{if(copyBusy)return;setCopyBusy(true);try{await navigator.clipboard.writeText(new URL(documentPath(data.workspace.urlKey,document),location.origin).href);toast.success(t('Copied document link to clipboard'))}catch(error){toast.error(error instanceof Error?error.message:t('Could not copy document link'))}finally{setCopyBusy(false)}}
  const toggleFavorite=async()=>{if(favoriteBusy)return;setFavoriteBusy(true);try{if(favorite)await removeFavorite('document',document.id);else await addFavorite('document',document.id);await onReload()}catch(error){toast.error(error instanceof Error?error.message:t('Could not update favorite'))}finally{setFavoriteBusy(false)}}
  const toggleSubscription=async()=>{if(subscriptionBusy)return;setSubscriptionBusy(true);try{if(subscribed)await removeSubscription('document',document.id);else await addSubscription('document',document.id);await onReload();toast.success(t(subscribed?'Unsubscribed from document':'Subscribed to document'))}catch(error){toast.error(error instanceof Error?error.message:t('Could not update document subscription'))}finally{setSubscriptionBusy(false)}}
  return <main className="main-panel document-page">
    <header className="document-header">
      <nav aria-label="Document breadcrumb" className="document-breadcrumbs">
        {issue?<a className="document-breadcrumb-link" href={issuePath(data.workspace.urlKey,issue)}><FileText/><span data-i18n-ignore>{issue.identifier}</span></a>:project?<a className="document-breadcrumb-link" href={projectPath(data.workspace.urlKey,project)}><ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)}/><span data-i18n-ignore>{project.name}</span></a>:team?<><a className="document-breadcrumb-link" href={teamHomePath(data.workspace.urlKey,team.key)}><ViewGlyph color={team.color} icon={team.icon||'Team'}/><span data-i18n-ignore>{team.name}</span></a><span aria-hidden="true" className="document-breadcrumb-separator">›</span><a className="document-breadcrumb-link document-breadcrumb-documents" href={teamDocumentsPath(data.workspace.urlKey,team.key)}>Documents</a></>:<button className="document-breadcrumb-link" onClick={onBack}><FileText/><span>Documents</span></button>}
        <span aria-hidden="true" className="document-breadcrumb-separator">›</span><strong className="document-breadcrumb-current"><DocumentGlyph document={document}/><span data-i18n-ignore>{document.title}</span></strong>
      </nav>
      <div className="document-header-actions">
        {saveState==='error'&&<span className="document-save-state error">Could not save</span>}
        <button aria-busy={favoriteBusy||undefined} aria-checked={favorite} aria-label={favorite?'Remove from favorites':'Add to favorites'} disabled={favoriteBusy} onClick={()=>void toggleFavorite()} role="switch"><Star size={15} fill={favorite?'currentColor':'none'}/></button>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Document options"><MoreHorizontal size={16}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="document-menu" align="end" sideOffset={5}><DropdownMenu.Sub><DropdownMenu.SubTrigger><FileText size={14}/><span>Move to</span><ChevronDown size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="document-menu" sideOffset={6}>{data.projects.map(item=><DropdownMenu.CheckboxItem checked={document.projectIds.includes(item.id)} key={item.id} onCheckedChange={()=>void toggleProject(item.id)} onSelect={event=>event.preventDefault()}><i style={{background:item.color}}/><span data-i18n-ignore>{item.name}</span>{document.projectIds.includes(item.id)&&<Check size={13}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub><DropdownMenu.Item onSelect={()=>navigator.clipboard.writeText(location.href)}><Copy size={14}/><span>Copy link</span></DropdownMenu.Item><DropdownMenu.Item onSelect={()=>void openHistory()}><History size={14}/><span>Show document history</span></DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={()=>setDeleteOpen(true)}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        <span className="document-header-spacer"/>
        {collaborators.slice(0,4).map(user=>{const name=user.displayName||user.name||'?';return <span className="document-presence__avatar" key={user.id} title={`${name} is editing`}>{name.slice(0,2).toUpperCase()}</span>})}
        <button aria-busy={copyBusy||undefined} aria-label={t('Copy document URL')} disabled={copyBusy} onClick={()=>void copyDocumentURL()}><Link2 size={15}/></button>
        <button aria-busy={subscriptionBusy||undefined} aria-label={t(subscribed?'Unsubscribe':'Subscribe')} disabled={subscriptionBusy} onClick={()=>void toggleSubscription()}><Bell size={15} fill={subscribed?'currentColor':'none'}/></button>
      </div>
    </header>
    <Popover.Root>
      <Popover.Trigger asChild><button ref={editedTriggerRef} title={new Date(lastEditedAt).toLocaleString()} className="document-edited"><SlidersHorizontal size={13}/>Edited {new Date(lastEditedAt).toLocaleDateString(undefined,{month:'short',day:'numeric'})}</button></Popover.Trigger>
      <Popover.Portal><Popover.Content align="end" className="document-edited-popover" collisionPadding={10} onCloseAutoFocus={event=>{event.preventDefault();editedTriggerRef.current?.focus()}} sideOffset={4}>
        <div className="document-view-options">
          <span>Show comments</span><Toggle checked={commentsOpen} label="Show comments" onChange={value=>{setCommentsOpen(value);writeDocumentViewOption(document.id,'comments',value)}}/>
          <span>Show author names</span><Toggle checked={authorNamesOpen} label={authorNamesOpen?'Hide author names':'Show author names'} onChange={value=>{setAuthorNamesOpen(value);writeDocumentViewOption(document.id,'authors',value)}}/>
        </div>
        <div className="document-owner-row"><span>Owned by</span><a href={`/${data.workspace.urlKey}/member/${encodeURIComponent(document.creator.name)}/assigned`}><UserAvatar className="document-meta-avatar" name={document.creator.displayName}/><strong data-i18n-ignore>{document.creator.displayName}</strong></a></div>
        <div className="document-last-edit-row"><span>Last edit by</span><span><UserAvatar className="document-meta-avatar" name={lastEditor.displayName}/><strong data-i18n-ignore>{lastEditor.displayName}</strong></span><time dateTime={lastEditedAt}>{formatDocumentTimestamp(lastEditedAt)}</time></div>
        <div className="document-history-action"><Popover.Close asChild><button onClick={()=>void openHistory()} type="button"><History size={14}/>Show document history</button></Popover.Close></div>
      </Popover.Content></Popover.Portal>
    </Popover.Root>
    <article className="document-canvas">
      <DocumentIconPicker document={document} onChange={visual=>void updateDocument(document.id,visual).then(onReload)} triggerClassName={`document-icon${defaultDocumentIcon?' is-empty':''}`}/>
      <input className="document-title" aria-label="Document title" value={title} onChange={event=>{setTitle(event.target.value);schedule({title:event.target.value})}} onBlur={()=>{if(title.trim()&&title!==document.title)void updateDocument(document.id,{title:title.trim()}).then(onReload)}}/>
      <div className="document-editor-shell">{authorNamesOpen&&body.value.trim()&&<span className="document-author-name" data-i18n-ignore>{lastEditor.displayName}</span>}<IssueDescriptionEditor ariaLabel="Document content" placeholder="Start writing…" key={`${document.id}:${editorVersion}`} className="document-editor" value={body.value} state={body.state} collaboration={{workspaceKey:data.workspace.urlKey,documentId:document.id,viewer:data.viewer,onPresence:setPresence,onPersist:async snapshot=>{await updateDocument(document.id,{content:snapshot.markdown,contentState:snapshot.contentState,contentData:snapshot.document as Record<string,unknown>});await onReload()}}} onChange={snapshot=>{setBody({value:snapshot.markdown,state:snapshot.documentJSON});schedule({content:snapshot.markdown,contentState:snapshot.contentState,contentData:snapshot.document as Record<string,unknown>})}}/></div>
      {commentsOpen&&comments.length>0&&<section className="document-comments" aria-label="Comments"><header><MessageCircle size={16}/><strong>Comments</strong><span>{comments.filter(item=>!item.parentId).length}</span></header><div className="document-comment-list">{comments.filter(item=>!item.parentId).map(comment=><article className="document-comment-thread" key={comment.id}><div className="document-comment"><strong>{comment.user.displayName}</strong><small>{new Date(comment.createdAt).toLocaleString()}</small><p>{comment.body}</p><button type="button" onClick={()=>setReplyTo(replyTo===comment.id?undefined:comment.id)}>Reply</button><button type="button" onClick={()=>void deleteDocumentComment(document.id,comment.id).then(onReload)}>Delete</button></div>{comments.filter(item=>item.parentId===comment.id).map(reply=><div className="document-comment document-comment-reply" key={reply.id}><strong>{reply.user.displayName}</strong><small>{new Date(reply.createdAt).toLocaleString()}</small><p>{reply.body}</p></div>)}{replyTo===comment.id&&<div className="document-comment-composer is-reply"><textarea aria-label="Reply" value={commentBody} onChange={event=>setCommentBody(event.target.value)} /><button type="button" disabled={!commentBody.trim()} onClick={()=>void submitComment(comment.id)}><Send size={13}/>Reply</button></div>}</article>)}</div><div className="document-comment-composer"><textarea aria-label="Add comment" placeholder="Add a comment…" value={replyTo?"":commentBody} onChange={event=>{setReplyTo(undefined);setCommentBody(event.target.value)}} /><button type="button" disabled={!commentBody.trim()||!!replyTo} onClick={()=>void submitComment()}><Send size={13}/>Comment</button></div></section>}
    </article>
    <Dialog open={historyOpen} onOpenChange={setHistoryOpen}><DialogContent className="document-history"><DialogTitle>Restore version for <strong>{document.title}</strong></DialogTitle><button className="document-dialog-close" aria-label="Close modal dialog" onClick={()=>setHistoryOpen(false)}><X size={15}/></button>{document.revisions.length?<div className="document-history-body"><nav className="document-history-list">{document.revisions.map((revision,index)=><button className={revision.id===selectedRevisionId?'selected':''} key={revision.id} onClick={()=>setSelectedRevisionId(revision.id)}><span><strong>{new Date(revision.createdAt).toLocaleString()}</strong><small>{index===0?'Current · ':''}{revision.author.displayName}</small></span><History size={14}/></button>)}</nav>{selectedRevision&&<section className="document-history-preview"><header><div><strong>{new Date(selectedRevision.createdAt).toLocaleString()}</strong><small>Saved by {selectedRevision.author.displayName}</small></div><button aria-disabled={selectedRevisionCurrent} disabled={selectedRevisionCurrent} title={selectedRevisionCurrent?'This is the current version, you cannot restore it':undefined} onClick={()=>void restoreRevision()}><History size={13}/>Restore version</button></header>{!selectedRevisionCurrent&&<label className="document-history-highlight"><input type="checkbox" defaultChecked/>Highlight changes</label>}<div><article><h3>Current</h3><strong>{document.title}</strong><pre>{document.content||'Empty document'}</pre></article><article><h3>Selected version</h3><strong>{selectedRevision.title}</strong><pre>{selectedRevision.content||'Empty document'}</pre></article></div></section>}</div>:<div className="document-history-empty">There is no history yet.</div>}</DialogContent></Dialog>
    <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}><DialogContent className="document-delete"><DialogTitle>Delete "{document.title}"?</DialogTitle><p>Deleted documents are available in the "Recently deleted" view for 30 days, before they are permanently deleted.</p><footer><button onClick={()=>setDeleteOpen(false)}>Cancel</button><button className="danger" onClick={()=>void deleteDocument(document.id).then(onBack).catch(error=>toast.error(error instanceof Error?error.message:'Could not delete'))}>Delete</button></footer></DialogContent></Dialog>
  </main>
}
function readDocumentViewOption(id:string,key:string,fallback:boolean){try{const value=localStorage.getItem(`flow:document:${id}:${key}`);return value===null?fallback:value==='true'}catch{return fallback}}
function writeDocumentViewOption(id:string,key:string,value:boolean){try{localStorage.setItem(`flow:document:${id}:${key}`,String(value))}catch{/* View preferences are best-effort. */}}
function formatDocumentTimestamp(value:string){return new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}).format(new Date(value))}
