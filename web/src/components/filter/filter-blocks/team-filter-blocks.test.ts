import { describe, expect, it } from 'vitest'
import {
  TeamFilterBlocks,
  allTeamFilterBlocks,
  membersBlock,
  ownersBlock,
  privateBlock,
  teamFilterBlocks,
} from './team-filter-blocks'
import {
  blocksDivider,
  flattenGroupedTeamFilterBlocks,
  getGroupedTeamFilterBlocks,
  groupedTeamFilterBlocks,
} from './grouped-team-filter-blocks'

describe('TeamFilterBlocks (LS-0578)', () => {
  it('exposes Linear core four blocks in allFilterBlocks order', () => {
    expect(allTeamFilterBlocks.map(block => block.id)).toEqual([
      'createdAt',
      'members',
      'owners',
      'private',
    ])
    expect(TeamFilterBlocks.allFilterBlocks).toBe(allTeamFilterBlocks)
  })

  it('maps members/owners selections to REST memberId/ownerId AST', () => {
    expect(
      membersBlock.toModelFilter?.({ compareOption: 'contains', values: ['u1', 'u2'] }),
    ).toEqual({ field: 'memberId', operator: 'in', values: ['u1', 'u2'] })
    expect(
      ownersBlock.toModelFilter?.({ compareOption: 'notContains', values: ['u9'] }),
    ).toEqual({ field: 'ownerId', operator: 'notIn', values: ['u9'] })
  })

  it('maps private block to eq true/false', () => {
    expect(privateBlock.toModelFilter?.({ compareOption: 'is', values: ['true'] })).toEqual({
      field: 'private',
      operator: 'eq',
      values: ['true'],
    })
    expect(privateBlock.toModelFilter?.({ compareOption: 'not', values: ['true'] })).toEqual({
      field: 'private',
      operator: 'eq',
      values: ['false'],
    })
  })

  it('keeps discovery blocks in the registry pack', () => {
    expect(teamFilterBlocks.some(block => block.id === 'name')).toBe(true)
    expect(teamFilterBlocks.some(block => block.id === 'retired')).toBe(true)
  })
})

describe('GroupedTeamFilterBlocks (LS-0289)', () => {
  it('packs members, owners, divider(private), createdAt', () => {
    expect(groupedTeamFilterBlocks.map(block => block.id)).toEqual([
      'members',
      'owners',
      'private',
      'createdAt',
    ])
    const privateEntry = groupedTeamFilterBlocks[2]
    expect(privateEntry && 'divider' in privateEntry && privateEntry.divider).toBe(true)
    expect(blocksDivider(privateBlock).divider).toBe(true)
  })

  it('flattens divider metadata for registry use', () => {
    const flat = flattenGroupedTeamFilterBlocks(getGroupedTeamFilterBlocks())
    expect(flat.every(block => !('divider' in block && block.divider))).toBe(true)
    expect(flat.map(block => block.id)).toEqual(['members', 'owners', 'private', 'createdAt'])
  })
})
