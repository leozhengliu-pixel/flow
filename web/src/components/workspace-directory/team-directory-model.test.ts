import { describe, expect, it } from 'vitest'
import type { Team, TeamMember } from '@/types/flow'
import { compareDirectoryTeams, emptyTeamFilters, indexTeamPeople, matchesTeamDate, matchesTeamFilters, parseTeamFilters } from './team-directory-model'

const team = (id: string, dates: Partial<Team> = {}): Team => ({ id, name: id, key: id, color: '', ...dates })
const memberships = [
  { teamId: 'a', userId: 'owner1', role: 'owner' },
  { teamId: 'a', userId: 'owner2', role: 'owner' },
  { teamId: 'b', userId: 'member', role: 'member' },
] as TeamMember[]

describe('team directory filters and ordering', () => {
  it('uses independent timestamps and keeps unknown dates last in both directions', () => {
    const items = [team('unknown'), team('a', { createdAt: '2026-01-01', updatedAt: '2026-09-01' }), team('b', { createdAt: '2026-02-01', updatedAt: '2026-03-01' })]
    const order = (field: 'created' | 'updated', descending: boolean) => [...items].sort((a, b) => compareDirectoryTeams(a, b, field, descending)).map(item => item.id)
    expect(order('created', false)).toEqual(['a', 'b', 'unknown'])
    expect(order('updated', false)).toEqual(['b', 'a', 'unknown'])
    expect(order('updated', true)).toEqual(['a', 'b', 'unknown'])
  })
  it('matches either of multiple owners and applies AND, OR, and exclusion', () => {
    const people = indexTeamPeople(memberships)
    const filters = { ...emptyTeamFilters(), owners: ['owner1'], private: ['true'] }
    expect(matchesTeamFilters(team('a'), filters, people)).toBe(false)
    expect(matchesTeamFilters(team('a'), { ...filters, conjunction: 'or' }, people)).toBe(true)
    expect(matchesTeamFilters(team('a'), { ...emptyTeamFilters(), owners: ['owner2'] }, people)).toBe(true)
    expect(matchesTeamFilters(team('a'), { ...emptyTeamFilters(), owners: ['owner2'], operators: { owners: 'isNot' } }, people)).toBe(false)
    expect(matchesTeamFilters(team('retired', { retiredAt: '2026-01-01' }), emptyTeamFilters(), people)).toBe(false)
  })
  it('uses calendar months, real custom ranges and an inclusive end day', () => {
    const now = new Date('2026-03-31T12:00:00')
    expect(matchesTeamDate(team('a', { createdAt: '2026-02-28T12:00:00' }), '30', now)).toBe(true)
    expect(matchesTeamDate(team('a', { createdAt: '2026-09-10T23:59:59' }), 'date:2026-09-01/2026-09-10')).toBe(true)
    expect(matchesTeamDate(team('a', { createdAt: '2026-09-11T00:00:00' }), 'date:2026-09-01/2026-09-10')).toBe(false)
    expect(matchesTeamDate(team('team_1770000000000000000'), '365', now)).toBe(false)
  })
  it('round trips shared filters and rejects malformed URL fields', () => {
    const filters = { ...emptyTeamFilters(), advanced: true, conjunction: 'or' as const, owners: ['owner2'], created: ['date:2026-09-01/2026-09-10'], operators: { owners: 'isNot' as const } }
    expect(parseTeamFilters(JSON.stringify(filters))).toEqual(filters)
    expect(parseTeamFilters('null')).toEqual(emptyTeamFilters())
    expect(parseTeamFilters('{')).toEqual(emptyTeamFilters())
    expect(parseTeamFilters('{"members":[4,"id"],"private":["oops"],"created":["custom"]}')).toMatchObject({ members: ['id'], private: [], created: [] })
  })
})
