import { expect } from '@playwright/test'
import { test, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('keeps keyboard input in a newly chosen phone filter stage', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  await page.locator('.flow-inbox__header-controls button[aria-label="Add filter"]').click()
  await page.getByRole('option', { name: 'Team', exact: true }).click()
  const menu = page.locator('.inbox-filter-phone-values')
  await expect(menu.getByRole('combobox')).toBeFocused()
  await page.keyboard.type('zz')
  await expect(menu.getByRole('combobox')).toHaveValue('zz')
  await capture(menu, 'flow-inbox-filter-focus')
})
