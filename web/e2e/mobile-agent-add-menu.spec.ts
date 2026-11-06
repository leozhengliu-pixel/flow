import { expect, type Locator } from '@playwright/test'
import { test } from './workspace-fixture'

// The composer's "+" menu on a 390px phone, as in the reference app: a 358px sheet 110px from the top with a "Filter…" field
// over 46px rows (File, Skill), whose Skill row replaces the sheet with the skills under a "Search skills…" field; a chosen
// skill is a chip in the message, and "/" opens the same 227px menu as on desktop.
test.use({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
  colorScheme: 'dark',
  userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
  launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] },
})

const NAMES = ['Flow parity skill', 'Flow parity zetam', 'Flow parity alpha', 'Flow parity skill with a very long name that goes past the menu width', 'Flow parity gamma', 'Flow parity beta']
const box = async (locator: Locator) => (await locator.boundingBox())!

test('phone "+" sheet and skill drill-in match the reference geometry, and a chosen skill becomes a chip', async ({ page, workspaceKey }) => {
  await page.route('**/api/bootstrap*', async route => {
    const response = await route.fetch(), data = await response.json()
    data.agentSkills = NAMES.map((name, index) => ({ id: `skill-${index}`, name, instructions: 'Reply with the single word parity.', userId: data.viewer.id, createdAt: `2026-11-0${index + 1}T00:00:00Z`, updatedAt: `2026-11-0${index + 1}T00:00:00Z` }))
    await route.fulfill({ response, json: data })
  })
  await page.goto(`/${workspaceKey}/agent`)
  await page.evaluate(() => document.fonts.ready)
  await expect(page.getByRole('button', { name: 'Skills', exact: true })).toHaveCount(0)
  const plus = page.locator('[data-agent-add]').first()
  const composer = await box(page.locator('[data-agent-composer]'))
  const at = await box(plus)
  // 28px, 270px in and 68px down from the composer's corner; send is 24px, 8px after it.
  expect([at.x - composer.x, at.y - composer.y, at.width, at.height]).toEqual([270, 68, 28, 28])
  expect((await box(page.locator('[data-agent-send]'))).x).toBe(at.x + 28 + 8)
  await expect(plus).toHaveCSS('color', 'lch(90.826 1.425 272)')
  await expect(plus.locator('svg')).toHaveCSS('opacity', '1')
  await plus.tap()
  const sheet = page.locator('.phone-menu-surface').last()
  await expect(sheet).toBeVisible()
  await sheet.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  const input = sheet.getByRole('combobox')
  await expect(input).toHaveAttribute('placeholder', 'Filter...')
  expect(await box(input)).toMatchObject({ x: 22.5, y: 116.5, width: 346, height: 40 })
  const sheetBox = await box(sheet)
  expect([sheetBox.x, sheetBox.y, sheetBox.width]).toEqual([16, 110, 358])
  const rows = sheet.getByRole('option')
  await expect(rows).toHaveText(['File', 'Skill'])
  const [file, skill] = await Promise.all((await rows.all()).map(box))
  expect([file.x, file.y, file.width, file.height]).toEqual([22.5, 162.5, 346, 46])
  expect([skill.y, skill.height]).toEqual([208.5, 46])
  // Icon 12px in, name 36px in; no shortcut or marker on the phone.
  expect(await box(rows.first().locator('svg'))).toMatchObject({ x: 34.5, y: 177.5, width: 16, height: 16 })
  expect((await box(rows.first().locator('span'))).x).toBe(58.5)
  await expect(sheet.locator('kbd')).toHaveCount(0)
  await expect(sheet.locator('.phone-menu-current')).toHaveCount(0)
  await rows.nth(1).tap()
  await expect(input).toHaveAttribute('placeholder', 'Search skills…')
  await sheet.evaluate(element => Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished)))
  await expect.poll(async () => (await box(sheet.getByRole('option').first())).height).toBe(46)
  const skills = sheet.getByRole('option')
  await expect(skills).toHaveText([...NAMES].reverse())
  const listed = await Promise.all((await skills.all()).map(box))
  expect(listed.map(row => [row.x, row.width, row.height])).toEqual(NAMES.map(() => [22.5, 346, 46]))
  expect(listed[0].y).toBe(162.5)
  // Skills have no icon: the name starts 12px in.
  expect((await box(skills.first().locator('span'))).x).toBe(34.5)
  await skills.filter({ hasText: 'Flow parity zetam' }).tap()
  await expect(sheet).toBeHidden()
  const editor = page.locator('[role=textbox]').first()
  const chip = editor.locator('[data-mention-type="skill"]')
  await expect(chip).toHaveAttribute('data-mention-id', 'skill-1')
  const editorBox = await box(editor), chipBox = await box(chip)
  expect([chipBox.x, chipBox.y - editorBox.y, chipBox.height]).toEqual([editorBox.x, 1, 22])
})

test('phone "/" menu opens 4px left of the line and 4px under it, 227px wide', async ({ page, workspaceKey }) => {
  await page.route('**/api/bootstrap*', async route => {
    const response = await route.fetch(), data = await response.json()
    data.agentSkills = NAMES.slice(0, 2).map((name, index) => ({ id: `skill-${index}`, name, instructions: 'Reply with the single word parity.', userId: data.viewer.id, createdAt: `2026-11-0${index + 1}T00:00:00Z`, updatedAt: `2026-11-0${index + 1}T00:00:00Z` }))
    await route.fulfill({ response, json: data })
  })
  await page.goto(`/${workspaceKey}/agent`)
  const editor = page.locator('[role=textbox]').first()
  await editor.click()
  await page.keyboard.type('/')
  const menu = page.getByRole('listbox', { name: 'Insert block' })
  await expect(menu).toBeVisible()
  await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  const at = await box(editor), surface = await box(menu)
  expect([surface.x, surface.y, surface.width]).toEqual([at.x - 4, at.y + 24 + 4, 227])
  await expect(menu.getByRole('option').first()).toHaveAttribute('aria-selected', 'true')
})
