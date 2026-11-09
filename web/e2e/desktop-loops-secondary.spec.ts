import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { Loop } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop loop surfaces')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: 'dark' }))
    localStorage.setItem('flow:locale', 'en-US')
  })
})

test('keeps loop list, hub and editor metrics tied to their controls', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  await page.goto(`/${workspaceKey}/agent`)
  await page.getByRole('button', { name: 'Switch agent chat', exact: true }).click()
  await expect(page.getByTestId('agent-chat-switcher-popover')).toHaveCSS('background-color', 'lch(12.72 0.85 272)')
  await expect(page.getByText('No history', { exact: true })).toHaveCSS('font-size', '13px')
  await page.goto(`/${workspaceKey}/loops`)
  const hub = page.locator('.loops-hub-composer')
  await expect(hub).toBeVisible()
  await expect(page.getByRole('tab', { name: 'My loops', exact: true })).toBeVisible()
  expect(Math.abs((await page.locator('.loops-hub-header h2').boundingBox())!.y - 144)).toBeLessThanOrEqual(1)
  await expect(hub).toHaveCSS('padding', '12px')
  await expect(hub.locator('textarea')).toHaveCSS('height', '48px')
  await expect(hub.locator('textarea')).toHaveCSS('padding', '2px 6px')
  await expect(hub.locator('footer')).toHaveCSS('height', '24px')
  await expect(page.getByText('or pick a template', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Engineering', exact: true })).toBeVisible()

  const response = await request.post('/api/loops', { headers, data: { name: 'Daily review', level: 'workspace', status: 'draft', triggerType: 'schedule', triggerConfig: { interval: 1, unit: 'day', time: '23:00' } } })
  expect(response.status()).toBe(201)
  const loop = await response.json() as Loop
  await page.goto(`/${workspaceKey}/loops/all`)
  await expect(page.getByRole('searchbox', { name: 'Find loops…' })).toHaveCSS('padding', '6px 25px 6px 32px')
  await expect(page.getByRole('columnheader', { name: 'Runs (30d)', exact: true })).toBeVisible()
  await expect(page.locator('.loops-badge').first()).toHaveCSS('line-height', 'normal')
  await expect(page.locator('.loops-group-row').first()).toHaveCSS('background-color', 'lch(9.232 0.85 272)')
  await page.goto(`/${workspaceKey}/loops/new?draftId=${loop.id}`)
  await expect(page.locator('.loops-section-title').first()).toHaveCSS('line-height', 'normal')
  await expect(page.getByRole('button', { name: 'Compose with Agent' })).toHaveCSS('padding', '0px 8px')
  await expect(page.getByRole('combobox', { name: 'Interval unit', exact: true })).toHaveCSS('height', '26px')
  await expect(page.getByRole('spinbutton', { name: 'Interval', exact: true })).toHaveCSS('height', '26px')
  await expect(page.getByRole('button', { name: 'Team access', exact: true })).toHaveCSS('height', '30px')
  await expect(page.getByRole('region', { name: 'Capabilities & access' })).toBeVisible()
})

test('aligns the run panes and keeps the run question composer usable', async ({ page, request, workspaceKey }) => {
  const response = await request.post('/api/loops', { headers: { 'X-Workspace-Key': workspaceKey }, data: { name: 'Daily review', instructions: 'Review the workspace.', level: 'workspace', status: 'published', enabled: false } })
  expect(response.status()).toBe(201)
  const loop = await response.json() as Loop
  const run = { id: 'desktop-run', loopId: loop.id, status: 'completed', trigger: 'manual', triggerLabel: 'Manual run', startedAt: '2026-10-30T10:00:00Z', finishedAt: '2026-10-30T10:00:01Z', output: 'Review complete.' }
  await page.route(`**/api/loops/${loop.id}/runs**`, route => route.fulfill({ json: route.request().url().endsWith('/runs') ? [run] : run }))
  await page.goto(`/${workspaceKey}/loop/${loop.id}/run/${run.id}`)
  const composer = page.locator('.loops-run-composer')
  await expect(composer.getByRole('textbox', { name: 'Ask about this run…' })).toBeVisible()
  await expect(page.locator('.loops-run-tabs')).toHaveCSS('height', '43.5px')
  await expect(page.locator('.loops-run-header h1')).toHaveCSS('font-size', '13px')
  await expect(page.locator('.loops-run-header h1')).toHaveCSS('font-weight', '500')
  await expect(composer).toHaveCSS('margin', '0px 8px 8px')
  await expect(composer.getByRole('button', { name: 'Skills', exact: true })).toHaveCount(0)
  await expect(composer.getByRole('button', { name: 'Add files and more', exact: true })).toBeVisible()
  await expect(page.getByPlaceholder('Search runs by issue ID or title…')).toBeVisible()
  await composer.getByRole('textbox').fill('What changed?')
  await expect(composer.getByRole('button', { name: 'Send message', exact: true })).toBeEnabled()
})
