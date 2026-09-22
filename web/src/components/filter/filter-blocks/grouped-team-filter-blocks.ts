/**
 * LS-0289 GroupedTeamFilterBlocks — thin pack: members, owners, divider(private), createdAt.
 */
import type { FilterBlockDefinition } from '../filter-block-types'
import {
  createdAtBlock,
  membersBlock,
  ownersBlock,
  privateBlock,
} from './team-filter-blocks'

export type TeamFilterBlockDivider = FilterBlockDefinition & {
  divider: true
}

export function blocksDivider(block: FilterBlockDefinition): TeamFilterBlockDivider {
  return { ...block, divider: true }
}

/**
 * Linear `groupedTeamFilterBlocks` shape:
 * [members, owners, divider(private), createdAt]
 */
export const groupedTeamFilterBlocks: Array<FilterBlockDefinition | TeamFilterBlockDivider> = [
  membersBlock,
  ownersBlock,
  blocksDivider(privateBlock),
  createdAtBlock,
]

export function getGroupedTeamFilterBlocks(): Array<FilterBlockDefinition | TeamFilterBlockDivider> {
  return groupedTeamFilterBlocks
}

export function flattenGroupedTeamFilterBlocks(
  items: Array<FilterBlockDefinition | TeamFilterBlockDivider> = groupedTeamFilterBlocks,
): FilterBlockDefinition[] {
  return items.map(item => {
    if ('divider' in item && item.divider) {
      const { divider: _divider, ...block } = item
      return block
    }
    return item
  })
}
