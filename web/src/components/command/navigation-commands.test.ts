import { expect, it, vi } from 'vitest'
import { makeBootstrap, project } from '@/test/fixtures'
import { navigationOptions } from './navigation-commands'

it('opens teams, users and favorites through the same resource routes as the shell', () => {
  const data = makeBootstrap({ projects: [project], favorites: [] })
  const navigate = vi.fn(), close = vi.fn()
  navigationOptions(data, { id: 'teamOpen', label: 'Open a team' }, [], navigate, close)[0].select?.()
  expect(navigate).toHaveBeenLastCalledWith('/workspace/team/TST/overview')
  navigationOptions(data, { id: 'userOpen', label: 'Open a user' }, [], navigate, close)[0].select?.()
  expect(navigate).toHaveBeenLastCalledWith(expect.stringContaining('/workspace/profiles/'))
  data.favorites = [{ id: 'favorite-project', resourceType: 'project', resourceId: project.id, userId: data.viewer.id, position: 0, createdAt: '2026-10-30T00:00:00Z' }]
  const favorites = navigationOptions(data, { id: 'favoriteOpen', label: 'Open a favorite' }, [], navigate, close)
  expect(favorites[0].label).toBe(project.name)
  favorites[0].select?.()
  expect(navigate).toHaveBeenLastCalledWith(`/workspace/project/${project.slugId}/overview`)
  expect(close).toHaveBeenCalledTimes(3)
})
