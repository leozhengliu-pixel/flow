import { expect } from '@playwright/test'
import { test, bootstrap, preferences, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('uses light surfaces and visible document glyphs in light mode', async ({ page, request, workspaceKey }) => {
  await preferences(request, workspaceKey, 'Light', 'zh-CN')
  const data = await bootstrap(request, workspaceKey)
  expect((await request.post('/api/documents', { headers: { 'X-Workspace-Key': workspaceKey }, data: { title: '测试文档', teamIds: [data.teams[0].id] } })).status()).toBe(201)
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
  await expect(page.locator('.team-docs-row')).toBeVisible()
  const palette = await page.locator('.team-documents').evaluate(element => {
    const style = getComputedStyle(element)
    return { control: style.getPropertyValue('--phone-control-bg'), expected: style.getPropertyValue('--ui-pill-bg'), icon: style.getPropertyValue('--phone-document-icon'), text: style.getPropertyValue('--theme-text-primary') }
  })
  expect(palette.control).toBe(palette.expected)
  expect(palette.icon).toBe(palette.text)
  await capture(page.locator('.team-home-page'), 'flow-documents-light-zh')
})
