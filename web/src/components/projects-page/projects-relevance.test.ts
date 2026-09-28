import { describe, expect, it } from 'vitest'
import {
  projectRelevanceScore,
  relevanceInputFromProject,
  sortProjectsByRelevance,
  type ProjectRelevanceInput,
} from './projects-relevance'

const viewer = { id: 'viewer-1', activeTeamIds: ['team-a'] }
const now = Date.parse('2026-09-20T00:00:00.000Z')

function project(overrides: Partial<ProjectRelevanceInput> & { id: string; name: string }): ProjectRelevanceInput {
  return {
    memberIds: [],
    teamIds: ['team-a'],
    createdAt: '2026-01-01T00:00:00.000Z',
    isActive: false,
    isClosed: false,
    ...overrides,
  }
}

describe('ProjectsRelevance (LS-0500)', () => {
  it('boosts lead over member and applies closed penalty', () => {
    const lead = project({ id: '1', name: 'Lead', leadId: 'viewer-1' })
    const member = project({ id: '2', name: 'Member', memberIds: ['viewer-1'] })
    const closedLead = project({ id: '3', name: 'Closed lead', leadId: 'viewer-1', isClosed: true })
    expect(projectRelevanceScore(lead, viewer, now)).toBeCloseTo(1.25)
    expect(projectRelevanceScore(member, viewer, now)).toBeCloseTo(1)
    expect(projectRelevanceScore(closedLead, viewer, now)).toBeCloseTo(-0.75)
  })

  it('penalizes inaccessible teams and boosts recent / self-created / active', () => {
    const inaccessible = project({ id: '1', name: 'Hidden', teamIds: ['other'] })
    const recent = project({ id: '2', name: 'Recent', createdAt: '2026-09-19T00:00:00.000Z' })
    const self = project({
      id: '3',
      name: 'Mine',
      createdAt: '2026-09-15T00:00:00.000Z',
      creatorId: 'viewer-1',
    })
    const active = project({ id: '4', name: 'Active', isActive: true })
    expect(projectRelevanceScore(inaccessible, viewer, now)).toBeCloseTo(-0.3)
    expect(projectRelevanceScore(recent, viewer, now)).toBeCloseTo(0.15)
    expect(projectRelevanceScore(self, viewer, now)).toBeCloseTo(0.5)
    expect(projectRelevanceScore(active, viewer, now)).toBeCloseTo(0.25)
  })

  it('sorts by -relevance then name and returns original order without viewer', () => {
    const projects = [
      project({ id: 'b', name: 'Beta' }),
      project({ id: 'a', name: 'Alpha', leadId: 'viewer-1' }),
      project({ id: 'c', name: 'Charlie', memberIds: ['viewer-1'] }),
    ]
    expect(sortProjectsByRelevance(projects, undefined).map(item => item.id)).toEqual(['b', 'a', 'c'])
    expect(sortProjectsByRelevance(projects, viewer, { now }).map(item => item.id)).toEqual(['a', 'c', 'b'])
  })

  it('adapts domain projects for scoring', () => {
    const input = relevanceInputFromProject({
      id: 'p1',
      name: 'Launch',
      lead: { id: 'viewer-1' },
      memberIds: [],
      teamIds: ['team-a'],
      createdAt: '2026-09-19T00:00:00.000Z',
      status: { type: 'started' },
    })
    expect(input.isActive).toBe(true)
    expect(projectRelevanceScore(input, viewer, now)).toBeGreaterThan(1)
  })
})
