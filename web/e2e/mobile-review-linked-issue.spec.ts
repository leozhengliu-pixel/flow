import { expect } from '@playwright/test'
import { test, bootstrap, createReview, preferences, capture } from './review-findings-fixture'

test.afterEach(async ({ request, workspaceKey }) => {
  await request.patch('/api/account/settings', { headers: { 'X-Workspace-Key': workspaceKey }, data: { interfaceTheme: 'System preference', language: 'en-US' } })
})
for (const width of [375, 390]) for (const theme of ['Light', 'Dark'] as const) for (const locale of ['en-US', 'zh-CN']) test(`opens the linked-issue command sheet and persists unlinking ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
  if (process.env.FLOW_REVIEW_SHOTS) test.setTimeout(90_000)
  await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
  await preferences(request, workspaceKey, theme, locale)
  const zh = locale === 'zh-CN', suffix = `${width}-${theme}-${locale}`
  const actionLabel = zh ? '事项操作' : 'Issue actions', typeLabel = zh ? '更改关联类型' : 'Change link type'
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await bootstrap(request, workspaceKey)
  const issueResponse = await request.post('/api/issues', { headers, data: { title: 'Linked review issue', teamId: data.teams[0].id, stateId: 'state_todo' } })
  expect(issueResponse.status()).toBe(201)
  const issue = await issueResponse.json() as { id: string }
  const id = await createReview(request, workspaceKey)
  expect((await request.patch(`/api/reviews/${id}`, { headers, data: { issueIds: [issue.id] } })).ok()).toBe(true)
  await page.goto(`/${workspaceKey}/review/github-1`)
  await page.getByRole('button', { name: actionLabel, exact: true }).click()
  const menu = page.getByRole('dialog', { name: actionLabel })
  await expect(menu).toBeVisible()
  await capture(menu, `flow-review-issue-menu-${suffix}`)
  await expect(menu.getByRole('option', { name: 'Link issues', exact: true })).toHaveCount(0)
  await menu.getByRole('option', { name: typeLabel }).hover()
  await page.keyboard.press('ArrowDown')
  await capture(menu, `flow-review-issue-menu-keyboard-${suffix}`)
  await menu.getByRole('option', { name: typeLabel }).click()
  const types = page.getByRole('dialog', { name: typeLabel })
  await expect(types.getByRole('option')).toHaveCount(3)
  await capture(types, `flow-review-link-types-${suffix}`)
  let releaseSave!: () => void
  const saveHeld = new Promise<void>(resolve => { releaseSave = resolve })
  await page.route(`**/api/reviews/${id}`, async route => {
    if (route.request().method() === 'PATCH') await saveHeld
    await route.continue()
  })
  try {
    await types.getByRole('option', { name: zh ? '相关' : 'Related', exact: true }).click()
    await expect(page.getByRole('button', { name: actionLabel, exact: true })).toBeDisabled()
    await capture(page.locator('.review-mobile-properties'), `flow-review-link-pending-${suffix}`)
  } finally { releaseSave() }
  await expect(page.locator('[data-mobile-property=related]')).toContainText('Linked review issue')
  await page.reload()
  await expect(page.locator('[data-mobile-property=related]')).toContainText('Linked review issue')
  expect((await (await request.get(`/api/reviews/${id}`, { headers })).json()).issueLinkTypes).toEqual({ [issue.id]: 'related' })
  await page.getByRole('button', { name: actionLabel, exact: true }).click()
  await menu.getByRole('option', { name: typeLabel }).click()
  await expect(types.getByRole('option', { name: zh ? '相关' : 'Related', exact: true }).locator('svg')).toHaveCount(2)
  await capture(types, `flow-review-link-selected-${suffix}`)
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: actionLabel, exact: true }).click()
  await menu.getByRole('option', { name: zh ? '取消事项与拉取请求的关联' : 'Unlink issue from pull request' }).click()
  await expect(page.locator('.review-mobile-issue')).toHaveCount(0)
  const updated = await request.get(`/api/reviews/${id}`, { headers })
  expect((await updated.json()).issueIds).toEqual([])
  expect((await (await request.get(`/api/reviews/${id}`, { headers })).json()).issueLinkTypes ?? {}).toEqual({})
})
