import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.use({ colorScheme: 'dark' })

test('replaces quick results without losing the command palette or keyboard selection', async ({ page, request, workspaceKey }, testInfo) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap', { headers })
  expect(bootstrap.ok()).toBeTruthy()
  const data = await bootstrap.json() as BootstrapData
  const created = await request.post('/api/issues', { headers, data: { title: 'Copy editing pass', teamId: data.teams[0].id } })
  expect(created.status()).toBe(201)
  const issue = await created.json() as Issue
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  await expect(page.locator('[data-content-view-header]')).toBeVisible()
  await page.keyboard.press('Meta+k')
  const palette = page.locator('[cmdk-root]')
  const input = palette.locator('[cmdk-input]')
  await input.fill('copy')
  const result = palette.getByRole('option', { name: /Copy editing pass/ })
  await expect(result).toBeVisible()
  const group = result.locator('xpath=ancestor::*[@cmdk-group]')
  const groupId = await group.getAttribute('data-value')
  await input.fill('no-matching-resource')
  await expect(result).toHaveCount(0)
  await expect(palette.locator(`[cmdk-group][data-value="${groupId}"]`)).toHaveCount(1)
  await input.fill('copy edit')
  await expect(result).toBeVisible()
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/command-search-${testInfo.project.name}.png`, animations: 'disabled' })
  await input.press('End')
  await expect(result).toHaveAttribute('aria-selected', 'true')
  await input.press('Enter')
  await expect(palette).toHaveCount(0)
  await expect(page).toHaveURL(new RegExp(`/issue/${issue.identifier}(?:[/?#]|$)`))
  expect(errors).toEqual([])
})
