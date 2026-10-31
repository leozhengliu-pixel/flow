import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, FlowDocument, Issue } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop review details')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('keeps an empty document send arrow painted and enables it only with text', async ({ page, request, workspaceKey }) => {
  const response = await request.post('/api/documents', { headers: { 'X-Workspace-Key': workspaceKey }, data: { title: '[audit] Composer', content: '123123123' } })
  expect(response.status()).toBe(201)
  const document = await response.json() as FlowDocument
  await page.goto(`/${workspaceKey}/document/${document.slugId}`)
  await page.locator('.document-editor .ProseMirror p').first().click({ clickCount: 3 })
  await page.getByRole('button', { name: 'Comment', exact: true }).click()
  const draft = page.locator('.document-thread-draft')
  const send = draft.getByRole('button', { name: 'Submit comment' })
  await expect(send).toBeDisabled()
  await expect(send).toHaveCSS('opacity', '1')
  await expect(send).toHaveCSS('background-color', 'lch(17.349 1.139 272)')
  await expect(send).toHaveCSS('color', 'lch(64.714 1.425 272)')
  await expect(draft.locator('.document-thread-avatar')).toHaveCSS('background-color', 'lch(70 60 30)')
  expect(await send.evaluate(el => {
    const r = el.getBoundingClientRect()
    for (let p = el.parentElement; p; p = p.parentElement) {
      const s = getComputedStyle(p), parent = p.getBoundingClientRect()
      if (['hidden', 'clip', 'auto'].includes(s.overflowX) && (r.right > parent.right || r.left < parent.left)) return false
    }
    return r.width === 24 && r.height === 24 && r.right <= innerWidth
  })).toBe(true)
  await draft.getByRole('textbox', { name: 'Add a comment…' }).fill('[audit] Comment')
  await expect(send).toBeEnabled()
  await send.click()
  await expect(page.locator('.document-thread-body')).toContainText('[audit] Comment')
})

test('dims the issue placeholder, updates event disclosure and fits project creation at 186px', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const response = await request.post('/api/issues', { headers, data: { title: '[audit] Desktop details', teamId: data.teams[0].id, stateId: data.states.find(state => state.type === 'unstarted')!.id } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as Issue
  for (const priority of [1, 2, 3]) expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { priority } })).ok()).toBeTruthy()
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  const placeholder = page.locator('.issue-document > .composer .is-editor-empty').first()
  await expect(placeholder).toBeVisible()
  expect(await placeholder.evaluate(el => getComputedStyle(el, '::before').color)).toBe('lch(39.452 1.425 272)')
  const events = page.getByRole('button', { name: /^Show 3 events/ })
  await events.click()
  await expect(page.getByRole('button', { name: 'Show less', exact: true })).toHaveAttribute('aria-expanded', 'true')
  await page.getByRole('button', { name: 'Show less', exact: true }).click()
  await expect(events).toHaveAttribute('aria-expanded', 'false')
  await page.locator('.issue-properties .issue-project-trigger').click()
  const picker = page.getByRole('dialog', { name: 'Change Project' })
  await expect(picker).toHaveCSS('width', '186px')
  const create = picker.getByRole('option', { name: 'Create new project…', exact: true })
  await expect(create).toBeVisible()
  expect(await create.locator('span').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  await create.click()
  await expect(page.getByRole('dialog', { name: 'Create project' })).toBeVisible()
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await page.getByRole('button', { name: 'Issue options', exact: true }).click()
  const arrow = page.locator('.issue-options-chevron svg').first()
  await expect(arrow).toBeVisible()
  await expect(arrow).toHaveAttribute('stroke-width', '1.5')
  await expect(arrow).toHaveAttribute('width', '14')
  await page.keyboard.press('Escape')
  await page.goto(`/${workspaceKey}/team/${issue.team.key}/all`)
  await page.locator(`[data-issue-row-id="${issue.id}"]`).click({ button: 'right' })
  const rowArrow = page.locator('[role=menu] .flow-submenu-chevron > svg').first()
  await expect(rowArrow).toBeVisible()
  await expect(rowArrow).toHaveAttribute('stroke-width', '1.5')
})

test('uses the tray-delete glyph in the notification preview', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const response = await request.post('/api/issues', { headers, data: { title: '[audit] Inbox preview', teamId: data.teams[0].id } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as Issue
  expect((await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })).status()).toBe(201)
  await expect.poll(async () => (await (await request.get('/api/notifications', { headers })).json()).notifications.length).toBeGreaterThan(0)
  await page.goto(`/${workspaceKey}/inbox`)
  await page.locator('.flow-inbox-row').first().click()
  const remove = page.getByRole('button', { name: 'Delete notification', exact: true })
  await expect(remove).toBeVisible()
  await expect(remove).toHaveCSS('color', 'lch(90.451 1.2 272)')
  await expect(remove.locator('svg')).toHaveAttribute('viewBox', '0 0 16 16')
  await expect(remove.locator('path')).toHaveAttribute('fill-rule', 'evenodd')
  await remove.click()
  await expect(page.locator('.flow-inbox-row')).toHaveCount(0)
})
