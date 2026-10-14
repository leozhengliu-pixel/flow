import { describe, expect, it } from 'vitest'

import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, TeamMember, WorkspaceRole } from '@/types/flow'

import { teamContentAccessible } from './team-access'

const teams = [
  { id: 'public', key: 'PUB', name: 'Public', color: '#111' },
  { id: 'private', key: 'PRV', name: 'Private', color: '#222', private: true },
  { id: 'child', key: 'SUB', name: 'Sub', color: '#333' },
]

function data(viewerRole: WorkspaceRole, joined: string[] = []): BootstrapData {
  return makeBootstrap({
    teams,
    teamSettings: {
      public: { teamId: 'public', access: 'public' } as never,
      private: { teamId: 'private', access: 'private' } as never,
      child: { teamId: 'child', access: 'public', parentTeamId: 'private' } as never,
    },
    teamMembers: joined.map((teamId): TeamMember => ({ teamId, userId: viewer.id, role: 'member', joinedAt: '2026-01-01' })),
    viewer,
    viewerRole,
  })
}

const readable = (bootstrap: BootstrapData) => bootstrap.teams.filter(team => teamContentAccessible(bootstrap, team)).map(team => team.id)

describe('teamContentAccessible', () => {
  it('keeps a private team’s content from an admin who has not joined it', () => {
    expect(readable(data('admin'))).toEqual(['public'])
    expect(readable(data('owner'))).toEqual(['public'])
  })

  it('opens the private team and its sub-teams to members, admins included', () => {
    expect(readable(data('admin', ['private']))).toEqual(['public', 'private', 'child'])
    expect(readable(data('member', ['private']))).toEqual(['public', 'private', 'child'])
  })

  it('lets a direct member of a sub-team read it', () => {
    expect(readable(data('admin', ['child']))).toEqual(['public', 'child'])
  })
})
