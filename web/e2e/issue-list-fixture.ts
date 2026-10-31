import { test as base } from './workspace-fixture'
import { expect } from '@playwright/test'
import type { BootstrapData, Issue } from '../src/types/flow'

export const test = base.extend<{ issueList: { data: BootstrapData; route: string; issues: Issue[] } }>({
  issueList: async ({ request, workspaceKey }, provide) => {
    const bootstrap = await request.get('/api/bootstrap', { headers: { 'X-Flow-Workspace': workspaceKey } })
    expect(bootstrap.ok()).toBeTruthy()
    const data = await bootstrap.json() as BootstrapData
    const team = data.teams[0]
    const state = data.states.find(state => state.type === 'unstarted')!
    const issues: Issue[] = []
    for (let index = 0; index < 3; index++) {
      const response = await request.post('/api/issues', {
        headers: { 'X-Flow-Workspace': workspaceKey },
        data: { title: `[audit] Queue check ${index + 1}`, teamId: team.id, stateId: state.id, priority: 2, ...(index === 0 ? { assigneeId: data.viewer.id } : {}) },
      })
      expect(response.status()).toBe(201)
      issues.push(await response.json() as Issue)
    }
    await provide({ data, route: `/${workspaceKey}/team/${team.key}/all`, issues })
  },
})

export { expect }
