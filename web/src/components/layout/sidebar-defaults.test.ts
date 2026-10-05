import { renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useSidebarCustomizationState } from './sidebar-customization-state'

afterEach(() => localStorage.clear())

describe('sidebar defaults (Linear parity)', () => {
  it('shows Initiatives, Projects, Loops and Views; Members, Teams and Releases go under More', () => {
    const { result } = renderHook(() => useSidebarCustomizationState('usr_new'))
    expect(result.current.preferences).toMatchObject({ initiatives: 'always', projects: 'always', loops: 'always', views: 'always', members: 'never', teams: 'never', releases: 'never' })
    expect(result.current.order.workspace.slice(0, 4)).toEqual(['initiatives', 'projects', 'loops', 'views'])
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
    expect(result.current.order.workspace.slice(0, 4)).toEqual(['initiatives', 'projects', 'loops', 'views'])
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
    expect(result.current.order.workspace.slice(0, 4)).toEqual(['initiatives', 'projects', 'loops', 'views'])
    expect(localStorage.getItem('flow.sidebar.order:usr_appended')).toBeNull()
  })
})
