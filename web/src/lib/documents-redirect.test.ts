import { describe, expect, it } from 'vitest'
import type { BootstrapData, Team } from '@/types/flow'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { parseAppRoute, teamDocumentsPath, teamsPath } from './app-routes'
import { documentsRedirectPath } from './documents-redirect'

const team = (id: string, key: string, extra: Partial<Team> = {}) => ({ id, key, name: key, color: '#000000', ...extra }) as Team
const withTeams = (teams: Team[], memberOf: string[]): BootstrapData => makeBootstrap({
  teams,
  teamMembers: memberOf.map(teamId => ({ teamId, userId: viewer.id, role: 'member' })),
} as Partial<BootstrapData>)

describe('workspace documents redirect', () => {
  it('keeps the old route parsing so old links resolve', () => {
    expect(parseAppRoute('/workspace/documents', '')).toEqual({ kind: 'documents', workspaceSlug: 'workspace' })
  })
  it('goes to the Documents tab of the first team the viewer belongs to', () => {
    const data = withTeams([team('t1', 'AAA'), team('t2', 'BBB'), team('t3', 'CCC')], ['t3', 't2'])
    expect(documentsRedirectPath(data)).toBe(teamDocumentsPath('workspace', 'BBB'))
  })
  it('skips archived teams the viewer belongs to', () => {
    const data = withTeams([team('t1', 'AAA', { archivedAt: '2026-01-01T00:00:00Z' }), team('t2', 'BBB')], ['t1', 't2'])
    expect(documentsRedirectPath(data)).toBe(teamDocumentsPath('workspace', 'BBB'))
  })
  it('falls back to the first team when the viewer is in none', () => {
    const data = withTeams([team('t1', 'AAA'), team('t2', 'BBB')], [])
    expect(documentsRedirectPath(data)).toBe(teamDocumentsPath('workspace', 'AAA'))
  })
  it('falls back to the teams directory without teams', () => {
    expect(documentsRedirectPath(withTeams([], []))).toBe(teamsPath('workspace'))
  })
})
