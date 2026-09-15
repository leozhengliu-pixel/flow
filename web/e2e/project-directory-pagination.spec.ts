import { expect, test } from '@playwright/test'

test('renders the first project page and requests more only at the viewport end', async ({ page, request }, testInfo) => {
  const workspaceKey = `project-pages-${testInfo.project.name}-${Date.now()}`
  const created = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Project pagination', urlKey: workspaceKey, region: 'us' },
  })
  expect(created.status()).toBe(201)
  const workspace = await created.json()
  const team = workspace.teams[0]
  const calls: string[] = []
  await page.route('**/api/projects?*', async route => {
    const url = new URL(route.request().url())
    const offset = Number(url.searchParams.get('cursor') || 0)
    calls.push(url.search)
    const items = Array.from({ length: 100 }, (_, index) => {
      const number = offset + index
      return {
        id: `project-${number}`, name: `Project ${String(number).padStart(4, '0')}`, slugId: `project-${number}`, summary: 'Paged project', description: '', icon: 'Project', color: '#6acddb',
        priority: 0, position: number, priorityLabel: 'No priority', progress: 0.5, health: 'onTrack', status: { id: 'ps_progress', name: 'In Progress', type: 'started', color: '#e2b714', position: 2 },
        memberIds: [], labelIds: [], teamIds: [team.id], dependencyIds: [], initiatives: [], customers: [], resources: [], milestones: [], comments: [], descriptionRevisions: [], updateCadence: 'none', issueCount: 0,
        createdAt: '2026-09-15T00:00:00Z', updatedAt: '2026-09-15T00:00:00Z',
      }
    })
    await route.fulfill({ json: { items, total: 4549, hasMore: true, nextCursor: String(offset + 100) } })
  })
  const bootstrapRequest = page.waitForRequest(request => request.url().endsWith('/api/bootstrap') || request.url().endsWith('/api/issue-records/bootstrap'))
  await page.goto(`/${workspaceKey}/projects/all`)
  expect((await bootstrapRequest).headers()['x-flow-projection']).toBe('project-list')
  await expect(page.getByText('Project 0000', { exact: true })).toBeVisible()
  await expect.poll(() => calls.length).toBe(1)
  expect(await page.locator('.lp-project-row').count()).toBeLessThan(50)
  const scroller = page.locator('[data-virtual-columns] > div').nth(1)
  await scroller.evaluate(element => { element.scrollTop = element.scrollHeight })
  await expect.poll(() => calls.length).toBeGreaterThanOrEqual(2)
  await expect(page.getByText('Project 0100', { exact: true })).toBeAttached()
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
})
