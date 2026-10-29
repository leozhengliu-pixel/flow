import type { BootstrapData, RealtimeEvent, WorkspaceMember } from '@/types/flow'

/**
 * A member who is suspended, removed or resumed elsewhere reaches open sessions as a
 * workspace_member.suspended / .removed / .resumed realtime event. The session marks the
 * person inactive (or active again) at once, so pickers and the "@" menu stop (or start)
 * offering them without a reload; records that already name them keep the user, so a
 * picked value stays visible and can be cleared.
 */
export type MemberStatusChange = { userId: string; status: WorkspaceMember['status'] }

const STATUS_BY_EVENT: Record<string, WorkspaceMember['status']> = {
  'workspace_member.suspended': 'suspended',
  'workspace_member.resumed': 'active',
  'workspace_member.removed': 'departed',
}

export function memberStatusChange(event: Pick<RealtimeEvent, 'type' | 'aggregateId' | 'payload'>): MemberStatusChange | undefined {
  const status = STATUS_BY_EVENT[event.type]
  const fromPayload = (event.payload as { userId?: unknown } | undefined)?.userId
  const userId = typeof fromPayload === 'string' && fromPayload ? fromPayload : event.aggregateId
  return status && userId ? { userId, status } : undefined
}

export function applyMemberStatusChange<T extends Pick<BootstrapData, 'users' | 'members'>>(data: T, change: MemberStatusChange): T {
  const active = change.status === 'active'
  let changed = false
  const users = data.users.map(user => {
    if (user.id !== change.userId || user.active === active) return user
    changed = true
    return { ...user, active }
  })
  const members = data.members?.map(member => {
    if (member.user.id !== change.userId || member.status === change.status) return member
    changed = true
    return { ...member, status: change.status, user: { ...member.user, active } }
  })
  return changed ? { ...data, users, members } : data
}
