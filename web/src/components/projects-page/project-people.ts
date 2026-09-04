import type { Invitation, User } from '@/types/flow'

export type ProjectPersonChoice = {
  id: string
  label: string
  color?: string
  name?: string
  email?: string
  avatarUrl?: string
  active?: boolean
  online?: boolean
  invited?: boolean
  disabled?: boolean
  end?: string
}

const INVITATION_PREFIX = 'invitation:'

export function projectPeopleChoices(users: User[], invitations: Invitation[] = [], onlineUserIds?: ReadonlySet<string>): ProjectPersonChoice[] {
  const knownEmails = new Set(users.map(user => user.email.trim().toLowerCase()))
  const choices: ProjectPersonChoice[] = users.map(user => ({
    id: user.id,
    label: user.displayName || user.name || user.email,
    name: user.name,
    email: user.email,
    avatarUrl: user.avatarUrl,
    color: avatarColor(user.id),
    active: user.active,
    // Account activity controls eligibility; live presence controls the status dot.
    online: user.active && Boolean(onlineUserIds?.has(user.id)) && !pendingInvitation(invitations, user.email),
    end: pendingInvitation(invitations, user.email) ? 'Invited' : undefined,
  }))
  for (const invitation of invitations) {
    const email = invitation.email.trim()
    if (invitation.status !== 'pending' || !email || knownEmails.has(email.toLowerCase())) continue
    choices.push({
      id: `${INVITATION_PREFIX}${invitation.id}`,
      label: email,
      name: email,
      email,
      active: false,
      invited: true,
      end: 'Invited',
      disabled: true,
    })
  }
  return choices
}

function avatarColor(value: string) {
  const colors = ['#d15f5f', '#5e6ad2', '#4c9a67', '#d09b42']
  return colors[[...value].reduce((sum, character) => sum + character.charCodeAt(0), 0) % colors.length]
}

function pendingInvitation(invitations: Invitation[], email: string) {
  const normalized = email.trim().toLowerCase()
  return invitations.some(invitation => invitation.status === 'pending' && invitation.email.trim().toLowerCase() === normalized)
}
