import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mentionOptions } from '@/components/editor/mentions/mention-options'
import { PeopleProvider } from '@/components/property/people-provider'
import { usePeopleDirectory } from '@/components/property/people-context'
import type { User, WorkspaceMember } from '@/types/flow'
import { applyMemberStatusChange, memberStatusChange } from './member-status-realtime'

const ada: User = { id: 'u-ada', name: 'ada', displayName: 'Ada Lovelace', email: 'ada@example.com', emailVerified: true, active: true }
const bob: User = { id: 'u-bob', name: 'bob', displayName: 'Bob Paused', email: 'bob@example.com', emailVerified: true, active: true }
const member = (user: User): WorkspaceMember => ({ user, role: 'member', status: 'active', joinedAt: '2026-01-01T00:00:00Z' })

function Directory() {
  const { users, members } = usePeopleDirectory()
  return <ul>{[...users.values()].map(user => <li key={user.id}>{`${user.displayName}: ${user.active === false ? 'inactive' : 'active'} ${members.get(user.id)?.status ?? ''}`}</li>)}</ul>
}

const mentionable = (users: User[]) => mentionOptions(undefined, '', { users, issues: [] }).filter(option => option.kind === 'user').map(option => option.attrs.id)

describe('member status realtime notices', () => {
  it('reads the member and the status from the event', () => {
    expect(memberStatusChange({ type: 'workspace_member.suspended', aggregateId: 'u-bob', payload: { userId: 'u-bob', status: 'suspended' } as never })).toEqual({ userId: 'u-bob', status: 'suspended' })
    expect(memberStatusChange({ type: 'workspace_member.removed', aggregateId: 'u-bob' })).toEqual({ userId: 'u-bob', status: 'departed' })
    expect(memberStatusChange({ type: 'workspace_member.resumed', aggregateId: 'u-bob' })).toEqual({ userId: 'u-bob', status: 'active' })
    expect(memberStatusChange({ type: 'workspace_member.updated', aggregateId: 'u-bob' })).toBeUndefined()
    expect(memberStatusChange({ type: 'workspace_member.suspended' })).toBeUndefined()
  })

  it('updates the open people directory and the mention menu without a reload, keeping the person on record', () => {
    let data = { users: [ada, bob], members: [member(ada), member(bob)] }
    const view = render(<PeopleProvider users={data.users} members={data.members}><Directory /></PeopleProvider>)
    expect(screen.getByText('Bob Paused: active active')).toBeInTheDocument()
    expect(mentionable(data.users)).toEqual(expect.arrayContaining(['u-ada', 'u-bob']))

    data = applyMemberStatusChange(data, memberStatusChange({ type: 'workspace_member.suspended', aggregateId: 'u-bob' })!)
    view.rerender(<PeopleProvider users={data.users} members={data.members}><Directory /></PeopleProvider>)
    // Still in the directory (a picked value renders), but inactive: no picker or "@" menu offers them.
    expect(screen.getByText('Bob Paused: inactive suspended')).toBeInTheDocument()
    expect(mentionable(data.users)).toEqual(['u-ada'])

    data = applyMemberStatusChange(data, memberStatusChange({ type: 'workspace_member.resumed', aggregateId: 'u-bob' })!)
    view.rerender(<PeopleProvider users={data.users} members={data.members}><Directory /></PeopleProvider>)
    expect(screen.getByText('Bob Paused: active active')).toBeInTheDocument()
    expect(mentionable(data.users)).toEqual(expect.arrayContaining(['u-ada', 'u-bob']))

    data = applyMemberStatusChange(data, memberStatusChange({ type: 'workspace_member.removed', aggregateId: 'u-bob' })!)
    view.rerender(<PeopleProvider users={data.users} members={data.members}><Directory /></PeopleProvider>)
    expect(screen.getByText('Bob Paused: inactive departed')).toBeInTheDocument()
    expect(mentionable(data.users)).toEqual(['u-ada'])
  })

  it('leaves the data as it is when nothing changes', () => {
    const data = { users: [ada], members: [member(ada)] }
    expect(applyMemberStatusChange(data, { userId: 'u-ada', status: 'active' })).toBe(data)
    expect(applyMemberStatusChange(data, { userId: 'u-nobody', status: 'suspended' })).toBe(data)
  })
})

describe('the "@" menu', () => {
  it('never offers people who are no longer active members', () => {
    expect(mentionable([ada, { ...bob, active: false }])).toEqual(['u-ada'])
  })
})
