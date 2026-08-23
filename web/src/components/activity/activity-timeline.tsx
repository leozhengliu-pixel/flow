import { format, formatDistanceToNow } from 'date-fns'
import { Copy, Edit3, Ellipsis, MessageSquareReply, SmilePlus, Trash2 } from 'lucide-react'
import type { ActivityEvent, Comment } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { Composer } from '@/components/editor/composer'
import { useState } from 'react'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { RichComment } from './rich-comment'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useI18n } from '@/i18n/i18n'

export function ActivityTimeline({ events, comments, viewerId, onReply, onEdit, onDelete, onReaction, onAttach }: { events: ActivityEvent[]; comments: Comment[]; viewerId: string; onReply: (body: string, bodyData: Record<string, unknown> | undefined, parentId: string) => Promise<void>; onEdit: (id: string, body: string, bodyData?: Record<string, unknown>) => Promise<void>; onDelete: (id: string) => Promise<void>; onReaction: (id: string, emoji: string) => Promise<void>; onAttach?:()=>void }) {
  const [replying,setReplying]=useState<string|null>(null),[editing,setEditing]=useState<string|null>(null),[deleting,setDeleting]=useState<Comment|null>(null),[busy,setBusy]=useState<string|null>(null),[expanded,setExpanded]=useState(false)
  const topLevel=comments.filter(comment=>!comment.parentId)
  const eventItems=events.filter(event=>event.type!=='comment.created'&&event.type!=='comment.updated'&&event.type!=='comment.deleted')
  const items=[...eventItems.map(event=>({...event,kind:'event' as const})),...topLevel.map(comment=>({...comment,kind:'comment' as const}))].sort((a,b)=>new Date(a.createdAt).getTime()-new Date(b.createdAt).getTime())
  const visible=expanded||items.length<=8?items:items.slice(-8), hidden=items.length-visible.length
  const run=async(id:string,task:()=>Promise<void>)=>{setBusy(id);try{await task()}finally{setBusy(null)}}
  return <>
    <div className="timeline">
      {hidden>0&&<button type="button" className="show-older-activity" onClick={()=>setExpanded(true)}>Show {hidden} older activities</button>}
      {visible.map(item=><div className={`timeline-item ${item.kind}`} id={item.kind==='comment'?`comment-${item.id}`:`activity-${item.id}`} key={`${item.kind}-${item.id}`}><Avatar name={item.kind==='event'?item.actor.displayName:item.user.displayName}/><div>{item.kind==='event'?<ActivityRow event={item}/>:<article className="comment-card">
        <header><strong>{item.user.displayName}</strong><a href={`#comment-${item.id}`} title={format(new Date(item.createdAt),'PPpp')}>{formatDistanceToNow(new Date(item.createdAt),{addSuffix:true})}</a>{item.editedAt&&<span>edited</span>}<CommentMenu own={item.user.id===viewerId} body={item.body} onEdit={()=>setEditing(item.id)} onDelete={()=>setDeleting(item)}/></header>
        {editing===item.id?<Composer compact initialValue={item.body} initialData={item.bodyData} placeholder="Edit comment…" onCancel={()=>setEditing(null)} onSubmit={async(body,data)=>{await onEdit(item.id,body,data);setEditing(null)}}/>:<div className="comment-body"><RichComment body={item.body} data={item.bodyData}/></div>}
        <ReactionPills reactions={item.reactions} viewerId={viewerId} onToggle={emoji=>run(item.id,()=>onReaction(item.id,emoji))}/>
        <div className="comment-actions"><EmojiPicker align="start" onSelect={emoji=>run(item.id,()=>onReaction(item.id,emoji))}><button type="button" aria-label="Add reaction"><SmilePlus size={13}/><span>Add reaction</span></button></EmojiPicker><button type="button" aria-label="Reply" onClick={()=>setReplying(current=>current===item.id?null:item.id)}><MessageSquareReply size={13}/><span>Reply</span></button></div>
        {comments.filter(reply=>reply.parentId===item.id).map(reply=><div className="comment-reply" key={reply.id}><Avatar name={reply.user.displayName}/><div><header><strong>{reply.user.displayName}</strong><time>{formatDistanceToNow(new Date(reply.createdAt),{addSuffix:true})}</time></header><div className="reply-body"><RichComment body={reply.body} data={reply.bodyData}/></div><ReactionPills reactions={reply.reactions} viewerId={viewerId} onToggle={emoji=>run(reply.id,()=>onReaction(reply.id,emoji))}/></div></div>)}
        {replying===item.id&&<Composer compact placeholder="Leave a reply…" onAttach={onAttach} onCancel={()=>setReplying(null)} onSubmit={async(body,data)=>{await onReply(body,data,item.id);setReplying(null)}}/>}
      </article>}</div></div>)}
    </div>
    <DeleteCommentDialog open={Boolean(deleting)} busy={busy===deleting?.id} onOpenChange={open=>!open&&setDeleting(null)} onConfirm={()=>deleting&&run(deleting.id,async()=>{await onDelete(deleting.id);setDeleting(null)})}/>
  </>
}

function ActivityRow({event}:{event:ActivityEvent}) { const {t}=useI18n();return <p className="activity-row"><span><strong>{event.actor.displayName}</strong> {describe(event,t)}</span><span className="activity-dot">·</span><a href={`#activity-${event.id}`} title={format(new Date(event.createdAt),'PPpp')}>{formatDistanceToNow(new Date(event.createdAt),{addSuffix:true})}</a></p> }

function CommentMenu({own,body,onEdit,onDelete}:{own:boolean;body:string;onEdit:()=>void;onDelete:()=>void}){return <DropdownMenu><DropdownMenuTrigger asChild><button type="button" aria-label="Comment options"><Ellipsis size={14}/></button></DropdownMenuTrigger><DropdownMenuContent className="comment-options" align="end"><DropdownMenuItem onSelect={()=>void navigator.clipboard.writeText(location.href.split('#')[0])}><Copy size={14}/>Copy link to comment</DropdownMenuItem><DropdownMenuItem onSelect={()=>void navigator.clipboard.writeText(body)}><Copy size={14}/>Copy content as Markdown</DropdownMenuItem>{own&&<><DropdownMenuSeparator/><DropdownMenuItem onSelect={onEdit}><Edit3 size={14}/>Edit comment</DropdownMenuItem><DropdownMenuItem className="danger" onSelect={onDelete}><Trash2 size={14}/>Delete comment</DropdownMenuItem></>}</DropdownMenuContent></DropdownMenu>}

function DeleteCommentDialog({open,busy,onOpenChange,onConfirm}:{open:boolean;busy:boolean;onOpenChange:(open:boolean)=>void;onConfirm:()=>void}){return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="confirm-dialog"><DialogTitle>Delete comment?</DialogTitle><p>This comment and its replies will be permanently deleted.</p><footer><Button variant="ghost" onClick={()=>onOpenChange(false)}>Cancel</Button><Button className="danger-button" disabled={busy} onClick={onConfirm}>{busy?'Deleting…':'Delete comment'}</Button></footer></DialogContent></Dialog>}

function describe(event:ActivityEvent,t:(source:string)=>string){if(event.type==='issue.created')return'created the issue';if(event.type==='issue.updated'){const fields=Object.keys(event.metadata).filter(field=>!['documentContent'].includes(field));return fields.length?`changed ${fields.join(', ')}`:'updated the issue'};if(event.type==='issue.releases_updated'){if(event.metadata.added)return`${t('added to release')} ${event.metadata.added}`;if(event.metadata.removed)return`${t('removed from release')} ${event.metadata.removed}`;return t('updated releases')};if(event.type==='issue.relation_added')return`marked this as ${relationText(event.metadata.type)} ${event.metadata.relatedIssueId||''}`;if(event.type==='issue.relation_removed')return'removed an issue relation';if(event.type==='attachment.created')return`attached ${event.metadata.title||'a file'}`;if(event.type==='attachment.deleted')return'removed an attachment';return event.type.replaceAll('.',' ').replaceAll('_',' ')}
function relationText(type:string){return type==='blocks'?'blocking':type==='blocked_by'?'blocked by':type==='duplicate'?'a duplicate of':type==='parent_of'?'parent of':type==='sub_issue_of'?'sub-issue of':'related to'}
