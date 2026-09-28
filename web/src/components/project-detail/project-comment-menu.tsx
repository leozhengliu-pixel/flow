import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, BellOff, Check, Copy, Ellipsis, Link2, Pencil, SquarePen, Trash2 } from 'lucide-react'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { useI18n } from '@/i18n/i18n'
import './project-action-menu.css'
import './project-comment-menu.css'

export type ProjectCommentThreadControl = {
  /** Whether the viewer currently follows the thread (explicitly or as a participant). */
  following: boolean
  onToggle: () => void
}

export type ProjectCommentMenuProps = {
  commentId: string
  body: string
  /** The viewer wrote the comment: only authors may edit. */
  canEdit: boolean
  /** Authors and workspace admins may delete (matches the API permission). */
  canDelete: boolean
  /** Thread-level controls render on root comments only. */
  thread?: ProjectCommentThreadControl
  resolved?: boolean
  onResolve?: () => void
  onEdit: () => void
  onDelete: () => void
  onNewIssue: () => void
  triggerClassName?: string
  iconSize?: number
}

/**
 * Linear's project comment "Comment options" menu: Edit / thread subscription,
 * Resolve thread, copy actions, New issue from comment, and Delete, each
 * group divided by a hairline separator.
 */
export function ProjectCommentMenu({ commentId, body, canEdit, canDelete, thread, resolved = false, onResolve, onEdit, onDelete, onNewIssue, triggerClassName, iconSize = 16 }: ProjectCommentMenuProps) {
  const { t } = useI18n()
  const copy = (value: string) => void navigator.clipboard.writeText(value)
    .then(() => toast.success(t('Copied to clipboard')))
    .catch(() => toast.error(t('Could not copy to clipboard')))
  const groups: ReactNode[][] = [
    [
      canEdit && <Item key="edit" icon={<Pencil/>} label={t('Edit')} onSelect={onEdit}/>,
      thread && (thread.following
        ? <Item key="thread" icon={<BellOff/>} label={t('Unsubscribe from thread')} onSelect={thread.onToggle}/>
        : <Item key="thread" icon={<Bell/>} label={t('Subscribe to thread')} onSelect={thread.onToggle}/>),
    ],
    [onResolve && <Item key="resolve" icon={<Check/>} label={t(resolved ? 'Unresolve thread' : 'Resolve thread')} onSelect={onResolve}/>],
    [
      <Item key="link" icon={<IssueActionGlyph label="Copy URL" fallback={<Link2/>}/>} label={t('Copy link to comment')} onSelect={() => copy(`${location.href.split('#')[0]}#comment-${commentId}`)}/>,
      <Item key="markdown" icon={<IssueActionGlyph label="Copy content as Markdown" fallback={<Copy/>}/>} label={t('Copy content as Markdown')} onSelect={() => copy(body)}/>,
    ],
    [<Item key="issue" icon={<SquarePen/>} label={t('New issue from comment…')} onSelect={onNewIssue}/>],
    [canDelete && <Item key="delete" icon={<IssueActionGlyph label="Delete" fallback={<Trash2/>}/>} label={t('Delete')} onSelect={onDelete}/>],
  ]
  const visible = groups.map(group => group.filter(Boolean)).filter(group => group.length > 0)
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>
      <button aria-label="Comment options" className={triggerClassName} type="button"><Ellipsis size={iconSize}/></button>
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content data-flow-motion="floating" align="end" sideOffset={4} collisionPadding={16} className="project-action-menu project-comment-menu" aria-label={t('Comment options')}>
        {visible.map((group, index) => [index > 0 && <DropdownMenu.Separator key={`separator-${index}`}/>, ...group])}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function Item({ icon, label, onSelect }: { icon: ReactNode; label: string; onSelect: () => void }) {
  return <DropdownMenu.Item onSelect={onSelect}><span className="project-menu-icon" aria-hidden="true">{icon}</span><span className="project-menu-label">{label}</span></DropdownMenu.Item>
}
