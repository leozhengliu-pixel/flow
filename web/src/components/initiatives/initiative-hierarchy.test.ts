import { describe, expect, it } from 'vitest'
import type { Initiative, Project, TeamSettings } from '@/types/flow'
import { initiativeGraph, initiativeTreeRows, initiativesForTeam, MAX_INITIATIVE_NESTING } from './initiative-hierarchy'
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
  it('enforces Linear\'s five-level nesting limit for new and moved sub-initiatives', () => {
    expect(MAX_INITIATIVE_NESTING).toBe(5)
    const chain = [item('l1'), item('l2', ['l1']), item('l3', ['l2']), item('l4', ['l3']), item('l5', ['l4']), item('tree'), item('tree-child', ['tree']), item('solo')]
    const graph = initiativeGraph(chain)
    expect(graph.ancestorDepth('l5')).toBe(4)
    expect(graph.descendantDepth('l1')).toBe(4)
    expect(graph.canCreateChild('l4')).toBe(true)
    expect(graph.canCreateChild('l5')).toBe(false)
    expect(graph.canParent('solo', 'l4')).toBe(true)
    expect(graph.canParent('solo', 'l5')).toBe(false)
    // A two-level subtree fits under the third level but not under the fourth.
    expect(graph.canParent('tree', 'l3')).toBe(true)
    expect(graph.canParent('tree', 'l4')).toBe(false)
  })
})
