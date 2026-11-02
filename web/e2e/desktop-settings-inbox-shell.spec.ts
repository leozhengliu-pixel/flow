import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.use({ viewport: { width: 1440, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] })

test('offers desktop review onboarding with both code-host connections', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/reviews`)
  const onboarding = page.getByRole('region', { name: 'Get started with reviews' })
  await expect(onboarding).toBeVisible()
  for (const name of ['Guided reviews', 'Review inbox', 'GitHub sync', 'Iterate']) await expect(onboarding.getByRole('heading', { name, exact: true })).toBeVisible()
  await expect(onboarding.getByRole('img', { name: 'Review preview' })).toBeVisible()
  await expect(onboarding.getByRole('link', { name: 'Learn more' })).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/blob/main/docs/code-scanning-review.md')
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  const preview = onboarding.getByRole('img', { name: 'Review preview' })
  expect(await preview.evaluate(node => node.scrollHeight <= node.clientHeight && node.scrollWidth <= node.clientWidth)).toBe(true)
  if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/desktop-reviews.png` })
  await page.setViewportSize({ width: 1200, height: 900 })
  expect(await preview.evaluate(node => node.scrollHeight <= node.clientHeight && node.scrollWidth <= node.clientWidth)).toBe(true)
  await onboarding.getByRole('button', { name: 'Connect personal GitHub account' }).click()
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/integrations/github$`))
  await page.goto(`/${workspaceKey}/reviews`)
  await page.getByRole('button', { name: 'Connect GitLab', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/integrations/gitlab$`))
})

test.describe('phone review connection view', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  test('keeps the existing compact code-host connection actions', async ({ page, workspaceKey }) => {
    await page.goto(`/${workspaceKey}/reviews`)
    await expect(page.getByRole('button', { name: 'Connect GitHub', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Connect GitLab', exact: true })).toBeVisible()
    await expect(page.getByRole('region', { name: 'Get started with reviews' })).toHaveCount(0)
    await expect(page.locator('.reviews-topbar-inner')).toHaveCSS('display', 'contents')
    await page.evaluate(() => document.fonts.ready)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_UI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_UI_SHOTS}/phone-reviews.png` })
    await page.getByRole('button', { name: 'Connect GitLab', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/integrations/gitlab$`))
  })
})

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
