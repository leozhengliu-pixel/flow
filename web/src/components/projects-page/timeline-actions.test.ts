import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ClientStorage } from '@/lib/client-storage'
import {
  TIMELINE_SHOW_PROJECTS_ASIDE_KEY,
  activateAndSelectTimelineProject,
  buildTimelineActions,
  readTimelineShowProjectsAside,
  toggleTimelineShowProjectsAside,
  writeTimelineShowProjectsAside,
} from './timeline-actions'

describe('TimelineActions (LS-0608)', () => {
  beforeEach(() => {
    ClientStorage.remove(TIMELINE_SHOW_PROJECTS_ASIDE_KEY, 'local')
  })

  it('persists timelineShowProjectsAside preference', () => {
    expect(readTimelineShowProjectsAside()).toBe(true)
    writeTimelineShowProjectsAside(false)
    expect(readTimelineShowProjectsAside()).toBe(false)
    expect(toggleTimelineShowProjectsAside(false)).toBe(true)
    expect(readTimelineShowProjectsAside()).toBe(true)
  })

  it('builds toggle + select command actions', async () => {
    const setShowProjectsAside = vi.fn()
    const selectProject = vi.fn()
    const actions = buildTimelineActions({
      showProjectsAside: true,
      setShowProjectsAside,
      selectedProjectId: 'p1',
      selectProject,
    })
    expect(actions.map(action => action.id)).toEqual([
      'timeline.toggle-projects-aside',
      'timeline.select-project',
    ])
    await actions[0].run({})
    expect(setShowProjectsAside).toHaveBeenCalledWith(false)
    await actions[1].run({ payload: { projectId: 'p1' } })
    expect(selectProject).toHaveBeenCalledWith(undefined)
  })

  it('activates and selects a project', () => {
    const activateProject = vi.fn()
    const selectProject = vi.fn()
    activateAndSelectTimelineProject(
      {
        showProjectsAside: true,
        setShowProjectsAside: () => undefined,
        selectProject,
        activateProject,
      },
      'proj-9',
    )
    expect(activateProject).toHaveBeenCalledWith('proj-9')
    expect(selectProject).toHaveBeenCalledWith('proj-9')
  })
})
