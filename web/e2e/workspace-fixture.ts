import { test as base, expect, type APIRequestContext } from '@playwright/test'
import { randomUUID } from 'node:crypto'

export async function createFixtureWorkspace(request: APIRequestContext, name: string, urlKey: string) {
  const response = await request.post('/api/workspaces', {
    data: { name: `${name} ${urlKey.slice(-8)}`, urlKey, region: 'us', tutorial: false },
  })
  expect(response.status()).toBe(201)
  const { workspace } = await response.json() as { workspace: { id: string; urlKey: string } }
  return workspace
}

export const test = base.extend<{ workspaceKey: string; workspaceNamePrefix: string; workspaceURLKey?: string }>({
  workspaceNamePrefix: ['Verify', { option: true }],
  workspaceURLKey: [undefined, { option: true }],
  workspaceKey: async ({ request, workspaceNamePrefix, workspaceURLKey }, provide) => {
    const urlKey = workspaceURLKey ?? `browser-${randomUUID()}`
    const workspace = await createFixtureWorkspace(request, workspaceNamePrefix, urlKey)
    await provide(workspace.urlKey)
  },
})
