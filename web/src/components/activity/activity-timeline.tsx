import { format, formatDistanceToNow } from 'date-fns'
import { Copy, Edit3, Ellipsis, Link2, MessageSquareReply, Paperclip, SmilePlus, Trash2 } from 'lucide-react'
import type { ActivityEvent, Comment, WorkflowState } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { Composer } from '@/components/editor/composer'
import { useRef, useState } from 'react'
import { useActivityHighlight, useActivityHighlightTarget, type ActivityHighlightTarget } from './activity-highlight'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { RichComment } from './rich-comment'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useI18n } from '@/i18n/i18n'
import { CalendarIcon, CycleIcon, LabelIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { activityTimeLabel, describeIssueActivity, type ActivityContext } from './issue-activity-model'
import './activity-timeline.css'

export function ActivityTimeline({ events, comments, viewerId, context, highlightTarget, onReply, onEdit, onDelete, onReaction, onAttach }: { events: ActivityEvent[]; comments: Comment[]; viewerId: string; context?: ActivityContext; highlightTarget?: ActivityHighlightTarget; onReply: (body: string, bodyData: Record<string, unknown> | undefined, parentId: string) => Promise<void>; onEdit: (id: string, body: string, bodyData?: Record<string, unknown>) => Promise<void>; onDelete: (id: string) => Promise<void>; onReaction: (id: string, emoji: string) => Promise<void>; onAttach?:()=>void }) {
  const { t } = useI18n()
  const [replying,setReplying]=useState<string|null>(null),[editing,setEditing]=useState<string|null>(null),[deleting,setDeleting]=useState<Comment|null>(null),[busy,setBusy]=useState<string|null>(null),[expanded,setExpanded]=useState(false)
  const { target, highlightAnchor } = useActivityHighlightTarget(highlightTarget)
  const timelineRef = useRef<HTMLDivElement>(null)
  const topLevel=comments.filter(comment=>!comment.parentId)
  const eventItems=events.flatMap(event=>{const description=describeIssueActivity(event,context,t);return description?[{...event,description}]:[]})
  const items=[...eventItems.map(event=>({...event,kind:'event' as const})),...topLevel.map(comment=>({...comment,kind:'comment' as const}))].sort((a,b)=>new Date(a.createdAt).getTime()-new Date(b.createdAt).getTime())
  const targetComment = target?.kind === 'comment' ? comments.find(comment => comment.id === target.id) : undefined
  const targetIndex = items.findIndex(item => item.id === (targetComment?.parentId ?? target?.id) && item.kind === (target?.kind === 'comment' ? 'comment' : 'event'))
  const start = expanded ? 0 : Math.min(Math.max(0, items.length - 8), targetIndex < 0 ? items.length : targetIndex)
  const visible = items.slice(start), hidden = start
  const contentKey = target?.kind === 'comment' ? targetComment?.id : targetIndex >= 0 ? target?.id : undefined
  useActivityHighlight(timelineRef, target, contentKey)
  const run=async(id:string,task:()=>Promise<void>)=>{setBusy(id);try{await task()}finally{setBusy(null)}}
  return <>
    <div ref={timelineRef} className="timeline issue-activity-timeline" onClick={event => {
      const href = (event.target as Element).closest('a')?.getAttribute('href')
      if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && href?.match(/^#(activity|comment)-/)) highlightAnchor(href)
    }}>
      {hidden>0&&<button type="button" className="show-older-activity" onClick={()=>setExpanded(true)}>Show {hidden} older activities</button>}
      {visible.map(item=><div className={`timeline-item ${item.kind}`} data-activity-anchor={item.kind==='event'?`activity-${item.id}`:undefined} id={item.kind==='comment'?`comment-${item.id}`:`activity-${item.id}`} key={`${item.kind}-${item.id}`}>{item.kind==='event'?<div className="activity-event-icon"><ActivityEventIcon event={item} context={context}/></div>:<Avatar name={item.user.displayName}/>}<div>{item.kind==='event'?<ActivityRow event={item} description={item.description}/>:<article className="comment-card" data-activity-anchor={`comment-${item.id}`}>
        <header><strong data-i18n-ignore>{item.user.displayName}</strong><a href={`#comment-${item.id}`} title={format(new Date(item.createdAt),'PPpp')}>{formatDistanceToNow(new Date(item.createdAt),{addSuffix:true})}</a>{item.editedAt&&<span>edited</span>}<CommentMenu id={item.id} own={item.user.id===viewerId} body={item.body} onEdit={()=>setEditing(item.id)} onDelete={()=>setDeleting(item)}/></header>
        {editing===item.id?<Composer compact initialValue={item.body} initialData={item.bodyData} placeholder="Edit comment…" onCancel={()=>setEditing(null)} onSubmit={async(body,data)=>{await onEdit(item.id,body,data);setEditing(null)}}/>:<div className="comment-body"><RichComment body={item.body} data={item.bodyData}/></div>}
        <ReactionPills reactions={item.reactions} viewerId={viewerId} onToggle={emoji=>run(item.id,()=>onReaction(item.id,emoji))}/>
        <div className="comment-actions"><EmojiPicker align="start" onSelect={emoji=>run(item.id,()=>onReaction(item.id,emoji))}><button type="button" aria-label="Add reaction"><SmilePlus size={13}/><span>Add reaction</span></button></EmojiPicker><button type="button" aria-label="Reply" onClick={()=>setReplying(current=>current===item.id?null:item.id)}><MessageSquareReply size={13}/><span>Reply</span></button></div>
        {comments.filter(reply=>reply.parentId===item.id).map(reply=><div className="comment-reply" id={`comment-${reply.id}`} data-activity-anchor={`comment-${reply.id}`} key={reply.id}><Avatar name={reply.user.displayName}/><div><header><strong>{reply.user.displayName}</strong><time>{formatDistanceToNow(new Date(reply.createdAt),{addSuffix:true})}</time></header><div className="reply-body"><RichComment body={reply.body} data={reply.bodyData}/></div><ReactionPills reactions={reply.reactions} viewerId={viewerId} onToggle={emoji=>run(reply.id,()=>onReaction(reply.id,emoji))}/></div></div>)}
        {replying===item.id&&<Composer compact placeholder="Leave a reply…" onAttach={onAttach} onCancel={()=>setReplying(null)} onSubmit={async(body,data)=>{await onReply(body,data,item.id);setReplying(null)}}/>}
      </article>}</div></div>)}
    </div>
    <DeleteCommentDialog open={Boolean(deleting)} busy={busy===deleting?.id} onOpenChange={open=>!open&&setDeleting(null)} onConfirm={()=>deleting&&run(deleting.id,async()=>{await onDelete(deleting.id);setDeleting(null)})}/>
  </>
}

function ActivityRow({event,description}:{event:ActivityEvent;description:string}) { const {locale}=useI18n();return <p className="activity-row" data-i18n-ignore><strong>{event.actor.displayName}</strong> {description}<span className="activity-time"><span className="activity-dot">·</span><a href={`#activity-${event.id}`} title={format(new Date(event.createdAt),'PPpp')}><time dateTime={event.createdAt}>{activityTimeLabel(event.createdAt,Date.now(),locale)}</time></a></span></p> }

function ActivityEventIcon({ event, context }: { event: ActivityEvent; context?: ActivityContext }) {
  const m = event.metadata ?? {}
  if (event.type === 'issue.updated') {
    if (m.state) return <StatusIcon size={14} state={context?.states.find(state => state.id === m.stateId) ?? { id: m.stateId || m.state, name: m.state, color: 'currentColor', type: (m.stateType || 'unstarted') as WorkflowState['type'] }}/>
    if (m.priority) return <PriorityIcon size={14} priority={Math.max(0,['No priority','Urgent','High','Medium','Low'].indexOf(m.priority))}/>
    if ('labels' in m) return <LabelIcon size={14}/>
    if ('project' in m || 'projectMilestone' in m) return <ProjectIcon size={14}/>
    if ('cycle' in m) return <CycleIcon size={14}/>
    if ('dueDate' in m) return <CalendarIcon size={14}/>
    if ('title' in m) return <Edit3 size={14}/>
  }
  if (event.type.startsWith('attachment.')) return <Paperclip size={14}/>
  if (event.type.includes('relation') || event.type.includes('review')) return <Link2 size={14}/>
  return <Avatar name={event.actor.displayName}/>
}

function CommentMenu({id,own,body,onEdit,onDelete}:{id:string;own:boolean;body:string;onEdit:()=>void;onDelete:()=>void}){return <DropdownMenu><DropdownMenuTrigger asChild><button type="button" aria-label="Comment options"><Ellipsis size={14}/></button></DropdownMenuTrigger><DropdownMenuContent className="comment-options" align="end"><DropdownMenuItem onSelect={()=>void navigator.clipboard.writeText(`${location.href.split('#')[0]}#comment-${id}`)}><Copy size={14}/>Copy link to comment</DropdownMenuItem><DropdownMenuItem onSelect={()=>void navigator.clipboard.writeText(body)}><Copy size={14}/>Copy content as Markdown</DropdownMenuItem>{own&&<><DropdownMenuSeparator/><DropdownMenuItem onSelect={onEdit}><Edit3 size={14}/>Edit comment</DropdownMenuItem><DropdownMenuItem className="danger" onSelect={onDelete}><Trash2 size={14}/>Delete comment</DropdownMenuItem></>}</DropdownMenuContent></DropdownMenu>}

function DeleteCommentDialog({open,busy,onOpenChange,onConfirm}:{open:boolean;busy:boolean;onOpenChange:(open:boolean)=>void;onConfirm:()=>void}){return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="confirm-dialog"><DialogTitle>Delete comment?</DialogTitle><p>This comment and its replies will be permanently deleted.</p><footer><Button variant="ghost" onClick={()=>onOpenChange(false)}>Cancel</Button><Button className="danger-button" disabled={busy} onClick={onConfirm}>{busy?'Deleting…':'Delete comment'}</Button></footer></DialogContent></Dialog>}
