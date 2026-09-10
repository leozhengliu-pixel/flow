import { expect, it } from 'vitest'
import type { Initiative } from '@/types/flow'
import { countInitiativeFilterValues } from './initiative-filter-counts'

it('counts a large collection once instead of scanning it for every menu option',()=>{
  let reads=0
  const initiatives=Array.from({length:10000},(_,i)=>({get leadTeamId(){reads++;return `team-${i%100}`},contributingTeamIds:['a','a','b'],labelIds:['x']} as Initiative))
  const counts=countInitiativeFilterValues(initiatives,'leadTeamId')
  expect(reads).toBe(10000)
  expect(counts.get('team-99')).toBe(100)
  expect(countInitiativeFilterValues(initiatives,'teamId')).toEqual(new Map([['a',10000],['b',10000]]))
})
