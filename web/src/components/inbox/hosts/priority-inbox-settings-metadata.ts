/**
 * LS-0715 priorityInboxSettingsMetadata — rule groups that drive Priority tab
 * membership when Priority inbox is enabled. Persisted client-side until the
 * notification preferences API gains an equivalent REST field.
 */

export type PriorityInboxRuleId =
  | 'assigned-to-you'
  | 'triage-responsibility'
  | 'project-updates'
  | 'document-activity'
  | 'subscribed-issue-activity'
  | 'subscribed-project-activity'
  | 'reviews'
  | 'mentions'
  | 'replies'
  | 'resolved-threads'
  | 'update-reminders'

export interface PriorityInboxRuleGroup {
  id: PriorityInboxRuleId
  title: string
  description: string
  /** Inbox notificationType / kind tokens that land in Priority when enabled. */
  notificationTypes: string[]
  defaultEnabled: boolean
}

export const PRIORITY_INBOX_RULE_GROUPS: PriorityInboxRuleGroup[] = [
  {
    id: 'assigned-to-you',
    title: 'Assigned to you',
    description: 'Issues assigned to you',
    notificationTypes: ['assignment'],
    defaultEnabled: true,
  },
  {
    id: 'triage-responsibility',
    title: 'Triage responsibility',
    description: 'New issues when you are responsible for triage',
    notificationTypes: ['triage'],
    defaultEnabled: true,
  },
  {
    id: 'project-updates',
    title: 'Project updates',
    description: 'Updates from projects you follow',
    notificationTypes: ['project', 'reminder'],
    defaultEnabled: true,
  },
  {
    id: 'document-activity',
    title: 'Document activity',
    description: 'Comments and content changes on followed docs',
    notificationTypes: ['document', 'documents'],
    defaultEnabled: false,
  },
  {
    id: 'subscribed-issue-activity',
    title: 'Issue activity',
    description: 'Comments and changes from followed issues',
    notificationTypes: ['comment', 'status'],
    defaultEnabled: true,
  },
  {
    id: 'subscribed-project-activity',
    title: 'Project activity',
    description: 'Comments and content on followed projects',
    notificationTypes: ['subscription', 'subscriptions'],
    defaultEnabled: false,
  },
  {
    id: 'reviews',
    title: 'Reviews',
    description: 'Pull request review requests and decisions',
    notificationTypes: ['review'],
    defaultEnabled: true,
  },
  {
    id: 'mentions',
    title: 'Mentions',
    description: 'Cross-entity mentions',
    notificationTypes: ['mention'],
    defaultEnabled: true,
  },
  {
    id: 'replies',
    title: 'Replies',
    description: 'New comments and PR comments',
    notificationTypes: ['comment'],
    defaultEnabled: true,
  },
  {
    id: 'resolved-threads',
    title: 'Resolved threads',
    description: 'Threads resolved across entities',
    notificationTypes: [],
    defaultEnabled: false,
  },
  {
    id: 'update-reminders',
    title: 'Update reminders',
    description: 'Reminders to post project or initiative updates',
    notificationTypes: ['reminder'],
    defaultEnabled: true,
  },
]

export type PriorityInboxRuleState = Record<PriorityInboxRuleId, boolean>

export const PRIORITY_INBOX_RULES_STORAGE_KEY = 'flow.inbox.priority-rules'

export function defaultPriorityInboxRuleState(): PriorityInboxRuleState {
  return Object.fromEntries(
    PRIORITY_INBOX_RULE_GROUPS.map(group => [group.id, group.defaultEnabled]),
  ) as PriorityInboxRuleState
}

export function readPriorityInboxRuleState(): PriorityInboxRuleState {
  const defaults = defaultPriorityInboxRuleState()
  if (typeof window === 'undefined') return defaults
  try {
    const raw = window.localStorage.getItem(PRIORITY_INBOX_RULES_STORAGE_KEY)
    if (!raw) return defaults
    const parsed = JSON.parse(raw) as Partial<PriorityInboxRuleState>
    return { ...defaults, ...parsed }
  } catch {
    return defaults
  }
}

export function writePriorityInboxRuleState(state: PriorityInboxRuleState) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(PRIORITY_INBOX_RULES_STORAGE_KEY, JSON.stringify(state))
  } catch {
    // Private mode / quota — in-memory callers still hold the value.
  }
}

/** Resolve whether a notification type belongs in Priority given rule state. */
export function notificationTypeMatchesPriorityRules(
  notificationType: string,
  kind: string,
  rules: PriorityInboxRuleState,
) {
  const enabledTypes = new Set<string>()
  for (const group of PRIORITY_INBOX_RULE_GROUPS) {
    if (!rules[group.id]) continue
    for (const type of group.notificationTypes) enabledTypes.add(type)
  }
  if (enabledTypes.has(notificationType)) return true
  if (enabledTypes.has(kind)) return true
  return false
}
