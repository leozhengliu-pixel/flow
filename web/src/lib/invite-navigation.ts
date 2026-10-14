import type { BootstrapData } from '@/types/flow'
import { roleAllowed } from './settings-access'

/** Opens Flow's invite flow (the members page with the "Invite people" dialog open) from "Invite and add/assign…" rows. */
export function openMemberInvite() {
  const workspace = location.pathname.split('/').filter(Boolean)[0]
  if (workspace) location.assign(`/${workspace}/members?invite=1`)
}

/**
 * Whether the viewer may invite people: Settings › Security "New user
 * invitations" (the API's "invite" permission; unset means admins only).
 */
export function canInviteMembers(data: Pick<BootstrapData, 'viewerRole' | 'workspaceSettings'>): boolean {
  return roleAllowed(data.viewerRole, data.workspaceSettings?.invitePermission)
}

let viewerMayInvite = true

/**
 * Pickers deep in the tree ("Invite and assign…", "Invite and add…") read the
 * signed-in viewer's invite permission from here; the app sets it whenever the
 * workspace bootstrap changes. The reference app hides those rows from people who cannot
 * invite.
 */
export function setViewerCanInvite(value: boolean) {
  viewerMayInvite = value
}

export function viewerCanInvite() {
  return viewerMayInvite
}
