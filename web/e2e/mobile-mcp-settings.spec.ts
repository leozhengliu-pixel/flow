import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ launchOptions: { args: ['--force-device-scale-factor=3'] } })

for (const width of [375, 390]) {
  test.describe(`MCP settings at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 }, deviceScaleFactor: 3 })
    test('opens connector settings from navigation with an unobstructed stacked selector', async ({ page, workspaceKey }) => {
      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      await page.getByRole('button', { name: 'Settings navigation' }).click()
      await page.getByRole('button', { name: 'MCP connectors', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/mcps$`))
      await expect(page.getByRole('heading', { name: 'MCP connectors', exact: true })).toBeVisible()
      await expect(page.getByRole('checkbox', { name: 'Enable connectors' })).toBeVisible()
      const row = page.locator('.settings-row-select')
      const copy = await row.locator('.settings-row-copy').boundingBox()
      const control = await page.getByRole('combobox', { name: 'Manage connectors' }).boundingBox()
      expect(control!.y).toBeGreaterThanOrEqual(copy!.y + copy!.height)
      expect(control!.width).toBeGreaterThanOrEqual(copy!.width - 1)
      expect(control!.x).toBeGreaterThanOrEqual(0)
      expect(control!.x + control!.width).toBeLessThanOrEqual(width)
    })
  })
}
