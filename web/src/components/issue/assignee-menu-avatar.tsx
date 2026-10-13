import { avatarColor } from '@/components/issue/avatar-color'
import { UserAvatar } from '@/components/ui/user-avatar'
import type { User } from '@/types/flow'

/** A person's (or agent's) 16px avatar in an assignee menu: 9px initials, the agent logo for the built-in agent. */
export function AssigneeMenuAvatar({ user }: { user: Pick<User, 'id' | 'name' | 'displayName' | 'avatarUrl'> }) {
  return <UserAvatar avatarUrl={user.avatarUrl} className="avatar core-person-picker-avatar" color={avatarColor(user.id)} name={user.displayName || user.name}/>
}
