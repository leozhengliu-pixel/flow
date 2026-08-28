import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, Copy, MessageCircle, MoreHorizontal, Pencil, SmilePlus, SquarePen, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState, type KeyboardEvent, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { toast } from 'sonner'
import { NoAssigneeIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { EmojiPicker } from '@/components/reactions/emoji-picker'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import { useCommentComposer } from '@/hooks/use-comment-composer'
import type { Project, ProjectUpdate, User } from '@/types/flow'

const HEALTHS: { id: Project['health']; label: string }[] = [{ id: 'onTrack', label: 'On track' }, { id: 'atRisk', label: 'At risk' }, { id: 'offTrack', label: 'Off track' }]

export function ProjectUpdatesPreview({ onClose, onComment, onCreate, onDelete, onOpenProject, onReact, onUpdate, project, updates, viewer }: {
  onClose: () => void
  onComment?: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onCreate?: (projectId: string, input: { body: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onDelete?: (projectId: string, updateId: string) => Promise<void>
  onOpenProject?: (project: Project) => void
  onReact?: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onUpdate?: (projectId: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  project: Project
  updates: ProjectUpdate[]
  viewer?: User
}) {
  const ref = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const reactionRef = useRef<HTMLDivElement>(null)
  const deleteButtonRef = useRef<HTMLButtonElement>(null)
  const [subscribed, setSubscribed] = useStoredBoolean(`flow:project:${project.id}:subscribed`, true)
  const [mode, setMode] = useState<'list' | 'create' | 'edit'>('list')
  const [editingId, setEditingId] = useState<string>()
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>(project.health === 'noUpdate' ? 'onTrack' : project.health)
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ProjectUpdate>()
  const [deleting, setDeleting] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  useDismissibleLayer({ open: !deleteTarget, refs: [ref, menuRef, reactionRef], onDismiss: onClose })
  useEffect(() => ref.current?.focus(), [])
  useEffect(() => setActiveIndex(index => Math.min(index, Math.max(0, updates.length - 1))), [updates.length])

  const beginCreate = () => {
    setEditingId(undefined)
    setBody('')
    setHealth(project.health === 'noUpdate' ? 'onTrack' : project.health)
    setMode('create')
  }
  const beginEdit = (update: ProjectUpdate) => {
    setEditingId(update.id)
    setBody(update.body)
    setHealth(update.health)
    setMode('edit')
  }
  const submit = async () => {
    if (!body.trim() || saving) return
    if (mode === 'create' && !onCreate) return
    if (mode === 'edit' && (!onUpdate || !editingId)) return
    setSaving(true)
    try {
      if (mode === 'edit') await onUpdate!(project.id, editingId!, { body: body.trim(), health })
      else await onCreate!(project.id, { body: body.trim(), health })
      setBody('')
      setMode('list')
    } catch (error) {
      toast.error(mode === 'edit' ? 'Could not edit project update' : 'Could not post project update', { description: error instanceof Error ? error.message : undefined })
    } finally {
      setSaving(false)
    }
  }
  const confirmDelete = async () => {
    if (!deleteTarget || !onDelete || deleting) return
    setDeleting(true)
    try {
      await onDelete(project.id, deleteTarget.id)
      setDeleteTarget(undefined)
    } catch (error) {
      toast.error('Could not delete project update', { description: error instanceof Error ? error.message : undefined })
    } finally {
      setDeleting(false)
    }
  }
  const navigateUpdates = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!event.shiftKey || !['ArrowDown', 'ArrowUp'].includes(event.key) || !updates.length || mode !== 'list') return
    event.preventDefault()
    const next = (activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + updates.length) % updates.length
    setActiveIndex(next)
    ref.current?.querySelector<HTMLElement>(`[data-update-index="${next}"]`)?.scrollIntoView({ block: 'nearest' })
  }

  return createPortal(<>
    <div aria-label={`${project.name} project updates`} className="lp-project-updates-preview" onKeyDown={navigateUpdates} ref={ref} role="dialog" tabIndex={-1}>
      <header>
        <button className="lp-project-updates-preview__title" onClick={() => onOpenProject?.(project)} type="button"><ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)}/><strong>{project.name}</strong></button>
        <button className="lp-project-updates-preview__subscribe" onClick={() => setSubscribed(value => !value)} type="button">{subscribed && <Check size={13}/>}<span>{subscribed ? 'Subscribed' : 'Subscribe'}</span></button>
        <button aria-label="Write project update" className="lp-project-updates-preview__new" onClick={beginCreate} type="button"><SquarePen size={13}/><span>New update</span></button>
      </header>
      <div className="lp-project-updates-preview__body">
        {mode !== 'list' && <ProjectUpdateComposer body={body} health={health} mode={mode} saving={saving} setBody={setBody} setHealth={setHealth} onCancel={() => setMode('list')} onSubmit={() => void submit()} viewer={viewer}/>} 
        {mode === 'list' && updates.length === 0 && <ProjectUpdatesEmptyState onCreate={beginCreate}/>} 
        {mode === 'list' && updates.map((update, index) => <ProjectUpdateArticle active={activeIndex === index} dataIndex={index} key={update.id} menuRef={menuRef} onComment={onComment} onDelete={() => setDeleteTarget(update)} onEdit={() => beginEdit(update)} onReact={onReact} project={project} reactionRef={reactionRef} update={update} viewer={viewer}/>)}
      </div>
      <footer className="lp-project-updates-preview__hint"><kbd>Shift</kbd><kbd>↑</kbd><span>or</span><kbd>↓</kbd><span>to navigate</span></footer>
    </div>
    <Dialog.Root open={Boolean(deleteTarget)} onOpenChange={open => { if (!open && !deleting) setDeleteTarget(undefined) }}>
      <Dialog.Portal>
        <Dialog.Overlay className="lp-project-update-delete__overlay"/>
        <Dialog.Content aria-describedby={undefined} aria-label="Delete this project update?" className="lp-project-update-delete" onOpenAutoFocus={event => { event.preventDefault(); deleteButtonRef.current?.focus() }}>
          <Dialog.Title>Delete this project update?</Dialog.Title>
          <footer><Dialog.Close asChild><button disabled={deleting} type="button">Cancel</button></Dialog.Close><button className="is-danger" disabled={deleting} onClick={() => void confirmDelete()} ref={deleteButtonRef} type="button">{deleting ? 'Deleting…' : 'Delete'}</button></footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </>, document.body)
}

function ProjectUpdatesEmptyState({ onCreate }: { onCreate: () => void }) {
  return <section className="lp-project-updates-preview__empty">
    <NoProjectUpdatesIllustration/>
    <div className="lp-project-updates-preview__empty-copy"><strong>Project updates</strong><p>Write a short status report to keep everyone up-to-date on the progress and health of this project</p></div>
    <div className="lp-project-updates-preview__empty-actions"><button aria-label="Write project update" onClick={onCreate} type="button">New project update<span className="lp-project-updates-preview__sequence"><kbd>N</kbd><span>then</span><kbd>U</kbd></span></button><a href="https://flow.app/docs/initiative-and-project-updates" rel="noreferrer" target="_blank">Documentation</a></div>
  </section>
}

function ProjectUpdateComposer({ body, health, mode, onCancel, onSubmit, saving, setBody, setHealth, viewer }: {
  body: string
  health: Project['health']
  mode: 'create' | 'edit'
  onCancel: () => void
  onSubmit: () => void
  saving: boolean
  setBody: (value: string) => void
  setHealth: (value: Project['health']) => void
  viewer?: User
}) {
  return <section className="lp-project-updates-preview__composer">
    <header><strong>{mode === 'edit' ? 'Edit update' : 'New update'}</strong><div className="lp-project-updates-preview__healths">{HEALTHS.map(option => <button className={health === option.id ? 'is-active' : ''} key={option.id} onClick={() => setHealth(option.id)} type="button"><i className={`is-${option.id}`}/>{option.label}</button>)}</div></header>
    <textarea autoFocus aria-label="Project update" onChange={event => setBody(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); onSubmit() } }} placeholder="Write a project update…" value={body}/>
    <footer><span>{viewer?.displayName ?? 'Project update'}</span><div><button onClick={onCancel} type="button">Cancel</button><button className="is-primary" disabled={!body.trim() || saving} onClick={onSubmit} type="button">{saving ? 'Saving…' : mode === 'edit' ? 'Save update' : 'Post update'}</button></div></footer>
  </section>
}

function ProjectUpdateArticle({ active, dataIndex, menuRef, onComment, onDelete, onEdit, onReact, project, reactionRef, update, viewer }: { active: boolean; dataIndex: number; menuRef: RefObject<HTMLDivElement | null>; onComment?: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>; onDelete: () => void; onEdit: () => void; onReact?: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>; project: Project; reactionRef: RefObject<HTMLDivElement | null>; update: ProjectUpdate; viewer?: User }) {
  const canModify = !viewer || viewer.id === update.user.id
  const [commentsOpen, setCommentsOpen] = useState(false)
  const {comment,posting,setComment,submitComment}=useCommentComposer(body=>onComment ? onComment(project.id,update.id,body) : Promise.reject(new Error('Comments are unavailable')))
  const comments = update.comments ?? []
  const reactions = update.reactions ?? {}
  return <article className={`lp-project-updates-preview__item ${active ? 'is-active' : ''}`} data-update-index={dataIndex}>
    <header>
      <span className={`lp-project-updates-preview__health is-${update.health}`}><i/>{healthLabel(update.health)}</span>
      <UpdateAuthorAvatar user={update.user}/><strong>{update.user.displayName}</strong><time title={new Date(update.createdAt).toLocaleString()}>{formatRelative(update.createdAt)}{update.editedAt ? ' · edited' : ''}</time>
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className="lp-project-updates-preview__item-menu" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="lp-project-update-menu" ref={menuRef} sideOffset={4}>
        <DropdownMenu.Item onSelect={() => void copyUpdateLink(project, update)}><Copy size={14}/><span>Copy link</span></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => void navigator.clipboard?.writeText(`**${healthLabel(update.health)}** — ${update.body}`)}><Copy size={14}/><span>Copy as markdown</span></DropdownMenu.Item>
        {canModify && <><DropdownMenu.Separator/><DropdownMenu.Item onSelect={onEdit}><Pencil size={14}/><span>Edit</span></DropdownMenu.Item><DropdownMenu.Item className="is-danger" onSelect={onDelete}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></>}
      </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    </header>
    <p>{update.body}</p>
    <ProjectUpdateMetadata project={project}/>
    <div className="lp-project-update-reactions">{Object.entries(reactions).map(([emoji, userIds]) => <button aria-pressed={Boolean(viewer && userIds.includes(viewer.id))} key={emoji} onClick={() => onReact && void onReact(project.id, update.id, emoji)} type="button"><span>{emoji}</span>{userIds.length}</button>)}</div>
    <footer><button aria-expanded={commentsOpen} aria-label={`${comments.length} comments`} onClick={() => setCommentsOpen(value => !value)} type="button"><MessageCircle size={14}/>{comments.length > 0 && <span>{comments.length}</span>}</button><EmojiPicker align="start" contentRef={reactionRef} onSelect={async emoji => { await onReact?.(project.id, update.id, emoji) }}><button aria-label="Add reaction" type="button"><SmilePlus size={14}/></button></EmojiPicker></footer>
    {commentsOpen && <section className="lp-project-update-comments">
      {comments.map(item => <article key={item.id}><UpdateAuthorAvatar user={item.user}/><div><header><strong>{item.user.displayName}</strong><time>{formatRelative(item.createdAt)}</time></header><p>{item.body}</p></div></article>)}
      <div className="lp-project-update-comment-box"><UpdateAuthorAvatar user={viewer ?? update.user}/><textarea aria-label="Add comment" onChange={event => setComment(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submitComment() } }} placeholder="Leave a comment…" value={comment}/><button disabled={!comment.trim() || posting || !onComment} onClick={() => void submitComment()} type="button">{posting ? 'Sending…' : 'Comment'}</button></div>
    </section>}
  </article>
}

function ProjectUpdateMetadata({ project }: { project: Project }) {
  return <div className="lp-project-updates-preview__metadata">
    <div><span>Priority</span><PriorityIcon priority={project.priority} size={14}/><strong>{project.priorityLabel}</strong></div>
    <div><span>Lead</span>{project.lead ? <><UpdateAuthorAvatar user={project.lead}/><strong>{project.lead.displayName} assigned</strong></> : <><NoAssigneeIcon size={14}/><strong>No lead</strong></>}</div>
    {project.startDate && <div><span>Start date</span><strong>set to {formatLongDate(project.startDate)}</strong></div>}
  </div>
}

function NoProjectUpdatesIllustration() {
  return <svg aria-label="No project updates illustration" className="lp-project-updates-preview__illustration" fill="none" viewBox="0 0 119 68"><path d="M108.141 43.127 22.635 66.453c-2.237.61-4.906.435-7.408-.379-2.503-.813-4.763-2.242-6.212-4.051l-5.96-7.443c-.881-1.099-1.305-2.224-1.305-3.233v-3.773c0-.093.053-.3.49-.418l89.608-24.289c1.878-.509 4.116-.392 6.25.253 2.131.644 4.095 1.797 5.44 3.287l8.212 9.097v3.782c0 1.699-1.227 3.19-3.609 3.84Z"/><path d="M3.107 50.575C.34 47.14 1.55 43.428 5.812 42.28l85.212-22.939c4.333-1.166 10.137.766 12.86 4.281l6.546 8.45c2.639 3.406 1.395 7.03-2.799 8.164L22.687 63.18c-4.27 1.154-9.99-.706-12.766-4.15l-6.814-8.456Z"/><path d="M6.054 45.578c-.88-1.099-1.304-2.224-1.304-3.233v-3.773c0-.094.053-.3.49-.418l89.608-24.289c1.878-.509 4.116-.392 6.249.253 2.132.644 4.096 1.797 5.441 3.287l8.212 9.097V30.284c0 1.699-1.227 3.19-3.609 3.84L25.635 57.452c-2.237.61-4.906.435-7.409-.378-2.502-.814-4.762-2.243-6.212-4.052l-5.96-7.443Z"/><path d="M6.108 41.573c-2.769-3.435-1.558-7.147 2.704-8.295L94.024 10.34c4.333-1.166 10.137.766 12.861 4.281l6.546 8.45c2.638 3.406 1.395 7.03-2.8 8.164L25.687 54.179c-4.27 1.154-9.99-.706-12.766-4.15l-6.813-8.456Z"/><path d="M9.054 36.58c-.88-1.1-1.304-2.225-1.304-3.234v-3.773c0-.093.053-.3.49-.418L97.848 4.866c1.878-.509 4.116-.392 6.249.253 2.132.644 4.095 1.797 5.441 3.287l8.212 9.097V21.285c0 1.699-1.227 3.19-3.609 3.84L28.635 48.453c-2.238.61-4.907.435-7.409-.378-2.502-.814-4.763-2.243-6.212-4.052l-5.96-7.443Z"/><path d="M9.107 32.574c-2.768-3.435-1.558-7.147 2.705-8.295L97.023 1.34c4.333-1.166 10.138.766 12.861 4.282l6.546 8.45c2.638 3.405 1.395 7.03-2.8 8.163L28.686 45.18c-4.27 1.154-9.99-.706-12.765-4.15l-6.814-8.456Z"/><path className="is-detail" d="m17.204 29.843.24-.436.48-.872 1.999.64.48-.872.24-.436"/><path className="is-detail" d="M14.215 28.705c-.108-.51.191-.943.72-1.227a8.89 8.89 0 0 1 2.07-.79c.881-.217 1.792-.3 2.482-.328.752-.03 1.574.127 2.247.498.449.249.947.582 1.305.986.357.404.522.82.594 1.16.109.51-.19.943-.72 1.227a8.89 8.89 0 0 1-2.07.79c-.881.217-1.791.3-2.482.328-.752.03-1.574-.127-2.247-.498-.449-.248-.947-.582-1.305-.986a2.532 2.532 0 0 1-.594-1.16Z"/><rect height="1.592" rx=".796" transform="matrix(.96562 -.25994 .62836 .77792 17.712 35.129)" width="91.639"/><rect height="1.592" rx=".796" transform="matrix(.96562 -.25994 .62836 .77792 21.713 40.082)" width="22.771"/></svg>
}

function UpdateAuthorAvatar({ user }: { user: User }) {
  if (user.avatarUrl) return <img alt="" className="lp-project-updates-preview__avatar" src={user.avatarUrl}/>
  if (!user.displayName) return <NoAssigneeIcon size={16}/>
  return <span className="lp-project-updates-preview__avatar">{user.displayName.split(/\s|@/).filter(Boolean).slice(0, 2).map(part => part[0]?.toUpperCase()).join('')}</span>
}
function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No update' })[value] }
function formatRelative(value: string) { const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000)); if (seconds < 60) return 'just now'; if (seconds < 3600) return `${Math.floor(seconds / 60)}m`; if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`; return new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' }).format(new Date(value)) }
function formatLongDate(value: string) { return new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' }).format(new Date(`${value}T00:00:00`)) }
function copyUpdateLink(project: Project, update: ProjectUpdate) { const workspace = location.pathname.split('/')[1] || 'cleantrack'; return navigator.clipboard?.writeText(`${location.origin}/${workspace}/project/${project.slugId}/activity#${update.id}`) }
function useStoredBoolean(key: string, fallback: boolean) {
  const [value, setValue] = useState(() => localStorage.getItem(key) === null ? fallback : localStorage.getItem(key) === 'true')
  const update = (next: boolean | ((current: boolean) => boolean)) => setValue(current => { const resolved = typeof next === 'function' ? next(current) : next; localStorage.setItem(key, String(resolved)); return resolved })
  return [value, update] as const
}
