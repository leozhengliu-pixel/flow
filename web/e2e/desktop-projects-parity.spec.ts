import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Project } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop project geometry')
test.use({ viewport: { width: 1440, height: 900 } })

test('honors row properties, opens an icon-only date picker and keeps the overview rail', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap', { headers })
  const data = await bootstrap.json() as BootstrapData
  const response = await request.post('/api/projects', { headers, data: { name: '[audit] Desktop project', summary: 'Delivery summary', teamIds: [data.teams[0].id] } })
  expect(response.ok()).toBeTruthy()
  const project = await response.json() as Project
  const milestone = await request.post(`/api/projects/${project.id}/milestones`, { headers, data: { name: 'Alpha' } })
  expect(milestone.ok()).toBeTruthy()
  await page.goto(`/${workspaceKey}/projects/all`)
  const row = page.getByRole('row', { name: project.name, exact: true })
  await expect(row).toBeVisible()
  await expect(row.locator('.lp-project-row__milestone')).toBeVisible()
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  const display = page.getByRole('dialog', { name: 'Display options', exact: true })
  await display.locator('.lp-projects-display-select').filter({ has: page.locator('> span', { hasText: /^Grouping$/ }) }).getByRole('combobox').click()
  await page.getByRole('option', { name: 'No grouping', exact: true }).click()
  await expect(page.locator('.lp-project-group__header')).toHaveCount(0)
  await display.getByRole('button', { name: 'Summary', exact: true }).click()
  await display.getByRole('button', { name: 'Milestones', exact: true }).click()
  await expect(row.locator('small').filter({ hasText: 'Delivery summary' })).toBeHidden()
  await expect(row.locator('.lp-project-row__milestone')).toBeHidden()
  await display.getByRole('button', { name: 'Milestones', exact: true }).click()
  await page.keyboard.press('Escape')
  const date = row.locator('.lp-project-row__date')
  await expect(date).toHaveCSS('width', '28px')
  await expect(date.locator('.lp-project-row__date-icon')).toBeVisible()
  await date.click()
  await expect(page.getByRole('dialog').last()).toBeVisible()
  await page.keyboard.press('Escape')
  await row.locator('strong').click()
  const rail = page.getByRole('complementary', { name: 'Project sidebar', exact: true })
  await expect(rail).toBeVisible()
  const overview = await page.locator('.project-overview').boundingBox()
  expect(Math.abs(overview!.x - 292.5)).toBeLessThanOrEqual(1)
  expect(Math.abs(overview!.width - 691)).toBeLessThanOrEqual(1)
})
