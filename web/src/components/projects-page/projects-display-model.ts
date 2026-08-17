export type ProjectsDisplaySettings = {
  layout: 'list' | 'board' | 'timeline'
  grouping: string
  groupOrder: 'asc' | 'desc'
  subGrouping: string
  ordering: string
  orderingDirection: 'asc' | 'desc'
  showClosed: string
  showEmptyGroups: boolean
  properties: string[]
}

export const DEFAULT_PROJECTS_DISPLAY: ProjectsDisplaySettings = {
  grouping: 'Status',
  groupOrder: 'asc',
  layout: 'list',
  ordering: 'Name',
  orderingDirection: 'asc',
  properties: ['Milestones', 'Priority', 'Status', 'Health', 'Lead', 'Target date', 'Issues'],
  showClosed: 'All',
  showEmptyGroups: false,
  subGrouping: 'No grouping',
}
