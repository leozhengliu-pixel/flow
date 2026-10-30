import { expect, test, type Locator, type Page } from '@playwright/test'

const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
const base = process.env.FLOW_MOBILE_BASE_URL ?? 'http://127.0.0.1:4173'
const shots = process.env.FLOW_MOBILE_SHOTS

async function geometry(locator: Locator, expected: Partial<{ x: number; y: number; width: number; height: number }>) {
  await expect(locator).toBeVisible()
  await expect.poll(async () => {
    const box = await locator.boundingBox()
    return box && Object.entries(expected).every(([key, value]) => Math.abs(box[key as keyof typeof box] - value) <= 1)
  }).toBeTruthy().catch(async error => {
    console.error({ expected, actual: await locator.boundingBox(), className: await locator.getAttribute('class') })
    throw error
  })
}

async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px shell`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })

    test('navigation, workspace menus, search, commands and composer geometry', async ({ page }) => {
      await page.goto(`${base}/verify/inbox`)
      const main = page.locator('.app > main, .app > .flow-framed-workspace')
      await geometry(main, { x: 0, y: 0, width: viewport.width, height: viewport.height })
      const trigger = page.locator('[data-sidebar-trigger]').first()
      await geometry(trigger, { x: 8, width: 32, height: 32 })
      await noOverflow(page)
      await trigger.click()
      const drawer = page.locator('[data-sidebar-surface]')
      await geometry(drawer, { x: 0, y: 0, width: 330, height: viewport.height })
      await geometry(drawer.locator('.nav-item').first(), { x: 12, y: 56, height: 28, width: 306 })
      await geometry(drawer.locator('.sidebar-compose'), { x: 286, width: 32, height: 32 })
      if (shots) await page.screenshot({ path: `${shots}/flow-drawer-${viewport.width}.png` })

      await drawer.locator('.workspace-menu-trigger').click()
      const menu = page.locator('.phone-menu-surface')
      await geometry(menu, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(menu.locator('[cmdk-item]').first(), { height: 46 })
      await menu.getByRole('option', { name: 'Switch workspace', exact: true }).click()
      await expect(menu.getByRole('option', { name: /Verify$/ })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(menu).not.toBeVisible()
      await page.locator('.sidebar-scrim.open').click({ position: { x: viewport.width - 12, y: 100 } })
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')

      await trigger.click()
      await drawer.getByRole('button', { name: 'Search workspace', exact: true }).click()
      await expect(page).toHaveURL(/\/search$/)
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')
      await geometry(page.locator('.workspace-search-header'), { height: 44 })
      await geometry(page.locator('.workspace-search-toolbar'), { height: 43.5 })
      await expect(page.getByRole('button', { name: '2 more', exact: true })).toBeVisible()
      await noOverflow(page)
      await page.getByRole('button', { name: '2 more', exact: true }).click()
      await geometry(page.locator('.phone-menu-surface'), { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await page.locator('.phone-menu-surface input').fill('Documents')
      await expect(page.locator('.phone-menu-surface').getByRole('option')).toHaveCount(1)
      await page.locator('.phone-menu-surface').getByRole('option', { name: 'Documents' }).click()
      await expect(page).toHaveURL(/type=document/)

      await page.keyboard.press('Escape')
      await page.locator('.workspace-search-header input').blur()
      await page.keyboard.press('Meta+k')
      const commands = page.locator('.command-dialog')
      await geometry(commands, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(commands.locator('[cmdk-item]').first(), { height: 46 })
      await expect(commands.locator('[cmdk-item]').first()).toHaveCSS('font-size', '15px')
      if (shots) await page.screenshot({ path: `${shots}/flow-command-${viewport.width}.png` })
      await page.keyboard.press('Escape')

      await page.locator('[data-sidebar-trigger]').first().click()
      await drawer.getByRole('button', { name: 'Create new issue', exact: true }).click()
      const composer = page.getByRole('dialog', { name: 'New issue', exact: true })
      await geometry(composer, { x: 12, y: Math.min(110, viewport.height * .1304), width: viewport.width - 24 })
      await geometry(composer.locator('header'), { height: 60 })
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')
      await geometry(composer.getByRole('button', { name: 'Close', exact: true }), { width: 36, height: 36 })
      await expect(composer.getByRole('button', { name: 'Expand', exact: true })).not.toBeVisible()
      await geometry(composer.getByRole('button', { name: 'Create issue', exact: true }), { height: 32 })
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/flow-composer-${viewport.width}.png` })
      await composer.getByRole('combobox', { name: /Change priority/i }).click()
      const picker = page.locator('.phone-menu-surface')
      await geometry(picker, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(picker.getByRole('option').first(), { height: 46 })
      await expect(picker.getByRole('option').first()).toHaveCSS('font-size', '15px')
      if (shots) await page.screenshot({ path: `${shots}/flow-picker-${viewport.width}.png` })
      await picker.locator('input').fill('Urgent')
      await expect(picker.getByRole('option')).toHaveCount(1)
      await page.keyboard.press('Escape')
      await expect(picker).not.toBeVisible()
      await expect(composer).toBeVisible()
      await composer.getByRole('textbox', { name: 'Issue title', exact: true }).fill('[mobile] Local draft')
      await composer.getByRole('button', { name: 'Close', exact: true }).click()
      const confirm = page.getByRole('dialog', { name: 'Save to drafts?', exact: true })
      await geometry(confirm, { x: 16, width: viewport.width - 32 })
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/flow-confirm-${viewport.width}.png` })
      await confirm.getByRole('button', { name: 'Discard', exact: true }).click()
      await expect(composer).not.toBeVisible()
      await page.locator('[data-sidebar-trigger]').first().click()
      await drawer.getByRole('button', { name: 'Open Help menu' }).click()
      await geometry(page.locator('.phone-menu-surface'), { width: viewport.width - 32 })
      await geometry(page.locator('.phone-menu-surface [cmdk-item]').first(), { height: 46 })
      await page.keyboard.press('Escape')
      await drawer.getByRole('button', { name: 'Connect GitHub', exact: true }).click()
      await expect(page.locator('.flow-toast')).toBeVisible()
      await page.waitForTimeout(500)
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/flow-toast-${viewport.width}.png` })
    })
  })
}

test.describe('shared search empty state', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('fits the phone frame without recent history', async ({ page }) => {
    await page.route('**/api/search?*', route => route.fulfill({ json: { results: [], history: [], recent: [] } }))
    await page.goto(`${base}/verify/search`)
    await expect(page.locator('.workspace-search-empty')).toBeVisible()
    await geometry(page.locator('.workspace-search-header input'), { x: 66, height: 22 })
    await geometry(page.locator('.workspace-search-empty-illustration'), { x: 105, y: 378.25, width: 180, height: 120 })
    await geometry(page.locator('.workspace-search-empty strong'), { x: 173.17, y: 514.25 })
    await noOverflow(page)
    if (shots) await page.screenshot({ path: `${shots}/flow-search-390.png` })
  })
})

test.describe('main page headers', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('keeps phone navigation and page actions aligned', async ({ page }) => {
    for (const route of ['my-issues/assigned', 'projects/all', 'initiatives/active', 'reviews', 'agent']) {
      await page.goto(`${base}/verify/${route}`)
      await geometry(page.locator('.app > main, .app > .flow-framed-workspace'), { x: 0, y: 0, width: 390, height: 844 })
      await geometry(page.locator('[data-sidebar-trigger]').first(), { x: 8, width: 32, height: 32 })
      await geometry(page.locator('main h1, main h2').first(), { x: route === 'agent' ? 52.5 : 52 })
      await noOverflow(page)
    }
    await page.goto(`${base}/verify/my-issues/assigned`)
    await geometry(page.getByRole('button', { name: 'New issue', exact: true }), { x: 350, width: 32, height: 32 })
    if (shots) await page.screenshot({ path: `${shots}/flow-header-390.png` })
    await page.getByRole('button', { name: 'New issue', exact: true }).click()
    await expect(page.getByRole('dialog', { name: 'New issue', exact: true })).toBeVisible()
  })
})

test('desktop frame stays docked at 1280px', async ({ browser }) => {
  const page = await browser.newPage({ viewport: { width: 1280, height: 768 } })
  await page.goto(`${base}/verify/inbox`)
  await expect(page.locator('[data-sidebar-surface]')).not.toHaveAttribute('aria-hidden')
  await expect(page.locator('[data-sidebar-trigger]').first()).not.toBeVisible()
  await expect(page.locator('.app > main, .app > .flow-framed-workspace')).toHaveCSS('border-top-left-radius', '12px')
  await page.close()
})
