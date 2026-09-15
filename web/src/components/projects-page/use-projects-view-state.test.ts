import { describe, expect, it } from 'vitest'

import type { ProjectStatus } from '@/types/flow'
import type { ProjectPageItem } from './projects-data-view'
import { DEFAULT_PROJECTS_DISPLAY } from './projects-display-model'
import { groupProjectsForView, projectStatusesForLayout, type ProjectsViewState } from './use-projects-view-state'

const statuses: ProjectStatus[] = [
  { id: 'status-backlog', name: '待规划', color: '#8a8d93', type: 'backlog', position: 0 },
  { id: 'status-planned', name: '规划中', color: '#d6b326', type: 'planned', position: 1 },
  { id: 'status-building', name: '建设中', color: '#f2c94c', type: 'started', position: 2 },
  { id: 'status-running', name: '运行中', color: '#4aa3f7', type: 'started', position: 3 },
  { id: 'status-completed', name: '已完成', color: '#5e6ad2', type: 'completed', position: 4 },
  { id: 'status-canceled', name: '已取消', color: '#95a2b3', type: 'canceled', position: 5 },
]

function project(status: ProjectStatus, overrides: Partial<ProjectPageItem> = {}): ProjectPageItem {
  return {
    id: `project-${status.id}`,
    name: `${status.name}项目`,
    health: 'on-track',
    priority: 'none',
    issueCount: 0,
    progress: 0,
    status: status.name,
    statusId: status.id,
    statusType: status.type,
    statusColor: status.color,
    updatedAt: new Date().toISOString(),
    ...overrides,
  }
}

function state(overrides: Partial<ProjectsViewState['display']> = {}): ProjectsViewState {
  return {
    display: { ...DEFAULT_PROJECTS_DISPLAY, grouping: 'Status', layout: 'board', ordering: 'Status', showEmptyGroups: true, ...overrides },
    selectedIds: [],
    sort: { column: 'status', direction: 'asc' },
  }
}

describe('workspace project status dictionaries', () => {
  it('orders custom statuses by lifecycle type and position for each layout', () => {
    expect(projectStatusesForLayout(statuses, 'board').map(status => status.name)).toEqual([
      '待规划', '规划中', '建设中', '运行中', '已完成', '已取消',
    ])
    expect(projectStatusesForLayout(statuses, 'list').map(status => status.name)).toEqual([
      '运行中', '建设中', '规划中', '待规划', '已完成', '已取消',
    ])
  })

  it('builds empty groups from the workspace dictionary without English defaults', () => {
    const groups = groupProjectsForView([project(statuses[2])], state(), projectStatusesForLayout(statuses, 'board'))
    expect(groups.map(group => group.name)).toEqual(['待规划', '规划中', '建设中', '运行中', '已完成', '已取消'])
    expect(groups.find(group => group.name === '建设中')?.color).toBe('#f2c94c')
    expect(groups.flatMap(group => group.projects)).toHaveLength(1)
    expect(groups.some(group => ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled'].includes(group.name))).toBe(false)
  })

  it('uses status type rather than its translated name for closed windows', () => {
    const recent = new Date().toISOString()
    const old = new Date(Date.now() - 40 * 24 * 60 * 60 * 1000).toISOString()
    const projects = [
      project(statuses[2], { id: 'active-old', updatedAt: old }),
      project(statuses[4], { id: 'completed-recent', updatedAt: recent }),
      project(statuses[4], { id: 'completed-old', updatedAt: old }),
      project(statuses[5], { id: 'canceled-old', updatedAt: old }),
    ]

    const none = groupProjectsForView(projects, state({ showClosed: 'None', showEmptyGroups: false }), statuses)
    expect(none.flatMap(group => group.projects).map(item => item.id)).toEqual(['active-old'])

    const pastWeek = groupProjectsForView(projects, state({ showClosed: 'Past week', showEmptyGroups: false }), statuses)
    expect(pastWeek.flatMap(group => group.projects).map(item => item.id).sort()).toEqual(['active-old', 'completed-recent'])
  })

  it('keeps renamed statuses matched by stable ID', () => {
    const renamed = statuses.map(status => status.id === 'status-completed' ? { ...status, name: '交付完毕' } : status)
    const staleProject = project(statuses[4], { status: '旧完成名称', statusId: 'status-completed', statusType: undefined })
    const groups = groupProjectsForView([staleProject], state({ showClosed: 'None', showEmptyGroups: false }), renamed)
    expect(groups).toEqual([])
  })

  it('reflects additions and removals in empty groups immediately', () => {
    const added = { id: 'status-review', name: '待评审', color: '#24b4c7', type: 'started', position: 3.5 }
    const withAdded = projectStatusesForLayout([...statuses, added], 'board')
    expect(groupProjectsForView([], state(), withAdded).map(group => group.name)).toContain('待评审')

    const withoutPlanned = withAdded.filter(status => status.id !== 'status-planned')
    expect(groupProjectsForView([], state(), withoutPlanned).map(group => group.name)).not.toContain('规划中')
  })
})
