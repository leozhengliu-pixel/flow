import { expect } from '@playwright/test'
import { test, bootstrap, capture } from './review-findings-fixture'

for (const width of [375, 390]) {
  test(`preserves project identity beside a long milestone ${width}`, async ({ page, request, workspaceKey }) => {
    const data = await bootstrap(request, workspaceKey)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const response = await request.post('/api/projects', { headers, data: { name: 'Compare Test', teamIds: [data.teams[0].id] } })
    expect(response.status()).toBe(201)
    const project = await response.json() as { id: string }
    expect((await request.post(`/api/projects/${project.id}/milestones`, { headers, data: { name: 'International customer migration and rollout readiness' } })).ok()).toBe(true)
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await page.goto(`/${workspaceKey}/projects/all`)
    const row = page.getByRole('row', { name: 'Compare Test', exact: true })
    await expect(row).toBeVisible()
    const name = (await row.locator('strong').boundingBox())!
    const fullNameWidth = await row.locator('strong').evaluate(element => element.scrollWidth)
    expect(name.width + 1).toBeGreaterThanOrEqual(fullNameWidth)
    await capture(row, `flow-project-milestone-${width}`)
  })
}
