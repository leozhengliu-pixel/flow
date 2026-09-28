import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Flag, MessageCircle, MoreHorizontal, Paperclip, SmilePlus, Trash2, X } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import type { Editor } from '@tiptap/react'
import { createDraft, deleteDraft, listProjectHistory, updateDraft, uploadProjectCommentAttachment } from '@/lib/api'
import { AgentChatPanel } from '@/lib/route-pages'
import { AgentWriteIcon } from '@/components/agent/agent-icons'
import { useI18n } from '@/i18n/i18n'
import { clearComposerDraft, readComposerDraft, writeComposerDraft, type ComposerDraftType } from '@/lib/composer-drafts'
import { Avatar } from '@/components/issue/issue-row'
import { CalendarIcon, PriorityIcon, ProjectIcon } from '@/components/issue/issue-icons'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { DeleteCommentDialog } from '@/components/activity/activity-timeline'
import { ResolvedComment } from '@/components/activity/resolved-comment'
import { ProjectCommentMenu } from './project-comment-menu'
import { Composer } from '@/components/editor/composer'
import type { AuditLogEntry, Comment, Project, ProjectUpdate, ThreadSubscription, ThreadSubscriptionState } from '@/types/flow'
import type { ProjectDetailProps } from './project-detail-types'
import { PROJECT_HEALTHS } from './project-detail-types'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { RichComment } from '@/components/activity/rich-comment'
import { insertImageFiles } from '@/components/issue/editor/image-extension'
import { insertEmbedFiles } from '@/components/issue/editor/file-extension'
import { ActivityPage } from '@/components/activity/activity-page'
import { getMostRecent, reconcileSelection, selectionFromRoute } from '@/components/activity/entity-activity-update-helper'

export function ProjectActivity({ activities, drafts = [], project, projectUpdates, viewer, viewerRole, users, threadSubscriptions, onCommentProject, onUpdateProjectComment, onDeleteProjectComment, onReactProjectComment, onResolveProjectComment, onProjectCommentThreadSubscription, onCreateIssue, onCommentProjectUpdate, onCreateUpdate, onDeleteUpdate, onReactProjectUpdate, onUpdateProjectUpdate, onUploadProjectUpdateAttachment, onDeleteProjectUpdateAttachment }: ProjectDetailProps) {
  const parentDrafts = useMemo(() => ({
    comment: drafts.find(item => item.type === 'comment' && item.resourceId === project.id && (item.metadata?.resourceType ?? 'issue') === 'project') ?? readComposerDraft('comment', project.id),
    project_update: drafts.find(item => item.type === 'project_update' && item.resourceId === project.id) ?? readComposerDraft('project_update', project.id),
  }), [drafts, project.id])
  const initialDraft = parentDrafts.comment ?? parentDrafts.project_update
  const [composerMode, setComposerMode] = useState<'comment'|'update'>(() => initialDraft?.type === 'project_update' ? 'update' : 'comment')
  const draftType: ComposerDraftType = composerMode === 'comment' ? 'comment' : 'project_update'
  const [body, setBody] = useState(initialDraft?.body ?? '')
  const [commentData,setCommentData]=useState<Record<string,unknown>|undefined>(initialDraft?.metadata?.bodyData as Record<string,unknown>|undefined)
  const [health, setHealth] = useState<Project['health']>(typeof initialDraft?.metadata?.health === 'string' ? initialDraft.metadata.health as Project['health'] : project.health === 'noUpdate' ? 'onTrack' : project.health)
  const [saving, setSaving] = useState(false)
  const [files, setFiles] = useState<File[]>([])
  const fileRef = useRef<HTMLInputElement>(null)
  const [deleteTarget, setDeleteTarget] = useState<ProjectUpdate>()
  const [editing, setEditing] = useState<ProjectUpdate>()
  const draftIds = useRef<Partial<Record<ComposerDraftType, string>>>({ comment: parentDrafts.comment?.id ?? '', project_update: parentDrafts.project_update?.id ?? '' })
  // Replies live in the same project comment list (parentId) and render
  // inside their root comment's card thread, never as feed entries.
  const commentThreads = useMemo(() => {
    const comments = project.comments ?? []
    const ids = new Set(comments.map(comment => comment.id))
    const replies = new Map<string, Comment[]>()
    for (const comment of comments) {
      if (!comment.parentId || !ids.has(comment.parentId)) continue
      replies.set(comment.parentId, [...(replies.get(comment.parentId) ?? []), comment])
    }
    for (const items of replies.values()) items.sort((a, b) => +new Date(a.createdAt) - +new Date(b.createdAt))
    return { roots: comments.filter(comment => !comment.parentId || !ids.has(comment.parentId)), replies }
  }, [project.comments])
  const feed = useMemo(() => [
    ...projectUpdates.map(update => ({ type: 'update' as const, createdAt: update.createdAt, update })),
    ...commentThreads.roots.map(comment => ({ type: 'comment' as const, createdAt: comment.createdAt, comment })),
  ].sort((a, b) => +new Date(b.createdAt) - +new Date(a.createdAt)), [commentThreads, projectUpdates])
  // Projects created before the creator was recorded keep the lead/viewer fallback.
  const creator = project.creator ?? project.lead ?? viewer
  const { t } = useI18n()
  const [editorKey, setEditorKey] = useState(0)
  const [agentOpen, setAgentOpen] = useState(false)
  const [history, setHistory] = useState<AuditLogEntry[]>([])
  const historyVersion = `${project.updatedAt}:${projectUpdates.length}`
  useEffect(() => {
    let active = true
    listProjectHistory(project.id).then(page => { if (active) setHistory(page?.nodes ?? []) }).catch(() => undefined)
    return () => { active = false }
  }, [historyVersion, project.id])
  const updateChanges = useMemo(() => projectUpdateChanges(projectUpdates, history, project.createdAt), [history, project.createdAt, projectUpdates])
  const hasContent = Boolean(body.trim()) || files.length > 0
  const composerEditorRef = useRef<Editor | null>(null)
  const setComposerEditor = useCallback((editor: Editor | null) => { composerEditorRef.current = editor }, [])
  const [pendingUploads, setPendingUploads] = useState(0)
  // Inline media (paperclip, paste, and drop) uploads through the project so the
  // returned URL can be embedded directly in the comment or update body.
  const uploadInlineMedia = useCallback(async (file: File) => {
    setPendingUploads(count => count + 1)
    try {
      const attachment = await uploadProjectCommentAttachment(project.id, file)
      if (!attachment?.url) throw new Error('Upload failed')
      return attachment.url
    } catch (error) {
      toast.error(t('Could not upload file'), { description: error instanceof Error ? error.message : undefined })
      throw error
    } finally { setPendingUploads(count => Math.max(0, count - 1)) }
  }, [project.id, t])
  const insertComposerFiles = (picked: File[]) => {
    const view = composerEditorRef.current?.view
    if (!view || !picked.length) return
    const images = picked.filter(file => file.type.startsWith('image/'))
    const rest = picked.filter(file => !file.type.startsWith('image/'))
    if (images.length) insertImageFiles(view, images, uploadInlineMedia)
    if (rest.length) insertEmbedFiles(view, rest, uploadInlineMedia)
    composerEditorRef.current?.commands.focus()
  }
  const propertyEvents = useMemo(() => buildProjectEvents(activities, { actor: creator, createdAt: project.createdAt, color: project.color }), [activities, creator, project.createdAt, project.color])

  useEffect(() => {
    if (!body.trim() || saving) return
    const timer = window.setTimeout(() => {
      const input = { type: draftType, resourceId: project.id, title: project.name, body: body.trim(), metadata: { resourceType: 'project', health, bodyData: composerMode === 'comment' ? commentData : undefined } }
      const save = async () => {
        if (!draftIds.current[draftType]) return createDraft(input)
        try { return await updateDraft(draftIds.current[draftType]!, input) } catch { draftIds.current[draftType] = ''; return createDraft(input) }
      }
      void save().then(saved => {
        draftIds.current[draftType] = saved.id
        writeComposerDraft({ id: saved.id, type: draftType, resourceId: project.id, title: project.name, body: body.trim(), metadata: input.metadata, updatedAt: saved.updatedAt })
      }).catch(() => undefined)
    }, 350)
    return () => window.clearTimeout(timer)
  }, [body, commentData, composerMode, draftType, health, project.id, project.name, saving])

  const switchComposerMode = (next: 'comment' | 'update') => {
    if (next === composerMode) return
    const nextType: ComposerDraftType = next === 'comment' ? 'comment' : 'project_update'
    const nextDraft = parentDrafts[nextType]
    setComposerMode(next)
    setEditorKey(key => key + 1)
    setBody(nextDraft?.body ?? '')
    setCommentData(nextDraft?.metadata?.bodyData as Record<string,unknown>|undefined)
    if (typeof nextDraft?.metadata?.health === 'string') setHealth(nextDraft.metadata.health as Project['health'])
  }

  const submit = async () => {
    if (!body.trim() || saving || pendingUploads > 0) return
    setSaving(true)
    try {
      if (composerMode === 'comment') await onCommentProject(project.id, body.trim(), commentData)
      else {
        const update = await onCreateUpdate(project.id, { body: body.trim(), health })
        for (const file of files) await onUploadProjectUpdateAttachment(project.id, update.id, file)
      }
      const savedType = draftType
      const savedId = draftIds.current[savedType]
      if (savedId && !savedId.startsWith('local:')) await deleteDraft(savedId).catch(() => undefined)
      clearComposerDraft(savedType, project.id)
      draftIds.current[savedType] = ''
      setBody('')
      setCommentData(undefined)
      setFiles([])
      setEditorKey(key => key + 1)
    } catch (error) { toast.error('Could not post to project', { description: error instanceof Error ? error.message : undefined }) }
    finally { setSaving(false) }
  }

  const cancel = () => {
    const savedId = draftIds.current[draftType]
    if (savedId && !savedId.startsWith('local:')) void deleteDraft(savedId).catch(() => undefined)
    clearComposerDraft(draftType, project.id)
    draftIds.current[draftType] = ''
    setBody('')
    setCommentData(undefined)
    setFiles([])
    setEditorKey(key => key + 1)
  }

  const targetedUpdateId = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('updateId') ?? undefined : undefined
  const updateRefs = projectUpdates.map(update => ({ id: update.id, createdAt: update.createdAt }))
  const selectionState = reconcileSelection(
    selectionFromRoute(targetedUpdateId, `project:${project.id}`),
    { boundaryKey: `project:${project.id}`, targetedUpdateId, requestsDifferentActivityTarget: Boolean(targetedUpdateId) },
    updateRefs,
  )
  const initialUpdateId = selectionState.selection?.id ?? getMostRecent(updateRefs)?.id

  return <ActivityPage entityId={project.id} entityType="project" hideHeader initialUpdateId={initialUpdateId}>
    <div className="project-activity">
    <section className="project-activity__composer" data-mode={composerMode}>
      <header><div aria-label="Post type" role="tablist"><button aria-selected={composerMode === 'comment'} onClick={() => switchComposerMode('comment')} role="tab" type="button">Comment</button><button aria-selected={composerMode === 'update'} onClick={() => switchComposerMode('update')} role="tab" type="button">Update</button></div>{composerMode === 'update' && <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={`project-activity__health is-${health}`} type="button"><HealthGlyph health={health}/>{healthLabel(health)}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className="project-detail-page__menu project-activity__health-menu" sideOffset={4}>{PROJECT_HEALTHS.slice(0, 3).map(option => <DropdownMenu.Item aria-checked={health === option.id} key={option.id} onSelect={() => setHealth(option.id)}><span className={`project-activity__health-glyph is-${option.id}`}><HealthGlyph health={option.id}/></span><span>{option.label}</span></DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}</header>
      <IssueDescriptionEditor className="project-activity__editor" key={`${composerMode}:${editorKey}`} users={users} ariaLabel={composerMode === 'comment' ? 'Project comment' : 'Project update'} value={body} state={composerMode === 'comment' && commentData ? JSON.stringify(commentData) : undefined} placeholder={composerMode === 'comment' ? 'Leave a comment…' : 'Write a project update…'} editorRef={setComposerEditor} onInsertImage={uploadInlineMedia} onSubmit={() => void submit()} onChange={snapshot=>{setBody(snapshot.markdown);if(composerMode==='comment')setCommentData(snapshot.document as Record<string,unknown>)}}/>
      {files.length>0&&<div className="project-activity__files">{files.map((file,index)=><span key={`${file.name}-${index}`}><Paperclip size={12}/>{file.name}<button aria-label={`Remove ${file.name}`} onClick={()=>setFiles(current=>current.filter((_,item)=>item!==index))}><X size={11}/></button></span>)}</div>}
      <footer>{composerMode === 'update' ? <button className="project-activity__agent" onClick={() => setAgentOpen(true)} type="button"><AgentWriteIcon size={14}/>Write with Agent</button> : <span/>}<div><button aria-label="Attach images, files, or videos" className="project-activity__attach" onClick={()=>fileRef.current?.click()} type="button"><Paperclip size={14}/></button><input ref={fileRef} hidden multiple type="file" onChange={event=>{const picked=Array.from(event.target.files??[]);if(composerMode==='comment')insertComposerFiles(picked);else setFiles(current=>[...current,...picked]);event.target.value=''}}/>{hasContent && <button className="project-activity__cancel" disabled={saving} onClick={cancel} type="button">Cancel</button>}<button className={hasContent ? 'is-submit is-primary' : 'is-submit'} disabled={!body.trim() || saving || pendingUploads > 0} onClick={() => void submit()} type="button">{saving ? 'Posting…' : composerMode === 'update' ? 'Post update' : 'Comment'}</button></div></footer>
    </section>
    {agentOpen && <Suspense fallback={null}><AgentChatPanel autoSubmit draftFence="update" draftCard={{ context: project.name, title: 'Update draft', current: body }} initialPrompt={`${t('Help me write an update for this project:')} ${project.name}`} issues={[]} open onClose={() => setAgentOpen(false)} onDraft={draft => { setComposerMode('update'); setBody(draft); setCommentData(undefined); setEditorKey(key => key + 1) }} pageContext={{ type: 'project', id: project.id, label: project.name }}/></Suspense>}

    <div className="project-activity__feed">
      {feed.map(item => item.type === 'update' ? <UpdateEntry key={item.update.id} onComment={body => onCommentProjectUpdate(project.id, item.update.id, body)} onDelete={() => setDeleteTarget(item.update)} onDeleteAttachment={attachmentId=>onDeleteProjectUpdateAttachment(project.id,item.update.id,attachmentId)} onEdit={() => setEditing(item.update)} onReact={emoji => onReactProjectUpdate(project.id, item.update.id, emoji)} update={item.update} changes={updateChanges.get(item.update.id) ?? []} viewerId={viewer.id}/> : <ProjectCommentCard canModerate={viewerRole === 'admin' || viewerRole === 'owner'} comment={item.comment} key={item.comment.id} replies={commentThreads.replies.get(item.comment.id) ?? []} threadState={threadSubscriptions?.find(entry => entry.projectId === project.id && entry.commentId === item.comment.id && entry.userId === viewer.id)?.state ?? null} users={users} viewerId={viewer.id} onNewIssue={comment => onCreateIssue(project.id, undefined, { description: comment.body })} onResolve={(commentId, resolved) => onResolveProjectComment(project.id, commentId, resolved)} onThreadSubscription={(commentId, state) => onProjectCommentThreadSubscription(project.id, commentId, state)} onDelete={commentId => onDeleteProjectComment(project.id, commentId)} onEdit={(commentId, body, bodyData) => onUpdateProjectComment(project.id, commentId, body, bodyData)} onReact={(commentId, emoji) => onReactProjectComment(project.id, commentId, emoji)} onReply={(body, bodyData) => onCommentProject(project.id, body, bodyData, item.comment.id)} onUpload={uploadInlineMedia}/>)}
      <ActivityPropertyTimeline events={propertyEvents}/>
    </div>

    <Dialog.Root onOpenChange={open => { if (!open) setDeleteTarget(undefined) }} open={Boolean(deleteTarget)}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="project-detail-page__delete-dialog project-activity__delete-dialog"><Dialog.Title>Delete this project update?</Dialog.Title><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button autoFocus className="is-danger" onClick={() => deleteTarget && void onDeleteUpdate(project.id, deleteTarget.id).then(() => setDeleteTarget(undefined))} type="button">Delete</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
    <EditUpdateDialog key={editing?.id ?? 'edit-closed'} onOpenChange={open => { if (!open) setEditing(undefined) }} open={Boolean(editing)} update={editing} onSave={async (updateId, input) => { await onUpdateProjectUpdate(project.id, updateId, input); setEditing(undefined) }}/>
  </div>
  </ActivityPage>
}

function UpdateEntry({ changes, onComment, onDelete, onDeleteAttachment, onEdit, onReact, update, viewerId }: { changes: ProjectPropertyChange[]; onComment: (body: string) => Promise<ProjectUpdate>; onDelete: () => void; onDeleteAttachment: (attachmentId:string)=>Promise<ProjectUpdate>; onEdit: () => void; onReact: (emoji: string) => Promise<ProjectUpdate>; update: ProjectUpdate; viewerId: string }) {
  const [commentsOpen, setCommentsOpen] = useState(false)
  const [comment, setComment] = useState('')
  return <article className="project-activity__update" data-update-id={update.id}><header><span className={`project-activity__update-health is-${update.health}`}><HealthGlyph health={update.health}/>{healthLabel(update.health)}</span><Avatar name={update.user.displayName}/><strong>{update.user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(update.createdAt), { addSuffix: true })}</time><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open update menu" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Item onSelect={onEdit}><span>Edit</span></DropdownMenu.Item><DropdownMenu.Item className="is-danger" onSelect={onDelete}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></header><div className="project-activity__rich"><RichComment body={update.body}/></div>{changes.length > 0 && <ul aria-label="Changes since last update" className="project-activity__changes">{changes.map(change => <li key={change.field}><span>{PROPERTY_LABELS[change.field] ?? change.field}</span><span>{formatPropertyValue(change.field, change.from)}</span><span aria-hidden="true">→</span><span>{formatPropertyValue(change.field, change.to)}</span></li>)}</ul>}{update.attachments?.length>0&&<div className="project-activity__attachments">{update.attachments.map(attachment=><span key={attachment.id}><a href={attachment.url} target="_blank" rel="noreferrer"><Paperclip size={12}/>{attachment.title||'Attachment'}</a><button aria-label={`Remove ${attachment.title||'attachment'}`} onClick={()=>void onDeleteAttachment(attachment.id)}><X size={11}/></button></span>)}</div>}
    <div className="project-activity__reactions">{Object.entries(update.reactions ?? {}).map(([emoji, users]) => <button aria-pressed={users.includes(viewerId)} key={emoji} onClick={() => void onReact(emoji)} type="button">{emoji} {users.length}</button>)}</div>
    <footer><button aria-label={`${update.comments?.length ?? 0} comments`} onClick={() => setCommentsOpen(value => !value)} type="button"><MessageCircle size={13}/>{update.comments?.length ? update.comments.length : null}</button><EmojiPicker onSelect={async emoji => { await onReact(emoji) }}><button aria-label="Add reaction" type="button"><SmilePlus size={13}/></button></EmojiPicker></footer>
    {commentsOpen && <div className="project-activity__comments">{(update.comments ?? []).map(item => <CommentEntry comment={item} key={item.id}/>)}<div className="project-activity__comment-box"><Avatar name="You"/><input aria-label="Add comment" onChange={event => setComment(event.target.value)} placeholder="Leave a comment…" value={comment}/><button disabled={!comment.trim()} onClick={() => void onComment(comment.trim()).then(() => setComment(''))} type="button">Comment</button></div></div>}
  </article>
}

/** Linear's compact comment timestamp: "just now", "8min ago", "2h ago", "3d ago", then a date. */
function commentTimeLabel(value: string, locale: string, now = Date.now()) {
  const at = new Date(value)
  if (Number.isNaN(+at)) return ''
  const zh = locale === 'zh-CN'
  const seconds = Math.max(0, (now - +at) / 1000)
  if (seconds < 60) return zh ? '刚刚' : 'just now'
  if (seconds < 604800) {
    const [size, unit, short] = seconds < 3600 ? [60, 'minute', 'min'] as const : seconds < 86400 ? [3600, 'hour', 'h'] as const : [86400, 'day', 'd'] as const
    const count = Math.floor(seconds / size)
    return zh ? new Intl.RelativeTimeFormat('zh-CN', { numeric: 'always', style: 'short' }).format(-count, unit) : `${count}${short} ago`
  }
  const sameYear = at.getFullYear() === new Date(now).getFullYear()
  return format(at, zh ? (sameYear ? 'M月d日' : 'yyyy年M月d日') : (sameYear ? 'MMM d' : 'MMM d, yyyy'))
}

type ProjectCommentCardProps = {
  comment: Comment
  replies: Comment[]
  users: ProjectDetailProps['users']
  viewerId: string
  /** Workspace admins may delete any comment (matches the API). */
  canModerate: boolean
  /** The viewer's explicit choice for this thread; participants follow implicitly. */
  threadState: ThreadSubscription['state'] | null
  onThreadSubscription: (commentId: string, state: ThreadSubscriptionState | null) => Promise<unknown>
  onResolve: (commentId: string, resolved: boolean) => Promise<unknown>
  onNewIssue: (comment: Comment) => void
  onReply: (body: string, bodyData?: Record<string, unknown>) => Promise<unknown>
  onEdit: (commentId: string, body: string, bodyData?: Record<string, unknown>) => Promise<unknown>
  onDelete: (commentId: string) => Promise<void>
  onReact: (commentId: string, emoji: string) => Promise<unknown>
  onUpload: (file: File) => Promise<string>
}

/** A posted project comment rendered as Linear's framed comment card with its reply thread. */
function ProjectCommentCard({ comment, replies, users, viewerId, canModerate, threadState, onThreadSubscription, onResolve, onNewIssue, onReply, onEdit, onDelete, onReact, onUpload }: ProjectCommentCardProps) {
  const [threadOpen, setThreadOpen] = useState(false)
  const [editing, setEditing] = useState<string>()
  const [deleting, setDeleting] = useState<Comment>()
  const [busy, setBusy] = useState(false)
  const [resolving, setResolving] = useState(false)
  const { locale, t } = useI18n()
  // Linear: the author and repliers follow a thread implicitly; "muted" opts
  // a participant out and "subscribed" opts anyone else in.
  const participating = comment.user.id === viewerId || replies.some(reply => reply.user.id === viewerId)
  const following = threadState === 'subscribed' || (participating && threadState !== 'muted')
  const toggleThread = () => {
    const next: ThreadSubscriptionState | null = following ? (participating ? 'muted' : null) : (participating ? null : 'subscribed')
    void onThreadSubscription(comment.id, next).then(() => toast.success(t(following ? 'Unsubscribed from thread' : 'Subscribed to thread')), () => undefined)
  }
  const resolve = async (resolved: boolean) => {
    setResolving(true)
    try { await onResolve(comment.id, resolved) } catch { /* the caller reports the failure */ } finally { setResolving(false) }
  }
  const menu = (item: Comment, iconSize?: number) => <ProjectCommentMenu body={item.body} canDelete={item.user.id === viewerId || canModerate} canEdit={item.user.id === viewerId} commentId={item.id} iconSize={iconSize} resolved={Boolean(comment.resolved)} thread={item.id === comment.id ? { following, onToggle: toggleThread } : undefined} triggerClassName="project-activity__comment-action" onDelete={() => setDeleting(item)} onEdit={() => setEditing(item.id)} onNewIssue={() => onNewIssue(item)} onResolve={item.id === comment.id ? () => void resolve(!comment.resolved) : undefined}/>
  const react = (commentId: string, emoji: string) => onReact(commentId, emoji).then(() => undefined, () => undefined)
  const threadLabel = replies.length ? `Open ${replies.length} ${replies.length === 1 ? 'comment' : 'comments'}` : 'Open comments'
  const confirmDelete = async () => {
    if (!deleting) return
    setBusy(true)
    try { await onDelete(deleting.id); setDeleting(undefined) } catch { /* the caller reports the failure */ } finally { setBusy(false) }
  }
  const body = (item: Comment, className: string) => editing === item.id
    ? <Composer compact initialData={item.bodyData} initialValue={item.body} placeholder="Edit comment…" users={users} onCancel={() => setEditing(undefined)} onSubmit={async (next, data) => { await onEdit(item.id, next, data); setEditing(undefined) }} onUpload={onUpload}/>
    : <div className={className}><RichComment body={item.body} data={item.bodyData} version={item.version}/></div>
  const card = <article className="project-activity__comment-card" data-activity-anchor={`comment-${comment.id}`} data-resolved={comment.resolved || undefined} id={`comment-${comment.id}`}>
    <header>
      <Avatar name={comment.user.displayName}/>
      <strong data-i18n-ignore>{comment.user.displayName}</strong>
      <time data-i18n-ignore dateTime={comment.createdAt} title={format(new Date(comment.createdAt), 'PPpp')}>{commentTimeLabel(comment.createdAt, locale)}</time>
      {comment.editedAt && <span className="project-activity__comment-edited">edited</span>}
      <div className="project-activity__comment-actions">
        <EmojiPicker align="end" onSelect={emoji => react(comment.id, emoji)}><button aria-label="Add reaction" className="project-activity__comment-action" type="button"><SmilePlus size={16}/></button></EmojiPicker>
        {menu(comment, 16)}
      </div>
    </header>
    {body(comment, 'project-activity__comment-body')}
    <ReactionPills reactions={comment.reactions} viewerId={viewerId} onToggle={emoji => react(comment.id, emoji)}/>
    <footer>
      <button aria-expanded={threadOpen} aria-label={threadLabel} className="project-activity__thread-toggle" onClick={() => setThreadOpen(open => !open)} type="button"><MessageCircle size={16}/>{replies.length > 0 && <span>{replies.length}</span>}</button>
    </footer>
    {threadOpen && <div className="project-activity__thread">
      {replies.map(reply => <div className="project-activity__reply" data-activity-anchor={`comment-${reply.id}`} id={`comment-${reply.id}`} key={reply.id}>
        <header><Avatar name={reply.user.displayName}/><strong data-i18n-ignore>{reply.user.displayName}</strong><time data-i18n-ignore dateTime={reply.createdAt} title={format(new Date(reply.createdAt), 'PPpp')}>{commentTimeLabel(reply.createdAt, locale)}</time>{reply.editedAt && <span className="project-activity__comment-edited">edited</span>}<div className="project-activity__comment-actions"><EmojiPicker align="end" onSelect={emoji => react(reply.id, emoji)}><button aria-label="Add reaction" className="project-activity__comment-action" type="button"><SmilePlus size={14}/></button></EmojiPicker>{menu(reply, 14)}</div></header>
        {body(reply, 'project-activity__reply-body')}
        <ReactionPills reactions={reply.reactions} viewerId={viewerId} onToggle={emoji => react(reply.id, emoji)}/>
      </div>)}
      <Composer compact placeholder="Leave a reply…" users={users} onSubmit={async (next, data) => { await onReply(next, data) }} onUpload={onUpload}/>
    </div>}
  </article>
  const dialog = <DeleteCommentDialog busy={busy} open={Boolean(deleting)} onConfirm={() => void confirmDelete()} onOpenChange={open => { if (!open) setDeleting(undefined) }}/>
  // Resolved threads collapse behind the shared resolved-thread bar, like issue comments.
  return comment.resolved
    ? <ResolvedComment busy={resolving} comment={comment} onResolve={resolved => resolve(resolved)}>{card}{dialog}</ResolvedComment>
    : <>{card}{dialog}</>
}

function CommentEntry({ comment }: { comment: Comment }) { return <article className="project-activity__comment"><Avatar name={comment.user.displayName}/><div><header><strong>{comment.user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(comment.createdAt), { addSuffix: true })}</time></header><div className="project-activity__rich"><RichComment body={comment.body} data={comment.bodyData} version={comment.version}/></div></div></article> }
function PropertyEvent({ actor, icon, text, time }: { actor?: string; icon?: ReactNode; text: string; time: string }) {
  // Linear bolds the actor and runs the date straight after the sentence.
  const rest = actor && text.startsWith(`${actor} `) ? text.slice(actor.length) : undefined
  return <div className="project-activity__event">{icon ?? <span className="project-activity__event-dot"/>}<span>{rest !== undefined ? <><strong>{actor}</strong>{rest}</> : text}<time> · {format(new Date(time), 'MMM d')}</time></span></div>
}

type ProjectPropertyEvent = { id: string; icon?: ReactNode; actor?: string; text: string; time: string }
// Linear's project Activity tab lists events without month group headings.
function ActivityPropertyTimeline({ events }: { events: ProjectPropertyEvent[] }) {
  return <>{events.map(event => <PropertyEvent actor={event.actor} icon={event.icon} key={event.id} text={event.text} time={event.time}/>)}</>
}

type ProjectPropertyChange = { field: string; from: string; to: string }
const PROPERTY_LABELS: Record<string, string> = { name: 'Name', status: 'Status', priority: 'Priority', lead: 'Lead', startDate: 'Start date', targetDate: 'Target date' }

function formatPropertyValue(field: string, value: string) {
  if (!value) return field === 'priority' ? 'No priority' : 'None'
  if ((field === 'startDate' || field === 'targetDate') && /^\d{4}-\d{2}-\d{2}$/.test(value)) return format(new Date(`${value}T00:00:00`), 'MMM d')
  return value
}

function historyChanges(entry: AuditLogEntry): ProjectPropertyChange[] {
  const raw = entry.metadata?.changes
  if (!Array.isArray(raw)) return []
  return raw.filter((item): item is ProjectPropertyChange => Boolean(item) && typeof item === 'object' && typeof (item as ProjectPropertyChange).field === 'string').map(item => ({ field: item.field, from: String(item.from ?? ''), to: String(item.to ?? '') }))
}

/** Folds project history into per-update "changes since last update" lists. */
export function projectUpdateChanges(updates: ProjectUpdate[], history: AuditLogEntry[], projectCreatedAt: string) {
  const result = new Map<string, ProjectPropertyChange[]>()
  const entries = history.filter(entry => entry.action === 'updated').sort((left, right) => +new Date(left.createdAt) - +new Date(right.createdAt))
  const ordered = [...updates].sort((left, right) => +new Date(left.createdAt) - +new Date(right.createdAt))
  let since = Date.parse(projectCreatedAt) || 0
  for (const update of ordered) {
    const until = Date.parse(update.createdAt)
    const merged = new Map<string, ProjectPropertyChange>()
    for (const entry of entries) {
      const at = Date.parse(entry.createdAt)
      if (at <= since || at > until) continue
      for (const change of historyChanges(entry)) {
        const previous = merged.get(change.field)
        merged.set(change.field, { field: change.field, from: previous ? previous.from : change.from, to: change.to })
      }
    }
    result.set(update.id, [...merged.values()].filter(change => change.from !== change.to))
    since = until
  }
  return result
}

function buildProjectEvents(activities: ProjectDetailProps['activities'], creation?: { actor: { displayName: string }; createdAt: string; color?: string }): ProjectPropertyEvent[] {
  const events: ProjectPropertyEvent[] = activities.map(event => ({ id: event.id, actor: event.actor.displayName, icon: event.type === 'project.created' ? <ProjectIcon size={16}/> : event.type.includes('initiative') ? <Flag size={16}/> : event.type.includes('date') ? <CalendarIcon size={16}/> : event.type.includes('priority') ? <PriorityIcon priority={Number(event.metadata.priority ?? 0)} size={16}/> : undefined, text: activityLabel(event), time: event.createdAt }))
  // The API does not persist a project.created event, so synthesize the creation entry Linear always shows at the bottom of the feed.
  if (creation && !Number.isNaN(Date.parse(creation.createdAt)) && !activities.some(event => event.type === 'project.created')) events.push({ id: 'project-created', actor: creation.actor.displayName, icon: <ProjectIcon size={16} style={creation.color ? { color: creation.color } : undefined}/>, text: `${creation.actor.displayName} created the project`, time: creation.createdAt })
  return events.sort((left, right) => +new Date(right.time) - +new Date(left.time))
}

function activityLabel(event: ProjectDetailProps['activities'][number]) {
  const actor = event.actor.displayName
  const value = event.metadata.name ?? event.metadata.label ?? event.metadata.status ?? event.metadata.projectName ?? ''
  const labels: Record<string, string> = { 'project.created': 'created the project', 'project.updated': 'updated the project', 'project.commented': 'commented on the project', 'project.update_created': 'posted a project update', 'project.reminder_created': 'created a project reminder', 'project.milestone_created': 'added a milestone', 'project.milestone_updated': 'updated a milestone', 'project.milestone_deleted': 'deleted a milestone' }
  return `${actor} ${labels[event.type] ?? event.type.replaceAll('.', ' ')}${value ? ` ${value}` : ''}`
}

function EditUpdateDialog({ onOpenChange, onSave, open, update }: { onOpenChange: (open: boolean) => void; onSave: (id: string, input: { body?: string; health?: Project['health'] }) => Promise<void>; open: boolean; update?: ProjectUpdate }) {
  const [body, setBody] = useState(update?.body ?? '')
  const [health, setHealth] = useState<Project['health']>(update?.health ?? 'onTrack')
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="project-detail-page__form-dialog project-activity__edit-dialog"><Dialog.Title>Edit update</Dialog.Title><div className="project-activity__edit-health">{PROJECT_HEALTHS.slice(0,3).map(option => <button className={health === option.id ? 'is-active' : ''} key={option.id} onClick={() => setHealth(option.id)} type="button">{option.label}</button>)}</div><textarea autoFocus aria-label="Edit project update" onChange={event => setBody(event.target.value)} value={body}/><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!body.trim()} onClick={() => update && void onSave(update.id, { body: body.trim(), health })} type="button">Save update</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

const HEALTH_TRENDS: Partial<Record<Project['health'], string>> = { onTrack: '4 10 6.8 7 9 9 12 5.8', atRisk: '4.3 8.2 6.7 10.4 11.7 5.6', offTrack: '4 6 6.8 9 9 7 12 10.2' }

function HealthGlyph({ health }: { health: Project['health'] }) {
  const trend = HEALTH_TRENDS[health]
  return <svg aria-hidden="true" className="project-activity__health-icon" height="16" viewBox="0 0 16 16" width="16"><circle cx="8" cy="8" fill="currentColor" fillOpacity={.25} r="8"/>{trend && <polyline fill="none" points={trend} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5"/>}</svg>
}

function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No update' })[value] }
