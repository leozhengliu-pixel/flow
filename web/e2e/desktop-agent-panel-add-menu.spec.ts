import { expect, type Locator } from '@playwright/test'
import { test } from './workspace-fixture'

// The bottom-right agent panel has the same "+" as the Agent page, measured against the reference app at 1440x900, real DPR 2:
// the footer is [context toggle][+][send], 24px each, 8px apart, ending 50.5px short of the window's right edge.
test.use({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
  userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36',
  launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] },
})
test.beforeEach(({ isMobile }) => { test.skip(isMobile, 'Desktop agent panel') })

const box = async (locator: Locator) => (await locator.boundingBox())!

for (const scheme of ['dark', 'light'] as const) {
  test(`the panel's "+" sits between the tools and send, is lit on hover and while open, and its menu keeps off the window edge (${scheme})`, async ({ page, workspaceKey }) => {
    await page.emulateMedia({ colorScheme: scheme })
    await page.route('**/api/bootstrap*', async route => {
      const response = await route.fetch(), data = await response.json()
      data.agentSkills = [{ id: 'skill-1', name: 'Flow parity skill', instructions: 'Reply with the single word parity.', userId: data.viewer.id, createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z' }]
      await route.fulfill({ response, json: data })
    })
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await page.getByRole('button', { name: 'Agent', exact: true }).last().click()
    const plus = page.locator('[data-variant=floating] [data-agent-add]')
    await expect(plus).toBeVisible()
    await expect(page.locator('[data-variant=floating]').getByRole('button', { name: 'Skills', exact: true })).toHaveCount(0)
    const send = page.getByRole('button', { name: 'Send message', exact: true })
    // The panel scales in when it opens: measure once it has settled.
    await expect.poll(async () => { const at = await box(plus); return [at.x, at.y, at.width, at.height] }).toEqual([1333.5, 823.5, 24, 24])
    await expect.poll(async () => { const at = await box(send); return [at.x, at.y, at.width, at.height] }).toEqual([1365.5, 823.5, 24, 24])
    const rest = scheme === 'dark' ? 'lch(91.201 1.65 272)' : 'lch(20 1 282)'
    const lit = scheme === 'dark' ? ['lch(21.431 1.493 272)', 'lch(100 0 272)'] : ['lch(96.914 0 282)', 'lch(10 0 282)']
    await expect(plus).toHaveCSS('color', rest)
    await expect(plus).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
    await plus.hover()
    await expect(plus).toHaveCSS('background-color', lit[0])
    await expect(plus).toHaveCSS('color', lit[1])
    await plus.click()
    const menu = page.getByRole('menu', { name: 'Add files and more', exact: true })
    await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
    // Pushed in from the window edge: its right side is 11px short of it.
    expect(await box(menu)).toEqual({ x: 1254, y: 743, width: 175, height: 77 })
    await page.mouse.move(10, 10)
    await expect(plus).toHaveCSS('background-color', lit[0])
    await page.keyboard.press('Escape')
    await expect(plus).toBeFocused()
  })
}
