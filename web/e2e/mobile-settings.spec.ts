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

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px settings navigation drawer scrolled to the end`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('shows integrations, administration and the nested team tree', async ({ page, request }, testInfo) => {
      const workspaceKey = `mobile-settings-nav-lower-${testInfo.project.name}-${testInfo.retry}-${viewport.width}`
      const created = await request.post(`${API}/api/workspaces`, {
        data: { name: 'Mobile settings navigation lower', urlKey: workspaceKey, region: 'us', tutorial: false },
      })
      expect(created.status()).toBe(201)
      const headers = { 'X-Workspace-Key': workspaceKey }
      const parent = await request.post(`${API}/api/workspaces/${workspaceKey}/teams`, {
        data: { name: '[mobile] Picker Test', key: 'PICK', color: '#F55DA8', icon: 'StarOutline' },
        headers,
      })
      expect(parent.status()).toBe(201)
      const parentTeam = await parent.json() as { id: string }
      const child = await request.post(`${API}/api/workspaces/${workspaceKey}/teams`, {
        data: { name: '[mobile] Invite Test', key: 'INV', color: '#EB5757', icon: 'StarOutline', parentTeamId: parentTeam.id },
        headers,
      })
      expect(child.status()).toBe(201)
      expect((await request.put(`${API}/api/integrations/gitlab`, { data: { name: 'GitLab', config: { mode: 'oauth' } }, headers })).ok()).toBe(true)

      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      await page.locator('.settings-mobile-menu').click()
      await page.waitForTimeout(400)
      await page.locator('.settings-sidebar nav').evaluate(nav => { nav.scrollTop = nav.scrollHeight })

      const sections = page.locator('.settings-sidebar nav section h2 > span')
      await expect(sections.filter({ hasText: 'Integrations' })).toHaveCount(1)
      await expect(sections.filter({ hasText: 'Administration' })).toHaveCount(1)
      await expect(sections.filter({ hasText: 'Your teams' })).toHaveCount(1)

      const admin = page.locator('.settings-sidebar nav section:nth-of-type(6)')
      await expect(admin.getByRole('button', { name: 'MCP connectors' })).toBeVisible()
      await expect(admin.getByRole('button', { name: 'Workspace' })).toBeVisible()
      await expect(admin.getByRole('button', { name: 'Usage & limits' })).toBeVisible()

      const teams = page.locator('.settings-sidebar nav section:nth-of-type(7)')
      await expect(teams.getByRole('button', { name: '[mobile] Picker Test' })).toBeVisible()
      const childButton = teams.getByRole('button', { name: '[mobile] Invite Test' })
      await expect(childButton).toBeVisible()
      await expect(childButton).toHaveAttribute('data-depth', '1')
      expect(await childButton.evaluate(node => getComputedStyle(node, '::before').borderLeftWidth)).toBe('1px')
      expect(await teams.getByRole('button', { name: '[mobile] Picker Test' }).evaluate(node => getComputedStyle(node, '::after').height)).toBe('6px')

      const createButton = teams.getByRole('button', { name: 'Create a team' })
      const footerBox = await page.locator('.settings-sidebar-footer').boundingBox()
      expect((await createButton.boundingBox())!.y + (await createButton.boundingBox())!.height).toBeLessThanOrEqual(footerBox!.y)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/settings-nav-lower-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
    })
  })
}
