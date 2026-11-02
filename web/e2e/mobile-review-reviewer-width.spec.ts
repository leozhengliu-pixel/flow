import { expect } from '@playwright/test'
import { test, createReview, preferences, withinViewport, capture } from './review-findings-fixture'

for (const width of [375, 390]) for (const theme of ['Light', 'Dark'] as const) for (const language of ['en-US', 'zh-CN']) {
  test(`keeps multiple reviewers and their add action reachable ${width} ${theme} ${language}`, async ({ page, request, workspaceKey }) => {
    await preferences(request, workspaceKey, theme, language)
    await createReview(request, workspaceKey)
    await page.route('**/api/bootstrap*', async route => {
      const response = await route.fetch()
      const data = await response.json()
      const users = [
        { ...data.viewer, id: 'reviewer-one', displayName: '陈 王', name: 'reviewer-one', avatarUrl: '' },
        { ...data.viewer, id: 'reviewer-two', displayName: 'Alexandria Catherine Montgomery-Sutherland', name: 'reviewer-two', avatarUrl: '' },
      ]
      data.users.push(...users)
      data.reviews = data.reviews.map((review: { reviewerIds: string[] }) => ({ ...review, reviewerIds: users.map(user => user.id) }))
      await route.fulfill({ response, json: data })
    })
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await page.goto(`/${workspaceKey}/review/github-1`)
    const row = page.locator('[data-mobile-property=reviewers]')
    await expect(row).toContainText('Alexandria')
    await withinViewport(row.locator('button').last(), page)
    expect(await row.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    const avatar = row.locator('.avatar').first()
    expect(await avatar.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await capture(row, `flow-review-reviewers-${width}-${theme}-${language}`)
    await row.locator('button').last().click()
    await expect(page.getByRole('dialog')).toBeVisible()
  })
}
