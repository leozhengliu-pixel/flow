import { UserAvatar } from '@/components/ui/user-avatar'

export type LightboxComment = {
  id: string
  body: string
  authorName?: string
  authorAvatarUrl?: string
  createdAtLabel?: string
}

/** Compact comment chip shown when URL hash points at a comment (LS-0382). */
export function CommentPopover({
  comment,
  onClose,
}: {
  comment: LightboxComment
  onClose?: () => void
}) {
  const author = comment.authorName ?? 'Someone'
  return (
    <aside aria-label="Comment" className="flow-comment-popover" role="dialog">
      <header>
        <UserAvatar avatarUrl={comment.authorAvatarUrl} className="flow-comment-popover-avatar" name={author} />
        <p>
          Comment from <strong data-i18n-ignore>{author}</strong>
          {comment.createdAtLabel ? ` ${comment.createdAtLabel}` : ''}
        </p>
        {onClose && (
          <button aria-label="Close comment" className="flow-comment-popover-close" onClick={onClose} type="button">
            ×
          </button>
        )}
      </header>
      <p className="flow-comment-popover-body">{comment.body}</p>
    </aside>
  )
}
