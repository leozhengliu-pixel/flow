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

test('directory search fields own their paint and filter controls retain usable hit targets', async ({ page, workspaceKey }) => {
  await page.emulateMedia({ colorScheme: 'light' })
  for (const [route, name] of [['teams', 'Filter by name…'], ['members', 'Search by name or email']]) {
    await page.goto(`/${workspaceKey}/settings/${route}`)
    const search = page.getByRole('textbox', { name, exact: true })
    await expect(search).toHaveCSS('padding', '6px 25px 6px 32px')
    await expect(search).toHaveCSS('border-radius', '8px')
    expect((await search.boundingBox())!.width).toBe(300)
    await search.fill('no matching directory entry')
    await expect(page.locator('.settings-member-directory-row')).toHaveCount(0)
    await search.clear()
    await expect(page.locator('.settings-member-directory-row').first()).toBeVisible()
    if (route === 'members') {
      const filter = page.getByRole('button', { name: 'Member filter', exact: true })
      await expect(filter).toHaveCSS('padding', '1px 28px 1px 10px')
      await filter.click()
      await expect(page.getByRole('menuitemradio', { name: 'All', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(page.getByRole('menuitemradio', { name: 'All', exact: true })).toBeHidden()
    }
    if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/desktop-${route}-directory.png` })
  }
})
