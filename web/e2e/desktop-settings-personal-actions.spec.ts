import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

for (const colorScheme of ['light', 'dark'] as const) {
  test.describe(`${colorScheme} desktop personal actions`, () => {
    test.use({ viewport: { width: 1440, height: 900 }, colorScheme })

    test('keeps sidebar customization compact and opens its dialog', async ({ page, workspaceKey }) => {
      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      const customize = page.getByRole('button', { name: 'Customize sidebar', exact: true })
      await expect(customize).toHaveText('Customize')
      await expect(customize).toHaveCSS('margin-right', '-6px')
      await expect(customize).toHaveCSS('font-weight', '500')
      await expect(customize).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
      await page.evaluate(() => document.fonts.ready)
      if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/desktop-settings-${colorScheme}.png` })
      await customize.click()
      await expect(page.getByRole('dialog', { name: 'Customize sidebar', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await page.goto(`/${workspaceKey}/settings/account/security`)
      await expect(page.getByRole('button', { name: 'New passkey', exact: true })).toHaveCSS('margin-right', '0px')
    })
  })
}
