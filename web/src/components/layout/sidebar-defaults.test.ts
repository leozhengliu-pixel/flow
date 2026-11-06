import { renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useSidebarCustomizationState } from './sidebar-customization-state'

afterEach(() => localStorage.clear())

describe('sidebar defaults', () => {
  it('shows Initiatives, Projects, Views and Loops; Customers, Members, Releases and Teams go under More', () => {
    const { result } = renderHook(() => useSidebarCustomizationState('usr_new'))
    expect(result.current.preferences).toMatchObject({ initiatives: 'always', projects: 'always', loops: 'always', views: 'always', members: 'never', teams: 'never', releases: 'never' })
    expect(result.current.order.workspace.slice(0, 4)).toEqual(['initiatives', 'projects', 'views', 'loops'])
  })

  it('lists the More entries in the reference app order: Customers, Members, Releases, Teams', () => {
    const { result } = renderHook(() => useSidebarCustomizationState('usr_order'))
    expect(result.current.order.workspace.slice(4)).toEqual(['customers', 'members', 'releases', 'teams'])
  })

  it('migrates a full legacy save: old defaults follow the new ones, real choices stay', () => {
    localStorage.setItem('flow.sidebar.preferences:usr_old', JSON.stringify({ members: 'always', teams: 'always', releases: 'always', loops: 'never', projects: 'always' }))
    localStorage.setItem('flow.sidebar.order:usr_old', JSON.stringify({ workspace: ['members', 'initiatives', 'projects', 'teams', 'views', 'dashboards', 'releases', 'loops', 'customers'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_old'))
    expect(result.current.preferences).toMatchObject({ members: 'never', teams: 'never', releases: 'never', loops: 'never', projects: 'always' })
    expect(result.current.order.workspace[0]).toBe('initiatives')
    expect(JSON.parse(localStorage.getItem('flow.sidebar.preference-overrides:usr_old') ?? '{}')).toEqual({ loops: 'never' })
  })
})

describe('sidebar order migration', () => {
  it('treats an older default order (before Loops existed) as uncustomised', () => {
    localStorage.setItem('flow.sidebar.order:usr_older', JSON.stringify({ workspace: ['members', 'initiatives', 'projects', 'teams', 'views', 'releases'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_older'))
    expect(result.current.order.workspace.slice(0, 4)).toEqual(['initiatives', 'projects', 'views', 'loops'])
  })

  it('treats the previous default order as uncustomised and drops the stale save', () => {
    localStorage.setItem('flow.sidebar.order:usr_prev', JSON.stringify({ personal: ['inbox', 'reviews', 'myIssues', 'pulse', 'drafts', 'agent'], workspace: ['initiatives', 'projects', 'loops', 'views', 'members', 'customers', 'releases', 'teams'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_prev'))
    expect(result.current.order.workspace).toEqual(['initiatives', 'projects', 'views', 'loops', 'customers', 'members', 'releases', 'teams'])
    expect(localStorage.getItem('flow.sidebar.order:usr_prev')).toBeNull()
  })

  it('keeps a rearranged order that starts like the previous default', () => {
    localStorage.setItem('flow.sidebar.order:usr_kept', JSON.stringify({ workspace: ['initiatives', 'projects', 'loops', 'views', 'teams', 'members', 'customers', 'releases'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_kept'))
    expect(result.current.order.workspace).toEqual(['initiatives', 'projects', 'loops', 'views', 'teams', 'members', 'customers', 'releases'])
  })

  it('keeps an order the user rearranged', () => {
    localStorage.setItem('flow.sidebar.order:usr_custom', JSON.stringify({ workspace: ['views', 'projects', 'initiatives'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_custom'))
    expect(result.current.order.workspace.slice(0, 3)).toEqual(['views', 'projects', 'initiatives'])
  })
})

describe('sidebar order written by older builds', () => {
  it('ignores entries an older build appended at the end', () => {
    localStorage.setItem('flow.sidebar.order:usr_appended', JSON.stringify({ workspace: ['members', 'initiatives', 'projects', 'teams', 'views', 'releases', 'loops', 'customers', 'dashboards'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_appended'))
    expect(result.current.order.workspace.slice(0, 4)).toEqual(['initiatives', 'projects', 'views', 'loops'])
    expect(localStorage.getItem('flow.sidebar.order:usr_appended')).toBeNull()
  })

  it('drops the Dashboards entry older builds stored (it is a Views tab now)', () => {
    localStorage.setItem('flow.sidebar.preference-overrides:usr_dash', JSON.stringify({ dashboards: 'always', loops: 'never' }))
    localStorage.setItem('flow.sidebar.order:usr_dash', JSON.stringify({ workspace: ['views', 'dashboards', 'projects', 'initiatives'] }))
    const { result } = renderHook(() => useSidebarCustomizationState('usr_dash'))
    expect(result.current.preferences).not.toHaveProperty('dashboards')
    expect(result.current.order.workspace).not.toContain('dashboards')
    expect(JSON.parse(localStorage.getItem('flow.sidebar.preference-overrides:usr_dash') ?? '{}')).toEqual({ loops: 'never' })
  })
})
