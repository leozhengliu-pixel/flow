import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('aligns label text with its editable hit target and preserves sorting and editing', async ({ page, request, workspaceKey }) => {
  for (const name of ['Alpha', 'Zulu']) {
    const response = await request.post('/api/labels', { headers: { 'X-Workspace-Key': workspaceKey }, data: { name, resourceType: 'issue' } })
    expect(response.status()).toBe(201)
  }
  await page.goto(`/${workspaceKey}/settings/issue-labels`)
  await page.evaluate(() => document.fonts.ready)
  const input = page.getByRole('textbox', { name: 'Alpha', exact: true })
  const text = page.getByTestId('domain-label-name').filter({ hasText: /^Alpha$/ })
  expect(Math.abs((await input.boundingBox())!.x - (await text.boundingBox())!.x)).toBeLessThan(9)
  const inactive = page.getByRole('button', { name: 'Order by Created', exact: true }).locator('svg')
  await expect(inactive).toHaveCSS('visibility', 'hidden')
  await expect(inactive).toHaveCSS('display', 'block')
  if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/desktop-label-table.png` })
  await page.getByRole('button', { name: 'Order by Name, sorted ascending' }).click()
  await expect(page.getByTestId('domain-label-name').first()).toHaveText('Zulu')
  await input.click()
  const editor = page.getByRole('textbox', { name: 'Edit Name', exact: true })
  await expect(editor).toBeFocused()
  await editor.fill('Beta')
  await editor.press('Enter')
  await expect(page.getByRole('textbox', { name: 'Beta', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('textbox', { name: 'Beta', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})
