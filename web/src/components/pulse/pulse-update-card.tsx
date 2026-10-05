import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import type { Editor } from '@tiptap/react'
import { BellOff, BellRing, Copy, FolderKanban, FileText, HelpCircle, Link2, MessageCircle, MoreHorizontal, Pencil, SmilePlus, SquareArrowOutUpRight, Trash2, X } from 'lucide-react'
import { memo, useEffect, useRef, useState } from 'react'
import { formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { RichComment } from '@/components/activity/rich-comment'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { EmojiPicker } from '@/components/reactions/emoji-picker'
import { UserAvatar } from '@/components/ui/user-avatar'
import { FlowTooltip } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { useInitiativeProjectUpdatesSubscription } from '@/lib/pulse-subscriptions'
import type { Comment, Project, PulseItem, User } from '@/types/flow'
import { HEALTH_LABELS, pulseUpdateLink, pulseUpdateMarkdown, userName } from './pulse-card-model'
import { PulseDiffBlock } from './pulse-diff-block'
import { PulseWhyDialog } from './pulse-why-dialog'

export type PulseCardHandlers = {
  onComment: (item: PulseItem, body: string, bodyData?: Record<string, unknown>) => Promise<void>
  onReact: (item: PulseItem, emoji: string) => Promise<void>
  onEdit: (item: PulseItem, input: { body: string; bodyData?: Record<string, unknown>; health: Project['health'] }) => Promise<void>
  onDelete: (item: PulseItem) => Promise<void>
  onDeleteAttachment: (item: PulseItem, attachmentId: string) => Promise<void>
  onToggleSubscription: (item: PulseItem, subscribe: boolean) => Promise<void>
  onToggleTeam: (teamId: string, subscribed: boolean) => Promise<void>
  onNavigate: (path: string) => void
}

type Props = PulseCardHandlers & {
  item: PulseItem
  view: 'following' | 'popular' | 'all'
  viewer: User
  viewerRole: string
  users: User[]
  data: Parameters<typeof PulseWhyDialog>[0]['data']
  /** Shown after unsubscribing on the For me tab. */
  unsubscribed?: boolean
  /** Bumped by the R shortcut on the active card. */
  replyRequest?: number
  /** Focus entered the card (R falls back to the focused card). */
  onActivate?: (itemId: string) => void
  /** The pointer is over / left the card (R replies to the hovered card first). */
  onHoverChange?: (itemId: string, hovered: boolean) => void
}

function commentsLabel(open: boolean, count: number) {
  return open ? 'Close comments' : count ? 'Open comments' : 'Leave a comment'
}

export const PulseUpdateCard = memo(function PulseUpdateCard(props: Props) {
  const { item, viewer } = props
  const { t, formatDate } = useI18n()
  const update = item.update
  const type = item.kind === 'project' ? t('project') : t('initiative')
  const [commentsOpen, setCommentsOpen] = useState(false)
  const [comment, setComment] = useState<DescriptionSnapshot>()
  const [composerKey, setComposerKey] = useState(0)
  const [posting, setPosting] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editBody, setEditBody] = useState<DescriptionSnapshot>()
  const [editHealth, setEditHealth] = useState(update.health)
  const [saving, setSaving] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [whyOpen, setWhyOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  // Initiatives: "Subscribe to initiative's project updates" (every update of its projects).
  const projectUpdates = useInitiativeProjectUpdatesSubscription(item.kind === 'initiative' ? item.source.id : undefined, item.kind === 'initiative' ? item.projectUpdatesSubscribed ?? false : false, menuOpen && item.kind === 'initiative')
  const editorRef = useRef<Editor | null>(null)
  const canEdit = update.user.id === viewer.id
  const canDelete = canEdit || props.viewerRole === 'admin' || props.viewerRole === 'owner'
  const comments = update.comments ?? []
  const reactions = update.reactions ?? {}
  const attachments = update.attachments ?? []
  const created = new Date(update.createdAt)

  // R: "Reply to update" opens the thread and focuses the composer.
  const replyRequest = props.replyRequest
  useEffect(() => {
    if (!replyRequest) return
    setCommentsOpen(true)
    const timer = window.setTimeout(() => editorRef.current?.commands.focus('end'), 30)
    return () => window.clearTimeout(timer)
  }, [replyRequest])

  if (props.unsubscribed) {
    return <article className="pulse-post-card is-unsubscribed" data-pulse-item={item.id}>
      <p>{t('Unsubscribed from updates to the {name} {type}').split(/(\{name\}|\{type\})/).map((part, index) => part === '{name}' ? <strong data-i18n-ignore key={index}>{item.source.name}</strong> : part === '{type}' ? <span key={index}>{type}</span> : <span key={index}>{part}</span>)}</p>
      <button className="pulse-secondary-button" onClick={() => props.onToggleSubscription(item, true).catch(() => undefined)} type="button">{t('Subscribe')}</button>
    </article>
  }

  const submitComment = async () => {
    const body = comment?.markdown.trim()
    if (!body || posting) return
    setPosting(true)
    try {
      await props.onComment(item, body, comment?.document as Record<string, unknown> | undefined)
      setComment(undefined)
      setComposerKey(key => key + 1)
    } catch { /* the app shows the error */ } finally { setPosting(false) }
  }
  const save = async () => {
    const body = editBody?.markdown.trim() ?? update.body.trim()
    if (!body || saving) return
    setSaving(true)
    try {
      await props.onEdit(item, { body, bodyData: editBody?.document as Record<string, unknown> | undefined, health: editHealth })
      setEditing(false)
    } catch { /* the app shows the error */ } finally { setSaving(false) }
  }
  const remove = async () => {
    setDeleting(true)
    try {
      await props.onDelete(item)
      setDeleteOpen(false)
    } catch { /* the app shows the error */ } finally { setDeleting(false) }
  }
  const copy = async (text: string, description: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('Copied to clipboard'), { description: description.replace('{name}', item.source.name) })
    } catch { toast.error(t('Could not copy to clipboard')) }
  }
  const toggleSubscription = async () => {
    const next = !item.subscribed
    await props.onToggleSubscription(item, next)
    toast(next ? t('Subscribed to {type}').replace('{type}', type) : t('Unsubscribed from {type}').replace('{type}', type), {
      description: (next ? t('You will now receive updates for this {type}') : t('You will no longer receive updates for this {type}')).replace('{type}', type),
    })
  }
  const toggleProjectUpdates = async () => {
    const next = !projectUpdates.subscribed
    try {
      const value = await projectUpdates.toggle(next)
      toast(value ? t("Subscribed to initiative's project updates") : t("Unsubscribed from initiative's project updates"), {
        description: value ? t('You will now receive all project updates for this initiative') : t('You will no longer receive all project updates for this initiative'),
      })
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : t('Could not update subscription')) }
  }
  const label = commentsLabel(commentsOpen, comments.length)

  return <article
    className="pulse-post-card"
    data-pulse-item={item.id}
    onFocus={() => props.onActivate?.(item.id)}
    onMouseEnter={() => props.onHoverChange?.(item.id, true)}
    onMouseLeave={() => props.onHoverChange?.(item.id, false)}
    // A card that rendered under a resting cursor gets no mouseenter; the first move claims it.
    onPointerMove={() => props.onHoverChange?.(item.id, true)}
  >
    <header className="pulse-post-header">
      <div className="pulse-post-heading">
        <PulseSourceTitle item={item} onNavigate={props.onNavigate}/>
        <div className="pulse-post-byline">
          <span className={`pulse-post-health is-${update.health}`}><HealthGlyph className="pulse-health-glyph" health={update.health}/>{t(HEALTH_LABELS[update.health])}</span>
          <UserAvatar avatarUrl={update.user.avatarUrl} className="avatar pulse-byline-avatar" name={userName(update.user)}/>
          <span className="pulse-post-author" data-i18n-ignore>{userName(update.user)}</span>
          <FlowTooltip label={formatDate(created, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })}>
            <time dateTime={update.createdAt}>{formatDistanceToNowStrict(created, { addSuffix: true })}{update.editedAt ? ` · ${t('edited')}` : ''}</time>
          </FlowTooltip>
        </div>
      </div>
      <DropdownMenu.Root onOpenChange={setMenuOpen}>
        <DropdownMenu.Trigger asChild><button aria-label={t('Post actions')} className="pulse-post-menu-button" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger>
        <DropdownMenu.Portal><DropdownMenu.Content align="end" className="pulse-menu pulse-post-menu" data-flow-motion="floating" sideOffset={4}>
          <DropdownMenu.Item onSelect={() => props.onNavigate(item.source.url)}><SquareArrowOutUpRight size={14}/>{item.kind === 'project' ? t('Go to update in project') : t('Go to update in initiative')}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => void copy(pulseUpdateLink(item), item.kind === 'project' ? t('The project update URL for {name} is copied to clipboard') : t('The initiative update URL for {name} is copied to clipboard'))}><Link2 size={14}/>{t('Copy link')}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => void copy(pulseUpdateMarkdown(item), item.kind === 'project' ? t('The project update for {name} is copied to clipboard') : t('The initiative update for {name} is copied to clipboard'))}><Copy size={14}/>{t('Copy as markdown')}</DropdownMenu.Item>
          <DropdownMenu.Separator/>
          <DropdownMenu.Item onSelect={() => toggleSubscription().catch(() => undefined)}>{item.subscribed ? <BellOff size={14}/> : <BellRing size={14}/>}{(item.subscribed ? t('Unsubscribe from this {type}') : t('Subscribe to this {type}')).replace('{type}', type)}</DropdownMenu.Item>
          {item.kind === 'initiative' && <DropdownMenu.Item onSelect={() => void toggleProjectUpdates()}>{projectUpdates.subscribed ? <BellOff size={14}/> : <FolderKanban size={14}/>}{projectUpdates.subscribed ? t("Unsubscribe from initiative's project updates") : t("Subscribe to initiative's project updates")}</DropdownMenu.Item>}
          {props.view === 'following' && item.reasons.length > 0 && <DropdownMenu.Item onSelect={() => setWhyOpen(true)}><HelpCircle size={14}/>{t('Why am I seeing this?')}</DropdownMenu.Item>}
          {(canEdit || canDelete) && <DropdownMenu.Separator/>}
          {canEdit && <DropdownMenu.Item onSelect={() => { setEditBody(undefined); setEditHealth(update.health); setEditing(true) }}><Pencil size={14}/>{t('Edit…')}</DropdownMenu.Item>}
          {canDelete && <DropdownMenu.Item className="is-danger" onSelect={() => setDeleteOpen(true)}><Trash2 size={14}/>{t('Delete')}</DropdownMenu.Item>}
        </DropdownMenu.Content></DropdownMenu.Portal>
      </DropdownMenu.Root>
    </header>
    {editing ? <div className="pulse-post-editor">
      <div className="pulse-inline-health">{(['onTrack', 'atRisk', 'offTrack'] as Project['health'][]).map(value => <button aria-pressed={editHealth === value} className={editHealth === value ? 'is-active' : ''} key={value} onClick={() => setEditHealth(value)} type="button"><HealthGlyph className={`pulse-health-glyph is-${value}`} health={value}/>{t(HEALTH_LABELS[value])}</button>)}</div>
      <IssueDescriptionEditor ariaLabel={t('Update body')} className="pulse-post-rich-editor" onChange={setEditBody} onSubmit={() => void save()} placeholder={t('Write an update…')} state={editBody?.documentJSON ?? (update.bodyData ? JSON.stringify(update.bodyData) : undefined)} users={props.users} value={editBody?.markdown ?? update.body}/>
      <footer><button onClick={() => setEditing(false)} type="button">{t('Cancel')}</button><button className="is-primary" disabled={!(editBody?.markdown ?? update.body).trim() || saving} onClick={() => void save()} type="button">{saving ? t('Saving…') : t('Save update')}</button></footer>
    </div> : <div className="pulse-post-body"><RichComment body={update.body} data={update.bodyData}/></div>}
    <PulseDiffBlock diff={item.diff ?? update.diff} kind={item.kind}/>
    {attachments.length > 0 && <div className="pulse-post-attachments">{attachments.map(attachment => <span key={attachment.id}><a href={attachment.url} rel="noreferrer" target="_blank"><FileText size={13}/><b data-i18n-ignore>{attachment.title}</b></a>{canEdit && <button aria-label={t('Remove attachment')} onClick={() => props.onDeleteAttachment(item, attachment.id).catch(() => undefined)} type="button"><X size={11}/></button>}</span>)}</div>}
    <footer className="pulse-post-footer">
      <div className="pulse-post-reactions">
        {Object.entries(reactions).filter(([, userIds]) => userIds.length).map(([emoji, userIds]) => <button aria-pressed={userIds.includes(viewer.id)} key={emoji} onClick={() => props.onReact(item, emoji).catch(() => undefined)} type="button"><span>{emoji}</span>{userIds.length}</button>)}
        <EmojiPicker align="start" onSelect={emoji => props.onReact(item, emoji).catch(() => undefined)}><button aria-label={t('Add reaction')} className="pulse-footer-button" type="button"><SmilePlus size={14}/></button></EmojiPicker>
      </div>
      <FlowTooltip label={t(label)} shortcut="R">
        <button aria-expanded={commentsOpen} aria-label={t(label)} className="pulse-footer-button" onClick={() => setCommentsOpen(value => !value)} type="button"><MessageCircle size={14}/>{comments.length > 0 && <span>{comments.length}</span>}</button>
      </FlowTooltip>
    </footer>
    {commentsOpen && <section aria-label={t('Comments')} className="pulse-post-comments">
      {comments.map(entry => <PulseComment comment={entry} key={entry.id}/>)}
      <div className="pulse-post-comment-box">
        <UserAvatar avatarUrl={viewer.avatarUrl} className="avatar pulse-byline-avatar" name={userName(viewer)}/>
        <div className="pulse-post-comment-editor">
          <IssueDescriptionEditor ariaLabel={t('Leave a comment…')} className="pulse-post-comment-rich-editor" editorRef={editor => { editorRef.current = editor }} key={composerKey} onChange={setComment} onSubmit={() => void submitComment()} placeholder={t('Leave a comment…')} state={comment?.documentJSON} users={props.users} value={comment?.markdown ?? ''}/>
          <button className="pulse-post-comment-submit" disabled={!comment?.markdown.trim() || posting} onClick={() => void submitComment()} type="button">{posting ? t('Sending…') : t('Comment')}</button>
        </div>
      </div>
    </section>}
    <Dialog.Root onOpenChange={setDeleteOpen} open={deleteOpen}><Dialog.Portal><Dialog.Overlay className="pulse-dialog-overlay is-delete" data-flow-motion="backdrop"/><Dialog.Content aria-describedby={undefined} className="pulse-delete-dialog" data-flow-motion="dialog">
      <Dialog.Title>{item.kind === 'project' ? t('Delete this project update?') : t('Delete this initiative update?')}</Dialog.Title>
      {comments.length > 0 && <p>{t('This will delete the update, along with all comments.')}</p>}
      <footer><Dialog.Close asChild><button disabled={deleting} type="button">{t('Cancel')}</button></Dialog.Close><button className="is-danger" disabled={deleting} onClick={() => void remove()} type="button">{deleting ? t('Deleting…') : t('Delete')}</button></footer>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
    {whyOpen && <PulseWhyDialog data={props.data} item={item} onOpenChange={setWhyOpen} onToggleSubscription={() => toggleSubscription().catch(() => undefined)} onToggleTeam={(teamId, subscribed) => props.onToggleTeam(teamId, subscribed).catch(() => undefined)} open={whyOpen}/>}
  </article>
})

function PulseComment({ comment }: { comment: Comment }) {
  const { formatDate } = useI18n()
  const created = new Date(comment.createdAt)
  return <article className="pulse-post-comment">
    <UserAvatar avatarUrl={comment.user.avatarUrl} className="avatar pulse-byline-avatar" name={userName(comment.user)}/>
    <div>
      <header><strong data-i18n-ignore>{userName(comment.user)}</strong><time dateTime={comment.createdAt} title={formatDate(created, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })}>{formatDistanceToNowStrict(created, { addSuffix: true })}</time></header>
      <div className="pulse-post-comment-body"><RichComment body={comment.body} data={comment.bodyData} version={comment.version}/></div>
    </div>
  </article>
}

/** Project / initiative name with Linear's hover card. */
function PulseSourceTitle({ item, onNavigate }: { item: PulseItem; onNavigate: (path: string) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const timer = useRef<number | undefined>(undefined)
  const schedule = (next: boolean) => {
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setOpen(next), next ? 450 : 120)
  }
  useEffect(() => () => window.clearTimeout(timer.current), [])
  const icon = item.source.icon || (item.kind === 'project' ? 'Project' : 'Initiative')
  return <Popover.Root onOpenChange={setOpen} open={open}>
    <Popover.Anchor asChild>
      <a className="pulse-post-title" href={item.source.url} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); onNavigate(item.source.url) }} onPointerEnter={() => schedule(true)} onPointerLeave={() => schedule(false)}>
        <ViewGlyph color={item.source.color} icon={icon}/><span data-i18n-ignore>{item.source.name}</span>
      </a>
    </Popover.Anchor>
    <Popover.Portal><Popover.Content align="start" className="pulse-hover-card" data-flow-motion="floating" onOpenAutoFocus={event => event.preventDefault()} onPointerEnter={() => schedule(true)} onPointerLeave={() => schedule(false)} sideOffset={6}>
      <div className="pulse-hover-card-title"><ViewGlyph color={item.source.color} icon={icon}/><strong data-i18n-ignore>{item.source.name}</strong></div>
      <div className="pulse-hover-card-meta"><HealthGlyph className={`pulse-health-glyph is-${item.update.health}`} health={item.update.health}/><span>{t(HEALTH_LABELS[item.update.health])}</span><span>·</span><span>{item.kind === 'project' ? t('Project') : t('Initiative')}</span></div>
      <button className="pulse-link-button" onClick={() => { setOpen(false); onNavigate(item.source.url) }} type="button">{item.kind === 'project' ? t('Open project') : t('Open initiative')}</button>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}
