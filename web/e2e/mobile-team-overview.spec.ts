import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px team view pills`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('tab fills match the measured surfaces without changing menu search', async ({ page, request, workspaceKey }) => {
      const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
      const { teams } = await response.json() as { teams: Array<{ key: string }> }
      for (const view of ['overview', 'documents', 'members']) {
        await page.goto(`/${workspaceKey}/team/${teams[0].key}/${view}`)
        const active = page.locator('.team-home-tabs a[aria-current="page"]')
        await expect(active).toBeVisible()
        await expect(active).toHaveCSS('background-color', 'lch(16.706 0.979 272)')
        await expect(active).toHaveCSS('border-top-color', 'rgba(0, 0, 0, 0)')
        const more = page.locator('.team-home-tabs-overflow-pill')
        if (await more.count()) await expect(more).toHaveCSS('background-color', 'lch(10.149 0.593 272)')
        await expect(page.locator('.bottom-agent')).toBeHidden()
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      }
      await page.goto(`/${workspaceKey}/team/${teams[0].key}/documents`)
      await page.locator('.team-documents-toolbar button[aria-label="Add filter"]').click()
      const search = page.locator('[data-layout="documents"] input')
      await expect(search).toBeVisible()
      expect(await search.evaluate(input => getComputedStyle(input, '::placeholder').color)).toBe('lch(41.778 1.425 272)')
    })
  })
}
