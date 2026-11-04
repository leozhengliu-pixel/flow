import { expect, test } from '@playwright/test'

for (const theme of ['light', 'dark'] as const) {
  test(`keeps the ${theme} startup shell through slow entry, auth and bootstrap`, async ({ page, request }, testInfo) => {
    const workspace = `startup-${theme}-${testInfo.project.name}-${Date.now()}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Startup verification', urlKey: workspace, region: 'us' },
    })
    expect(response.status()).toBe(201)
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
      await page.goto(`/${workspace}/projects/all`, { waitUntil: 'commit' })
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
          const disc = element.querySelector('.flow-startup__disc')!.getBoundingClientRect()
          return { rail: rail.width, card: [card.x, card.y, card.width, card.height], disc: [disc.width, disc.height], dx: disc.x + disc.width / 2 - (card.x + card.width / 2), dy: disc.y + disc.height / 2 - (card.y + card.height / 2), overflow: document.documentElement.scrollWidth > innerWidth }
        })
        // The loading surface is the shell the app draws next: a blank sidebar column, the main card and a 32px disc at its centre.
        expect(geometry.rail).toBe(244)
        expect(geometry.card[0]).toBe(244)
        expect(geometry.card[1]).toBe(8)
        expect(geometry.disc).toEqual([32, 32])
        // Centred between the sidebar column and the window edge, so 4px right of the card's own centre.
        expect(geometry.dx).toBeCloseTo(4, 0)
        expect(geometry.dy).toBeCloseTo(0, 0)
        expect(geometry.overflow).toBe(false)
        await expect(page.locator('.flow-startup__pulse')).toHaveCount(0)
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
  await expect(page.locator('.flow-startup__disc')).toBeVisible()
})
