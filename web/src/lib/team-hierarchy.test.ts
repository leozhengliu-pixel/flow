import { describe, expect, it } from 'vitest'
import { teamHierarchy } from './team-hierarchy'
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
})
