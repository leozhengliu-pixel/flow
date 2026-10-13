/**
 * A document comment thread card (right gutter): the first comment with its
 * author, time, resolve and reaction buttons and "…" menu, the replies, and
 * a reply composer.
 */
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ArrowUp, Bell, BellOff, Check, CircleDot, Copy, FilePlus2, Link2, MoreHorizontal, Pencil, RotateCcw, SmilePlus, Trash2 } from 'lucide-react'
import { forwardRef, useState, type ReactNode } from 'react'
import { toast } from '@/lib/toast'

import { RichComment } from '@/components/activity/rich-comment'
import { Composer } from '@/components/editor/composer'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuSeparator } from '@/components/ui/row-context-menu'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { Comment, User } from '@/types/flow'
import { commentMarkdown, type InlineThread } from './inline-comments-model'
import { ViaApp } from '@/components/ui/via-app'
import { attributedActorAvatar, attributedActorName } from '@/lib/actor-attribution'
import { hasAgentThread, useAgentCommentThreadIds, type AgentThreadControls } from '@/components/agent/issue-agent-task-context'

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
  /** The agent session a comment of this thread started; like the reference app it answers in the card and takes its reply row. */
  renderAgentThread?: (threadIds: string[], controls: AgentThreadControls) => ReactNode
}

export const InlineThreadCard = forwardRef<HTMLDivElement, ThreadCardProps>(function InlineThreadCard({ thread, viewer, users, active, canComment, canModerate, muted, actions, onActivate, style, variant = 'gutter', renderAgentThread }, ref) {
  const { t } = useI18n()
  const resolved = variant === 'resolved'
  const agentThreads = useAgentCommentThreadIds()
  const threadIds = [thread.root.id, ...thread.replies.map(reply => reply.id)]
  const agentThread = renderAgentThread && hasAgentThread(agentThreads, threadIds)
    ? renderAgentThread(threadIds, { following: !muted, onToggleSubscription: () => void actions.toggleThreadSubscription(thread), resolved: Boolean(thread.root.resolved), onResolve: canComment ? () => void actions.resolve(thread, !thread.root.resolved) : undefined })
    : null
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
        {!resolved && <FlowMenuItem icon={muted ? <Bell size={14}/> : <BellOff size={14}/>} label={muted ? 'Subscribe to thread' : 'Unsubscribe from thread'} onSelect={() => void actions.toggleThreadSubscription(thread)}/>}
        {canComment && (resolved
          ? <FlowMenuItem icon={<RotateCcw size={14}/>} label="Reopen thread" onSelect={() => void actions.resolve(thread, false)}/>
          : <FlowMenuItem icon={<CircleDot size={14}/>} label="Resolve thread" onSelect={() => void actions.resolve(thread, true)}/>)}
      </>}
      headerExtra={!resolved && canComment && <FlowTooltip label={t('Resolve thread')}><button aria-label={t('Resolve thread')} className="document-thread-icon" onClick={() => void actions.resolve(thread, true)} type="button"><Check size={14}/></button></FlowTooltip>}
    />
    {thread.replies.map(reply => <ThreadComment key={reply.id} comment={reply} viewer={viewer} users={users} canComment={canComment} canModerate={canModerate} actions={actions} thread={thread}/>)}
    {agentThread ?? (!resolved && canComment && (active
      ? <div className="document-thread-reply">
        <UserAvatar avatarUrl={viewer.avatarUrl} className="document-thread-avatar" name={viewer.displayName || viewer.name}/>
        <div className="document-thread-reply-composer"><Composer compact users={users} placeholder={t('Reply…')} onSubmit={(body, bodyData) => actions.reply(thread, body, bodyData)}/></div>
      </div>
      : <button aria-label={t('Reply…')} className="document-thread-reply-trigger" onClick={onActivate} type="button">
        <UserAvatar avatarUrl={viewer.avatarUrl} className="document-thread-avatar" name={viewer.displayName || viewer.name}/>
        <span>{t('Reply…')}</span>
        <span aria-hidden className="document-thread-send"><ArrowUp size={14}/></span>
      </button>))}
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
  const name = attributedActorName(comment.user, comment.botActor)
  const copyMarkdown = async () => {
    try { await navigator.clipboard.writeText(commentMarkdown(comment)); toast.success(t('Copied comment to clipboard')) }
    catch { toast.error(t('Could not copy comment')) }
  }
  return <div className={`document-thread-comment${root ? ' is-root' : ''}`} id={`comment-${comment.id}`}>
    <header>
      <UserAvatar avatarUrl={attributedActorAvatar(comment.user, comment.botActor)} className="document-thread-avatar" name={name}/>
      <strong data-i18n-ignore>{name}</strong><ViaApp user={comment.user} botActor={comment.botActor}/>
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
            <FlowDropdownMenuContent align="end" label={t('Comment options')}>
              {canManage && <FlowMenuItem icon={<Pencil size={14}/>} label="Edit" onSelect={() => setEditing(true)}/>}
              {extraMenu}
              <FlowMenuSeparator/>
              <FlowMenuItem icon={<Link2 size={14}/>} label="Copy link to comment" onSelect={() => void actions.copyLink(comment)}/>
              <FlowMenuItem icon={<Copy size={14}/>} label="Copy content as Markdown" onSelect={() => void copyMarkdown()}/>
              <FlowMenuItem icon={<FilePlus2 size={14}/>} label="New issue from comment…" onSelect={() => actions.newIssue(comment)}/>
              {canManage && <><FlowMenuSeparator/><FlowMenuItem icon={<Trash2 size={14}/>} label="Delete" onSelect={() => void actions.remove(thread, comment)}/></>}
            </FlowDropdownMenuContent>
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
