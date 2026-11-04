import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

test.skip(({ isMobile }) => isMobile, 'desktop-only tooltip parity')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2', '--window-size=1440,900'] } })

test.beforeEach(async ({ page, context }) => {
  const session = await context.newCDPSession(page)
  await session.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 0, mobile: false })
  expect(await page.evaluate(() => [innerWidth, innerHeight, devicePixelRatio])).toEqual([1440, 900, 2])
})

async function hover(page: Page, target: Locator) {
  const box = (await target.boundingBox())!
  await page.mouse.move(700, 450, { steps: 8 })
  await page.mouse.move(box.x + box.width / 2 - 30, box.y + box.height / 2 + 40, { steps: 4 })
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 6 })
}

test('shortcut tooltips draw their key caps 8px after the label and 5px from the edge', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as { teams: Array<{ key: string }> }
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/all`)
  await page.evaluate(() => document.fonts.ready)
  for (const [name, label, keys] of [['Search', 'Search', ['/']], ['Create new issue', 'Create new issue', ['C']], ['Open details', 'Open details', ['⌘', 'I']], ['Add filter', 'Add Filter', ['F']]] as const) {
    await hover(page, page.getByRole('button', { name, exact: true }).first())
    const tooltip = page.locator('.flow-tooltip-content').filter({ hasText: label })
    await expect(tooltip).toBeVisible()
    // The open animation scales the surface slightly; measure once it has settled.
    await expect.poll(async () => (await tooltip.boundingBox())!.height, { message: `${name} tooltip height` }).toBeCloseTo(28.1, 1)
    await page.waitForTimeout(250)
    const box = (await tooltip.boundingBox())!
    const copy = (await tooltip.locator('.flow-tooltip-copy').boundingBox())!
    const caps = tooltip.locator('kbd.flow-tooltip-key')
    await expect(caps).toHaveText([...keys])
    const first = (await caps.first().boundingBox())!
    const last = (await caps.last().boundingBox())!
    expect(first.x - (copy.x + copy.width)).toBeCloseTo(8, 1)
    expect(box.x + box.width - (last.x + last.width)).toBeCloseTo(5.5, 1)
    expect(first.height).toBeCloseTo(17.1, 1)
    expect(first.y - box.y).toBeCloseTo(5.5, 1)
  }
})

test('sidebar links show their go-to tooltip beside the row, not over the next one', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  const link = page.locator('a.nav-item[href$="/inbox"]')
  await hover(page, link)
  const tooltip = page.getByRole('tooltip')
  await expect(tooltip).toBeVisible()
  await expect(tooltip.locator('.flow-tooltip-copy')).toHaveText('Go to inbox')
  await expect(tooltip.locator('.flow-tooltip-shortcut > *')).toHaveText(['G', 'then', 'I'])
  const row = (await link.boundingBox())!
  await expect.poll(async () => (await tooltip.boundingBox())!.height).toBeCloseTo(28.1, 1)
  const box = (await tooltip.boundingBox())!
  expect(box.x).toBeCloseTo(row.x + row.width + 8, 1)
  expect(box.y + box.height / 2).toBeCloseTo(row.y + row.height / 2, 1)
})
