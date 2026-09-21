import type { FilterBlockDefinition, FilterEntityType } from '../filter-block-types'
import { issueFilterBlocks } from './issue-filter-blocks'
import { projectFilterBlocks } from './project-filter-blocks'
import { initiativeFilterBlocks } from './initiative-filter-blocks'
import { notificationFilterBlocks } from './notification-filter-blocks'
import { teamFilterBlocks } from './team-filter-blocks'
import {
  baseFilterBlocks,
  customerFilterBlocks,
  documentFilterBlocks,
  feedItemFilterBlocks,
  memberFilterBlocks,
  pullRequestFilterBlocks,
  searchResultFilterBlocks,
  workflowDefinitionFilterBlocks,
} from './other-filter-blocks'

const PACKS: Record<FilterEntityType, () => FilterBlockDefinition[]> = {
  base: () => baseFilterBlocks,
  issue: () => issueFilterBlocks,
  project: () => projectFilterBlocks,
  initiative: () => initiativeFilterBlocks,
  team: () => teamFilterBlocks,
  pullRequest: () => pullRequestFilterBlocks,
  notification: () => notificationFilterBlocks,
  customer: () => customerFilterBlocks,
  searchResult: () => searchResultFilterBlocks,
  document: () => documentFilterBlocks,
  member: () => memberFilterBlocks,
  feedItem: () => feedItemFilterBlocks,
  workflowDefinition: () => workflowDefinitionFilterBlocks,
}

export function getFilterBlocksPack(type: FilterEntityType): FilterBlockDefinition[] {
  return PACKS[type]?.() ?? []
}

/** Async pack loader — keeps RegisterFilterValuesShouldBeLazyLoaded chunk-friendly. */
export async function loadFilterBlocksPack(type: FilterEntityType): Promise<FilterBlockDefinition[]> {
  switch (type) {
    case 'issue':
      return (await import('./issue-filter-blocks')).issueFilterBlocks
    case 'project':
      return (await import('./project-filter-blocks')).projectFilterBlocks
    case 'initiative':
      return (await import('./initiative-filter-blocks')).initiativeFilterBlocks
    case 'notification':
      return (await import('./notification-filter-blocks')).notificationFilterBlocks
    case 'team':
      return (await import('./team-filter-blocks')).teamFilterBlocks
    case 'pullRequest':
      return (await import('./other-filter-blocks')).pullRequestFilterBlocks
    case 'customer':
      return (await import('./other-filter-blocks')).customerFilterBlocks
    case 'document':
      return (await import('./other-filter-blocks')).documentFilterBlocks
    case 'member':
      return (await import('./other-filter-blocks')).memberFilterBlocks
    case 'feedItem':
      return (await import('./other-filter-blocks')).feedItemFilterBlocks
    case 'searchResult':
      return (await import('./other-filter-blocks')).searchResultFilterBlocks
    case 'workflowDefinition':
      return (await import('./other-filter-blocks')).workflowDefinitionFilterBlocks
    case 'base':
      return (await import('./other-filter-blocks')).baseFilterBlocks
    default:
      return []
  }
}

export {
  issueFilterBlocks,
  projectFilterBlocks,
  initiativeFilterBlocks,
  notificationFilterBlocks,
  teamFilterBlocks,
  pullRequestFilterBlocks,
  customerFilterBlocks,
  documentFilterBlocks,
  memberFilterBlocks,
  feedItemFilterBlocks,
  searchResultFilterBlocks,
  workflowDefinitionFilterBlocks,
  baseFilterBlocks,
}
