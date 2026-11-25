import { expect, type Response } from '@playwright/test'
import { test } from './workspace-fixture'

test.setTimeout(90_000)

function projectPage(response: Response) {
  return response.request().method() === 'GET' && new URL(response.url()).pathname === '/api/projects'
}

test('renders the first project page and requests more only at the viewport end', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap?projection=project-list', { headers })
  expect(bootstrap.ok()).toBeTruthy()
  const { teams: [team] } = await bootstrap.json()
  for (let index = 0; index < 110; index++) {
    const response = await request.post('/api/projects', {
      headers, data: { name: `Project ${String(index).padStart(4, '0')}`, teamIds: [team.id] },
    })
    expect(response.status()).toBe(201)
  }
  let calls = 0
  page.on('request', request => {
    if (request.method() === 'GET' && new URL(request.url()).pathname === '/api/projects') calls++
  })
  await page.addInitScript(key => {
    localStorage.setItem(`flow:projects:view:${key}:workspace:all`, JSON.stringify({
      grouping: 'No grouping', groupOrder: 'asc', layout: 'list', ordering: 'Name', orderingDirection: 'asc',
      properties: ['Priority', 'Status', 'Health', 'Lead'], showClosed: 'All', showEmptyGroups: false,
    }))
  }, workspaceKey)
  const bootstrapRequest = page.waitForRequest(request => request.url().endsWith('/api/bootstrap') || request.url().endsWith('/api/issue-records/bootstrap'))
  const firstPage = page.waitForResponse(projectPage)
  await page.goto(`/${workspaceKey}/projects/all`)
  expect((await bootstrapRequest).headers()['x-flow-projection']).toBe('project-list')
  const { items: firstItems, nextCursor } = await (await firstPage).json() as { items: { name: string }[]; nextCursor?: string }
  expect(firstItems).toHaveLength(100)
  expect(nextCursor).toBeTruthy()
  const firstVisible = firstItems.map(item => item.name).sort()[0]
  await expect(page.getByText(firstVisible, { exact: true })).toBeVisible()
  expect(calls).toBe(1)
  expect(await page.locator('.lp-project-row').count()).toBeLessThan(50)
  const scroller = page.locator('[data-virtual-columns] > div').nth(1)
  const secondPage = page.waitForResponse(projectPage)
  await scroller.evaluate(element => { element.scrollTop = element.scrollHeight })
  const { items: lastItems } = await (await secondPage).json() as { items: { name: string }[] }
  expect(lastItems).toHaveLength(10)
  expect(calls).toBe(2)
  await scroller.evaluate(element => { element.scrollTop = 0 })
  await expect(page.getByText('Project 0000', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
})
