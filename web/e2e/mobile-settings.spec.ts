import { expect, test } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { test as workspaceTest } from './workspace-fixture'

const API = process.env.FLOW_E2E_API_URL ?? 'http://127.0.0.1:4180'

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  workspaceTest.describe(`${viewport.width}px workspace general`, () => {
    workspaceTest.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark', workspaceNamePrefix: '[mobile] Workspace general' })

    workspaceTest('stacks workspace fields and persists the fiscal month', async ({ page, workspaceKey }) => {
      await page.goto(`/${workspaceKey}/settings/workspace`)
      const card = page.locator('.settings-section:first-of-type .settings-card')
      await expect(page.getByRole('heading', { name: 'Workspace', exact: true })).toBeVisible()
      expect((await card.boundingBox())?.y).toBeCloseTo(115.5, 1)
      expect((await card.boundingBox())?.height).toBeCloseTo(290, 1)
      const name = page.getByRole('textbox', { name: 'Name', exact: true })
      expect((await name.boundingBox())?.width).toBeCloseTo(256, 1)
      const month = page.getByRole('combobox', { name: 'First month of the fiscal year' })
      await expect(month).toHaveValue('January')
      await expect(page.getByText('United States', { exact: true })).toBeVisible()
      await page.getByRole('button', { name: 'Edit URL', exact: true }).click()
      await expect(page.getByRole('textbox', { name: 'URL', exact: true })).toBeFocused()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/workspace-general-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
      await month.selectOption('April')
      await expect(month).toHaveValue('April')
      await page.reload()
      await expect(month).toHaveValue('April')
      await page.getByRole('button', { name: 'Configure announcements' }).click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await page.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(page.getByRole('dialog')).toBeHidden()
    })
  })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  workspaceTest.describe(`${viewport.width}px account notifications`, () => {
    workspaceTest.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark', workspaceNamePrefix: '[mobile] Account notifications' })

    workspaceTest('shows the priority control and opens supported notification channels', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey, 'X-Workspace-Key': workspaceKey }
      const response = await request.get('/api/notification-preferences', { headers })
      expect(response.ok()).toBe(true)
      const preferences = await response.json()
      expect((await request.patch('/api/notification-preferences', { headers, data: { ...preferences, desktop: { ...preferences.desktop, enabled: false }, email: { ...preferences.email, enabled: true } } })).ok()).toBe(true)
      await page.goto(`/${workspaceKey}/settings/account/notifications`)
      const priority = page.getByRole('checkbox', { name: 'Priority inbox', exact: true })
      await expect(priority).toBeVisible()
      await expect(priority).not.toBeChecked()
      const inboxCard = page.locator('.personal-section:first-of-type > .settings-card')
      expect((await inboxCard.boundingBox())?.y).toBeCloseTo(192.5, 1)
      expect((await inboxCard.boundingBox())?.height).toBeCloseTo(159, 1)
      const channels = page.locator('.personal-channel')
      await expect(channels).toHaveCount(4)
      await expect(channels.nth(0)).toContainText('Disabled')
      await expect(channels.nth(2)).toContainText('Enabled for all notifications')
      await expect(page.getByRole('button', { name: 'Mobile', exact: true })).toBeDisabled()
      await expect(channels.nth(0).locator('.personal-channel-icon')).toHaveCSS('width', '32px')
      await expect(channels.nth(0).locator('.personal-channel-status')).toBeVisible()
      expect((await channels.nth(0).boundingBox())?.y).toBeCloseTo(470.5, 1)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.getByRole('link', { name: 'Desktop', exact: true }).click()
      await expect(page.getByRole('checkbox', { name: 'Enable desktop notifications' })).not.toBeChecked()
      await page.getByRole('main').getByRole('button', { name: 'Notifications', exact: true }).click()
      await page.getByRole('link', { name: 'Email', exact: true }).click()
      await expect(page.getByRole('checkbox', { name: 'Enable email notifications' })).toBeChecked()
    })
  })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  workspaceTest.describe(`${viewport.width}px account profile`, () => {
    workspaceTest.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark', workspaceNamePrefix: '[mobile] Account profile' })

    workspaceTest('stacks profile fields and preserves editing and workspace access', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const bootstrap = await request.get('/api/bootstrap', { headers })
      const { viewer } = await bootstrap.json() as { viewer: { id: string } }
      expect((await request.patch('/api/account/profile', { headers, data: { displayName: '[mobile] Profile User', username: 'mobile-profile', jobTitle: '' } })).ok()).toBe(true)
      expect((await request.patch(`/api/workspaces/${workspaceKey}/members/${viewer.id}`, { headers, data: { displayName: '[mobile] Profile User', email: 'mobile-profile@example.test' } })).ok()).toBe(true)

      await page.goto(`/${workspaceKey}/settings/account/profile`)
      const card = page.locator('.personal-section:first-of-type > .settings-card')
      await expect(page.locator('.personal-avatar')).toBeVisible()
      await expect(page.locator('.personal-avatar')).toHaveText('MP')
      await expect(page.locator('.personal-static')).toHaveText('mobile-profile@example.test')
      expect((await card.boundingBox())?.height).toBeCloseTo(482.5, 1)
      expect((await page.getByRole('textbox', { name: 'Full name', exact: true }).boundingBox())?.width).toBeCloseTo(viewport.width - 65.3333, 1)
      await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue('')
      expect((await page.getByRole('heading', { name: 'Workspace access' }).boundingBox())?.y).toBeCloseTo(622, 1)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/profile-${viewport.width}.png`, scale: 'css', animations: 'disabled' })

      await page.getByRole('textbox', { name: 'Title', exact: true }).fill('Engineer')
      await page.getByRole('button', { name: 'Update', exact: true }).click()
      await page.reload()
      await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue('Engineer')
      await page.getByRole('button', { name: 'Leave workspace', exact: true }).click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await page.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(page.getByRole('dialog')).toBeHidden()
    })
  })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px settings navigation drawer`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('phone drawer shows the settings sections and the pinned footer', async ({ page, request }) => {
      const requestedKey = `settings-${randomUUID()}`
      const created = await request.post(`${API}/api/workspaces`, {
        data: { name: 'Mobile settings navigation', urlKey: requestedKey, region: 'us', tutorial: false },
      })
      expect(created.status()).toBe(201)
      const { workspace: { urlKey: workspaceKey } } = await created.json() as { workspace: { urlKey: string } }

      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      await expect(page.locator('.bottom-agent')).toBeHidden()
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
      expect(await page.locator('.settings-search input').evaluate(input => getComputedStyle(input, '::placeholder').color)).toBe('lch(61.803 1.2 272)')
      await expect(page.locator('.settings-search input')).toHaveCSS('border-color', 'lch(22.193 1.93 272)')
      await expect(page.locator('.settings-search input')).toHaveCSS('border-style', 'solid')
      await expect(page.locator('.bottom-agent')).toBeHidden()

      const sections = page.locator('.settings-sidebar nav section h2 > span')
      await expect(sections.nth(0)).toHaveText('Personal')
      await expect(sections.nth(1)).toHaveText('Issues')
      await expect(sections.nth(2)).toHaveText('Projects')
      await expect(sections.nth(3)).toHaveText('Features')
      expect((await sections.nth(0).boundingBox())?.y).toBeCloseTo(113.5, 1)
      await expect(page.locator('.settings-sidebar nav')).toHaveCSS('padding-top', '26px')
      await expect(page.locator('.settings-sidebar nav button[aria-current="page"]')).toContainText('Preferences')
      expect((await page.locator('.settings-sidebar nav button[aria-current="page"]').boundingBox())?.width).toBe(306)
      expect((await page.locator('.settings-sidebar nav button[aria-current="page"]').boundingBox())?.height).toBe(28)

      const footer = page.locator('.settings-sidebar-footer')
      const footerBox = await footer.boundingBox()
      expect(footerBox).toMatchObject({ x: 0, width: 330, height: 52 })
      expect(footerBox?.y).toBe(viewport.height - 52)
      expect((await page.locator('.settings-sidebar-footer .sidebar-help').boundingBox())?.width).toBe(32)
      await expect(page.locator('.settings-sidebar-footer .sidebar-help > svg')).toHaveCSS('color', 'lch(61.803 1.2 272)')
      expect((await page.locator('.settings-plan-button').boundingBox())?.x).toBe(50)
      await expect(page.locator('.settings-plan-button')).toContainText('Free plan')

      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/settings-nav-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
      await page.locator('.settings-sidebar-scrim.open').click({ position: { x: viewport.width - 12, y: 100 } })
      await expect(drawer).not.toBeVisible()
      await expect(page.locator('.bottom-agent')).toBeHidden()
    })
  })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px account preferences`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('matches the compact preferences groups', async ({ page, request }) => {
      const requestedKey = `settings-preferences-${randomUUID()}`
      const created = await request.post(`${API}/api/workspaces`, {
        data: { name: '[mobile] Account preferences', urlKey: requestedKey, region: 'us', tutorial: false },
      })
      expect(created.status()).toBe(201)
      const { workspace: { urlKey: workspaceKey } } = await created.json() as { workspace: { urlKey: string } }

      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await expect(page.locator('.settings-mobile-header')).toHaveCSS('height', '43.5px')
      expect((await page.locator('.personal-page-header h1').boundingBox())?.x).toBeCloseTo(38, 1)
      expect((await page.locator('.personal-section:first-of-type > .settings-card').boundingBox())?.x).toBeCloseTo(22, 1)
      expect((await page.locator('.personal-section:first-of-type > .settings-card').boundingBox())?.height).toBeCloseTo(386, 1)
      await expect(page.locator('.personal-section:first-of-type > .settings-card > .settings-row:first-child')).toBeHidden()
      await expect(page.getByRole('combobox', { name: 'Default home view' })).toHaveText('Flow Agent (default)')
      await expect(page.getByRole('checkbox', { name: 'Convert text emoticons into emojis' })).toBeChecked()
      await expect(page.locator('.personal-section:nth-of-type(2) .personal-action')).toHaveCSS('height', '36px')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    })
  })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px settings navigation drawer scrolled to the end`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('shows integrations, administration and the nested team tree', async ({ page, request }) => {
      const requestedKey = `settings-${randomUUID()}`
      const created = await request.post(`${API}/api/workspaces`, {
        data: { name: 'Mobile settings navigation lower', urlKey: requestedKey, region: 'us', tutorial: false },
      })
      expect(created.status()).toBe(201)
      const { workspace: { urlKey: workspaceKey } } = await created.json() as { workspace: { urlKey: string } }
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
      await expect(page.locator('.settings-sidebar nav')).toHaveCSS('padding-top', '26px')
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
