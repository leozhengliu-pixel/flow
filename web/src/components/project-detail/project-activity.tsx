import { useMemo, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bot, Check, Flag, MessageCircle, MoreHorizontal, Paperclip, SmilePlus, Trash2 } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { CalendarIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { EmojiPicker } from '@/components/reactions/emoji-picker'
import type { Comment, Project, ProjectUpdate } from '@/types/flow'
import type { ProjectDetailProps } from './project-detail-types'
import { PROJECT_HEALTHS } from './project-detail-types'

export function ProjectActivity({ project, projectUpdates, viewer, onCommentProject, onCommentProjectUpdate, onCreateUpdate, onDeleteUpdate, onReactProjectUpdate, onUpdateProjectUpdate }: ProjectDetailProps) {
  const [composerMode, setComposerMode] = useState<'comment'|'update'>('update')
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>(project.health === 'noUpdate' ? 'onTrack' : project.health)
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ProjectUpdate>()
  const [editing, setEditing] = useState<ProjectUpdate>()
  const feed = useMemo(() => [
    ...projectUpdates.map(update => ({ type: 'update' as const, createdAt: update.createdAt, update })),
    ...(project.comments ?? []).map(comment => ({ type: 'comment' as const, createdAt: comment.createdAt, comment })),
  ].sort((a, b) => +new Date(b.createdAt) - +new Date(a.createdAt)), [project.comments, projectUpdates])
  const propertyEvents = useMemo(() => buildProjectEvents(project, viewer.displayName), [project, viewer.displayName])

  const submit = async () => {
    if (!body.trim() || saving) return
    setSaving(true)
    try {
      if (composerMode === 'comment') await onCommentProject(project.id, body.trim())
      else await onCreateUpdate(project.id, { body: body.trim(), health })
      setBody('')
    } catch (error) { toast.error('Could not post to project', { description: error instanceof Error ? error.message : undefined }) }
    finally { setSaving(false) }
  }

  return <div className="project-activity">
    <section className="project-activity__composer" data-mode={composerMode}>
      <header><div aria-label="Post type" role="tablist"><button aria-selected={composerMode === 'comment'} onClick={() => setComposerMode('comment')} role="tab" type="button">Comment</button><button aria-selected={composerMode === 'update'} onClick={() => setComposerMode('update')} role="tab" type="button">Update</button></div>{composerMode === 'update' && <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={`project-activity__health is-${health}`} type="button"><i/>{healthLabel(health)}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu" sideOffset={4}>{PROJECT_HEALTHS.slice(0, 3).map(option => <DropdownMenu.Item key={option.id} onSelect={() => setHealth(option.id)}><span className={`project-activity__health-dot is-${option.id}`}/><span>{option.label}</span>{health === option.id && <Check size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}</header>
      <textarea aria-label={composerMode === 'update' ? 'Project update' : 'Project comment'} onChange={event => setBody(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submit() } }} placeholder={composerMode === 'update' ? 'Write a project update…' : 'Leave a comment…'} value={body}/>
      {composerMode === 'update' && <div className="project-activity__metadata">
        <div><span>Priority</span><div><PriorityIcon priority={0} size={13}/><small>No priority</small><b>→</b><PriorityIcon priority={project.priority} size={13}/><strong>{project.priorityLabel}</strong></div></div>
        {project.lead && <div><span>Lead</span><div><Avatar name={project.lead.displayName}/><strong>{project.lead.displayName} assigned</strong></div></div>}
        {project.startDate && <div><span>Start date</span><div><strong>set to {format(new Date(`${project.startDate}T00:00:00`), 'MMM do')}</strong></div></div>}
      </div>}
      <footer>{composerMode === 'update' ? <button onClick={() => toast.info('Agent drafting will be connected with the Agent module.')} type="button"><Bot size={13}/>Write with Agent</button> : <span/>}<div><button aria-label="Attach images, files, or videos" onClick={() => toast.info('Project attachments are not connected yet.')} type="button"><Paperclip size={13}/></button><button className="is-submit" disabled={!body.trim() || saving} onClick={() => void submit()} type="button">{saving ? 'Posting…' : composerMode === 'update' ? 'Post update' : 'Comment'}</button></div></footer>
    </section>

    <div className="project-activity__feed">
      {feed.map(item => item.type === 'update' ? <UpdateEntry key={item.update.id} onComment={body => onCommentProjectUpdate(project.id, item.update.id, body)} onDelete={() => setDeleteTarget(item.update)} onEdit={() => setEditing(item.update)} onReact={emoji => onReactProjectUpdate(project.id, item.update.id, emoji)} update={item.update} viewerId={viewer.id}/> : <CommentEntry comment={item.comment} key={item.comment.id}/>)}
      <ActivityPropertyTimeline events={propertyEvents}/>
    </div>

    <Dialog.Root onOpenChange={open => { if (!open) setDeleteTarget(undefined) }} open={Boolean(deleteTarget)}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__delete-dialog project-activity__delete-dialog"><Dialog.Title>Delete this project update?</Dialog.Title><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button autoFocus className="is-danger" onClick={() => deleteTarget && void onDeleteUpdate(project.id, deleteTarget.id).then(() => setDeleteTarget(undefined))} type="button">Delete</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
    <EditUpdateDialog key={editing?.id ?? 'edit-closed'} onOpenChange={open => { if (!open) setEditing(undefined) }} open={Boolean(editing)} update={editing} onSave={async (updateId, input) => { await onUpdateProjectUpdate(project.id, updateId, input); setEditing(undefined) }}/>
  </div>
}

function UpdateEntry({ onComment, onDelete, onEdit, onReact, update, viewerId }: { onComment: (body: string) => Promise<ProjectUpdate>; onDelete: () => void; onEdit: () => void; onReact: (emoji: string) => Promise<ProjectUpdate>; update: ProjectUpdate; viewerId: string }) {
  const [commentsOpen, setCommentsOpen] = useState(false)
  const [comment, setComment] = useState('')
  return <article className="project-activity__update"><header><span className={`project-activity__update-health is-${update.health}`}><i/>{healthLabel(update.health)}</span><Avatar name={update.user.displayName}/><strong>{update.user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(update.createdAt), { addSuffix: true })}</time><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open update menu" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Item onSelect={onEdit}><span>Edit</span></DropdownMenu.Item><DropdownMenu.Item className="is-danger" onSelect={onDelete}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></header><p>{update.body}</p>
    <div className="project-activity__reactions">{Object.entries(update.reactions ?? {}).map(([emoji, users]) => <button aria-pressed={users.includes(viewerId)} key={emoji} onClick={() => void onReact(emoji)} type="button">{emoji} {users.length}</button>)}</div>
    <footer><button aria-label={`${update.comments?.length ?? 0} comments`} onClick={() => setCommentsOpen(value => !value)} type="button"><MessageCircle size={13}/>{update.comments?.length ? update.comments.length : null}</button><EmojiPicker onSelect={async emoji => { await onReact(emoji) }}><button aria-label="Add reaction" type="button"><SmilePlus size={13}/></button></EmojiPicker></footer>
    {commentsOpen && <div className="project-activity__comments">{(update.comments ?? []).map(item => <CommentEntry comment={item} key={item.id}/>)}<div className="project-activity__comment-box"><Avatar name="You"/><input aria-label="Add comment" onChange={event => setComment(event.target.value)} placeholder="Leave a comment…" value={comment}/><button disabled={!comment.trim()} onClick={() => void onComment(comment.trim()).then(() => setComment(''))} type="button">Comment</button></div></div>}
  </article>
}

function CommentEntry({ comment }: { comment: Comment }) { return <article className="project-activity__comment"><Avatar name={comment.user.displayName}/><div><header><strong>{comment.user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(comment.createdAt), { addSuffix: true })}</time></header><p>{comment.body}</p></div></article> }
function PropertyEvent({ icon, text, time }: { icon?: ReactNode; text: string; time: string }) { return <div className="project-activity__event">{icon ?? <span className="project-activity__event-dot"/>}<span>{text}</span><time>· {format(new Date(time), 'MMM d')}</time></div> }

type ProjectPropertyEvent = { id: string; icon?: ReactNode; text: string; time: string }
function ActivityPropertyTimeline({ events }: { events: ProjectPropertyEvent[] }) {
  let month = ''
  return <>{events.map(event => { const nextMonth = format(new Date(event.time), 'MMMM'); const heading = nextMonth !== month; month = nextMonth; return <div key={event.id}>{heading && <h2>{nextMonth}</h2>}<PropertyEvent icon={event.icon} text={event.text} time={event.time}/></div> })}</>
}

function buildProjectEvents(project: Project, viewerName: string): ProjectPropertyEvent[] {
  const actor = project.lead?.displayName ?? viewerName
  const current = project.updatedAt
  const created = project.createdAt
  const events: ProjectPropertyEvent[] = []
  for (const initiative of project.initiatives ?? []) events.push({ id: `initiative-${initiative}`, icon: <Flag size={13}/>, text: `${actor} added initiative ${initiative}`, time: current })
  if (project.priority > 0) events.push({ id: 'priority', icon: <PriorityIcon priority={project.priority} size={13}/>, text: `${actor} changed priority from No priority to ${project.priorityLabel}`, time: current })
  if (project.lead) events.push({ id: 'lead', text: `${project.lead.displayName} assigned themselves as a lead`, time: current })
  if (project.status.type !== 'backlog') events.push({ id: 'status', text: `${actor} changed status from Backlog to ${project.status.name}`, time: created })
  if (project.startDate) events.push({ id: 'start', icon: <CalendarIcon size={13}/>, text: `${actor} set start date to ${format(new Date(`${project.startDate}T00:00:00`), 'MMM do')}`, time: created })
  events.push({ id: 'created', text: `${actor} created the project`, time: created })
  return events.sort((left, right) => +new Date(right.time) - +new Date(left.time))
}

function EditUpdateDialog({ onOpenChange, onSave, open, update }: { onOpenChange: (open: boolean) => void; onSave: (id: string, input: { body?: string; health?: Project['health'] }) => Promise<void>; open: boolean; update?: ProjectUpdate }) {
  const [body, setBody] = useState(update?.body ?? '')
  const [health, setHealth] = useState<Project['health']>(update?.health ?? 'onTrack')
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__form-dialog project-activity__edit-dialog"><Dialog.Title>Edit update</Dialog.Title><div className="project-activity__edit-health">{PROJECT_HEALTHS.slice(0,3).map(option => <button className={health === option.id ? 'is-active' : ''} key={option.id} onClick={() => setHealth(option.id)} type="button">{option.label}</button>)}</div><textarea autoFocus aria-label="Edit project update" onChange={event => setBody(event.target.value)} value={body}/><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!body.trim()} onClick={() => update && void onSave(update.id, { body: body.trim(), health })} type="button">Save update</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No update' })[value] }
