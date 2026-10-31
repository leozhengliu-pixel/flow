import { expect, test as base } from '@playwright/test'

const test = base.extend<{ issueRoute: string }>({
  issueRoute: async ({ request }, provide, testInfo) => {
    const key = `audit-shell-polish-${testInfo.project.name || 'desktop'}-${testInfo.workerIndex}-${Date.now()}`
    const response = await request.post('/api/workspaces', {
      data: { name: '[audit] Desktop shell polish', urlKey: key, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const workspace = await response.json() as { teams: Array<{ key: string }> }
    expect(workspace.teams[0]?.key).toBeTruthy()
    await provide(`/${key}/team/${workspace.teams[0].key}/all`)
  },
})

test.use({ viewport: { width: 1440, height: 900 } })
test.skip(({ isMobile }) => isMobile, 'desktop shell styling')

test('keeps issue view widths and the add-view glyph at the desktop metrics', async ({ page, issueRoute }) => {
  await page.goto(issueRoute)
  const pills = page.locator('[data-content-view-subheader] nav a')
  await expect(pills).toHaveCount(3)
  await page.evaluate(() => document.fonts.ready)
  const metrics = await pills.evaluateAll(elements => elements.map(element => {
    const rect = element.getBoundingClientRect(), style = getComputedStyle(element)
    return { x: rect.x, width: rect.width, height: rect.height, fontSize: style.fontSize, fontWeight: style.fontWeight }
  }))
  const reference = [{ x: 252.5, width: 57.16 }, { x: 313.66, width: 67.04 }, { x: 384.7, width: 75.43 }]
  for (const [index, metric] of metrics.entries()) {
    expect(Math.abs(metric.x - reference[index].x)).toBeLessThanOrEqual(1)
    expect(Math.abs(metric.width - reference[index].width)).toBeLessThanOrEqual(0.1)
    expect(metric).toMatchObject({ height: 28, fontSize: '12px', fontWeight: '500' })
  }
  const add = page.getByRole('button', { name: 'Add new view', exact: true })
  await expect(add.locator('svg')).toHaveCSS('fill', 'lch(36.975 1.2 272)')
  await expect(page.getByRole('button', { name: 'Open insights' })).toBeVisible()
})

test('uses the desktop composer glyphs, bright team text and an undivided footer', async ({ page, issueRoute }) => {
  await page.goto(issueRoute)
  await expect(page.locator('[data-content-view-header]')).toBeVisible()
  await page.keyboard.press('c')
  const composer = page.locator('[data-flow-motion="create"]')
  await expect(composer).toBeVisible()
  await expect(composer.getByRole('combobox', { name: 'Set team' })).toHaveCSS('color', 'lch(91.201 1.65 272)')
  await expect(composer.locator('footer')).toHaveCSS('border-top-width', '0px')
  for (const [label, name, size] of [['Expand', 'openFullPage', 16], ['Close', 'closeChat', 16], ['Attach images, files, or videos', 'attach', 14]] as const) {
    const glyph = composer.getByRole('button', { name: label, exact: true }).locator(`[data-flow-glyph="${name}"]`)
    await expect(glyph).toBeVisible()
    await expect(glyph).toHaveAttribute('viewBox', '0 0 16 16')
    await expect(glyph).toHaveCSS('fill', 'lch(64.805 1.65 272)')
    await expect(glyph).toHaveCSS('stroke', 'none')
    await expect.poll(async () => (await glyph.boundingBox())?.width).toBe(size)
    await expect.poll(async () => (await glyph.boundingBox())?.height).toBe(size)
  }
  await composer.getByRole('button', { name: 'Expand', exact: true }).click()
  await expect(composer).toHaveAttribute('data-expanded', 'true')
  await composer.getByRole('button', { name: 'Collapse', exact: true }).click()
  await expect(composer).toHaveAttribute('data-expanded', 'false')
  const fileChooser = page.waitForEvent('filechooser')
  await composer.getByRole('button', { name: 'Attach images, files, or videos' }).click()
  await fileChooser
  await composer.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(composer).toHaveCount(0)
})

test('preserves the compact composer glyphs below the desktop breakpoint', async ({ page, issueRoute }) => {
  for (const width of [375, 390, 1023]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(issueRoute)
    await expect(page.locator('[data-content-view-header]')).toBeVisible()
    await page.keyboard.press('c')
    const composer = page.locator('[data-flow-motion="create"]')
    await expect(composer).toBeVisible()
    await expect(composer.getByRole('combobox', { name: 'Set team' })).toHaveCSS('color', 'lch(64.805 1.65 272)')
    for (const label of ['Close', 'Attach images, files, or videos']) {
      const button = composer.getByRole('button', { name: label, exact: true })
      await expect(button.locator('.lucide')).toBeVisible()
      await expect(button.locator('[data-flow-glyph]')).toBeHidden()
    }
    await composer.getByRole('button', { name: 'Close', exact: true }).click()
    await expect(composer).toHaveCount(0)
  }
})
