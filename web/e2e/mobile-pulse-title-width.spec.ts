import { expect } from '@playwright/test'
import { test, preferences, withinViewport, capture } from './review-findings-fixture'

for (const width of [375, 390]) for (const theme of ['Light', 'Dark'] as const) {
  test(`keeps long Pulse titles and post actions on screen ${width} ${theme}`, async ({ page, request, workspaceKey }) => {
    await preferences(request, workspaceKey, theme)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const response = await request.post('/api/initiatives', { headers, data: { name: 'International infrastructure reliability and customer support improvements' } })
    expect(response.status()).toBe(201)
    const { id } = await response.json() as { id: string }
    expect((await request.post(`/api/initiatives/${id}/updates`, { headers, data: { body: 'Weekly status', health: 'onTrack' } })).ok()).toBe(true)
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await page.goto(`/${workspaceKey}/pulse/all`)
    const card = page.locator('.pulse-post-card').first()
    await expect(card).toBeVisible()
    const actions = card.getByRole('button', { name: 'Post actions' })
    await withinViewport(actions, page)
    await actions.click()
    await expect(page.getByRole('menu')).toBeVisible()
    await page.keyboard.press('Escape')
    await capture(card, `flow-pulse-long-${width}-${theme}`)
  })
}
