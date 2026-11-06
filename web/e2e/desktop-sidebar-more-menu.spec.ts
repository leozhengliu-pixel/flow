import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, launchOptions: { args: ['--force-device-scale-factor=2'] } })
test.skip(({ isMobile }) => isMobile, 'Desktop sidebar menu')

// The sidebar More menu: 175px wide, 32px rows inset 6px with 8px corners, a 16px icon slot (14px glyph) 8px before
// the label, a 12px separator row whose hairline spans the menu, and a 16px Customize glyph. Measured at DPR 2.
const THEMES = {
  dark: {
    icon: 'lch(64.714 1.425 272)', text: 'lch(91.178 1.425 272)', hoverBg: 'lch(20.82 1.3 272)', hoverFg: 'lch(100 0 272)',
    border: '0.5px solid lch(25.68 1.93 272)', line: 'lch(20.28 1.93 272)', triggerBg: 'lch(8.445 1.3 272)', triggerFg: 'lch(100 0 272)',
  },
  light: {
    icon: 'lch(40 1 282)', text: 'lch(20 1 282)', hoverBg: 'lch(95 0 282)', hoverFg: 'lch(10 0 282)',
    border: '0.5px solid lch(91.9 0 282)', line: 'lch(91.9 0 282)', triggerBg: 'lch(91.64 0.5 282)', triggerFg: 'lch(9.444 0 282)',
  },
} as const

// Sidebar nav rows (text and icon share one colour): resting, hover, current page, and the keyboard focus ring.
const NAV = {
  dark: {
    rest: 'lch(60.621 1.2 272)', hover: 'lch(100 0 272)', hoverBg: 'lch(8.445 1.3 272)', activeBg: 'lch(13.845 1.3 272)',
    ring: 'lch(47.918 59.303 288.421) solid 1px',
  },
  light: {
    rest: 'lch(37.776 1.25 282)', hover: 'lch(9.444 0 282)', hoverBg: 'lch(91.64 0.5 282)', activeBg: 'lch(89.54 0.5 282)',
    ring: 'lch(53 52.26 286.91) solid 1px',
  },
} as const

async function openMore(page: Page, workspaceKey: string) {
  await page.goto(`/${workspaceKey}/my-issues`)
  const trigger = page.locator('.sidebar-more-trigger')
  await expect(trigger).toBeVisible()
  await trigger.click()
  const menu = page.locator('.sidebar-more-menu')
  await expect(menu).toBeVisible()
  // The entrance animation settles before geometry is read.
  await page.waitForTimeout(600)
  return { trigger, menu }
}

const box = async (locator: Locator) => (await locator.boundingBox())!

for (const [scheme, expected] of Object.entries(THEMES) as [keyof typeof THEMES, (typeof THEMES)[keyof typeof THEMES]][]) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('More menu keeps the measured geometry, glyph slots and colours', async ({ page, workspaceKey }) => {
      const { trigger, menu } = await openMore(page, workspaceKey)
      await expect(menu).toHaveCSS('width', '175px')
      await expect(menu).toHaveCSS('border-radius', '12px')
      await expect(menu).toHaveCSS('padding', '6px 0px')
      await expect(menu).toHaveCSS('border-top', expected.border)
      const menuBox = await box(menu)
      const triggerBox = await box(trigger)
      expect(menuBox.x).toBe(triggerBox.x)
      expect(menuBox.y - (triggerBox.y + triggerBox.height)).toBe(5.5)

      const rows = menu.getByRole('menuitem')
      const count = await rows.count()
      expect(count).toBeGreaterThanOrEqual(2)
      for (let index = 0; index < count; index += 1) {
        const row = rows.nth(index)
        const customize = index === count - 1
        const rowBox = await box(row)
        expect(rowBox.height).toBe(32)
        expect(rowBox.x - menuBox.x).toBe(6.5)
        expect(rowBox.width).toBe(162)
        await expect(row).toHaveCSS('border-radius', '8px')
        await expect(row).toHaveCSS('column-gap', '8px')
        await expect(row).toHaveCSS('padding', '0px 8px')
        const glyph = row.locator(':scope > svg')
        const glyphBox = await box(glyph)
        const size = customize ? 16 : 14
        expect([glyphBox.width, glyphBox.height]).toEqual([size, size])
        // The 16px slot starts 8px inside the row; a 14px glyph sits 1px inside the slot.
        expect(glyphBox.x - rowBox.x).toBe(customize ? 8 : 9)
        expect(glyphBox.y + glyphBox.height / 2 - (rowBox.y + rowBox.height / 2)).toBe(0)
        const label = row.locator(':scope > span')
        const labelBox = await box(label)
        expect(labelBox.x - rowBox.x).toBe(32)
        expect(labelBox.height).toBe(15.5)
        await expect(label).toHaveCSS('font-size', '13px')
        await expect(label).toHaveCSS('font-weight', '450')
        await expect(label).toHaveCSS('color', expected.text)
        await expect(glyph).toHaveCSS('color', expected.icon)
        await expect(glyph).toHaveCSS('fill', expected.icon)
      }

      const separator = menu.getByRole('separator')
      const separatorBox = await box(separator)
      expect(separatorBox.x).toBe(menuBox.x + 0.5)
      expect(separatorBox.width).toBe(menuBox.width - 1)
      expect(separatorBox.height).toBe(0.5)
      await expect(separator).toHaveCSS('margin', '6px 0px 5.5px')
      await expect(separator).toHaveCSS('background-color', expected.line)
      const lastRow = await box(rows.last())
      expect(lastRow.y - (separatorBox.y + separatorBox.height)).toBe(5.5)
      const previous = await box(rows.nth(count - 2))
      expect(separatorBox.y - (previous.y + previous.height)).toBe(6)
    })

    test('More menu rows and trigger highlight like the other menus', async ({ page, workspaceKey }) => {
      const { trigger, menu } = await openMore(page, workspaceKey)
      // Open, the trigger keeps the nav hover look.
      await expect(trigger).toHaveCSS('background-color', expected.triggerBg)
      await expect(trigger).toHaveCSS('color', expected.triggerFg)
      await expect(trigger.locator('svg')).toHaveCSS('width', '14px')
      const triggerSvg = await box(trigger.locator('svg'))
      const triggerLabel = await box(trigger.locator('span'))
      expect(triggerLabel.x - triggerSvg.x - triggerSvg.width).toBe(6)

      const row = menu.getByRole('menuitem').first()
      await row.hover()
      await expect(row).toHaveCSS('background-color', expected.hoverBg)
      await expect(row.locator('span')).toHaveCSS('color', expected.hoverFg)
      await expect(row.locator(':scope > svg')).toHaveCSS('fill', expected.hoverFg)
    })

    test('sidebar nav rows colour text and icon alike at rest, hover, active and focus', async ({ page, workspaceKey }) => {
      const nav = NAV[scheme]
      await page.goto(`/${workspaceKey}/my-issues`)
      await expect(page.locator('.sidebar-more-trigger')).toBeVisible()
      await page.mouse.move(900, 500)
      const rows = page.locator('.sidebar a.nav-item, .sidebar button.nav-item')
      const read = (row: Locator) => row.evaluate((element) => {
        const style = getComputedStyle(element)
        const icon = element.querySelector('svg')
        const label = element.querySelector('.nav-label') ?? element.querySelector('span:last-child')
        return {
          bg: style.backgroundColor, text: getComputedStyle(label!).color, icon: getComputedStyle(icon!).fill,
          ring: style.outlineStyle === 'none' ? 'none' : `${style.outlineColor} ${style.outlineStyle} ${style.outlineWidth}`,
        }
      })
      const inbox = rows.filter({ hasText: /^Inbox$/ })
      await expect(inbox).toHaveCSS('color', nav.rest)
      expect(await read(inbox)).toEqual({ bg: 'rgba(0, 0, 0, 0)', text: nav.rest, icon: nav.rest, ring: 'none' })
      await inbox.hover()
      await expect(inbox).toHaveCSS('background-color', nav.hoverBg)
      await expect(inbox).toHaveCSS('color', nav.hover)
      expect(await read(inbox)).toEqual({ bg: nav.hoverBg, text: nav.hover, icon: nav.hover, ring: 'none' })
      // The current page keeps its own background and the hover colours.
      const current = rows.filter({ hasText: /^My issues$/ })
      await page.mouse.move(900, 500)
      await expect(current).toHaveCSS('background-color', nav.activeBg)
      await expect(current).toHaveCSS('color', nav.hover)
      expect(await read(current)).toMatchObject({ bg: nav.activeBg, text: nav.hover, icon: nav.hover })
      // Keyboard focus draws only the ring; colours and background stay at rest.
      await page.evaluate(() => { (document.activeElement as HTMLElement | null)?.blur() })
      await inbox.focus()
      await page.keyboard.press('Shift+Tab')
      await page.keyboard.press('Tab')
      await expect(inbox).toBeFocused()
      await expect(inbox).toHaveCSS('outline-offset', '-1px')
      await expect(inbox).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
      await expect(inbox).toHaveCSS('color', nav.rest)
      expect(await read(inbox)).toEqual({ bg: 'rgba(0, 0, 0, 0)', text: nav.rest, icon: nav.rest, ring: nav.ring })
    })
  })
}

test.describe('sidebar default entries', () => {
  test('lists Initiatives, Projects, Views and Loops, then More with Customers, Members, Releases and Teams', async ({ page, workspaceKey }) => {
    await page.goto(`/${workspaceKey}/my-issues`)
    const trigger = page.locator('.sidebar-more-trigger')
    await expect(trigger).toBeVisible()
    const primary = await page.locator('.sidebar-section-workspace a.nav-item, .sidebar-section-workspace button.nav-item').allInnerTexts()
    expect(primary.map((text) => text.trim())).toEqual(['Initiatives', 'Projects', 'Views', 'Loops', 'More'])
    await trigger.click()
    const rows = (await page.locator('.sidebar-more-menu').getByRole('menuitem').allInnerTexts()).map((text) => text.trim())
    const inOrder = rows.filter((label) => ['Customers', 'Members', 'Releases', 'Teams'].includes(label))
    expect(inOrder).toEqual(['Customers', 'Members', 'Releases', 'Teams'].filter((label) => inOrder.includes(label)))
    expect(inOrder).toEqual(expect.arrayContaining(['Customers', 'Members', 'Teams']))
  })

  test('keeps the More row visible at rest, with the pointer away, and reachable by keyboard', async ({ page, workspaceKey }) => {
    await page.goto(`/${workspaceKey}/my-issues`)
    const trigger = page.locator('.sidebar-more-trigger')
    await page.mouse.move(900, 500)
    await expect(trigger).toBeVisible()
    for (const selector of ['.sidebar-more-trigger', '.sidebar-section-workspace', '.sidebar']) {
      await expect(page.locator(selector).first()).toHaveCSS('opacity', '1')
      await expect(page.locator(selector).first()).toHaveCSS('visibility', 'visible')
    }
    await page.evaluate(() => { (document.activeElement as HTMLElement | null)?.blur() })
    await page.locator('.sidebar a.nav-item', { hasText: /^Loops$/ }).focus()
    await page.keyboard.press('Tab')
    await expect(trigger).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page.locator('.sidebar-more-menu')).toBeVisible()
  })
})
