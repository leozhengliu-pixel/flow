import { describe, expect, it } from 'vitest'
import { FILTER_NULL_SENTINEL, rewriteWithinToIn } from '../filter-block-helper'
import {
  CURRENT_USER_SENTINEL,
  DATE_NULL_SENTINEL,
  MY_TEAMS_SENTINEL,
  buildTeamFilterOptions,
  buildUserFilterOptions,
  createDateFilterBlock,
  createEstimateFilterBlock,
  createInitiativeFilterBlocks,
  defaultCompareOptionForDates,
  initiativeFilterStateToModel,
  parseNumberFilterInput,
} from './index'

describe('dateBlockFilterUtils (LS-0693)', () => {
  it('exposes null sentinel, relative compare defaults, and within→in', () => {
    expect(DATE_NULL_SENTINEL).toBe(FILTER_NULL_SENTINEL)
    expect(defaultCompareOptionForDates([DATE_NULL_SENTINEL])).toBe('empty')
    expect(defaultCompareOptionForDates(['-P1W'])).toBe('after')
    expect(defaultCompareOptionForDates(['P1M'])).toBe('before')
    expect(rewriteWithinToIn('within')).toBe('in')
    const block = createDateFilterBlock({
      id: 'dueDate',
      key: 'dueDate',
      name: 'Due date',
      entityType: 'issue',
    })
    expect(block.valueType).toBe('dateOrInterval')
    expect(block.inputType).toBe('fuzzyDate')
  })
})

describe('numberBlockFilterUtils (LS-0710)', () => {
  it('parses free-form numbers and builds estimate block', () => {
    expect(parseNumberFilterInput('3K').parsedNumber).toBe(3000)
    expect(parseNumberFilterInput('1.5M').parsedNumber).toBe(1_500_000)
    const estimate = createEstimateFilterBlock()
    expect(estimate.id).toBe('estimate')
    expect(estimate.valueType).toBe('number')
  })
})

describe('UserFilterBlockHelper + team utils (LS-0637 / LS-0719)', () => {
  it('builds current-user and my-teams options', () => {
    const me = { id: 'u1', displayName: 'Ada', email: 'ada@flow.dev' }
    const users = buildUserFilterOptions({
      users: [me, { id: 'u2', displayName: 'Bai', app: true }],
      currentUser: me,
      includeNoAssignee: true,
    })
    expect(users.some(option => option.id === CURRENT_USER_SENTINEL)).toBe(true)
    expect(users.some(option => option.id === '')).toBe(true)

    const teams = buildTeamFilterOptions({
      teams: [
        { id: 't1', name: 'Platform' },
        { id: 't2', name: 'API', parentId: 't1' },
      ],
      includeMyTeams: true,
    })
    expect(teams[0]?.id).toBe(MY_TEAMS_SENTINEL)
    expect(teams.map(option => option.id)).toContain('t2')
  })
})

describe('initiativeBlockFilterUtils (LS-0703)', () => {
  it('packs blocks and converts FilterState to model', () => {
    const blocks = createInitiativeFilterBlocks()
    expect(blocks.some(block => block.key === 'ownerId')).toBe(true)
    const model = initiativeFilterStateToModel({ status: 'active', teamId: ['t1', 't2'] })
    expect(Array.isArray(model.and) ? model.and.length : 0).toBe(2)
  })
})
