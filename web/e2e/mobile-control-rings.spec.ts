import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px control hairlines`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('team pills, document actions, settings drawer and selected layout keep visible rings', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const response = await request.get('/api/bootstrap', { headers })
      expect(response.ok()).toBe(true)
      const { teams } = await response.json() as { teams: Array<{ key: string }> }
      const teamRoute = `/${workspaceKey}/team/${teams[0].key}`

      await page.goto(`${teamRoute}/documents`)
      const inactive = page.locator('.team-home-tabs a:not([aria-current="page"])').first()
      await expect(inactive).toBeVisible()
      expect(await inactive.evaluate(node => getComputedStyle(node, '::after').boxShadow)).toContain('lch(100 0 0 / 0.09)')
      const create = page.locator('.team-documents-new')
      expect(await create.evaluate(node => getComputedStyle(node, '::after').boxShadow)).toContain('lch(100 0 0 / 0.09)')
      await page.locator('.team-documents-toolbar button[aria-label="Add filter"]').click()
      await expect(page.locator('[data-layout="documents"] input')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.keyboard.press('Escape')

      await page.goto(`${teamRoute}/all`)
      await page.getByRole('button', { name: 'Display options', exact: true }).click()
      const panel = page.getByRole('dialog', { name: 'Display options' })
      const list = panel.getByRole('tab', { name: 'List', exact: true })
      await expect(list).toHaveAttribute('aria-selected', 'true')
      expect(await list.evaluate(node => getComputedStyle(node, '::after').boxShadow)).toBe('lch(100 0 0 / 0.247) 0px 0px 0px 0.5px')
      await panel.getByRole('tab', { name: 'Board', exact: true }).click()
      const board = panel.getByRole('tab', { name: 'Board', exact: true })
      await expect(board).toHaveAttribute('aria-selected', 'true')
      expect(await board.evaluate(node => getComputedStyle(node, '::after').boxShadow)).not.toBe('none')
      await list.click()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      await page.locator('.settings-mobile-menu').click()
      const drawer = page.locator('.settings-sidebar.open')
      await expect(drawer).toBeVisible()
      expect((await drawer.boundingBox())?.width).toBe(330)
      expect(await page.locator('.settings-app').evaluate(node => getComputedStyle(node, '::after').boxShadow)).toBe('lch(18.48 1.48 272) 0px 0px 0px 0.5px')
      await page.locator('.settings-sidebar-scrim.open').click({ position: { x: viewport.width - 12, y: 100 } })
      await expect(drawer).not.toBeVisible()
    })
  })
}
