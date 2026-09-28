/**
 * LS-0700 getAssigneeFilterOptionsForContext — context-scoped assignee options
 * (view assignees / team members / workspace users + No assignee + current user).
 */
import {
  CURRENT_USER_SENTINEL,
  buildUserFilterOptions,
  type FilterUserLike,
} from '@/components/filter/block-utils'
import type { FilterBlockOption } from '@/components/filter'

export interface AssigneeFilterContext {
  currentUser: FilterUserLike
  /** Teams in scope (their members are assignable). */
  teams?: Array<{ id: string; memberIds?: string[] }>
  /** Workspace / org users (already hydrated). */
  users: FilterUserLike[]
  /** Optional saved-view assignee allow-list. */
  view?: {
    persisted?: boolean
    assigneeIds?: string[]
    includeNoAssigneeOption?: boolean
  }
}

/**
 * Build assignee FilterBlock options for the active explorer / view context.
 * Persisted views with an assignee allow-list prefer that list; otherwise
 * team-assignable users (+ apps tagged for search).
 */
export function getAssigneeFilterOptionsForContext(
  context: AssigneeFilterContext,
): FilterBlockOption[] {
  const noAssignee: FilterBlockOption = {
    id: '',
    label: 'No assignee',
    keywords: 'unassigned none empty',
  }

  const viewAssignees = context.view?.assigneeIds ?? []
  if (context.view?.persisted && viewAssignees.length > 0) {
    const byId = new Map(context.users.map(user => [user.id, user]))
    const options = viewAssignees
      .map(id => byId.get(id))
      .filter((user): user is FilterUserLike => Boolean(user))
      .map(user => ({
        id: user.id === context.currentUser.id ? CURRENT_USER_SENTINEL : user.id,
        label:
          user.id === context.currentUser.id
            ? `${user.displayName} (me)`
            : user.displayName,
        keywords: user.displayName,
      }))
    return context.view.includeNoAssigneeOption === false
      ? options
      : [noAssignee, ...options]
  }

  const teamMemberIds = new Set(
    (context.teams ?? []).flatMap(team => team.memberIds ?? []),
  )
  const scopedUsers =
    teamMemberIds.size > 0
      ? context.users.filter(
          user =>
            teamMemberIds.has(user.id) ||
            user.id === context.currentUser.id ||
            Boolean(user.app),
        )
      : context.users

  return buildUserFilterOptions({
    users: scopedUsers.map(user => ({
      ...user,
      isAssignable: user.isAssignable !== false,
    })),
    currentUser: context.currentUser,
    includeCurrentUserSentinel: true,
    includeNoAssignee: true,
    assignableOnly: true,
  })
}
