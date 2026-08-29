import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, Check, ChevronDown, Clock3, Copy, FileText, History, MessageCircle, MoreHorizontal, Send, Star, Trash2, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { addFavorite, addSubscription, createDocumentComment, deleteDocument, deleteDocumentComment, deleteDocumentDraft, publishDocumentDraft, removeFavorite, removeSubscription, restoreDocumentRevision, saveDocumentDraft, updateDocument } from '@/lib/api'
import type { BootstrapData, FlowDocument, User } from '@/types/flow'

import './document-page.css'

export function DocumentPage({ data, document, onReload, onBack }: { data: BootstrapData; document: FlowDocument; onReload: () => Promise<void>; onBack: () => void }) {
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
  const [presence,setPresence]=useState<User[]>([])
  const pending=useRef<number | undefined>(undefined)
  const favorite=data.favorites.some(item=>item.resourceType==='document'&&item.resourceId===document.id) || document.favorite
  const subscribed=data.subscriptions.some(item=>item.resourceType==='document'&&item.resourceId===document.id) || document.subscriberIds.includes(data.viewer.id)
  const draft=data.documentContentDrafts?.find(item=>item.documentId===document.id&&item.userId===data.viewer.id)
  useEffect(()=>{setTitle(document.title);setBody({value:document.content,state:document.contentData?JSON.stringify(document.contentData):document.contentState})},[document])
  useEffect(()=>{if(historyOpen&&!document.revisions.some(item=>item.id===selectedRevisionId))setSelectedRevisionId(document.revisions[0]?.id??'')},[document.revisions,historyOpen,selectedRevisionId])
  useEffect(()=>()=>window.clearTimeout(pending.current),[])
  const schedule=(input: Parameters<typeof updateDocument>[1])=>{window.clearTimeout(pending.current);setSaveState('saving');pending.current=window.setTimeout(()=>{void updateDocument(document.id,input).then(async()=>{await onReload();setSaveState('saved');window.setTimeout(()=>setSaveState('idle'),900)}).catch(()=>setSaveState('error'))},600)}
  const project=data.projects.find(item=>document.projectIds.includes(item.id))
  const issue=data.issues.find(item=>item.id===document.issueId)
  const selectedRevision=document.revisions.find(item=>item.id===selectedRevisionId)
  const comments=data.comments[document.id] ?? []
  const submitComment=async(parentId?:string)=>{const body=commentBody.trim();if(!body)return;await createDocumentComment(document.id,{body,parentId});setCommentBody('');setReplyTo(undefined);await onReload()}
  const openHistory=async()=>{await onReload();setSelectedRevisionId(document.revisions[0]?.id??'');setHistoryOpen(true)}
  const restoreRevision=async()=>{if(!selectedRevision)return;const restored=await restoreDocumentRevision(document.id,selectedRevision.id);setTitle(restored.title);setBody({value:restored.content,state:restored.contentData?JSON.stringify(restored.contentData):restored.contentState});setEditorVersion(value=>value+1);setHistoryOpen(false);await onReload()}
  const toggleProject=async(id:string)=>{const next=document.projectIds.includes(id)?document.projectIds.filter(value=>value!==id):[...document.projectIds,id];await updateDocument(document.id,{projectIds:next});await onReload()}
  return <main className="main-panel document-page">
    <header className="document-header">
      <button className="document-breadcrumb" onClick={onBack}>{issue?.identifier??project?.name??'Documents'}</button><span>›</span><strong>{document.title}</strong>
      <div className="document-header-actions">
        {saveState!=='idle'&&<span className={`document-save-state ${saveState}`}>{saveState==='saving'?'Saving…':saveState==='saved'?<><Check size={12}/>Saved</>:'Could not save'}</span>}
        <button aria-label="Save document draft" onClick={()=>void saveDocumentDraft(document.id,{content:body.value,contentState:body.state,contentData:draftData(body.state??'')}).then(onReload)}>{draft?'Update draft':'Save draft'}</button>
        {draft&&<button aria-label="Publish document draft" onClick={()=>void publishDocumentDraft(document.id,draft.id).then(onReload)}>Publish draft</button>}
        {draft&&<button aria-label="Discard document draft" onClick={()=>void deleteDocumentDraft(document.id,draft.id).then(onReload)}><X size={14}/></button>}
        <button aria-label={favorite?'Remove from favorites':'Add to favorites'} onClick={()=>void (favorite?removeFavorite('document',document.id):addFavorite('document',document.id)).then(onReload)}><Star size={15} fill={favorite?'currentColor':'none'}/></button>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Document options"><MoreHorizontal size={16}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="document-menu" align="end" sideOffset={5}><DropdownMenu.Sub><DropdownMenu.SubTrigger><FileText size={14}/>Move to<ChevronDown size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="document-menu" sideOffset={6}>{data.projects.map(item=><DropdownMenu.CheckboxItem checked={document.projectIds.includes(item.id)} key={item.id} onCheckedChange={()=>void toggleProject(item.id)} onSelect={event=>event.preventDefault()}><i style={{background:item.color}}/>{item.name}{document.projectIds.includes(item.id)&&<Check size={13}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub><DropdownMenu.Item onSelect={()=>navigator.clipboard.writeText(location.href)}><Copy size={14}/>Copy link</DropdownMenu.Item><DropdownMenu.Item onSelect={()=>void openHistory()}><History size={14}/>Show document history</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={()=>setDeleteOpen(true)}><Trash2 size={14}/>Delete</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        <button aria-label={subscribed?'Unsubscribe':'Subscribe'} onClick={()=>void (subscribed?removeSubscription('document',document.id):addSubscription('document',document.id)).then(onReload)}><Bell size={15} fill={subscribed?'currentColor':'none'}/></button>
        <button title={new Date(document.updatedAt).toLocaleString()} className="document-edited" onClick={()=>void openHistory()}><Clock3 size={13}/>Edited {new Date(document.updatedAt).toLocaleDateString(undefined,{month:'short',day:'numeric'})}</button>
      </div>
    </header>
    <article className="document-canvas">
      <button className="document-icon" aria-label="Document icon"><FileText size={28}/></button>
      <input className="document-title" aria-label="Document title" value={title} onChange={event=>{setTitle(event.target.value);schedule({title:event.target.value})}} onBlur={()=>{if(title.trim()&&title!==document.title)void updateDocument(document.id,{title:title.trim()}).then(onReload)}}/>
      <div className="document-presence" aria-label={`${presence.length} people editing`}><span className="document-presence__status"><i/>Live</span>{presence.filter(user => user.id !== data.viewer.id).slice(0,4).map(user=><span className="document-presence__avatar" key={user.id} title={`${user.displayName} is editing`}>{user.displayName.slice(0,2).toUpperCase()}</span>)}</div><IssueDescriptionEditor key={`${document.id}:${editorVersion}`} className="document-editor" value={body.value} state={body.state} collaboration={{workspaceKey:data.workspace.urlKey,documentId:document.id,viewer:data.viewer,onPresence:setPresence,onPersist:async snapshot=>{await updateDocument(document.id,{content:snapshot.markdown,contentState:snapshot.contentState,contentData:snapshot.document as Record<string,unknown>});await onReload()}}} onChange={snapshot=>{setBody({value:snapshot.markdown,state:snapshot.documentJSON});schedule({content:snapshot.markdown,contentState:snapshot.contentState,contentData:snapshot.document as Record<string,unknown>})}}/>
      <section className="document-comments" aria-label="Comments"><header><MessageCircle size={16}/><strong>Comments</strong><span>{comments.filter(item=>!item.parentId).length}</span></header><div className="document-comment-list">{comments.filter(item=>!item.parentId).map(comment=><article className="document-comment-thread" key={comment.id}><div className="document-comment"><strong>{comment.user.displayName}</strong><small>{new Date(comment.createdAt).toLocaleString()}</small><p>{comment.body}</p><button type="button" onClick={()=>setReplyTo(replyTo===comment.id?undefined:comment.id)}>Reply</button><button type="button" onClick={()=>void deleteDocumentComment(document.id,comment.id).then(onReload)}>Delete</button></div>{comments.filter(item=>item.parentId===comment.id).map(reply=><div className="document-comment document-comment-reply" key={reply.id}><strong>{reply.user.displayName}</strong><small>{new Date(reply.createdAt).toLocaleString()}</small><p>{reply.body}</p></div>)}{replyTo===comment.id&&<div className="document-comment-composer is-reply"><textarea aria-label="Reply" value={commentBody} onChange={event=>setCommentBody(event.target.value)} /><button type="button" disabled={!commentBody.trim()} onClick={()=>void submitComment(comment.id)}><Send size={13}/>Reply</button></div>}</article>)}</div><div className="document-comment-composer"><textarea aria-label="Add comment" placeholder="Add a comment…" value={replyTo?"":commentBody} onChange={event=>{setReplyTo(undefined);setCommentBody(event.target.value)}} /><button type="button" disabled={!commentBody.trim()||!!replyTo} onClick={()=>void submitComment()}><Send size={13}/>Comment</button></div></section>
    </article>
    <Dialog open={historyOpen} onOpenChange={setHistoryOpen}><DialogContent className="document-history"><DialogTitle>Version history for <strong>{document.title}</strong></DialogTitle><button className="document-dialog-close" aria-label="Close modal dialog" onClick={()=>setHistoryOpen(false)}><X size={15}/></button>{document.revisions.length?<div className="document-history-body"><nav className="document-history-list">{document.revisions.map(revision=><button className={revision.id===selectedRevisionId?'selected':''} key={revision.id} onClick={()=>setSelectedRevisionId(revision.id)}><span><strong>{revision.title}</strong><small>{revision.author.displayName} · {new Date(revision.createdAt).toLocaleString()}</small></span><History size={14}/></button>)}</nav>{selectedRevision&&<section className="document-history-preview"><header><div><strong>{new Date(selectedRevision.createdAt).toLocaleString()}</strong><small>Saved by {selectedRevision.author.displayName}</small></div><button onClick={()=>void restoreRevision()}><History size={13}/>Restore this version</button></header><div><article><h3>Current</h3><strong>{document.title}</strong><pre>{document.content||'Empty document'}</pre></article><article><h3>Selected version</h3><strong>{selectedRevision.title}</strong><pre>{selectedRevision.content||'Empty document'}</pre></article></div></section>}</div>:<div className="document-history-empty">There is no history yet.</div>}</DialogContent></Dialog>
    <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}><DialogContent className="document-delete"><DialogTitle>Delete "{document.title}"?</DialogTitle><p>Deleted documents are available in the "Recently deleted" view for 30 days, before they are permanently deleted.</p><footer><button onClick={()=>setDeleteOpen(false)}>Cancel</button><button className="danger" onClick={()=>void deleteDocument(document.id).then(onBack).catch(error=>toast.error(error instanceof Error?error.message:'Could not delete'))}>Delete</button></footer></DialogContent></Dialog>
  </main>
}
function draftData(value:string){try{return JSON.parse(value) as Record<string,unknown>}catch{return undefined}}
