import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Copy, FileText, MessageCircle, MoreHorizontal, Pencil, SmilePlus, Trash2, X } from 'lucide-react'
import { useState } from 'react'
import { formatDistanceToNowStrict } from 'date-fns'
import { Avatar } from '@/components/issue/issue-row'
import { EmojiPicker } from '@/components/reactions/emoji-picker'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import type { InitiativeUpdate, Project, ProjectUpdate, User } from '@/types/flow'
import type { PulseUpdateItem } from './pulse-model'
import { useCommentComposer } from '@/hooks/use-comment-composer'
import { RichComment } from '@/components/activity/rich-comment'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'

type Props = {
  item: PulseUpdateItem
  viewerId: string
  workspaceSlug: string
  users: User[]
  onCommentProject: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onReactProject: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onUpdateProject: (projectId: string, updateId: string, input: { body?: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<ProjectUpdate>
  onDeleteProject: (projectId: string, updateId: string) => Promise<void>
  onDeleteProjectAttachment:(projectId:string,updateId:string,attachmentId:string)=>Promise<ProjectUpdate>
  onCommentInitiative: (initiativeId: string, updateId: string, body: string) => Promise<InitiativeUpdate>
  onReactInitiative: (initiativeId: string, updateId: string, emoji: string) => Promise<InitiativeUpdate>
  onUpdateInitiative: (initiativeId: string, updateId: string, input: { body?: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<InitiativeUpdate>
  onDeleteInitiative: (initiativeId: string, updateId: string) => Promise<void>
  onDeleteInitiativeAttachment:(initiativeId:string,updateId:string,attachmentId:string)=>Promise<InitiativeUpdate>
}

export function PulseUpdateCard(props: Props) {
  const { item } = props
  const update = item.update
  const source = item.kind === 'project' ? item.project : item.initiative
  const [commentsOpen, setCommentsOpen] = useState(false)
  const {comment,posting,setComment,submitComment:postComment}=useCommentComposer(body=>item.kind==='project'?props.onCommentProject(item.project.id,update.id,body):props.onCommentInitiative(item.initiative.id,update.id,body))
  const [editing, setEditing] = useState(false)
  const [editBody, setEditBody] = useState<DescriptionSnapshot>()
  const [editHealth, setEditHealth] = useState(update.health)
  const [saving, setSaving] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const canModify = update.user.id === props.viewerId
  const comments = update.comments ?? []
  const reactions = update.reactions ?? {}
  const attachments=update.attachments??[]
  const href = item.kind === 'project' ? `/${props.workspaceSlug}/project/${item.project.slugId}/activity` : `/${props.workspaceSlug}/initiative/${item.initiative.slugId}/activity`

  const react = async (emoji: string) => {
    if (item.kind === 'project') await props.onReactProject(item.project.id, update.id, emoji)
    else await props.onReactInitiative(item.initiative.id, update.id, emoji)
  }
  const save = async () => {
    const body=editBody?.markdown.trim()??update.body.trim()
    if (!body || saving) return
    setSaving(true)
    try {
      const input={body,bodyData:editBody?.document as Record<string,unknown>|undefined,health:editHealth}
      if (item.kind === 'project') await props.onUpdateProject(item.project.id, update.id,input)
      else await props.onUpdateInitiative(item.initiative.id, update.id,input)
      setEditing(false)
    } finally { setSaving(false) }
  }
  const remove = async () => {
    setDeleting(true)
    try {
      if (item.kind === 'project') await props.onDeleteProject(item.project.id, update.id)
      else await props.onDeleteInitiative(item.initiative.id, update.id)
      setDeleteOpen(false)
    } finally { setDeleting(false) }
  }

  return <article className="pulse-update-card">
    <div className="pulse-update-source"><a href={href}><ViewGlyph color={source.color} icon={source.icon || (item.kind === 'project' ? 'Project' : 'Initiative')}/><strong>{source.name}</strong></a><span>{item.kind === 'project' ? 'Project update' : 'Initiative update'}</span></div>
    <header><span className={`pulse-update-health is-${update.health}`}><i/>{healthLabel(update.health)}</span><Avatar name={update.user.displayName || update.user.name}/><strong>{update.user.displayName || update.user.name}</strong><time title={new Date(update.createdAt).toLocaleString()}>{formatDistanceToNowStrict(new Date(update.createdAt), { addSuffix: true })}{update.editedAt ? ' · edited' : ''}</time>
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open update menu" className="pulse-update-menu-button" type="button"><MoreHorizontal size={15}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="pulse-menu pulse-update-menu" sideOffset={4}><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(`${location.origin}${href}`)}><Copy size={14}/>Copy link</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(`**${healthLabel(update.health)}** — ${update.body}`)}><Copy size={14}/>Copy as markdown</DropdownMenu.Item>{canModify && <><DropdownMenu.Separator/><DropdownMenu.Item onSelect={() => { setEditBody(undefined); setEditHealth(update.health); setEditing(true) }}><Pencil size={14}/>Edit</DropdownMenu.Item><DropdownMenu.Item className="is-danger" onSelect={() => setDeleteOpen(true)}><Trash2 size={14}/>Delete</DropdownMenu.Item></>}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    </header>
    {editing ? <div className="pulse-update-editor"><HealthSelect health={editHealth} onChange={setEditHealth}/><IssueDescriptionEditor users={props.users} ariaLabel="Update body" className="pulse-update-rich-editor" placeholder="Write an update…" value={editBody?.markdown??update.body} state={editBody?.documentJSON??(update.bodyData?JSON.stringify(update.bodyData):undefined)} onChange={setEditBody} onSubmit={()=>void save()}/><footer><button onClick={() => setEditing(false)} type="button">Cancel</button><button className="is-primary" disabled={!(editBody?.markdown??update.body).trim() || saving} onClick={() => void save()} type="button">{saving ? 'Saving…' : 'Save update'}</button></footer></div> : <div className="pulse-update-body">{takeaway(update.body)!==update.body&&<strong>{takeaway(update.body)}</strong>}<RichComment body={update.body} data={update.bodyData}/></div>}
    {attachments.length>0&&<div className="pulse-update-attachments">{attachments.map(attachment=><span key={attachment.id}><a href={attachment.url} target="_blank" rel="noreferrer"><FileText size={13}/><b>{attachment.title}</b><small>{formatSize(attachment.size)}</small></a>{canModify&&<button aria-label={`Remove ${attachment.title}`} onClick={()=>void(item.kind==='project'?props.onDeleteProjectAttachment(item.project.id,update.id,attachment.id):props.onDeleteInitiativeAttachment(item.initiative.id,update.id,attachment.id))}><X size={11}/></button>}</span>)}</div>}
    <div className="pulse-update-reactions">{Object.entries(reactions).map(([emoji, userIds]) => <button aria-pressed={userIds.includes(props.viewerId)} key={emoji} onClick={() => void react(emoji)} type="button"><span>{emoji}</span>{userIds.length}</button>)}</div>
    <footer><button aria-expanded={commentsOpen} aria-label={`${comments.length} ${comments.length === 1 ? 'comment' : 'comments'}`} onClick={() => setCommentsOpen(value => !value)} type="button"><MessageCircle size={14}/>{comments.length > 0 && <span>{comments.length}</span>}</button><EmojiPicker align="start" onSelect={react}><button aria-label="Add reaction" type="button"><SmilePlus size={14}/></button></EmojiPicker></footer>
    {commentsOpen && <section className="pulse-update-comments">{comments.map(item => <article key={item.id}><Avatar name={item.user.displayName || item.user.name}/><div><header><strong>{item.user.displayName || item.user.name}</strong><time>{formatDistanceToNowStrict(new Date(item.createdAt), { addSuffix: true })}</time></header><p>{item.body}</p></div></article>)}<div className="pulse-comment-box"><Avatar name="You"/><textarea aria-label="Add comment" placeholder="Leave a comment…" value={comment} onChange={event => setComment(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void postComment() } }}/><button disabled={!comment.trim() || posting} onClick={() => void postComment()} type="button">{posting ? 'Sending…' : 'Comment'}</button></div></section>}
    <Dialog.Root onOpenChange={setDeleteOpen} open={deleteOpen}><Dialog.Portal><Dialog.Overlay className="pulse-dialog-overlay is-delete"/><Dialog.Content aria-describedby={undefined} className="pulse-delete-dialog"><Dialog.Title>Delete this {item.kind} update?</Dialog.Title><footer><Dialog.Close asChild><button disabled={deleting} type="button">Cancel</button></Dialog.Close><button className="is-danger" disabled={deleting} onClick={() => void remove()} type="button">{deleting ? 'Deleting…' : 'Delete'}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
  </article>
}

function HealthSelect({ health, onChange }: { health: Project['health']; onChange: (health: Project['health']) => void }) { return <div className="pulse-inline-health">{(['onTrack', 'atRisk', 'offTrack'] as Project['health'][]).map(value => <button className={health === value ? 'is-active' : ''} key={value} onClick={() => onChange(value)} type="button"><i className={`is-${value}`}/>{healthLabel(value)}</button>)}</div> }
function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No update' })[value] }
function takeaway(body:string){const normalized=body.trim().replace(/\s+/g,' ');const sentence=normalized.match(/^(.+?[。！？.!?])(?:\s|$)/)?.[1];return sentence??normalized}
function formatSize(size:number){if(!size)return'';if(size<1024)return`${size} B`;if(size<1024*1024)return`${Math.round(size/1024)} KB`;return`${(size/1024/1024).toFixed(1)} MB`}
