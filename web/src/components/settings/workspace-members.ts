import type { Invitation, WorkspaceMember } from '@/types/flow'

/**
 * Settings › Members (workspace), as the reference app lays it out: rows are grouped Active, Invited, Application, Inactive;
 * Inactive (suspended users and former members, "Left workspace") is shown only to admins.
 */
export const MEMBER_FILTERS = ['All', 'Admins', 'Members', 'Guests', 'Applications', 'Pending invites', 'Suspended', 'Left workspace'] as const
export type MemberFilter = (typeof MEMBER_FILTERS)[number]
export type MemberSort = 'name' | 'email' | 'status' | 'role' | 'joined' | 'lastSeen'
export type MemberGroup = 'Active' | 'Invited' | 'Application' | 'Inactive'
export const MEMBER_GROUPS: MemberGroup[] = ['Active', 'Invited', 'Application', 'Inactive']

export type MemberEntry =
  | { kind: 'member'; key: string; member: WorkspaceMember }
  | { kind: 'invitation'; key: string; invitation: Invitation }

export function isApplicationMember(member: WorkspaceMember) {
  // An app user whose installation is gone is listed as a plain former member (the reference app drops the app flag).
  return member.status !== 'departed' && (Boolean(member.user.app) || member.role === 'app')
}

export function isAdminRole(role?: string) {
  return role === 'admin' || role === 'owner'
}

/** The reference app: active non-app users are Active, app users (also suspended ones) Application, everyone else Inactive. */
export function memberGroup(member: WorkspaceMember): Exclude<MemberGroup, 'Invited'> {
  if (isApplicationMember(member)) return 'Application'
  return member.status === 'active' ? 'Active' : 'Inactive'
}

function title(value: string) {
  return value ? value[0].toUpperCase() + value.slice(1) : value
}

/** The Status column text: "Admin", "Member", "Application (Suspended)", "Suspended", "Left workspace". */
export function memberStatusLabel(member: WorkspaceMember) {
  if (isApplicationMember(member)) return member.status === 'suspended' ? 'Application (Suspended)' : 'Application'
  if (member.status === 'departed') return 'Left workspace'
  if (member.status === 'suspended') return 'Suspended'
  return title(member.role)
}

export function invitationStatusLabel(invitation: Invitation) {
  return `${title(invitation.role)} (Invited)`
}

/** Sort order of the Status column (the reference app's status priority). */
const STATUS_PRIORITY: Record<string, number> = { Owner: 0, Admin: 1, Member: 2, Guest: 3, Application: 4, 'Application (Suspended)': 5, Suspended: 6, 'Left workspace': 7 }

export function memberMatchesFilter(member: WorkspaceMember, filter: MemberFilter) {
  const app = isApplicationMember(member)
  const active = member.status === 'active'
  switch (filter) {
    case 'All': return true
    case 'Admins': return !app && active && isAdminRole(member.role)
    case 'Members': return !app && active && member.role === 'member'
    case 'Guests': return !app && member.status !== 'departed' && member.role === 'guest'
    case 'Applications': return app
    case 'Suspended': return member.status === 'suspended'
    case 'Left workspace': return member.status === 'departed'
    default: return false
  }
}

/**
 * The filters a viewer is offered (the reference app hides those they cannot use): applications only when the workspace has
 * app users, and invitations, suspended and departed users only for admins.
 */
export function availableMemberFilters(admin: boolean, hasApplications: boolean): MemberFilter[] {
  return MEMBER_FILTERS.filter(filter => {
    if (filter === 'Applications') return admin && hasApplications
    if (filter === 'Pending invites' || filter === 'Suspended' || filter === 'Left workspace') return admin
    return true
  })
}

/** The reference app's empty state: "No members", "No matching suspended users". */
export function emptyMembersMessage(filter: MemberFilter, filtered: boolean) {
  const noun = ({ All: 'members', Admins: 'admins', Members: 'members', Guests: 'guests', Applications: 'applications', 'Pending invites': 'pending invites', Suspended: 'suspended users', 'Left workspace': 'departed users' } as const)[filter]
  return filtered ? `No matching ${noun}` : `No ${noun}`
}

/** The reference app ignores a one-character search. */
export function memberSearchQuery(query: string) {
  const normalized = query.trim().toLowerCase()
  return normalized.length < 2 ? '' : normalized
}

export function memberMatchesQuery(member: WorkspaceMember, query: string) {
  return !query || [member.user.displayName, member.user.name, member.user.username, member.user.email].some(value => value?.toLowerCase().includes(query))
}

/**
 * Rows a viewer may select: admins select people who are active or suspended, never app users or former
 * members; a selection holds either active or inactive users, never both.
 */
export function memberSelectable(member: WorkspaceMember, admin: boolean, selectionActive?: boolean) {
  if (!admin || isApplicationMember(member) || member.status === 'departed') return false
  return selectionActive === undefined || (member.status === 'active') === selectionActive
}

export interface MemberSortContext {
  sort: MemberSort
  descending: boolean
  isOnline: (member: WorkspaceMember) => boolean
  now: number
}

function entryValue(entry: MemberEntry, context: MemberSortContext): string | number {
  if (entry.kind === 'invitation') {
    const { invitation } = entry
    switch (context.sort) {
      case 'name':
      case 'email': return invitation.email.toLowerCase()
      case 'status': return STATUS_PRIORITY[title(invitation.role)] ?? 9
      case 'role': return invitation.role
      case 'joined': return new Date(invitation.createdAt).getTime()
      default: return -1
    }
  }
  const { member } = entry
  switch (context.sort) {
    case 'name': return (member.user.displayName || member.user.name).toLowerCase()
    case 'email': return (member.user.email ?? '').toLowerCase()
    case 'status': return STATUS_PRIORITY[memberStatusLabel(member)] ?? 9
    case 'role': return member.role
    case 'joined': return new Date(member.joinedAt).getTime()
    default: return context.isOnline(member) ? context.now : member.lastSeenAt ? new Date(member.lastSeenAt).getTime() : -1
  }
}

export function sortMemberEntries(entries: MemberEntry[], context: MemberSortContext) {
  const direction = context.descending ? -1 : 1
  return entries
    .map((entry, index) => ({ entry, index, value: entryValue(entry, context) }))
    .sort((left, right) => {
      const order = typeof left.value === 'number' && typeof right.value === 'number'
        ? left.value - right.value
        : String(left.value).localeCompare(String(right.value))
      return order * direction || left.index - right.index
    })
    .map(item => item.entry)
}

export function memberEntryGroup(entry: MemberEntry): MemberGroup {
  return entry.kind === 'invitation' ? 'Invited' : memberGroup(entry.member)
}
