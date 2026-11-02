import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true })

test('keyboard navigation clears the previous pointer highlight in display selects', async ({ page, workspaceKey }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  await page.getByRole('combobox', { name: 'Grouping', exact: true }).click()
  const first = page.getByRole('option').first()
  await first.hover()
  await expect.poll(() => first.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  await page.keyboard.press('End')
  await expect.poll(() => first.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).toBe('rgba(0, 0, 0, 0)')
  const focused = page.locator('[role=option]:focus')
  await expect.poll(() => focused.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  expect(await focused.evaluate(e => getComputedStyle(e).outlineStyle)).toBe('none')
})

test('skill keyboard highlight fits long labels and restores its trigger', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  for (const name of ['中文', 'Review release readiness for international integrations']) {
    expect((await request.post('/api/agent/skills', { headers, data: { name, instructions: 'Check release readiness.' } })).ok()).toBeTruthy()
  }
  await page.goto(`/${workspaceKey}/agent`)
  const trigger = page.getByRole('button', { name: 'Skills', exact: true })
  await trigger.click()
  const menu = page.getByRole('dialog', { name: 'Skills', exact: true })
  await expect(menu).toBeVisible()
  await menu.evaluate(async e => { await Promise.all(e.getAnimations().map(a => a.finished)) })
  const input = menu.getByRole('textbox', { name: 'Search skills…' })
  await input.press('ArrowDown')
  await expect(input).toBeFocused()
  const active = menu.locator('[role=option][aria-selected=true]')
  await expect(active).toHaveCount(1)
  await expect.poll(() => active.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  for (const row of await menu.getByRole('option').all()) {
    const geometry = await row.evaluate(e => {
      const r = e.getBoundingClientRect(), label = e.querySelector('span')!.getBoundingClientRect()
      return { height: r.height, fits: label.top >= r.top && label.bottom <= r.bottom }
    })
    expect(geometry.height).toBe(46)
    expect(geometry.fits).toBe(true)
  }
  await menu.getByRole('option', { name: '中文', exact: true }).click()
  await expect(menu.getByRole('option', { name: '中文', exact: true }).locator('.lucide-check')).toBeVisible()
  await input.press('Escape')
  await expect(menu).not.toBeVisible()
  await expect(trigger).toBeFocused()
})

test('team phone menu and subscription level share row geometry and return focus', async ({ page, request, workspaceKey }) => {
  const data = await (await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })).json() as BootstrapData
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/all`)
  await page.getByRole('button', { name: 'Open sidebar', exact: true }).click()
  await page.getByRole('button', { name: 'Team menu', exact: true }).first().click()
  const menu = page.locator('.sidebar-team-menu[role=menu]')
  await expect(menu).toBeVisible()
  await menu.evaluate(async e => { await Promise.all(e.getAnimations().map(a => a.finished)) })
  expect((await menu.boundingBox())!.width).toBeCloseTo(358, 0)
  const subscribe = menu.getByRole('menuitem', { name: 'Subscribe', exact: true })
  expect((await subscribe.boundingBox())!.height).toBe(46)
  await subscribe.click()
  const submenu = page.locator('.sidebar-team-subscribe-menu[role=menu]')
  await expect(submenu).toBeVisible()
  await submenu.evaluate(async e => { await Promise.all(e.getAnimations().map(a => a.finished)) })
  const row = submenu.getByRole('menuitemcheckbox').first()
  expect((await row.boundingBox())!.height).toBe(46)
  await row.focus()
  await expect.poll(() => row.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  await submenu.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(menu).toBeVisible()
  await expect(subscribe).toBeFocused()
})
