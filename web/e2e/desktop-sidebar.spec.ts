import { expect, test, type APIRequestContext } from '@playwright/test'

async function createWorkspace(request: APIRequestContext, key: string) {
  const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Sidebar workspace', urlKey: key, region: 'us' },
  })
  expect(response.status()).toBe(201)
}

test('matches the desktop sidebar header, section labels and footer', async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'The sidebar layout is desktop-only')
  const workspaceKey = `e2e-sidebar-${Date.now()}`
  await createWorkspace(request, workspaceKey)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(`/${workspaceKey}/agent`)

  const name = page.locator('.workspace-row strong').first()
  await expect(name).toHaveCSS('font-size', '13px')
  await expect(name).toHaveCSS('font-weight', '550')
  await expect(name).toHaveCSS('line-height', '23px')
  await expect(name).toHaveCSS('letter-spacing', '-0.1px')

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

  const plan = page.locator('.sidebar-plan-button')
  const planBox = await plan.boundingBox()
  expect(planBox!.x).toBeCloseTo(42, 0)
  expect(planBox!.y).toBeCloseTo(866, 0)
  expect(Math.abs(planBox!.width - 84.5)).toBeLessThanOrEqual(1)
  expect(planBox!.height).toBeCloseTo(24, 0)
  await expect(plan).toHaveCSS('border-radius', '12px')
  await expect(plan.locator('span')).toHaveCSS('font-size', '11px')
  await expect(plan.locator('span')).toHaveCSS('font-weight', '500')

  await plan.click()
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/billing`))
})

test('collapses the desktop sidebar sections with 8px gaps', async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'The sidebar layout is desktop-only')
  const workspaceKey = `e2e-sidebar-collapse-${Date.now()}`
  await createWorkspace(request, workspaceKey)
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
