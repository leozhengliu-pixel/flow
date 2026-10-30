import { expect, test } from '@playwright/test'

test.describe('mobile issue lists and views', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  test('keeps list, board, triage and views surfaces inside the phone viewport', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-issues-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile issue parity', urlKey: workspaceKey, region: 'us' },
    })
    expect(response.status()).toBe(201)
    const workspace = await response.json() as { teams?: Array<{ key: string }> }
    const teamKey = workspace.teams?.[0]?.key
    expect(teamKey).toBeTruthy()

    const routes = [
      `/${workspaceKey}/my-issues/assigned`,
      `/${workspaceKey}/issues/all`,
      `/${workspaceKey}/views/issues`,
      `/${workspaceKey}/team/${teamKey}/triage`,
    ]
    for (const route of routes) {
      await page.goto(route)
      await expect(page.locator('main').first()).toBeVisible()
      const geometry = await page.evaluate(() => ({
        width: innerWidth,
        height: innerHeight,
        scrollWidth: document.documentElement.scrollWidth,
        overflow: document.documentElement.scrollWidth <= innerWidth,
      }))
      expect(geometry.width).toBe(390)
      expect(geometry.height).toBe(844)
      expect(geometry.overflow, `${route} should not overflow horizontally`).toBe(true)
    }

    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await page.getByRole('button', { name: 'Add filter', exact: true }).click()
    await expect(page.getByRole('dialog', { name: 'Add Filter…' })).toBeVisible()
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/filter-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    await expect(page.getByRole('dialog', { name: 'Display options' })).toBeVisible()
    await page.keyboard.press('Escape')

    await page.goto(`/${workspaceKey}/issues/all`)
    const display = page.getByRole('button', { name: 'Display options' }).first()
    await expect(display).toBeVisible()
    await display.click()
    const boardTab = page.getByRole('tab', { name: 'Board', exact: true })
    await expect(boardTab).toBeEnabled()
    await boardTab.click()
    await page.keyboard.press('Escape')
    const board = page.getByRole('list', { name: 'Issue board' })
    await expect(board).toBeVisible()
    await expect(board).toHaveCSS('overflow-x', 'auto')
  })
})
