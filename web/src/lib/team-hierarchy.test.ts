import { describe, expect, it } from 'vitest'
import { resolvedTeamSettings, sortTeamSiblings, teamHierarchy, teamRowTree, treeGuideLevels } from './team-hierarchy'
import type { Team } from '@/types/flow'

const teams: Team[] = Array.from({length:6},(_,i)=>({id:`t${i}`,name:`Level ${i+1}`,key:`T${i}`,color:'#888888'}))
const settings = Object.fromEntries(teams.slice(1,5).map((team,i)=>[team.id,{parentTeamId:`t${i}`}]))

describe('team hierarchy',()=>{
  it('counts the root as level one and validates a moved subtree',()=>{
    const h=teamHierarchy(teams,settings)
    expect(h.parentError('', 't3')).toBeUndefined()
    expect(h.parentError('', 't4')).toMatch(/five levels/)
    expect(h.parentError('t0','t5')).toMatch(/five levels/)
    expect(h.parentError('t0','t4')).toMatch(/ancestor/)
    expect(h.parentError('t1','t5')).toBeUndefined()
    expect(h.parentError('t1','')).toBeUndefined()
  })
  it('keeps hierarchy, paths, filtered descendants, and collapsed branches consistent',()=>{
    const h=teamHierarchy(teams,settings)
    expect(h.rows().map(row=>row.depth)).toEqual([0,1,2,3,4,0])
    expect(h.path('t4')).toBe('Level 1 › Level 2 › Level 3 › Level 4 › Level 5')
    expect(h.rows(teams,new Set(['t1'])).map(row=>row.team.id)).toEqual(['t0','t1','t5'])
    expect([...h.subtree('t2')]).toEqual(['t2','t3','t4'])
    expect(h.rows([teams[4]])[0].depth).toBe(0)
  })
  it('does not hide teams with inaccessible parents or legacy cycles',()=>{
    expect(teamHierarchy(teams.slice(1,5),settings).rows().map(row=>row.depth)).toEqual([0,1,2,3])
    expect(teamHierarchy(teams,{...settings,t0:{parentTeamId:'t4'}}).rows()).toHaveLength(6)
  })

  it('resolves inherited estimate settings without mutating child settings',()=>{
    const teams = [{id:'parent',name:'Parent',key:'P'},{id:'child',name:'Child',key:'C'}] as Team[]
    const settings = {
      parent: { teamId:'parent', parentTeamId:'', inheritIssueEstimation:false, estimateType:'fibonacci' },
      child: { teamId:'child', parentTeamId:'parent', inheritIssueEstimation:true, estimateType:'notUsed' },
    } as unknown as Record<string, import('@/types/flow').TeamSettings>
    expect(resolvedTeamSettings(settings,'child')?.estimateType).toBe('fibonacci')
    expect(settings.child.estimateType).toBe('notUsed')
    expect(teams).toHaveLength(2)
  })
})

describe('tree guide levels',()=>{
  it('keeps a level running past every sibling but the last, and past an earlier sibling’s own sub-teams',()=>{
    // Parent > {first > {nested}, middle, last}, then a second root with a single child.
    expect(treeGuideLevels([0,1,2,1,1,0,1])).toEqual([[],[1],[1],[1],[],[],[]])
    // Deeper: an ancestor level continues through its subtree; a last child ends its own level.
    expect(treeGuideLevels([0,1,2,2,3,1])).toEqual([[],[1],[1,2],[1],[1],[]])
    // A single child, and a flat list, draw no continuing guide.
    expect(treeGuideLevels([0,1])).toEqual([[],[]])
    expect(treeGuideLevels([0,0,0])).toEqual([[],[],[]])
  })
})

describe('team row tree',()=>{
  it('nests each sub-team under its parent in row order',()=>{
    const tree=teamRowTree(teamHierarchy(teams,{t1:{parentTeamId:'t0'},t2:{parentTeamId:'t0'},t3:{parentTeamId:'t2'}}).rows())
    const ids=(nodes:ReturnType<typeof teamRowTree>):unknown=>nodes.map(node=>node.children.length?[node.team.id,ids(node.children)]:node.team.id)
    expect(ids(tree)).toEqual([['t0',['t1',['t2',['t3']]]],'t4','t5'])
  })
})

describe('sibling order',()=>{
  const named=(name:string,createdAt?:string)=>({name,createdAt}) as Team
  it('sorts alphabetically ignoring case, and newest first by creation time with the list order as the tie-break',()=>{
    const list=[named('sub','2026-11-10T07:06:50Z'),named('Flow parity third','2026-11-11T10:25:19Z'),named('Flow parity sibling','2026-11-11T10:24:48Z')]
    expect(sortTeamSiblings(list).map(team=>team.name)).toEqual(['Flow parity sibling','Flow parity third','sub'])
    expect(sortTeamSiblings(list,'newest').map(team=>team.name)).toEqual(['Flow parity third','Flow parity sibling','sub'])
    expect(sortTeamSiblings([named('a'),named('b'),named('c')],'newest').map(team=>team.name)).toEqual(['c','b','a'])
    expect(sortTeamSiblings(list,'given')).toEqual(list)
  })
  it('orders every nesting level of the rows by the requested order',()=>{
    const all=[{id:'r',name:'Root'},{id:'z',name:'Zed'},{id:'a',name:'Alpha'},{id:'a2',name:'Beta'},{id:'a1',name:'Aardvark'}] as Team[]
    const h=teamHierarchy(all,{z:{parentTeamId:'r'},a:{parentTeamId:'r'},a2:{parentTeamId:'a'},a1:{parentTeamId:'a'}})
    const ids=(order?:'alphabetical'|'newest'|'given')=>h.rows(all,undefined,order).map(row=>`${row.team.id}:${row.depth}`)
    expect(ids()).toEqual(['r:0','a:1','a1:2','a2:2','z:1'])
    expect(ids('given')).toEqual(['r:0','z:1','a:1','a2:2','a1:2'])
    // Without creation times the list order is the creation order, so the last-listed sibling comes first.
    expect(ids('newest')).toEqual(['r:0','a:1','a1:2','a2:2','z:1'])
    // 'sidebar': sub-teams newest first, top-level teams as given.
    expect(h.rows([...all,{id:'r2',name:'Another'} as Team],undefined,'sidebar').map(row=>`${row.team.id}:${row.depth}`)).toEqual(['r:0','a:1','a1:2','a2:2','z:1','r2:0'])
    expect(h.rows(all.slice().reverse(),undefined,'newest').map(row=>row.team.id)).toEqual(['r','z','a','a2','a1'])
  })
})
