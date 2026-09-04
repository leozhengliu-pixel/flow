import { describe, expect, it } from 'vitest'

import { projectPeopleChoices } from './project-people'
import type { Invitation, User } from '@/types/flow'

const user = (email: string, id = 'user-1'): User => ({
  id,
  name: email.split('@')[0],
  displayName: email.split('@')[0],
  email,
  active: true,
  emailVerified: true,
})

const invitation = (email: string, id = 'invite-1'): Invitation => ({
  id,
  workspaceId: 'workspace-1',
  email,
  role: 'member',
  teamIds: [],
  status: 'pending',
  inviterId: 'user-1',
  expiresAt: '2026-09-10T00:00:00.000Z',
  createdAt: '2026-09-01T00:00:00.000Z',
})

describe('projectPeopleChoices', () => {
  it('keeps pending users visible and adds unmatched invitees as disabled rows', () => {
    const choices = projectPeopleChoices([user('member@example.com')], [invitation('MEMBER@example.com'), invitation('invitee@example.com', 'invite-2')])

    expect(choices).toHaveLength(2)
    expect(choices[0]).toMatchObject({ id: 'user-1', end: 'Invited', online: false })
    expect(choices[1]).toMatchObject({ id: 'invitation:invite-2', label: 'invitee@example.com', invited: true, disabled: true, end: 'Invited' })
  })

  it('uses explicit presence ids instead of treating active accounts as online', () => {
    const choices = projectPeopleChoices([
      user('online@example.com', 'online-user'),
      user('idle@example.com', 'idle-user'),
      { ...user('suspended@example.com', 'suspended-user'), active: false },
    ], [], new Set(['online-user', 'suspended-user']))

    expect(choices).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'online-user', active: true, online: true }),
      expect.objectContaining({ id: 'idle-user', active: true, online: false }),
      expect.objectContaining({ id: 'suspended-user', active: false, online: false }),
    ]))
  })

  it('keeps pending invitations offline even when a stale presence id exists', () => {
    const choices = projectPeopleChoices([user('member@example.com')], [invitation('member@example.com')], new Set(['user-1']))

    expect(choices[0]).toMatchObject({ active: true, online: false, end: 'Invited' })
  })
})
