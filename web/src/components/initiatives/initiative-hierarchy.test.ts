import { describe, expect, it } from 'vitest'
import type { Initiative, Project, TeamSettings } from '@/types/flow'
import { initiativeGraph, initiativeTreeRows, initiativesForTeam } from './initiative-hierarchy'
const item = (id: string, parents: string[] = [], projectIds: string[] = [], leadTeamId?: string) => ({ id, name: id, parentInitiativeIds: parents, projectIds, leadTeamId, contributingTeamIds: [] }) as unknown as Initiative
describe('initiative graph', () => {
  const items = [item('a', [], ['direct']), item('b', [], []), item('child', ['a', 'b'], ['shared']), item('leaf', ['child'], ['shared', 'nested'])]
  it('supports multiple parents and deduplicates inherited projects without materializing parent membership', () => {
    const graph = initiativeGraph(items)
    expect(graph.ancestors('leaf').sort()).toEqual(['a', 'b', 'child'])
    expect([...graph.projectIds('a')].sort()).toEqual(['direct', 'nested', 'shared'])
    expect(items[0].projectIds).toEqual(['direct'])
    expect(graph.canParent('a', 'leaf')).toBe(false)
    expect(graph.canParent('child', 'b')).toBe(true)
  })
  it('collapses nested rows but preserves a shared child through an expanded parent', () => {
    const graph = initiativeGraph(items)
    expect(initiativeTreeRows(items, graph, new Set(['a', 'b'])).map(row => row.initiative.id)).toEqual(['a', 'b'])
    expect(initiativeTreeRows(items, graph, new Set(['a'])).map(row => row.initiative.id)).toEqual(['a', 'b', 'child', 'leaf'])
  })
  it('does not loop on malformed legacy edges and ignores inaccessible nodes', () => {
    const graph = initiativeGraph([item('a', ['b', 'hidden']), item('b', ['a'])])
    expect(graph.ancestors('a')).toEqual(['b'])
    expect(graph.descendants('a')).toEqual(['b'])
  })
  it('includes team descendants, inherited project teams and ancestor lead teams', () => {
    const initiatives = [item('parent', [], [], 'child-team'), item('sub', ['parent'], ['p']), item('unrelated', [], [])]
    const teams = ['team', 'child-team', 'project-team'].map(id => ({ id, name: id, key: id, color: '' }))
    const settings = { 'child-team': { parentTeamId: 'team' } } as Record<string, Pick<TeamSettings, 'parentTeamId'>>
    const projects = [{ id: 'p', teamIds: ['project-team'] }] as Project[]
    expect(initiativesForTeam(initiatives, projects, teams, settings, 'team').map(i => i.id)).toEqual(['parent', 'sub'])
    expect(initiativesForTeam(initiatives, projects, teams, settings, 'team', false).map(i => i.id)).toEqual(['parent'])
    expect(initiativesForTeam(initiatives, projects, teams, settings, 'project-team').map(i => i.id)).toEqual(['parent', 'sub'])
  })
})
