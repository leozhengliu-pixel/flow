import { expect } from '@playwright/test'
import { test, bootstrap, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('paints document filter separators across the scroll container', async ({ page, request, workspaceKey }) => {
  const data = await bootstrap(request, workspaceKey)
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
  await page.locator('.team-documents-toolbar button[aria-label="Add filter"]').click()
  const menu = page.locator('[data-layout=documents][data-variant=root]')
  await expect(menu).toBeVisible()
  await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  const list = (await menu.locator('[cmdk-list]').boundingBox())!
  for (const separator of await menu.locator('[cmdk-separator]').all()) {
    const box = (await separator.boundingBox())!
    expect(box.x).toBeCloseTo(list.x, 0)
    expect(box.width).toBeCloseTo(list.width, 0)
  }
  await capture(menu, 'flow-document-filter-divider')
})
