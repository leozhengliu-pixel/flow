import { describe, expect, it } from 'vitest'
import type { Team, TeamMember } from '@/types/flow'
import { teamHierarchy } from '@/lib/team-hierarchy'
import { compareDirectoryTeams, emptyTeamFilters, indexTeamPeople, matchesTeamDate, matchesTeamFilters, parseTeamFilters, teamDirectoryRows } from './team-directory-model'

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

describe('team directory tree rows', () => {
  // Root > {Alpha > {Beta > {Gamma}, Delta}, Echo}, then Zulu > {Yankee}.
  const all = ['Root', 'Alpha', 'Beta', 'Gamma', 'Delta', 'Echo', 'Zulu', 'Yankee'].map(name => team(name))
  const parents: Record<string, string> = { Alpha: 'Root', Beta: 'Alpha', Gamma: 'Beta', Delta: 'Alpha', Echo: 'Root', Yankee: 'Zulu' }
  const hierarchy = teamHierarchy(all, Object.fromEntries(Object.entries(parents).map(([id, parentTeamId]) => [id, { parentTeamId }])))
  const byName = (descending = false) => (left: Team, right: Team) => compareDirectoryTeams(left, right, 'name', descending)
  const rows = (options: { matches?: (team: Team) => boolean; descending?: boolean; collapsed?: string[] } = {}) =>
    teamDirectoryRows(all, hierarchy, options.matches ?? (() => true), byName(options.descending), new Set(options.collapsed))
  const shape = (list: ReturnType<typeof rows>) => list.map(row => `${row.team.id}:${row.depth}:${row.through.join('')}${row.branchBelow ? '>' : ''}${row.context ? '~' : ''}`)

  it('nests every level under its parent with guides that run past each sibling but the last', () => {
    expect(shape(rows())).toEqual([
      'Root:0:>',
      // Alpha has a later sibling (Echo), so level 1 runs through Alpha's whole subtree.
      'Alpha:1:1>',
      'Beta:2:12>',
      // Gamma is Beta's only (so last) child: its own level 3 ends here, Beta's sibling Delta keeps level 2 running.
      'Gamma:3:12',
      'Delta:2:1',
      'Echo:1:',
      'Zulu:0:>',
      'Yankee:1:',
    ])
    expect(rows().find(row => row.team.id === 'Gamma')?.hasChildren).toBe(false)
    expect(rows().find(row => row.team.id === 'Beta')?.hasChildren).toBe(true)
  })

  it('hides a collapsed team’s sub-teams but keeps its arrow, and ends the guide at the last shown sibling', () => {
    const collapsed = rows({ collapsed: ['Alpha'] })
    expect(shape(collapsed)).toEqual(['Root:0:>', 'Alpha:1:1', 'Echo:1:', 'Zulu:0:>', 'Yankee:1:'])
    expect(collapsed.find(row => row.team.id === 'Alpha')?.hasChildren).toBe(true)
    expect(shape(rows({ collapsed: ['Root', 'Zulu'] }))).toEqual(['Root:0:', 'Zulu:0:'])
  })

  it('orders every level by the chosen order, keeping sub-teams under their parent', () => {
    expect(rows({ descending: true }).map(row => `${row.team.id}:${row.depth}`)).toEqual(['Zulu:0', 'Yankee:1', 'Root:0', 'Echo:1', 'Alpha:1', 'Delta:2', 'Beta:2', 'Gamma:3'])
    const created = all.map((item, index) => ({ ...item, createdAt: `2026-01-${String(10 + index)}T00:00:00Z` }))
    const newestFirst = teamDirectoryRows(created, hierarchy, () => true, (left, right) => compareDirectoryTeams(left, right, 'created', true))
    expect(newestFirst.map(row => row.team.id)).toEqual(['Zulu', 'Yankee', 'Root', 'Echo', 'Alpha', 'Delta', 'Beta', 'Gamma'])
  })

  it('keeps a matching sub-team under its parent, drawn as dimmed context', () => {
    const matches = (item: Team) => item.id === 'Gamma' || item.id === 'Yankee'
    expect(shape(rows({ matches }))).toEqual(['Root:0:>~', 'Alpha:1:>~', 'Beta:2:>~', 'Gamma:3:', 'Zulu:0:>~', 'Yankee:1:'])
    // The last matching sibling ends its guide even when unmatched siblings follow in the full tree.
    expect(shape(rows({ matches: item => item.id === 'Alpha' || item.id === 'Delta' }))).toEqual(['Root:0:>~', 'Alpha:1:>', 'Delta:2:'])
  })
})
