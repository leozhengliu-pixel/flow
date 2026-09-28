import { expect, test } from '@playwright/test'

for (const theme of ['light', 'dark'] as const) {
  test(`keeps an animated ${theme} startup shell through slow entry, auth and bootstrap`, async ({ page, request }, testInfo) => {
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
          const mark = element.querySelector('.flow-startup__mark')!.getBoundingClientRect()
          const content = element.querySelector('.flow-startup__content')!.getBoundingClientRect()
          return { mark: mark.width, width: content.width, height: content.height, x: content.x + content.width / 2, y: content.y + content.height / 2, viewportX: innerWidth / 2, viewportY: innerHeight / 2, overflow: document.documentElement.scrollWidth > innerWidth }
        })
        expect(geometry.mark).toBe(32)
        expect(geometry.width).toBe(64)
        expect(geometry.height).toBe(64)
        expect(geometry.x).toBeCloseTo(geometry.viewportX, 0)
        expect(geometry.y).toBeCloseTo(geometry.viewportY, 0)
        expect(geometry.overflow).toBe(false)
        const pulse = page.locator('.flow-startup__pulse')
        await expect(pulse).toHaveCSS('animation-duration', '3.2s')
        const first = await pulse.evaluate(element => getComputedStyle(element).transform)
        await expect.poll(() => pulse.evaluate(element => getComputedStyle(element).transform)).not.toBe(first)
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

test('respects reduced motion before JavaScript starts', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.route(url => url.pathname === '/src/main.tsx', route => route.abort())
  await page.goto('/join')
  await expect(page.locator('.flow-startup')).toBeVisible()
  await expect(page.locator('.flow-startup__pulse')).toHaveCSS('animation-name', 'none')
  await expect(page.locator('.flow-startup__text')).toHaveCSS('opacity', '1')
})
