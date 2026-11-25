import { expect } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { createFixtureWorkspace } from '../workspace-fixture'
import { readLocalRow, test } from './fixture'

test.use({ workspaceNamePrefix: 'Partials-budget' })

test('evicts history across workspaces before exceeding the origin budget', async ({ page, context, request, workspaceKey }, info) => {
  const otherKey = `browser-${randomUUID()}`
  const other = await createFixtureWorkspace(request, 'Other workspace', otherKey)
  expect(other.urlKey).toBe(otherKey)
  const seed = async (key: string, text: string) => {
    const headers = { 'X-Workspace-Key': key }
    expect((await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', language: 'en-US', timezone: 'UTC' } })).ok()).toBe(true)
    const { workspace, teams: [team], states } = await (await request.get('/api/bootstrap', { headers })).json() as {
      workspace: { id: string }; teams: { id: string; key: string }[]; states: { id: string; teamId?: string; type: string }[]
    }
    const state = states.find(state => state.teamId === team.id && state.type === 'unstarted') ?? states.find(state => !state.teamId && state.type === 'unstarted')
    expect(state).toBeDefined()
    const response = await request.post('/api/issues', { headers, data: { title: `Retained issue in ${key}`, teamId: team.id, stateId: state!.id } })
    expect(response.status()).toBe(201)
    const { id, title } = await response.json()
    expect((await request.post(`/api/issues/${id}/comments`, { headers, data: { body: text } })).status()).toBe(201)
    return { workspaceId: workspace.id as string, path: `/${key}/team/${team.key.toLowerCase()}/all`, id, title, slice: `history:issue:${id}` }
  }
  const old = await seed(workspaceKey, 'Old activity slice')
  const fresh = await seed(other.urlKey, 'New activity slice')
  const open = async (tab: typeof page, issue: typeof old) => {
    await tab.goto(issue.path, { waitUntil: 'commit' })
    await expect(tab.getByText(issue.title, { exact: true })).toBeVisible()
    await expect.poll(() => readLocalRow(tab, 'workspace', '_meta', issue.workspaceId)).toMatchObject({ resume: { schema: 2 } })
    await expect.poll(() => readLocalRow(tab, issue.id, 'issue', issue.workspaceId)).toMatchObject({ title: issue.title })
    await expect.poll(() => tab.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  }
  const meta = async (tab: typeof page, workspaceId: string) => {
    let checkpoint: { bytes: number; sliceBytes?: number } | undefined
    await expect.poll(async () => {
      checkpoint = await readLocalRow(tab, 'workspace', '_meta', workspaceId) as unknown as typeof checkpoint
      return checkpoint?.bytes
    }).toEqual(expect.any(Number))
    return checkpoint!
  }
  await open(page, old)
  await page.getByText(old.title, { exact: true }).click()
  await expect.poll(() => readLocalRow(page, old.slice, '_partial_activity', old.workspaceId)).toMatchObject({ complete: true })
  const next = await context.newPage()
  await open(next, fresh)
  const before = await meta(page, old.workspaceId)
  const current = await meta(next, fresh.workspaceId)
  const narrow = before.bytes + current.bytes + Math.floor((before.sliceBytes ?? 0) * 1.5)
  const estimate = await next.evaluate(async budget => {
    Object.defineProperty(navigator, 'storage', {
      configurable: true,
      value: { estimate: async () => ({ usage: 0, quota: budget * 5 }) },
    })
    return navigator.storage.estimate()
  }, narrow)
  expect(estimate).toEqual({ usage: 0, quota: narrow * 5 })
  await next.getByText(fresh.title, { exact: true }).click()
  await expect(next.getByText('New activity slice', { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(next, fresh.slice, '_partial_activity', fresh.workspaceId)).toMatchObject({ complete: true })
  await expect.poll(() => readLocalRow(page, old.slice, '_partial_activity', old.workspaceId)).toBeUndefined()
  const after = await meta(page, old.workspaceId)
  const saved = await meta(next, fresh.workspaceId)
  const used = after.bytes + saved.bytes + (after.sliceBytes ?? 0) + (saved.sliceBytes ?? 0)
  console.log(`local-store ${info.project.name}: retained ${used} bytes across two workspaces within ${narrow} byte budget`)
  expect(used).toBeLessThanOrEqual(narrow)
  expect(await readLocalRow(page, old.id, 'issue', old.workspaceId)).toMatchObject({ title: old.title })
  expect(await readLocalRow(page, fresh.id, 'issue', fresh.workspaceId)).toMatchObject({ title: fresh.title })
})
