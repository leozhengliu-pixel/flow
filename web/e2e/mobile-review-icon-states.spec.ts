import { expect } from '@playwright/test'
import { test, createReview, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('keeps merged and closed review strokes tinted without filling their paths', async ({ page, request, workspaceKey }) => {
  await createReview(request, workspaceKey, 'merged')
  await createReview(request, workspaceKey, 'closed')
  await page.goto(`/${workspaceKey}/reviews/created`)
  for (const state of ['Merged', 'Closed']) await page.getByRole('button', { name: new RegExp(state) }).click()
  const icons = page.locator('.review-row-icon--phone')
  await expect(icons).toHaveCount(2)
  const styles = await icons.evaluateAll(elements => elements.map(element => ({ state: element.getAttribute('data-review-state'), color: getComputedStyle(element).color, fill: getComputedStyle(element.querySelector('path')!).fill, expected: getComputedStyle(element).getPropertyValue(`--review-state-${element.getAttribute('data-review-state')}`) })))
  for (const style of styles) {
    expect(style.fill).toBe('none')
    expect(style.color).not.toBe('lch(100 0 272)')
  }
  expect(styles[0].color).not.toBe(styles[1].color)
  await capture(page.locator('.review-groups'), 'flow-review-icon-states')
})
