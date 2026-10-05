import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import type { TimelineZoom } from './project-timeline-model'

export type ProjectsDisplaySettings = {
  layout: 'list' | 'board' | 'timeline'
  grouping: string
  groupOrder: 'asc' | 'desc'
  subGrouping: string
  ordering: string
  orderingDirection: 'asc' | 'desc'
  showClosed: string
  showEmptyGroups: boolean
  /** Team projects pages only: keep projects whose lead team is the page's team (Linear's "Only show lead team projects"). */
  onlyLeadTeamProjects?: boolean
  properties: string[]
  /** Timeline layout zoom (Week / Month / Quarter / Year); persisted with the other display options. */
  timelineZoom?: TimelineZoom
}

export const DEFAULT_PROJECTS_DISPLAY: ProjectsDisplaySettings = {
  grouping: 'Status',
  groupOrder: 'asc',
  layout: 'list',
  onlyLeadTeamProjects: false,
  ordering: 'Name',
  orderingDirection: 'asc',
  properties: ['Milestones', 'Summary', 'Priority', 'Status', 'Health', 'Lead', 'Target date', 'Issues'],
  showClosed: 'All',
  showEmptyGroups: false,
  subGrouping: 'No grouping',
}

/** Linear's project display-property order; Flow-only properties sit where Linear shows them when the feature is on. */
const PROJECT_DISPLAY_PROPERTY_ORDER: Array<{ property: string; feature?: string }> = [
  { property: 'ID' },
  { property: 'Milestones' },
  { property: 'Summary' },
  { property: 'Priority' },
  { property: 'Status' },
  { property: 'Health' },
  { property: 'Teams' },
  { property: 'Initiatives', feature: 'initiatives' },
  { property: 'Lead' },
  { property: 'Members' },
  { property: 'Dependencies' },
  { property: 'Start date' },
  { property: 'Target date' },
  { property: 'Issues' },
  { property: 'Created' },
  { property: 'Updated' },
  { property: 'Completed' },
  { property: 'Customers', feature: 'customer-requests' },
  { property: 'Customer revenue', feature: 'customer-requests' },
  { property: 'Labels' },
]

/** Display properties offered for the workspace's enabled features (an absent flag counts as enabled). */
export function projectDisplayProperties(featureFlags?: Record<string, boolean>) {
  return PROJECT_DISPLAY_PROPERTY_ORDER
    .filter(entry => !entry.feature || workspaceFeatureEnabled(featureFlags, entry.feature))
    .map(entry => entry.property)
}

/** Whether two display settings differ in anything the display menu controls (timeline zoom is not part of it). */
export function projectsDisplayEqual(left: ProjectsDisplaySettings, right: ProjectsDisplaySettings) {
  if (left.layout !== right.layout
    || left.grouping !== right.grouping
    || left.groupOrder !== right.groupOrder
    || left.subGrouping !== right.subGrouping
    || left.ordering !== right.ordering
    || left.orderingDirection !== right.orderingDirection
    || left.showClosed !== right.showClosed
    || left.showEmptyGroups !== right.showEmptyGroups
    || Boolean(left.onlyLeadTeamProjects) !== Boolean(right.onlyLeadTeamProjects)) return false
  const properties = new Set(left.properties)
  return properties.size === new Set(right.properties).size && right.properties.every(property => properties.has(property))
}

/**
 * Applies "Only show lead team projects". The projects list endpoint cannot filter by lead team, so this
 * runs on the loaded pages; every count on the page is derived from its result to stay consistent.
 */
export function filterProjectsByLeadTeam<T extends { leadTeamId?: string }>(projects: T[], display: Pick<ProjectsDisplaySettings, 'onlyLeadTeamProjects'>, teamId?: string) {
  if (!teamId || !display.onlyLeadTeamProjects) return projects
  return projects.filter(project => project.leadTeamId === teamId)
}

const LABEL_GROUP_PREFIX = 'Label group:'
export function projectLabelGroupProperty(id: string) { return `${LABEL_GROUP_PREFIX}${id}` }
export function projectLabelGroupId(property: string) { return property.startsWith(LABEL_GROUP_PREFIX) ? property.slice(LABEL_GROUP_PREFIX.length) : undefined }
