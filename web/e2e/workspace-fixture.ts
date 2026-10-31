import { test as base, expect } from '@playwright/test'
import { randomUUID } from 'node:crypto'

export const test = base.extend<{ workspaceKey: string; workspaceNamePrefix: string }>({
  workspaceNamePrefix: ['Verify', { option: true }],
  workspaceKey: async ({ request, workspaceNamePrefix }, provide) => {
    const urlKey = `browser-${randomUUID()}`
    const response = await request.post('/api/workspaces', {
      data: { name: `${workspaceNamePrefix} ${urlKey.slice(-8)}`, urlKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const { workspace } = await response.json() as { workspace: { urlKey: string } }
    await provide(workspace.urlKey)
  },
})
