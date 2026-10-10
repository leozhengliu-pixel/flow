/**
 * LS-0566 SubIssueSharingIndicator — inherits-shared-access chrome on sub-issues.
 * Built on shareIssue / unshareIssue REST + Issue.shareToken.
 */
import { Link2 } from 'lucide-react'
import type { Issue, User } from '@/types/flow'
import styles from './sub-issue-sharing-indicator.module.css'
import { resolveSubIssueSharingState } from './sub-issue-sharing-state'

export interface SubIssueSharingIndicatorProps {
  issue: Pick<Issue, 'id' | 'parentId' | 'shareToken' | 'sharedAt'>
  parentIssue?: Pick<Issue, 'id' | 'shareToken' | 'sharedAt'> | null
  /** Users the parent (or issue) is shared with. */
  sharedUsers?: Array<Pick<User, 'id' | 'displayName' | 'avatarUrl'>>
  /** When false, sub-issue opted out of parent share inheritance. */
  inheritsSharedAccess?: boolean
  /** Workspace can use issue-level permissions. */
  issueLevelPermissions?: boolean
  onOpenSharing?: () => void
  className?: string
}

export function SubIssueSharingIndicator({
  issue,
  parentIssue,
  sharedUsers = [],
  inheritsSharedAccess = true,
  issueLevelPermissions = true,
  onOpenSharing,
  className,
}: SubIssueSharingIndicatorProps) {
  if (!issue.parentId) return null
  const state = resolveSubIssueSharingState({
    issue,
    parentIssue,
    sharedUsers,
    inheritsSharedAccess,
    issueLevelPermissions,
  })
  if (!state.visible) return null

  return (
    <button
      type="button"
      className={[styles.root, className].filter(Boolean).join(' ')}
      data-sub-issue-sharing-indicator=""
      data-inherits={inheritsSharedAccess ? 'true' : 'false'}
      aria-label={state.tooltip}
      title={state.tooltip}
      onClick={onOpenSharing}
    >
      <Link2 size={12} aria-hidden="true" />
      {state.sharedUsers.length > 0 ? (
        <span className={styles.count}>{state.sharedUsers.length}</span>
      ) : (
        <span className={styles.label}>Shared</span>
      )}
    </button>
  )
}
