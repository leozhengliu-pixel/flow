/**
 * LS-0637 UserFilterBlockHelper — current-user / __isMe__ / app-user option helpers
 * shared by assignee, creator, workflow, inbox-from filters.
 */
import type { FilterBlockOption } from '../filter-block-types'

export const CURRENT_USER_SENTINEL = '__isMe__'
export const MY_TEAMS_SENTINEL = '__myTeams__'

export interface FilterUserLike {
  id: string
  displayName: string
  email?: string
  active?: boolean
  isAssignable?: boolean
  app?: boolean
  avatarUrl?: string
}

export function isCurrentUserFilterValue(
  blockKey: string,
  option: Pick<FilterBlockOption, 'id'> | { value?: string },
): boolean {
  const value = 'id' in option ? option.id : option.value
  if (value === MY_TEAMS_SENTINEL) return true
  return (blockKey === 'assignee' || blockKey === 'creator' || blockKey === 'assigneeId' || blockKey === 'creatorId') &&
    value === CURRENT_USER_SENTINEL
}

export function currentUserFilterOption(currentUser: FilterUserLike): FilterBlockOption {
  return {
    id: CURRENT_USER_SENTINEL,
    label: 'Current user',
    keywords: `me myself ${currentUser.displayName} ${currentUser.email ?? ''}`,
  }
}

export function noAssigneeFilterOption(): FilterBlockOption {
  return {
    id: '',
    label: 'No assignee',
    keywords: 'unassigned none empty',
  }
}

export function userToFilterOption(
  user: FilterUserLike,
  currentUser?: FilterUserLike,
): FilterBlockOption {
  const isMe = currentUser && user.id === currentUser.id
  return {
    id: isMe ? CURRENT_USER_SENTINEL : user.id,
    label: isMe ? `${user.displayName} (me)` : user.displayName,
    keywords: [user.displayName, user.email, isMe ? 'me' : ''].filter(Boolean).join(' '),
    disabled: user.active === false,
  }
}

export function buildUserFilterOptions(args: {
  users: FilterUserLike[]
  currentUser?: FilterUserLike
  includeCurrentUserSentinel?: boolean
  includeNoAssignee?: boolean
  assignableOnly?: boolean
}): FilterBlockOption[] {
  const options: FilterBlockOption[] = []
  if (args.includeNoAssignee) options.push(noAssigneeFilterOption())
  if (args.includeCurrentUserSentinel !== false && args.currentUser) {
    options.push(currentUserFilterOption(args.currentUser))
  }
  const users = args.users
    .filter(user => (args.assignableOnly ? user.isAssignable !== false : true))
    .filter(user => user.active !== false)
  for (const user of users) {
    if (args.currentUser && user.id === args.currentUser.id && args.includeCurrentUserSentinel !== false) {
      continue
    }
    const option = userToFilterOption(user, args.currentUser)
    if (user.app) {
      options.push({ ...option, keywords: `${option.keywords ?? ''} app bot` })
    } else {
      options.push(option)
    }
  }
  return options
}

export const UserFilterBlockHelper = {
  CURRENT_USER_SENTINEL,
  MY_TEAMS_SENTINEL,
  isCurrentUserFilterValue,
  currentUserFilterOption,
  noAssigneeFilterOption,
  userToFilterOption,
  buildUserFilterOptions,
}
