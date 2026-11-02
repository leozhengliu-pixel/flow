import { expect } from '@playwright/test'
import { test, bootstrap, preferences, capture } from './review-findings-fixture'

for (const width of [375, 390]) for (const theme of ['Light', 'Dark'] as const) {
  test(`keeps Chinese team tab labels on one line ${width} ${theme}`, async ({ page, request, workspaceKey }) => {
    await preferences(request, workspaceKey, theme, 'zh-CN')
    const data = await bootstrap(request, workspaceKey)
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
    await expect(page.locator('.team-home-tabs')).toBeVisible()
    await expect(page.locator('.team-home-tabs a[aria-current="page"]')).toHaveText('文档')
    await page.evaluate(() => document.fonts.ready)
    await expect(async () => {
      const labels = await page.locator('.team-home-tabs .team-home-tab-label:visible').evaluateAll(elements => elements.map(element => {
        const range = document.createRange()
        range.selectNodeContents(element)
        return { lines: range.getClientRects().length, width: element.getBoundingClientRect().width }
      }))
      expect(labels.length).toBeGreaterThan(0)
      for (const label of labels) {
        expect(label.lines).toBe(1)
        expect(label.width).toBeGreaterThan(20)
      }
    }).toPass({ timeout: 5000 })
    await capture(page.locator('.team-home-band'), `flow-team-tabs-${width}-${theme}`)
  })
}
