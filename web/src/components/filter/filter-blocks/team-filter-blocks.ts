/**
 * LS-0578 TeamFilterBlocks — product filter-block pack for teams
 * (members / owners / private / createdAt), REST AST (no GraphQL).
 */
import { createDateFilterBlock } from '../block-utils/date-block-filter-utils'
import { UserFilterBlockHelper } from '../block-utils/user-filter-block-helper'
import type {
  FilterBlockDefinition,
  FilterBlockSelection,
  FilterModelNode,
  FilterValidationContext,
} from '../filter-block-types'

function userCollectionModel(
  field: 'memberId' | 'ownerId',
  selection: FilterBlockSelection,
): FilterModelNode {
  const values = selection.values.filter(Boolean)
  if (selection.compareOption === 'notContains' || selection.compareOption === 'not' || selection.compareOption === 'neither') {
    return { field, operator: 'notIn', values }
  }
  if (selection.compareOption === 'all' || selection.compareOption === 'notAll') {
    return { field, operator: selection.compareOption === 'all' ? 'all' : 'notAll', values }
  }
  return { field, operator: 'in', values }
}

function buildUserCollectionBlock(args: {
  id: string
  key: 'memberId' | 'ownerId'
  name: string
  sortPriority: number
}): FilterBlockDefinition {
  return {
    id: args.id,
    key: args.key,
    name: args.name,
    valueType: 'equalValue',
    inputType: 'options',
    entityType: 'team',
    sortPriority: args.sortPriority,
    defaultCompareOption: 'contains',
    options: (context?: FilterValidationContext) => {
      const users = (context?.users as Parameters<typeof UserFilterBlockHelper.buildUserFilterOptions>[0]['users'] | undefined) ?? []
      const currentUser = context?.currentUser as Parameters<typeof UserFilterBlockHelper.buildUserFilterOptions>[0]['currentUser'] | undefined
      return UserFilterBlockHelper.buildUserFilterOptions({
        users,
        currentUser,
        includeCurrentUserSentinel: true,
        includeNoAssignee: false,
      })
    },
    getCompareOption: selection =>
      selection.compareOption === 'notContains' || selection.compareOption === 'not' || selection.compareOption === 'neither'
        ? 'notContains'
        : 'contains',
    selectedValues: selection => selection.values,
    toModelFilter: selection => userCollectionModel(args.key, selection),
  }
}

/** Created date — Linear `createdAtBlock` (`Created date`, default after). */
export const createdAtBlock: FilterBlockDefinition = createDateFilterBlock({
  id: 'createdAt',
  key: 'createdAt',
  name: 'Created date',
  entityType: 'team',
  sortPriority: 10,
  defaultCompareOptionForDates: 'after',
  includeNullOption: false,
})

/** Members — Linear `membersBlock` / `users.id.in` → REST `memberId`. */
export const membersBlock: FilterBlockDefinition = buildUserCollectionBlock({
  id: 'members',
  key: 'memberId',
  name: 'Members',
  sortPriority: 20,
})

/** Owners — Linear `ownersBlock` / `owners.id.in` → REST `ownerId`. */
export const ownersBlock: FilterBlockDefinition = buildUserCollectionBlock({
  id: 'owners',
  key: 'ownerId',
  name: 'Owners',
  sortPriority: 30,
})

/** Private — Linear `privateBlock` (`private.eq`). */
export const privateBlock: FilterBlockDefinition = {
  id: 'private',
  key: 'private',
  name: 'Private',
  valueType: 'bool',
  inputType: 'options',
  entityType: 'team',
  sortPriority: 40,
  defaultCompareOption: 'is',
  options: [
    { id: 'true', label: 'Private', keywords: 'private locked' },
    { id: 'false', label: 'Public', keywords: 'public open' },
  ],
  getCompareOption: selection =>
    selection.compareOption === 'not' || selection.compareOption === 'neither' ? 'not' : 'is',
  toModelFilter: selection => {
    const wantPrivate = selection.values[0] !== 'false'
    const eq = selection.compareOption === 'not' || selection.compareOption === 'neither' ? !wantPrivate : wantPrivate
    return { field: 'private', operator: 'eq', values: [eq ? 'true' : 'false'] }
  },
}

/** Extra discovery blocks (directory / search) — not in Linear core four. */
export const teamNameBlock: FilterBlockDefinition = {
  id: 'name',
  key: 'name',
  name: 'Name',
  valueType: 'str',
  inputType: 'freeForm',
  entityType: 'team',
  sortPriority: 5,
  defaultCompareOption: 'contains',
  hideInMenuRoot: true,
  onlyVisibleThroughSearch: true,
}

export const teamParentBlock: FilterBlockDefinition = {
  id: 'parent',
  key: 'parentId',
  name: 'Parent team',
  valueType: 'equalValue',
  inputType: 'options',
  entityType: 'team',
  sortPriority: 15,
  defaultCompareOption: 'is',
  hideInMenuRoot: true,
  onlyVisibleThroughSearch: true,
}

export const teamRetiredBlock: FilterBlockDefinition = {
  id: 'retired',
  key: 'retired',
  name: 'Retired',
  valueType: 'bool',
  entityType: 'team',
  sortPriority: 50,
  defaultCompareOption: 'is',
  hideInMenuRoot: true,
  onlyVisibleThroughSearch: true,
}

/** Linear `allFilterBlocks` order: createdAt, members, owners, private. */
export const allTeamFilterBlocks: FilterBlockDefinition[] = [
  createdAtBlock,
  membersBlock,
  ownersBlock,
  privateBlock,
]

/** Registry pack used by RegisterFilterValues / getFilterBlocksPack('team'). */
export const teamFilterBlocks: FilterBlockDefinition[] = [
  teamNameBlock,
  teamParentBlock,
  ...allTeamFilterBlocks,
  teamRetiredBlock,
]

export const TeamFilterBlocks = {
  createdAtBlock,
  membersBlock,
  ownersBlock,
  privateBlock,
  allFilterBlocks: allTeamFilterBlocks,
  UserFilterBlockHelper,
}
