/**
 * A document comment thread card (right gutter): the first comment with its
 * author, time, resolve and reaction buttons and "…" menu, the replies, and
 * a reply composer.
 */
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, BellOff, Check, CircleDot, Copy, FilePlus2, Link2, MoreHorizontal, Pencil, RotateCcw, SmilePlus, Trash2 } from 'lucide-react'
import { forwardRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { RichComment } from '@/components/activity/rich-comment'
import { Composer } from '@/components/editor/composer'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuSeparator } from '@/components/ui/row-context-menu'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { Comment, User } from '@/types/flow'
import { commentMarkdown, type InlineThread } from './inline-comments-model'

export interface ThreadActions {
  reply: (thread: InlineThread, body: string, bodyData?: Record<string, unknown>) => Promise<void>
  edit: (comment: Comment, body: string, bodyData?: Record<string, unknown>) => Promise<void>
  remove: (thread: InlineThread, comment: Comment) => Promise<void>
  react: (comment: Comment, emoji: string) => Promise<void>
  resolve: (thread: InlineThread, resolved: boolean) => Promise<void>
  toggleThreadSubscription: (thread: InlineThread) => Promise<void>
  copyLink: (comment: Comment) => Promise<void>
  newIssue: (comment: Comment) => void
}

export interface ThreadCardProps {
  thread: InlineThread
  viewer: User
  users: User[]
  active: boolean
  canComment: boolean
  /** Workspace admins may edit and delete any comment. */
  canModerate: boolean
  /** The viewer unsubscribed from this thread. */
  muted: boolean
  actions: ThreadActions
  onActivate: () => void
  style?: React.CSSProperties
  /** Resolved-comments panel: show the quote and "Reopen thread" instead of resolve. */
  variant?: 'gutter' | 'resolved'
}

export const InlineThreadCard = forwardRef<HTMLDivElement, ThreadCardProps>(function InlineThreadCard({ thread, viewer, users, active, canComment, canModerate, muted, actions, onActivate, style, variant = 'gutter' }, ref) {
  const { t } = useI18n()
  const resolved = variant === 'resolved'
  return <TooltipProvider><div
    className={`document-thread-card${active ? ' is-active' : ''}${resolved ? ' is-resolved' : ''}`}
    data-thread-id={thread.root.id}
    onMouseDown={event => { if (!(event.target as HTMLElement).closest('button,[role=menuitem],.ProseMirror')) onActivate() }}
    ref={ref}
    role="group"
    aria-label={t('Comment thread')}
    style={style}
  >
    {resolved && thread.root.quotedText && <blockquote className="document-thread-quote" data-i18n-ignore>{thread.root.quotedText}</blockquote>}
    <ThreadComment
      comment={thread.root}
      viewer={viewer}
      users={users}
      canComment={canComment}
      canModerate={canModerate}
      actions={actions}
      thread={thread}
      root
      extraMenu={<>
        {!resolved && <LinearMenuItem icon={muted ? <Bell size={14}/> : <BellOff size={14}/>} label={muted ? 'Subscribe to thread' : 'Unsubscribe from thread'} onSelect={() => void actions.toggleThreadSubscription(thread)}/>}
        {canComment && (resolved
          ? <LinearMenuItem icon={<RotateCcw size={14}/>} label="Reopen thread" onSelect={() => void actions.resolve(thread, false)}/>
          : <LinearMenuItem icon={<CircleDot size={14}/>} label="Resolve thread" onSelect={() => void actions.resolve(thread, true)}/>)}
      </>}
      headerExtra={!resolved && canComment && <FlowTooltip label={t('Resolve thread')}><button aria-label={t('Resolve thread')} className="document-thread-icon" onClick={() => void actions.resolve(thread, true)} type="button"><Check size={14}/></button></FlowTooltip>}
    />
    {thread.replies.map(reply => <ThreadComment key={reply.id} comment={reply} viewer={viewer} users={users} canComment={canComment} canModerate={canModerate} actions={actions} thread={thread}/>)}
    {!resolved && canComment && (active
      ? <div className="document-thread-reply"><Composer compact users={users} placeholder={t('Reply…')} onSubmit={(body, bodyData) => actions.reply(thread, body, bodyData)}/></div>
      : <button className="document-thread-reply-trigger" onClick={onActivate} type="button">{t('Reply…')}</button>)}
  </div></TooltipProvider>
})

function ThreadComment({ comment, viewer, users, canComment, canModerate, actions, thread, root = false, extraMenu, headerExtra }: {
  comment: Comment
  viewer: User
  users: User[]
  canComment: boolean
  canModerate: boolean
  actions: ThreadActions
  thread: InlineThread
  root?: boolean
  extraMenu?: ReactNode
  headerExtra?: ReactNode
}) {
  const { t, formatRelative, formatDate } = useI18n()
  const [editing, setEditing] = useState(false)
  const own = comment.user.id === viewer.id
  const canManage = own || canModerate
  const name = comment.user.displayName || comment.user.name
  const copyMarkdown = async () => {
    try { await navigator.clipboard.writeText(commentMarkdown(comment)); toast.success(t('Copied comment to clipboard')) }
    catch { toast.error(t('Could not copy comment')) }
  }
  return <div className={`document-thread-comment${root ? ' is-root' : ''}`} id={`comment-${comment.id}`}>
    <header>
      <UserAvatar avatarUrl={comment.user.avatarUrl} className="document-thread-avatar" name={name}/>
      <strong data-i18n-ignore>{name}</strong>
      <time dateTime={comment.createdAt} title={formatDate(comment.createdAt, { dateStyle: 'full', timeStyle: 'short' })}>{formatRelative(comment.createdAt)}</time>
      {comment.editedAt && <span className="document-thread-edited">{t('(edited)')}</span>}
      <span className="document-thread-actions">
        {headerExtra}
        {canComment && <EmojiPicker align="end" label={t('Add reaction')} onSelect={emoji => actions.react(comment, emoji)}>
          <button aria-label={t('Add reaction')} className="document-thread-icon" type="button"><SmilePlus size={14}/></button>
        </EmojiPicker>}
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild><button aria-label={t('Comment options')} className="document-thread-icon" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <LinearDropdownMenuContent align="end" label={t('Comment options')}>
              {canManage && <LinearMenuItem icon={<Pencil size={14}/>} label="Edit" onSelect={() => setEditing(true)}/>}
              {extraMenu}
              <LinearMenuSeparator/>
              <LinearMenuItem icon={<Link2 size={14}/>} label="Copy link to comment" onSelect={() => void actions.copyLink(comment)}/>
              <LinearMenuItem icon={<Copy size={14}/>} label="Copy content as Markdown" onSelect={() => void copyMarkdown()}/>
              <LinearMenuItem icon={<FilePlus2 size={14}/>} label="New issue from comment…" onSelect={() => actions.newIssue(comment)}/>
              {canManage && <><LinearMenuSeparator/><LinearMenuItem icon={<Trash2 size={14}/>} label="Delete" onSelect={() => void actions.remove(thread, comment)}/></>}
            </LinearDropdownMenuContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </span>
    </header>
    <div className="document-thread-body">
      {editing
        ? <Composer compact users={users} initialValue={comment.body} initialData={comment.bodyData} placeholder={t('Edit comment…')} onCancel={() => setEditing(false)} onSubmit={async (body, bodyData) => { await actions.edit(comment, body, bodyData); setEditing(false) }}/>
        : <RichComment body={comment.body} data={comment.bodyData} version={comment.version}/>}
    </div>
    <ReactionPills reactions={comment.reactions} viewerId={viewer.id} onToggle={emoji => canComment ? actions.react(comment, emoji) : undefined}/>
  </div>
}
