import type { SubIssueSharingIndicatorProps } from './sub-issue-sharing-indicator'

export function resolveSubIssueSharingState(args: {
  issue: SubIssueSharingIndicatorProps['issue']
  parentIssue?: SubIssueSharingIndicatorProps['parentIssue']
  sharedUsers?: SubIssueSharingIndicatorProps['sharedUsers']
  inheritsSharedAccess?: boolean
  issueLevelPermissions?: boolean
}): { visible: boolean; sharedUsers: NonNullable<SubIssueSharingIndicatorProps['sharedUsers']>; tooltip: string } {
  if (args.issueLevelPermissions === false) {
    return { visible: false, sharedUsers: [], tooltip: '' }
  }
  const parentShared = Boolean(args.parentIssue?.shareToken)
  const ownShared = Boolean(args.issue.shareToken)
  const inherits = args.inheritsSharedAccess !== false
  const users = args.sharedUsers ?? []
  if (!parentShared && !ownShared) {
    return { visible: false, sharedUsers: [], tooltip: '' }
  }
  if (users.length === 0 && !ownShared && !parentShared) {
    return { visible: false, sharedUsers: [], tooltip: '' }
  }
  return {
    visible: true,
    sharedUsers: inherits || ownShared ? users : users,
    tooltip: inherits && parentShared && !ownShared
      ? 'Shared with parent issue — change sharing'
      : 'Change issue sharing',
  }
}
