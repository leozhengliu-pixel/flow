import { useResetOnChange } from '@/hooks/use-reset-on-change'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import type { Editor } from '@tiptap/react'
import { ArrowUp, BellOff, BellRing, Copy, FileText, HelpCircle, Link2, MessageCircle, MoreHorizontal, Paperclip, SmilePlus, SquareArrowOutUpRight } from 'lucide-react'
import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { formatDistanceToNowStrict } from 'date-fns'
import { toast } from '@/lib/toast'
import { liveActorUser } from '@/lib/actor-attribution'
import { RichComment } from '@/components/activity/rich-comment'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { insertPickedFiles } from '@/components/issue/editor/insert-picked-files'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { AddReactionIcon } from '@/components/issue/issue-icons'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { EmojiPicker } from '@/components/reactions/emoji-picker'
import { UserAvatar } from '@/components/ui/user-avatar'
import { FlowTooltip } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { useInitiativeProjectUpdatesSubscription } from '@/lib/pulse-subscriptions'
import type { Comment, PulseItem, User } from '@/types/flow'
import { HEALTH_LABELS, PULSE_HEALTH_LABELS, pulseUpdateLink, pulseUpdateMarkdown, userName } from './pulse-card-model'
import { PulseDiffBlock } from './pulse-diff-block'
import { PulseWhyDialog } from './pulse-why-dialog'

export type PulseCardHandlers = {
  onComment: (item: PulseItem, body: string, bodyData?: Record<string, unknown>) => Promise<void>
  /** Uploads a file for the comment box and returns the URL embedded in the comment body. */
  onUploadCommentAttachment?: (item: PulseItem, file: File) => Promise<string>
  onReact: (item: PulseItem, emoji: string) => Promise<void>
  onToggleSubscription: (item: PulseItem, subscribe: boolean) => Promise<void>
  onToggleTeam: (teamId: string, subscribed: boolean) => Promise<void>
  onNavigate: (path: string) => void
}

type Props = PulseCardHandlers & {
  item: PulseItem
  view: 'following' | 'popular' | 'all'
  viewer: User
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

/** "just now" for the first minute, then "2 minutes ago". */
function relativeTime(date: Date, now: string) {
  return Date.now() - date.getTime() < 60_000 ? now : formatDistanceToNowStrict(date, { addSuffix: true })
}

function commentsLabel(open: boolean, count: number) {
  return open ? 'Close comments' : count ? 'Open comments' : 'Leave a comment'
}

export const PulseUpdateCard = memo(function PulseUpdateCard(props: Props) {
  const { item, viewer } = props
  const { t, formatDate } = useI18n()
  const phone = usePhoneLayout()
  const update = item.update
  const author = liveActorUser(update.user)
  const type = item.kind === 'project' ? t('project') : t('initiative')
  const [commentsOpen, setCommentsOpen] = useState(false)
  const [comment, setComment] = useState<DescriptionSnapshot>()
  const [composerKey, setComposerKey] = useState(0)
  const [posting, setPosting] = useState(false)
  const [whyOpen, setWhyOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  // Initiatives: "Subscribe to initiative's project updates" (every update of its projects).
  const projectUpdates = useInitiativeProjectUpdatesSubscription(item.kind === 'initiative' ? item.source.id : undefined, item.kind === 'initiative' ? item.projectUpdatesSubscribed ?? false : false, menuOpen && item.kind === 'initiative')
  const editorRef = useRef<Editor | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadComment = props.onUploadCommentAttachment
  const uploadMedia = useMemo(() => uploadComment ? (file: File) => uploadComment(item, file) : undefined, [item, uploadComment])
  const attachFiles = (files: File[]) => {
    const editor = editorRef.current
    if (!editor || !files.length) return
    editor.commands.focus('end')
    insertPickedFiles(editor.view, files, uploadMedia)
  }
  const comments = update.comments ?? []
  const reactions = update.reactions ?? {}
  const attachments = update.attachments ?? []
  const created = new Date(update.createdAt)

  // R: "Reply to update" opens the thread and focuses the composer.
  const replyRequest = props.replyRequest
  useResetOnChange([replyRequest], () => { if (replyRequest) setCommentsOpen(true) }, { runOnMount: true })
  useEffect(() => {
    if (!replyRequest) return
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
        {/* The whole status line is the link to the update (the reference app wraps it in one anchor). */}
        <a className="pulse-post-byline" href={pulseUpdateLink(item)} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); props.onNavigate(`${item.source.url}#update-${update.id}`) }}>
          <span className={`pulse-post-health is-${update.health}`}>{phone && update.health === 'onTrack' ? <PulsePhoneHealthGlyph/> : <HealthGlyph className="pulse-health-glyph" health={update.health}/>}<span>{t(PULSE_HEALTH_LABELS[item.kind][update.health])}</span></span>
          <UserAvatar avatarUrl={author.avatarUrl} className="avatar pulse-byline-avatar" name={userName(author)}/>
          <span className="pulse-post-author" data-i18n-ignore>{userName(author)}</span>
          <FlowTooltip label={formatDate(created, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })}>
            <time dateTime={update.createdAt}>{relativeTime(created, t('just now'))}{update.editedAt ? ` · ${t('edited')}` : ''}</time>
          </FlowTooltip>
        </a>
      </div>
      <DropdownMenu.Root onOpenChange={setMenuOpen}>
        <DropdownMenu.Trigger asChild><button aria-label={t('Post actions')} className="pulse-post-menu-button" type="button">{phone ? <FlowGlyph name="ellipsis" size={14}/> : <MoreHorizontal size={14}/>}</button></DropdownMenu.Trigger>
        <DropdownMenu.Portal><DropdownMenu.Content align="end" className="pulse-menu pulse-post-menu" data-flow-motion="floating" sideOffset={4}>
          <DropdownMenu.Item onSelect={() => props.onNavigate(item.source.url)}>{item.kind === 'initiative' ? <ViewGlyph color="currentColor" icon="Initiative" style={{ width: 14, height: 14, flexShrink: 0 }}/> : <SquareArrowOutUpRight size={14}/>}{item.kind === 'project' ? t('Go to update in project') : t('Go to update in initiative')}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => void copy(pulseUpdateLink(item), item.kind === 'project' ? t('The project update URL for {name} is copied to clipboard') : t('The initiative update URL for {name} is copied to clipboard'))}><Link2 size={14}/>{t('Copy link')}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => void copy(pulseUpdateMarkdown(item), item.kind === 'project' ? t('The project update for {name} is copied to clipboard') : t('The initiative update for {name} is copied to clipboard'))}><Copy size={14}/>{t('Copy as markdown')}</DropdownMenu.Item>
          <DropdownMenu.Separator/>
          <DropdownMenu.Item onSelect={() => toggleSubscription().catch(() => undefined)}>{item.subscribed ? <BellOff size={14}/> : <BellRing size={14}/>}{(item.subscribed ? t('Unsubscribe from this {type}') : t('Subscribe to this {type}')).replace('{type}', type)}</DropdownMenu.Item>
          {item.kind === 'initiative' && <DropdownMenu.Item onSelect={() => void toggleProjectUpdates()}>{projectUpdates.subscribed ? <BellOff size={14}/> : <BellRing size={14}/>}{projectUpdates.subscribed ? t("Unsubscribe from initiative's project updates") : t("Subscribe to initiative's project updates")}</DropdownMenu.Item>}
          {props.view === 'following' && item.reasons.length > 0 && <DropdownMenu.Item onSelect={() => setWhyOpen(true)}><HelpCircle size={14}/>{t('Why am I seeing this?')}</DropdownMenu.Item>}
        </DropdownMenu.Content></DropdownMenu.Portal>
      </DropdownMenu.Root>
    </header>
    <div className="pulse-post-body"><RichComment body={update.body} data={update.bodyData}/></div>
    <PulseDiffBlock diff={item.diff ?? update.diff} kind={item.kind}/>
    {attachments.length > 0 && <div className="pulse-post-attachments">{attachments.map(attachment => <span key={attachment.id}><a href={attachment.url} rel="noreferrer" target="_blank"><FileText size={13}/><b data-i18n-ignore>{attachment.title}</b></a></span>)}</div>}
    <footer className="pulse-post-footer">
      <FlowTooltip label={t(label)} shortcut="R">
        <button aria-expanded={commentsOpen} aria-label={t(label)} className="pulse-footer-button" onClick={() => setCommentsOpen(value => !value)} type="button"><MessageCircle size={16}/>{comments.length > 0 && <span>{comments.length}</span>}</button>
      </FlowTooltip>
      <div className="pulse-post-reactions">
        {Object.entries(reactions).filter(([, userIds]) => userIds.length).map(([emoji, userIds]) => <button aria-pressed={userIds.includes(viewer.id)} key={emoji} onClick={() => props.onReact(item, emoji).catch(() => undefined)} type="button"><span>{emoji}</span>{userIds.length}</button>)}
        <EmojiPicker align="start" onSelect={emoji => props.onReact(item, emoji).catch(() => undefined)}><button aria-label={t('Add reaction')} className="pulse-footer-button" type="button">{phone ? <AddReactionIcon size={14}/> : <SmilePlus size={14}/>}</button></EmojiPicker>
      </div>
    </footer>
    {commentsOpen && <section aria-label={t('Comments')} className="pulse-post-comments">
      {comments.map(entry => <PulseComment comment={entry} key={entry.id}/>)}
      <div className="pulse-post-comment-box">
        <div className="pulse-post-comment-editor">
          <IssueDescriptionEditor ariaLabel={t('Leave a comment…')} className="pulse-post-comment-rich-editor" editorRef={editor => { editorRef.current = editor }} key={composerKey} onChange={setComment} onInsertImage={uploadMedia} onSubmit={() => void submitComment()} placeholder={t('Leave a comment…')} state={comment?.documentJSON} users={props.users} value={comment?.markdown ?? ''}/>
          <div className="pulse-post-comment-actions">
            {uploadMedia && <>
              <FlowTooltip label={t('Attach images, files, or videos')}><button aria-label={t('Attach images, files, or videos')} className="pulse-post-comment-attach" onClick={() => fileInputRef.current?.click()} type="button"><Paperclip size={16}/></button></FlowTooltip>
              <input aria-hidden hidden multiple onChange={event => { attachFiles(Array.from(event.target.files ?? [])); event.target.value = '' }} ref={fileInputRef} tabIndex={-1} type="file"/>
            </>}
            <button aria-label={posting ? t('Sending…') : t('Comment')} className="pulse-post-comment-submit" disabled={!comment?.markdown.trim() || posting} onClick={() => void submitComment()} type="button"><ArrowUp size={14}/></button>
          </div>
        </div>
      </div>
    </section>}
    {whyOpen && <PulseWhyDialog data={props.data} item={item} onOpenChange={setWhyOpen} onToggleSubscription={() => toggleSubscription().catch(() => undefined)} onToggleTeam={(teamId, subscribed) => props.onToggleTeam(teamId, subscribed).catch(() => undefined)} open={whyOpen}/>}
  </article>
})

function PulseComment({ comment }: { comment: Comment }) {
  const { formatDate, t } = useI18n()
  const created = new Date(comment.createdAt)
  const author = liveActorUser(comment.user)
  return <article className="pulse-post-comment">
    <UserAvatar avatarUrl={author.avatarUrl} className="avatar pulse-byline-avatar" name={userName(author)}/>
    <div>
      <header><strong data-i18n-ignore>{userName(author)}</strong><time dateTime={comment.createdAt} title={formatDate(created, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })}>{relativeTime(created, t('just now'))}</time></header>
      <div className="pulse-post-comment-body"><RichComment body={comment.body} data={comment.bodyData} version={comment.version}/></div>
    </div>
  </article>
}

/** Project / initiative name with the reference app's hover card. */
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
        <span data-i18n-ignore>{item.source.name}</span>
      </a>
    </Popover.Anchor>
    <Popover.Portal><Popover.Content align="start" className="pulse-hover-card" data-flow-motion="floating" onOpenAutoFocus={event => event.preventDefault()} onPointerEnter={() => schedule(true)} onPointerLeave={() => schedule(false)} sideOffset={6}>
      <div className="pulse-hover-card-title"><ViewGlyph color={item.source.color} icon={icon}/><strong data-i18n-ignore>{item.source.name}</strong></div>
      <div className="pulse-hover-card-meta"><HealthGlyph className={`pulse-health-glyph is-${item.update.health}`} health={item.update.health}/><span>{t(HEALTH_LABELS[item.update.health])}</span><span>·</span><span>{item.kind === 'project' ? t('Project') : t('Initiative')}</span></div>
      <button className="pulse-link-button" onClick={() => { setOpen(false); onNavigate(item.source.url) }} type="button">{item.kind === 'project' ? t('Open project') : t('Open initiative')}</button>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function PulsePhoneHealthGlyph() {
  return <svg aria-hidden="true" className="pulse-health-glyph" height="16" viewBox="0 0 16 16" width="16"><circle cx="8" cy="8" fill="currentColor" fillOpacity={.25} r="8"/><path d="M12.6807 5.7029C12.9925 5.97566 13.0241 6.44948 12.7513 6.76121L9.71942 10.2263C9.56569 10.402 9.33892 10.4961 9.10596 10.4808C8.873 10.4656 8.66044 10.3427 8.53094 10.1485L6.76432 7.49855L4.37742 10.2263C4.10466 10.5381 3.63083 10.5696 3.31911 10.2969C3.00739 10.0241 2.97581 9.55028 3.24857 9.23856L6.28056 5.77356C6.43429 5.59788 6.66106 5.50379 6.89401 5.51905C7.12696 5.53432 7.33952 5.65718 7.46902 5.85142L9.23562 8.50133L11.6224 5.77347C11.8952 5.46174 12.369 5.43015 12.6807 5.7029Z" fill="currentColor"/></svg>
}
