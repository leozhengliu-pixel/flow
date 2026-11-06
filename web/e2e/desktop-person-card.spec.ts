import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

// The person card sits on device-pixel positions (a left edge on a half pixel at a real scale of 2) and the reference text widths need the
// subpixel text positioning of a real Mac browser, so this file runs at the browser's own scale factor with the same text flags as
// desktop-settings-panel-offset.spec.ts (an emulated scale factor lays a 0.5px border out as 1px).
test.use({ viewport: { width: 1440, height: 900 }, timezoneId: 'Pacific/Auckland', launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] } })

test('the person card has the reference card measurements: a 32px avatar, 13px name, 12px handle and 12px detail rows', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  expect((await request.patch('/api/account/settings', { headers, data: { timezone: 'Pacific/Auckland' } })).ok()).toBe(true)
  await page.clock.setFixedTime(new Date('2026-11-04T12:00:00Z'))
  const data = await (await request.get('/api/bootstrap', { headers })).json()
  const team = data.teams[0]
  expect((await request.post('/api/issues', { headers, data: { title: 'Person card fixture', teamId: team.id, stateId: 'state_todo', assigneeId: data.viewer.id } })).status()).toBe(201)
  await page.goto(`/${workspaceKey}/team/${team.key}/all`)
  const trigger = page.locator('[data-property="assignee"]').first()
  await trigger.hover()
  const card = page.locator('.assignee-person-surface')
  await expect(card).toBeVisible()
  await page.waitForTimeout(400)
  const measured = await card.evaluate(root => {
    const box = (el: Element) => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height } }
    const style = (el: Element, ...props: string[]) => Object.fromEntries(props.map(prop => [prop, getComputedStyle(el).getPropertyValue(prop)]))
    const header = root.querySelector('header')!
    const avatar = root.querySelector('header .user-avatar')!
    const initials = root.querySelector('header .user-avatar-initials')!
    const handle = root.querySelector('header > div > span')!
    const details = root.querySelector('.assignee-person-card__details')!
    return {
      card: { ...style(root, 'padding-top', 'padding-left', 'border-top-left-radius'), height: box(root).height },
      header: { ...style(header, 'padding-top', 'padding-left', 'column-gap'), height: box(header).height },
      avatar: { ...box(avatar), ...style(avatar, 'box-shadow', 'border-top-left-radius') },
      initials: style(initials, 'font-size', 'font-weight'),
      name: style(root.querySelector('header strong')!, 'font-size', 'font-weight', 'line-height'),
      handle: style(handle, 'font-size', 'font-weight', 'line-height'),
      details: { ...style(details, 'padding-top', 'padding-left', 'padding-bottom', 'row-gap'), rows: [...details.children].map(row => box(row).height), icons: [...details.querySelectorAll('svg, i')].map(icon => box(icon).width) },
    }
  })
  expect(measured.card).toMatchObject({ 'padding-top': '5px', 'padding-left': '8px', 'border-top-left-radius': '8px' })
  expect(measured.header).toMatchObject({ 'padding-top': '4px', 'padding-left': '4px', 'column-gap': '10px', height: 44 })
  expect(measured.avatar).toMatchObject({ width: 32, height: 32, 'box-shadow': 'none', 'border-top-left-radius': '50%' })
  expect(measured.initials).toEqual({ 'font-size': '14px', 'font-weight': '400' })
  expect(measured.name).toEqual({ 'font-size': '13px', 'font-weight': '500', 'line-height': '18px' })
  expect(measured.handle).toEqual({ 'font-size': '12px', 'font-weight': '450', 'line-height': '18px' })
  expect(measured.details).toMatchObject({ 'padding-top': '12px', 'padding-left': '4px', 'padding-bottom': '6px', 'row-gap': '8px' })
  expect(measured.details.rows).toEqual([18, 18, 18])
  expect(measured.details.icons).toEqual([16, 16, 16])
  // The card opens 8px from its trigger, left-aligned with it like the reference card.
  const cardBox = (await card.boundingBox())!
  const triggerBox = (await trigger.boundingBox())!
  expect(cardBox.y - (triggerBox.y + triggerBox.height)).toBe(8)
})

test('an activity avatar opens the person card above it, 8px away and aligned with its left edge', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json()
  const created = await request.post('/api/issues', { headers, data: { title: 'Activity card fixture', teamId: data.teams[0].id, stateId: 'state_todo' } })
  expect(created.status()).toBe(201)
  const issue = await created.json()
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  const trigger = page.locator('.person-card-trigger [data-user-avatar]').first()
  await trigger.hover()
  const card = page.locator('.assignee-person-surface')
  await expect(card).toBeVisible()
  await page.waitForTimeout(400)
  const triggerBox = (await trigger.boundingBox())!
  const cardBox = (await card.boundingBox())!
  expect([triggerBox.width, triggerBox.height]).toEqual([14, 14])
  // The card is placed 8px above the trigger, left-aligned with it, and its top-left corner is rounded to the device pixel grid (Math.round of the
  // position times the scale, over the scale). The reference card shows the same rule at a scale of 2: its top is 417.75 - 8 - 149 = 260.75, which
  // lands on 261 (a 7.75px gap), and its left edge is 316.91 -> 317.
  const scale = await page.evaluate(() => devicePixelRatio)
  const snap = (value: number) => Math.round(value * scale) / scale
  expect(cardBox.y).toBe(snap(triggerBox.y - 8 - cardBox.height))
  expect(cardBox.x).toBe(snap(triggerBox.x))
  await expect(card.locator('header .user-avatar')).toHaveCSS('width', '32px')
  await expect(card.locator('header .user-avatar-initials')).toHaveCSS('font-size', '14px')
  await expect(card.locator('header .user-avatar-initials')).toHaveCSS('font-weight', '400')
  await page.mouse.move(700, 100)
  await page.mouse.move(720, 120)
  await expect(card).not.toBeVisible()
})
