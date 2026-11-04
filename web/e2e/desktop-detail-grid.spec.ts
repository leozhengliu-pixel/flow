import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.use({ colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2'] } })

test('adapts the issue document and properties to the pane width', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const response = await request.post('/api/issues', { headers, data: { title: 'Responsive detail', teamId: data.teams[0].id } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as Issue
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await page.locator('.issue-view').waitFor()
  await expect(page.locator('.issue-layout')).toBeVisible()
  await page.evaluate(() => document.fonts.ready)

  for (const sample of [
    { width: 1200, documentX: 271.546875, documentWidth: 552.734375, railWidth: 313.125 },
    { width: 1440, documentX: 292.4140625, documentWidth: 667.5234375, railWidth: 375.734375 },
    { width: 1600, documentX: 300.5, documentWidth: 779, railWidth: 400 },
  ]) {
    await page.setViewportSize({ width: sample.width, height: 900 })
    const document = await page.locator('.issue-document').boundingBox()
    const rail = await page.locator('.issue-properties-rail').boundingBox()
    const title = await page.locator('.title-editor').boundingBox()
    expect(document).not.toBeNull()
    expect(rail).not.toBeNull()
    expect(Math.abs(document!.x - sample.documentX)).toBeLessThanOrEqual(0.02)
    expect(Math.abs(document!.width - sample.documentWidth)).toBeLessThanOrEqual(0.02)
    expect(Math.abs(rail!.width - sample.railWidth)).toBeLessThanOrEqual(0.02)
    expect(Math.abs(title!.width - (document!.width - 14))).toBeLessThanOrEqual(0.02)
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBe(0)
  }
})
