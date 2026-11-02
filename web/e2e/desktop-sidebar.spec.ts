import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test('matches the desktop sidebar header, section labels and help footer', async ({ page, workspaceKey }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'The sidebar layout is desktop-only')
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(`/${workspaceKey}/agent`)

  const name = page.locator('.workspace-row strong').first()
  await expect(name).toHaveCSS('font-size', '13px')
  await expect(name).toHaveCSS('font-weight', '550')
  await expect(name).toHaveCSS('line-height', '23px')
  await expect(name).toHaveCSS('letter-spacing', '-0.1px')
  await expect(name).toHaveCSS('font-variation-settings', '"opsz" 28')
  await page.evaluate(() => document.fonts.ready)
  await expect(name).toBeInViewport({ ratio: 1 })
  const nameBox = (await name.boundingBox())!
  const triggerBox = (await page.locator('.workspace-menu-trigger').boundingBox())!
  expect(nameBox.x).toBeGreaterThanOrEqual(triggerBox.x)
  expect(nameBox.x + nameBox.width).toBeLessThanOrEqual(triggerBox.x + triggerBox.width)

  const sectionLabel = page.locator('.section-label span').first()
  await expect(sectionLabel).toHaveCSS('font-size', '12px')
  await expect(sectionLabel).toHaveCSS('font-weight', '500')
  await expect(sectionLabel).toHaveCSS('line-height', '14.5px')

  const footer = await page.locator('.sidebar-footer').boundingBox()
  expect(footer).not.toBeNull()
  expect(footer!.x).toBeCloseTo(0, 0)
  expect(footer!.y).toBeCloseTo(856, 0)
  expect(footer!.width).toBeCloseTo(244, 0)
  expect(footer!.height).toBeCloseTo(44, 0)
  await expect(page.locator('.sidebar-footer')).toHaveCSS('padding', '10px')

  const help = await page.locator('.sidebar-footer .sidebar-help').boundingBox()
  expect(help!.x).toBeCloseTo(10, 0)
  await expect(page.locator('.sidebar-footer .sidebar-help')).toHaveCSS('font-size', '12px')
  await expect(page.locator('.sidebar-footer .sidebar-help')).toHaveCSS('font-weight', '500')

  await expect(page.getByRole('button', { name: 'Free plan' })).toHaveCount(0)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-sidebar.png`, animations: 'disabled' })
  await page.locator('.sidebar-footer .sidebar-help').click()
  await expect(page.getByRole('menu')).toBeVisible()
})

test('collapses the desktop sidebar sections with 8px gaps', async ({ page, workspaceKey }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'The sidebar layout is desktop-only')
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(`/${workspaceKey}/agent`)

  await page.locator('.sidebar-section-workspace .section-label').click()
  await page.waitForTimeout(350)
  await page.locator('.sidebar-section-teams .section-label').click()
  await page.waitForTimeout(400)

  const boxes = await page.evaluate(() => {
    const rect = (selector: string) => {
      const value = document.querySelector(selector)!.getBoundingClientRect()
      return { y: value.y, height: value.height }
    }
    return {
      workspace: rect('.sidebar-section-workspace'),
      teams: rect('.sidebar-section-teams'),
      try: rect('.sidebar-section-try'),
    }
  })
  expect(boxes.workspace.height).toBeCloseTo(29, 0)
  expect(boxes.teams.y - (boxes.workspace.y + boxes.workspace.height)).toBeCloseTo(8, 0)
  expect(boxes.try.y - (boxes.teams.y + boxes.teams.height)).toBeCloseTo(8, 0)
})
