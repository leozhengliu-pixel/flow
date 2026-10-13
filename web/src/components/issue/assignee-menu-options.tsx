import type { PropertyOption } from '@/components/property/property-menu'
import { agentHoverProps } from '@/components/agent/agent-hover-props'
import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { AssigneeMenuAvatar } from '@/components/issue/assignee-menu-avatar'
import { assigneeCandidates, assigneeShortcut } from '@/lib/agent-members'
import type { User } from '@/types/flow'

/**
 * The option list of an assignee menu that is not a `PersonPicker` (new-issue composer, sub-issue editor):
 * "No assignee" (key 0), the people (1-9), then the assignable agents continuing the numbering, in one list
 * with no group header and no "Agent" pill. Agent rows open the agent hover card.
 */
export function assigneeMenuOptions(users: User[], { teamId, allowAgents = true, emptyIconSize }: { teamId?: string; allowAgents?: boolean; emptyIconSize?: number } = {}): PropertyOption[] {
  return [
    { id: '', label: 'No assignee', icon: <NoAssigneeIcon size={emptyIconSize}/>, shortcut: '0' },
    ...assigneeCandidates(users, teamId, { agents: allowAgents }).map((user, index) => ({
      id: user.id,
      label: user.displayName,
      keywords: user.email,
      icon: <AssigneeMenuAvatar user={user}/>,
      shortcut: assigneeShortcut(index + 1),
      ...(user.app ? agentHoverProps(user) : {}),
    })),
  ]
}
