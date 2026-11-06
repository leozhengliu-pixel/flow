import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

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

test('skill sheet rows fit long labels, and a chosen skill becomes a chip in the message', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  for (const name of ['中文', 'Review release readiness for international integrations']) {
    expect((await request.post('/api/agent/skills', { headers, data: { name, instructions: 'Check release readiness.' } })).ok()).toBeTruthy()
  }
  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.getByRole('button', { name: 'Skills', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Add files and more', exact: true }).click()
  const menu = page.getByRole('dialog', { name: 'Add files and more', exact: true })
  await expect(menu).toBeVisible()
  await menu.evaluate(async e => { await Promise.all(e.getAnimations().map(a => a.finished)) })
  await menu.getByRole('option', { name: 'Skill', exact: true }).click()
  // The sheet is replaced by the skills (its own dialog).
  const sheet = page.locator('.phone-menu-surface').last()
  const input = sheet.getByRole('combobox')
  await expect(input).toHaveAttribute('placeholder', 'Search skills…')
  await sheet.evaluate(async e => { await Promise.all(e.getAnimations({ subtree: true }).map(a => a.finished)) })
  await expect.poll(() => sheet.getByRole('option').first().evaluate(e => e.getBoundingClientRect().height)).toBe(46)
  await input.press('ArrowDown')
  await expect(input).toBeFocused()
  const active = sheet.locator('[cmdk-item][data-selected="true"]')
  await expect(active).toHaveCount(1)
  await expect.poll(() => active.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  for (const row of await sheet.getByRole('option').all()) {
    const geometry = await row.evaluate(e => {
      const r = e.getBoundingClientRect(), label = e.querySelector('span')!.getBoundingClientRect()
      return { height: r.height, fits: label.top >= r.top && label.bottom <= r.bottom }
    })
    expect(geometry.height).toBe(46)
    expect(geometry.fits).toBe(true)
  }
  await sheet.getByRole('option', { name: '中文', exact: true }).click()
  await expect(sheet).not.toBeVisible()
  await expect(page.locator('[role=textbox] [data-mention-type="skill"]')).toHaveCount(1)
})

test('team phone menu and subscription level share row geometry and return to the parent filter', async ({ page, request, workspaceKey }) => {
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
  const row = submenu.getByRole('menuitemcheckbox', { name: 'An issue is added to the team', exact: true, includeHidden: true })
  expect((await row.boundingBox())!.height).toBe(46)
  const filter = submenu.getByRole('textbox', { name: 'Filter…', exact: true })
  await expect(filter).toBeFocused()
  await filter.fill('triage')
  await expect(row).toBeHidden()
  await expect(submenu.getByRole('menuitemcheckbox')).toHaveCount(1)
  await expect(submenu.getByRole('menuitemcheckbox', { name: 'An issue is added to the triage queue', exact: true })).toBeVisible()
  if (process.env.FLOW_CI_SHOTS) await submenu.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/team-subscription-filtered.png`, animations: 'disabled' })
  await filter.fill('')
  await expect(row).toBeVisible()
  await filter.press('ArrowDown')
  await expect(row).toBeFocused()
  await expect.poll(() => row.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  if (process.env.FLOW_CI_SHOTS) await submenu.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/team-subscription-keyboard.png`, animations: 'disabled' })
  await page.keyboard.press('ArrowLeft')
  await expect(submenu).not.toBeVisible()
  await expect(menu).toBeVisible()
  await expect(menu.getByRole('textbox', { name: 'Filter…', exact: true })).toBeFocused()
  if (process.env.FLOW_CI_SHOTS) await menu.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/team-menu-parent-filter.png`, animations: 'disabled' })
})

test('checked document subscribers keep the shared highlight under pointer and keyboard', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.post('/api/documents', { headers, data: { title: 'Subscriber highlight check' } })
  expect(response.status()).toBe(201)
  const document = await response.json() as { slugId: string }
  await page.goto(`/${workspaceKey}/document/${document.slugId}`)
  await page.getByRole('button', { name: 'Change document subscribers', exact: true }).click()
  const panel = page.locator('.document-subscribers')
  const person = panel.getByRole('checkbox').first()
  if (await person.getAttribute('aria-checked') !== 'true') await person.click()
  await expect(person).toHaveAttribute('aria-checked', 'true')
  await person.hover()
  await expect.poll(() => person.evaluate(e => {
    const paint = getComputedStyle(e, '::before')
    return { background: paint.backgroundColor, inset: paint.inset, radius: paint.borderRadius }
  })).toMatchObject({ inset: '2px 0px', radius: '8px' })
  await expect.poll(() => person.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
  await page.mouse.move(0, 0)
  await panel.getByRole('textbox').focus()
  await page.keyboard.press('Tab')
  await expect(person).toBeFocused()
  await expect.poll(() => person.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
})
