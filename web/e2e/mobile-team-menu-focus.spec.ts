import { expect } from '@playwright/test'
import { test, bootstrap, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('shows a keyboard outline on each focused team overflow destination', async ({ page, request, workspaceKey }) => {
  const data = await bootstrap(request, workspaceKey)
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
  await page.locator('.team-home-tabs [aria-haspopup]').click()
  await page.keyboard.press('Tab')
  const focused = page.locator('.team-home-tabs-menu-item:focus-visible')
  await expect(focused).toBeVisible()
  await expect(focused).toHaveCSS('outline-style', 'solid')
  await expect(focused).toHaveCSS('outline-width', '1px')
  await capture(page.locator('.team-home-tabs-menu'), 'flow-team-menu-focus')
})
