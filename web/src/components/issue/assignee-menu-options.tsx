import type { PropertyOption } from '@/components/property/property-menu'
import { agentHoverProps } from '@/components/agent/agent-hover-props'
import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { AssigneeMenuAvatar } from '@/components/issue/assignee-menu-avatar'
import { assigneeCandidates, assigneeMenuLayout, assigneeShortcut } from '@/lib/agent-members'
import type { User } from '@/types/flow'

/**
 * The option list of an assignee menu that is not a `PersonPicker` (new-issue composer, sub-issue editor):
 * "No assignee" (key 0), the people (1-9), then the assignable agents continuing the numbering, in one list
 * with no group header and no "Agent" pill. Phone composers use the grouped issue-menu layout instead.
 */
export function assigneeMenuOptions(users: User[], { teamId, allowAgents = true, emptyIconSize, phoneLayout }: { teamId?: string; allowAgents?: boolean; emptyIconSize?: number; phoneLayout?: { viewerId: string; assigneeId?: string; locale: string } } = {}): PropertyOption[] {
  const candidates = assigneeCandidates(users, teamId, { agents: allowAgents })
  const layout = phoneLayout ? assigneeMenuLayout(candidates, phoneLayout) : undefined
  const option = (user: User, index: number): PropertyOption => ({
    id: user.id,
    label: user.displayName,
    keywords: user.email,
    icon: <AssigneeMenuAvatar user={user}/>,
    shortcut: assigneeShortcut(index + 1),
    ...(user.outOfOfficeUntil && new Date(user.outOfOfficeUntil).getTime() > Date.now() ? { labelContent: <>{user.displayName}<span className="property-command-away">Away</span></> } : {}),
    ...(user.app ? agentHoverProps(user) : {}),
  })
  return [
    { id: '', label: 'No assignee', icon: <NoAssigneeIcon size={emptyIconSize}/>, shortcut: '0' },
    ...(layout?.top ?? candidates).map(option),
    ...(layout?.otherAgents ?? []).map(user => ({ ...option(user, -1), shortcut: undefined, groupId: 'assignee-other-agents', groupLabel: 'Other agents' })),
    ...(layout?.members ?? []).map(user => ({ ...option(user, -1), shortcut: undefined, groupId: 'assignee-team-members', groupLabel: 'Team members' })),
  ]
}
