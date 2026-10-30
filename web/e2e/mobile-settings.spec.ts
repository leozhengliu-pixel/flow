import { expect, test } from '@playwright/test'

const API = process.env.FLOW_E2E_API_URL ?? 'http://127.0.0.1:4180'

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px settings navigation drawer`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('phone drawer shows the settings sections and the pinned footer', async ({ page, request }, testInfo) => {
      const workspaceKey = `mobile-settings-nav-${testInfo.project.name}-${testInfo.retry}-${viewport.width}`
      const created = await request.post(`${API}/api/workspaces`, {
        data: { name: 'Mobile settings navigation', urlKey: workspaceKey, region: 'us', tutorial: false },
      })
      expect(created.status()).toBe(201)

      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      await page.locator('.settings-mobile-menu').click()

      const drawer = page.locator('.settings-sidebar.open')
      const box = await drawer.boundingBox()
      expect(box).toMatchObject({ x: 0, y: 0, width: 330 })
      expect(box?.height).toBe(viewport.height)
      await expect(drawer).toHaveCSS('background-color', 'lch(5.52 0.4 272)')
      await expect(drawer).toHaveCSS('border-radius', '0px 12px 12px 0px')

      await expect(page.locator('.settings-sidebar-scrim.open')).toHaveCSS('background-color', 'lch(0 0 0 / 0.4)')
      expect((await page.locator('.settings-back').boundingBox())?.height).toBe(32)
      expect((await page.locator('.settings-back').boundingBox())?.y).toBeCloseTo(14.25, 1)
      expect((await page.locator('.settings-search input').boundingBox())?.width).toBe(306)

      const sections = page.locator('.settings-sidebar nav section h2 > span')
      await expect(sections.nth(0)).toHaveText('Personal')
      await expect(sections.nth(1)).toHaveText('Issues')
      await expect(sections.nth(2)).toHaveText('Projects')
      await expect(sections.nth(3)).toHaveText('Features')
      await expect(page.locator('.settings-sidebar nav button[aria-current="page"]')).toContainText('Preferences')
      expect((await page.locator('.settings-sidebar nav button[aria-current="page"]').boundingBox())?.width).toBe(306)
      expect((await page.locator('.settings-sidebar nav button[aria-current="page"]').boundingBox())?.height).toBe(28)

      const footer = page.locator('.settings-sidebar-footer')
      const footerBox = await footer.boundingBox()
      expect(footerBox).toMatchObject({ x: 0, width: 330, height: 52 })
      expect(footerBox?.y).toBe(viewport.height - 52)
      expect((await page.locator('.settings-sidebar-footer .sidebar-help').boundingBox())?.width).toBe(32)
      expect((await page.locator('.settings-plan-button').boundingBox())?.x).toBe(50)
      await expect(page.locator('.settings-plan-button')).toContainText('Free plan')

      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/settings-nav-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
    })
  })
}
