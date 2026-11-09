import { test, expect } from './issue-list-fixture'

// The reference snaps its menus and pickers to whole CSS pixels but leaves its panels on the device-pixel position the layout gives
// them, so the display options panel (right edge on the trigger's 0.5px-aligned edge, 4px under it) lands on half pixels at 2x.
test.skip(({ isMobile }) => isMobile, 'Desktop popup positions')
test.use({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
  deviceScaleFactor: 2,
  launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] },
})

const onDevicePixel = (value: number) => Math.abs(value * 2 - Math.round(value * 2)) < 1e-6

test('the display options panel keeps the device-pixel position under its trigger', async ({ page, issueList }) => {
  await page.goto(issueList.route)
  await expect(page.locator('[data-issue-row-id]').first()).toBeVisible()
  const trigger = page.getByRole('button', { name: 'Display options', exact: true })
  const triggerBox = (await trigger.boundingBox())!
  await trigger.click()
  const panel = page.getByRole('dialog', { name: 'Display options', exact: true })
  await expect(panel).toBeVisible()
  await panel.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  const box = (await panel.boundingBox())!
  expect(box.x + box.width).toBe(triggerBox.x + triggerBox.width)
  expect(box.y).toBe(Math.round((triggerBox.y + triggerBox.height + 4) * 2) / 2)
  expect(onDevicePixel(box.x) && onDevicePixel(box.y)).toBe(true)
})

test('an issue row menu snaps to whole pixels', async ({ page, issueList }) => {
  await page.goto(issueList.route)
  const row = page.locator('[data-issue-row-id]').first()
  await expect(row).toBeVisible()
  await row.click({ button: 'right' })
  const menu = page.getByRole('menu').first()
  await expect(menu).toBeVisible()
  await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  const box = (await menu.boundingBox())!
  expect(Number.isInteger(box.x) && Number.isInteger(box.y)).toBe(true)
})
