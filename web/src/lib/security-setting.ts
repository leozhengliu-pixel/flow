/** LS-0764 security role matrix for workspace permission fields. */

export type SecurityPermissionValue =
  | 'members'
  | 'owners_and_admins'
  | 'owners'
  | 'admins'
  | 'admins_only'
  | 'everyone'

export type SecuritySettingKey =
  | 'invitePermission'
  | 'teamCreatePermission'
  | 'labelPermission'
  | 'templatePermission'
  | 'apiKeyPermission'
  | 'initiativePermission'
  | 'loopPermission'
  | 'agentGuidancePermission'

export type SecurityRoleOption = {
  label: string
  value: SecurityPermissionValue
}

/** Linear-parity options: All members / Only owners and admins / Only owners / Only admins. */
export const SECURITY_ROLE_OPTIONS: SecurityRoleOption[] = [
  { label: 'All members', value: 'members' },
  { label: 'Only owners and admins', value: 'owners_and_admins' },
  { label: 'Only owners', value: 'owners' },
  { label: 'Only admins', value: 'admins' },
]

const RANK: Record<string, number> = {
  members: 0,
  everyone: 0,
  user: 0,
  admins: 1,
  owners_and_admins: 1,
  admins_only: 2,
  owners: 3,
}

export function normalizeSecurityPermission(value: string | undefined | null): SecurityPermissionValue {
  const next = String(value ?? '').trim().toLowerCase()
  if (next === 'everyone' || next === 'user') return 'members'
  if (next === 'owners_and_admins') return 'owners_and_admins'
  if (next === 'owners') return 'owners'
  if (next === 'admins_only') return 'admins_only'
  if (next === 'admins') return 'admins'
  if (next === 'members') return 'members'
  return 'admins'
}

export function securityPermissionLabel(value: string | undefined | null): string {
  const normalized = normalizeSecurityPermission(value)
  return SECURITY_ROLE_OPTIONS.find((option) => option.value === normalized)?.label
    ?? (normalized === 'admins_only' ? 'Only admins' : 'Only admins')
}

/** True when moving to a tighter gate (confirm API-key restrict). */
export function isRestrictingSecurityPermission(from: string | undefined | null, to: string): boolean {
  const left = RANK[normalizeSecurityPermission(from)] ?? 0
  const right = RANK[normalizeSecurityPermission(to)] ?? 0
  return right > left
}

export function roleSatisfiesSecurityPermission(
  role: string | undefined | null,
  permission: string | undefined | null,
): boolean {
  const viewer = String(role ?? '').trim().toLowerCase()
  if (viewer === 'guest' || viewer === '') return false
  const value = normalizeSecurityPermission(permission)
  switch (value) {
    case 'members':
    case 'everyone':
      return viewer === 'member' || viewer === 'admin' || viewer === 'owner'
    case 'owners_and_admins':
    case 'admins':
      return viewer === 'admin' || viewer === 'owner'
    case 'owners':
      return viewer === 'owner'
    case 'admins_only':
      return viewer === 'admin'
    default:
      return viewer === 'admin' || viewer === 'owner'
  }
}
