import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, FlowDocument } from '../src/types/flow'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', timezoneId: 'America/Los_Angeles' })
test.skip(({ isMobile }) => isMobile, 'Desktop document audit')

test('document list aligns its metadata and opens intrinsic metadata and subscriber controls', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const boot = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const created = await request.post('/api/documents', { headers, data: { title: '[audit] Plain document', content: 'A paragraph for the desktop audit.', teamIds: [boot.teams[0].id] } })
  expect(created.status()).toBe(201)
  const doc = await created.json() as FlowDocument
  await page.goto(`/${workspaceKey}/team/${boot.teams[0].key}/documents`)
  await expect(page.getByRole('button', { name: /Create document in/ }).first()).toBeVisible()
  const row = page.locator(`[data-document-id="${doc.id}"]`)
  await expect(row).toBeVisible()
  // Compare each heading with its own data column, not a platform-specific viewport coordinate.
  for (const [label, selector] of [['Created', '.team-docs-date.is-created'], ['Last edited', '.team-docs-date:not(.is-created)']] as const) {
    const heading = page.getByRole('button', { name: `Order by ${label}`, exact: true })
    const value = row.locator(selector)
    await expect(heading).toBeInViewport()
    await expect(value).toBeInViewport()
    await expect.poll(async () => Math.abs((await heading.locator('.team-docs-heading-label').boundingBox())!.x - (await value.boundingBox())!.x)).toBeLessThanOrEqual(1)
  }
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/docs-list.png`, animations: 'disabled' })
  await row.getByText(doc.title, { exact: true }).click()
  await expect(page).toHaveURL(`/${workspaceKey}/document/${doc.slugId}`)
  await page.locator('.document-edited').click()
  const metadata = page.locator('.document-edited-popover')
  await expect(metadata).toHaveCSS('min-width', '0px')
  const shortWidth = (await metadata.boundingBox())!.width
  await page.keyboard.press('Escape')
  // Reproduce the reference owner name, rather than fitting the panel to its sampled width.
  await page.route('**/api/bootstrap**', async route => {
    const response = await route.fetch()
    const data = await response.json() as BootstrapData
    data.viewer = { ...data.viewer, displayName: 'Skyler Anderson', name: 'Skyler Anderson' }
    data.users = data.users.map(user => user.id === data.viewer.id ? data.viewer : user)
    data.users.push({ ...data.viewer, id: 'audit-other', name: 'zheng liu', displayName: 'zheng liu', app: true })
    data.documents = data.documents.map(item => item.id === doc.id ? { ...item, creator: { ...item.creator, displayName: 'Skyler Anderson', name: 'Skyler Anderson' }, updatedAt: '2026-10-29T06:26:00Z', revisions: [] } : item)
    await route.fulfill({ response, json: data })
  })
  await page.route(/\/api\/documents\/[^/?]+(?:\?.*)?$/, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    const data = await response.json() as FlowDocument
    data.creator = { ...data.creator, displayName: 'Skyler Anderson', name: 'Skyler Anderson' }
    data.updatedAt = '2026-10-29T06:26:00Z'
    data.revisions = []
    await route.fulfill({ response, json: data })
  })
  await page.reload()
  await page.locator('.document-edited').click()
  await expect(metadata).toBeVisible()
  expect((await metadata.boundingBox())!.width).toBeGreaterThan(shortWidth)
  const ownerAvatar = (await metadata.locator('.document-owner-row .document-meta-avatar').boundingBox())!
  const editorAvatar = (await metadata.locator('.document-last-edit-row .document-meta-avatar').boundingBox())!
  expect(Math.abs(ownerAvatar.x - editorAvatar.x)).toBeLessThanOrEqual(1)
  await expect(metadata.getByRole('button', { name: 'Show document history', exact: true })).toHaveCSS('height', '28px')
  await expect(metadata.getByRole('button', { name: 'Show document history', exact: true })).toHaveCSS('padding', '0px 10px 0px 8px')
  await expect(metadata.getByRole('button', { name: 'Show document history', exact: true })).toHaveCSS('border-top-width', '0px')
  await page.waitForTimeout(500)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/docs-metadata.png`, animations: 'disabled' })
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'Change document subscribers' }).click()
  const search = page.getByRole('textbox', { name: 'Change subscribers…', exact: true })
  await expect(search).toBeVisible()
  await expect(search).toHaveCSS('padding', '10px 0px 9px')
  await expect(page.locator('.document-subscribers__badge')).toHaveCSS('line-height', '16px')
  await expect(page.getByText('Other users', { exact: true })).toBeVisible()
  await search.fill('zheng')
  await expect(page.getByRole('checkbox', { name: 'zheng liu', exact: true })).toBeVisible()
  await expect(page.getByRole('checkbox', { name: 'Skyler Anderson', exact: true })).toHaveCount(0)
})

test('document insert and inline comments fit the same article without reflow', async ({ page, request, workspaceKey }) => {
  const response = await request.post('/api/documents', { headers: { 'X-Workspace-Key': workspaceKey }, data: { title: '[audit] Comments', content: 'Select this paragraph to discuss the release.' } })
  expect(response.status()).toBe(201)
  const doc = await response.json() as FlowDocument
  await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
  const editor = page.getByRole('textbox', { name: 'Document content', exact: true })
  await expect(editor).toContainText('Select this paragraph')
  const before = (await page.locator('.document-title').boundingBox())!.x
  await editor.locator('p').first().click({ clickCount: 3 })
  await expect(page.getByRole('button', { name: 'Move selection to new document', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Comment', exact: true }).click()
  const composer = page.locator('.document-thread-card.is-draft')
  await expect(composer).toBeVisible()
  expect((await page.locator('.document-title').boundingBox())!.x).toBe(before)
  expect((await composer.boundingBox())!.x + (await composer.boundingBox())!.width).toBeLessThanOrEqual(1440)
  await composer.getByRole('textbox').fill('A contained desktop comment.')
  await composer.getByRole('button', { name: 'Submit comment', exact: true }).click()
  await expect(page.locator('.document-thread-card:not(.is-draft)').getByText('A contained desktop comment.')).toBeVisible()
  const thread = page.locator('.document-thread-card:not(.is-draft)').first()
  expect((await thread.boundingBox())!.x + (await thread.boundingBox())!.width).toBeLessThanOrEqual(1440)
  await page.waitForTimeout(500)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/docs-comment.png`, animations: 'disabled' })
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.press('Enter')
  await page.keyboard.type('/')
  const menu = page.getByRole('listbox', { name: 'Insert block', exact: true })
  await expect(menu).toBeVisible()
  await expect(menu.getByRole('option')).toHaveCount(13)
  await expect(menu.getByRole('option', { name: /Heading 4|Callout/ })).toHaveCount(0)
  await expect(menu).toHaveCSS('max-height', '482.5px')
  await page.keyboard.press('Escape')
})

test('the same non-owner role and one-version history omit inapplicable controls', async ({ page, request, workspaceKey }) => {
  const response = await request.post('/api/documents', { headers: { 'X-Workspace-Key': workspaceKey }, data: { title: '[audit] History', content: 'The current document version.' } })
  expect(response.status()).toBe(201)
  const doc = await response.json() as FlowDocument
  const creator = { ...doc.creator, id: 'audit-author', name: 'Skyler Anderson', displayName: 'Skyler Anderson' }
  const revisions = [{ id: 'audit-current', documentId: doc.id, title: doc.title, content: doc.content, createdAt: doc.updatedAt, author: creator }]
  await page.route('**/api/bootstrap**', async route => {
    const response = await route.fetch()
    const data = await response.json() as BootstrapData
    data.viewerRole = 'member'
    data.users.push(creator)
    data.documents = data.documents.map(item => item.id === doc.id ? { ...item, creator, revisions, permissions: [{ id: 'audit-edit', documentId: doc.id, subjectType: 'user', subjectId: data.viewer.id, role: 'editor', createdAt: doc.createdAt }] } : item)
    await route.fulfill({ response, json: data })
  })
  await page.route(`**/api/documents/${doc.id}/history**`, route => route.fulfill({ json: { nodes: revisions } }))
  await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
  await page.getByRole('button', { name: 'Document options', exact: true }).click()
  const menu = page.getByRole('menu', { name: 'Document options', exact: true })
  await expect(menu.getByRole('menuitem', { name: 'People with access', exact: true })).toHaveCount(0)
  await menu.getByRole('menuitem', { name: 'Show document history', exact: true }).click()
  const history = page.getByRole('dialog', { name: /History for/ })
  await expect(history).toBeVisible()
  await expect(history.getByRole('button', { name: 'Restore version', exact: true })).toBeDisabled()
  await expect(history.getByRole('checkbox', { name: 'Highlight changes', exact: true })).toHaveCount(0)
  await expect(history.getByRole('button', { name: 'Next change', exact: true })).toHaveCount(0)
  await expect(history.locator('.document-history-date')).toBeVisible()
  await expect(history.locator('.document-history-scroll')).toHaveCSS('padding', '52px 16px 84px 24px')
  await page.waitForTimeout(500)
  const box = (await history.boundingBox())!
  expect(Math.abs(box.x - 210)).toBeLessThanOrEqual(1)
  expect(Math.abs(box.height - 721)).toBeLessThanOrEqual(1)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/docs-history.png`, animations: 'disabled' })
})
