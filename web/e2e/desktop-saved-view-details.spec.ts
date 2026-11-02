import { test, expect } from './issue-list-fixture'
import type { SavedView } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop saved-view details')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('defaults the saved view details open, drills into unassigned issues, and remembers the toggle', async ({ page, request, workspaceKey, issueList }) => {
  const response = await request.post('/api/views', {
    headers: { 'X-Workspace-Key': workspaceKey },
    data: { name: '[audit] Saved queue', resource: 'issues', scope: 'personal', view: 'all', filters: [], display: { grouping: 'status' } },
  })
  expect(response.status()).toBe(201)
  const view = await response.json() as SavedView
  await page.goto(`/${workspaceKey}/view/${view.slugId || view.id}`)
  await expect(page.locator('[data-content-view-subheader] [class*="viewCount"]')).toHaveCSS('font-size', '13px')
  await expect(page.locator('[data-content-view-subheader] [class*="viewCount"]')).toHaveCSS('font-weight', '500')
  const panel = page.getByRole('complementary', { name: 'View sidebar' })
  await expect(panel).toBeVisible()
  await expect(panel.getByText('Personal', { exact: true })).toBeVisible()
  await expect(panel.getByText('Owner', { exact: true })).toBeVisible()
  await expect(panel.getByText('Owner', { exact: true })).toHaveCSS('font-size', '12px')
  await expect(panel.getByText('Visibility', { exact: true })).toHaveCSS('color', 'lch(63.304 1.425 272)')
  await expect(panel.getByText('Personal', { exact: true })).toHaveCSS('color', 'lch(100 0 272)')
  await expect(panel.getByRole('switch', { name: 'Add to favorites', exact: true })).toHaveCSS('width', '28px')
  await expect(panel.getByRole('tablist', { name: 'View summary' })).toHaveCSS('gap', '2px')
  await expect(page.locator('[data-content-view-header]')).toHaveCSS('padding-left', '8px')
  await expect(page.locator('[data-content-view-title] h2')).toHaveCSS('color', 'lch(90.451 1.2 272)')
  await expect(page.getByRole('button', { name: 'Close view details', exact: true })).toHaveCSS('background-color', 'lch(18.634 1.075 272)')
  await expect(panel.getByText(issueList.data.viewer.displayName, { exact: true }).first()).toBeVisible()
  expect((await panel.locator('[class*="identityCard"]').boundingBox())?.width).toBe(388)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-saved-view.png`, animations: 'disabled' })
  await panel.getByRole('button', { name: 'No assignee 2', exact: true }).click()
  await expect(page.locator('[data-issue-row-id]')).toHaveCount(2)
  await page.getByRole('button', { name: 'Clear all filters', exact: true }).click()
  await expect(page.locator('[data-issue-row-id]')).toHaveCount(3)
  await page.getByRole('button', { name: 'Close view details', exact: true }).click()
  await expect(panel).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole('button', { name: 'Open view details', exact: true })).toBeVisible()
  await expect(panel).toHaveCount(0)
  await page.getByRole('button', { name: 'Open view details', exact: true }).click()
  await page.reload()
  await expect(panel).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(panel).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Open view details', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(panel).toBeVisible()
})
