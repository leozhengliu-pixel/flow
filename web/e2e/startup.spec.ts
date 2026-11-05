import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

for (const theme of ['light', 'dark'] as const) {
  test(`keeps the ${theme} startup shell through slow entry, auth and bootstrap`, async ({ page, workspaceKey }) => {
    await page.addInitScript(value => {
      localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: value }))
      localStorage.setItem('flow:locale', 'en-US')
    }, theme)
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    const gates = ['/src/main.tsx', '/api/auth/session', '/api/account/bootstrap', '/api/bootstrap'].map(path => {
      let release!: () => void
      let arrived!: () => void
      const wait = new Promise<void>(resolve => { release = resolve })
      const reached = new Promise<void>(resolve => { arrived = resolve })
      return { path, wait, release, arrived, reached }
    })
    for (const gate of gates) {
      await page.route(url => url.pathname === gate.path, async route => {
        gate.arrived()
        await gate.wait
        await route.continue()
      })
    }
    try {
      await page.goto(`/${workspaceKey}/projects/all`, { waitUntil: 'commit' })
      for (const gate of gates) {
        await gate.reached
        const loader = page.locator('.flow-startup')
        await expect(loader).toBeVisible()
        await expect(loader).toHaveAttribute('role', 'status')
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
        await expect(page.locator('.auth-brand')).toHaveCount(0)
        const geometry = await loader.evaluate(element => {
          const rail = element.querySelector('.flow-startup__rail')!.getBoundingClientRect()
          const card = element.querySelector('.flow-startup__card')!.getBoundingClientRect()
          const mark = element.querySelector('.flow-startup__mark')!.getBoundingClientRect()
          return { rail: rail.width, card: [card.x, card.y, card.width, card.height], mark: [mark.width, mark.height], dx: mark.x + mark.width / 2 - (card.x + card.width / 2), dy: mark.y + mark.height / 2 - (card.y + card.height / 2), overflow: document.documentElement.scrollWidth > innerWidth }
        })
        const phone = page.viewportSize()!.width <= 1024
        // Narrow screens hide the sidebar and let the loading card fill the viewport.
        expect(geometry.rail).toBe(phone ? 0 : 244)
        expect(geometry.card[0]).toBe(phone ? 0 : 244)
        expect(geometry.card[1]).toBe(phone ? 0 : 8)
        if (phone) expect(geometry.card.slice(2)).toEqual([page.viewportSize()!.width, page.viewportSize()!.height])
        expect(geometry.mark).toEqual([32, 32])
        // Centred between the sidebar column and the window edge, so 4px right of the card's own centre.
        expect(geometry.dx).toBeCloseTo(phone ? 0 : 4, 0)
        expect(geometry.dy).toBeCloseTo(0, 0)
        expect(geometry.overflow).toBe(false)
        await expect(page.locator('.flow-startup__pulse')).toHaveCount(0)
        if (process.env.FLOW_CI_SHOTS && gate.path === '/api/bootstrap') await loader.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/startup-${phone ? 'phone' : 'desktop'}-${theme}.png`, animations: 'disabled' })
        gate.release()
      }
      await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
      await expect(page.locator('.flow-startup')).toHaveCount(0)
      // Theme from flow.theme must survive settings/bootstrap applyAccountTheme.
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      expect(errors).toEqual([])
    } finally {
      gates.forEach(gate => gate.release())
    }
  })
}

test('draws the startup shell before JavaScript starts', async ({ page }) => {
  await page.route(url => url.pathname === '/src/main.tsx', route => route.abort())
  await page.goto('/join')
  await expect(page.locator('.flow-startup')).toBeVisible()
  await expect(page.locator('.flow-startup__mark')).toBeVisible()
})
