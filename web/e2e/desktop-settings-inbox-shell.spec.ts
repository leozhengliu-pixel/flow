import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.use({ viewport: { width: 1440, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] })

test('keeps desktop inbox insets on the toolbar and its actions usable', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  await expect(page.locator('.flow-inbox__header')).toHaveCSS('padding', '0px')
  await expect(page.locator('.flow-inbox__header-inner')).toHaveCSS('display', 'flex')
  await expect(page.locator('.flow-inbox__header-inner')).toHaveCSS('margin', '0px 8px')
  await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Show unreads only', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Show read', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Notification actions', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'Go to settings', exact: true })).toBeVisible()
  await page.getByRole('menu').evaluate(async (node) => { await Promise.all(node.getAnimations({ subtree: true }).map(animation => animation.finished)) })
  if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/desktop-inbox.png` })
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menuitem', { name: 'Go to settings', exact: true })).toHaveCount(0)
})

test('keeps the desktop toast dismiss icon inside its padded target', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap', { headers })
  const data = await bootstrap.json() as BootstrapData
  const created = await request.post('/api/issues', { headers, data: { title: 'Toast verification', teamId: data.teams[0].id } })
  expect(created.status()).toBe(201)
  const issue = await created.json() as Issue
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await page.getByRole('button', { name: 'Copy issue URL', exact: true }).click()
  await expect(page.locator('.flow-toast__title')).toHaveText('Issue URL copied to clipboard')
  const dismiss = page.locator('.flow-toast__close')
  await expect(dismiss).toBeVisible()
  await expect(dismiss).toHaveCSS('padding', '6px')
  await expect(dismiss).toHaveCSS('width', '24px')
  await expect(dismiss.locator('svg')).toHaveCSS('width', '12px')
  await expect(page.locator('[data-sonner-toast]')).toHaveAttribute('data-mounted', 'true')
  await page.locator('[data-sonner-toast]').evaluate(async (node) => { await Promise.all(node.getAnimations({ subtree: true }).map(animation => animation.finished)) })
  if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/desktop-toast.png` })
  await dismiss.click()
  await expect(dismiss).toHaveCount(0)
})
