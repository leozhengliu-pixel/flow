/**
 * LS-0286 GroupedIssueFilterBlocks — Linear getGroupedIssueFilterBlocks pack.
 * Groups Relations / Dates / Product Intelligence / Customers / Project properties /
 * Releases around the flat issueFilterBlocks registry.
 */
import type { FilterBlockDefinition } from '../filter-block-types'
import { createEstimateFilterBlock } from '../block-utils'
import { issueFilterBlocks } from './issue-filter-blocks'

export interface IssueFilterBlockGroup {
  /** Group label; omit for top-level ungrouped blocks. */
  name?: string
  blocks: FilterBlockDefinition[]
}

export interface GroupedIssueFilterBlocksOptions {
  /** Include estimate number block (LS-0710). Default true. */
  includeEstimate?: boolean
  /** Extra blocks appended at the end (custom fields, etc.). */
  extraBlocks?: FilterBlockDefinition[]
}

function byIds(ids: string[]): FilterBlockDefinition[] {
  const map = new Map(issueFilterBlocks.map(block => [block.id, block]))
  return ids.map(id => map.get(id)).filter((block): block is FilterBlockDefinition => Boolean(block))
}

/**
 * Returns a mixed list of flat FilterBlockDefinitions and named groups
 * (Linear `getGroupedIssueFilterBlocks` shape).
 */
export function getGroupedIssueFilterBlocks(
  _context?: unknown,
  options: GroupedIssueFilterBlocksOptions = {},
): Array<FilterBlockDefinition | IssueFilterBlockGroup> {
  const relations: IssueFilterBlockGroup = {
    name: 'Relations',
    blocks: byIds(['relations']),
  }
  const dates: IssueFilterBlockGroup = {
    name: 'Dates',
    blocks: byIds(['dates', 'addedToCycle']),
  }
  const intelligence: IssueFilterBlockGroup = {
    name: 'Suggestions',
    blocks: byIds(['suggestedLabel']),
  }
  const customers: IssueFilterBlockGroup = {
    name: 'Customers',
    blocks: byIds(['customers']),
  }
  const projectProperties: IssueFilterBlockGroup = {
    name: 'Project properties',
    blocks: byIds(['projectProperties', 'projectMilestone']),
  }
  const releases: IssueFilterBlockGroup = {
    name: 'Releases',
    blocks: byIds(['releases']),
  }

  const topLevel = byIds([
    'status',
    'assignee',
    'agent',
    'agentSession',
    'creator',
    'priority',
    'labels',
    'project',
    'initiative',
    'cycle',
    'subscribers',
    'externalSource',
    'autoClosed',
    'content',
    'links',
    'template',
  ])

  const estimate =
    options.includeEstimate === false ? [] : [createEstimateFilterBlock('issue')]

  return [
    ...topLevel.slice(0, 6), // status → priority
    ...estimate,
    ...topLevel.slice(6, 7), // labels
    relations,
    intelligence,
    dates,
    projectProperties,
    ...topLevel.slice(7, 10), // project, initiative, cycle
    releases,
    customers,
    ...topLevel.slice(10), // subscribers → template
    ...(options.extraBlocks ?? []),
  ]
}

/** Flatten groups to a registry-friendly FilterBlockDefinition list. */
export function flattenGroupedIssueFilterBlocks(
  grouped: Array<FilterBlockDefinition | IssueFilterBlockGroup> = getGroupedIssueFilterBlocks(),
): FilterBlockDefinition[] {
  const result: FilterBlockDefinition[] = []
  for (const entry of grouped) {
    if ('blocks' in entry && Array.isArray((entry as IssueFilterBlockGroup).blocks) && 'name' in entry) {
      result.push(...(entry as IssueFilterBlockGroup).blocks)
    } else {
      result.push(entry as FilterBlockDefinition)
    }
  }
  return result
}

export const groupedIssueFilterBlocks = getGroupedIssueFilterBlocks()
