import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

async function initialsFit(avatar: Locator) {
  await expect(avatar).toBeVisible()
  const bounds = await avatar.evaluate(element => {
    const initials = element.matches('.workspace-initials') ? element : element.querySelector('.workspace-initials')
    if (!initials) throw new Error('Workspace initials missing')
    const range = document.createRange()
    range.selectNodeContents(initials)
    const text = range.getBoundingClientRect()
    const box = element.getBoundingClientRect()
    return { text: text.toJSON(), box: box.toJSON() }
  })
  const rounding = 1 / 64
  expect(bounds.text.left).toBeGreaterThanOrEqual(bounds.box.left - rounding)
  expect(bounds.text.right).toBeLessThanOrEqual(bounds.box.right + rounding)
  expect(bounds.text.top).toBeGreaterThanOrEqual(bounds.box.top - rounding)
  expect(bounds.text.bottom).toBeLessThanOrEqual(bounds.box.bottom + rounding)
  expect(Math.abs((bounds.text.left + bounds.text.right - bounds.box.left - bounds.box.right) / 2)).toBeLessThanOrEqual(rounding)
}

async function openDrawer(page: Page, phone: boolean) {
  if (phone) await page.locator('[data-sidebar-trigger]').click()
  await expect(page.locator('.workspace-menu-trigger')).toBeVisible()
  if (phone) {
    await expect(page.locator('[data-sidebar-surface]')).toHaveCSS('transform', 'none')
    await expect(page.locator('[data-sidebar-surface]')).toHaveCSS('width', '330px')
  }
}

for (const width of [1440, 390]) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`${width}px ${colorScheme} workspace menu`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme })
      for (const name of ['中文团队', '中a', 'a中', 'WW', 'MM', 'A', '中文团队 with a workspace name long enough to require truncation']) {
        test(`keeps initials contained and trigger hit areas stable for ${name}`, async ({ page, request, workspaceKey }, testInfo) => {
          test.skip(testInfo.project.name !== 'chromium', 'The matrix sets both viewport sizes itself')
          const phone = width === 390
          const headers = { 'X-Workspace-Key': workspaceKey }
          let actionPositions: number[] | undefined
          // Compare each name with the shortest label in this test's own workspace.
          for (const currentName of name === 'A' ? [name] : ['A', name]) {
            expect((await request.patch(`/api/workspaces/${workspaceKey}`, { headers, data: { name: currentName } })).ok()).toBe(true)
            await page.goto(`/${workspaceKey}/my-issues/assigned`)
            await expect(page.locator('.workspace-menu-trigger strong')).toHaveText(currentName)
            await openDrawer(page, phone)
            await page.evaluate(() => document.fonts.ready)
            const trigger = page.locator('.workspace-menu-trigger')
            await initialsFit(trigger.locator('.workspace-avatar'))
            const actions = await page.locator('.sidebar-top-action').evaluateAll(elements => elements.map(element => {
              const box = element.getBoundingClientRect()
              return { x: box.x, width: box.width }
            }))
            expect(actions.map(action => action.width)).toEqual(phone ? [32, 32] : [28, 28])
            if (actionPositions) actions.forEach((action, index) => expect(action.x).toBeCloseTo(actionPositions![index], 1))
            else actionPositions = actions.map(action => action.x)
            if (currentName !== name) continue
            const chevron = trigger.locator(':scope > svg')
            await expect(chevron).toBeInViewport({ ratio: 1 })
            if (!phone) {
              expect(await chevron.evaluate(element => getComputedStyle(element).color)).toBe(await trigger.locator('strong').evaluate(element => getComputedStyle(element).color))
              expect(await chevron.evaluate(element => getComputedStyle(element).fill)).toBe(await chevron.evaluate(element => getComputedStyle(element).color))
            }
            const triggerBox = (await trigger.boundingBox())!
            const iconBox = (await chevron.boundingBox())!
            expect(iconBox.x + iconBox.width).toBeLessThanOrEqual(triggerBox.x + triggerBox.width)
            const points = [{ x: 1, y: 14 }, { x: 15, y: 14 }, { x: triggerBox.width / 2, y: 14 }, { x: triggerBox.width - 2, y: 14 }, { x: iconBox.x - triggerBox.x + iconBox.width / 2, y: iconBox.y - triggerBox.y + iconBox.height / 2 }]
            for (const position of points) {
              await trigger.click({ position })
              const menu = page.locator(phone ? '.workspace-phone-menu' : '.workspace-primary-menu')
              await expect(menu).toBeVisible()
              if (!phone) {
                const settings = menu.getByRole('menuitem', { name: /^Settings/ })
                await settings.hover()
                await expect(settings).toHaveCSS('border-radius', '8px')
                await expect(settings).toHaveCSS('height', '32px')
                const box = (await menu.boundingBox())!
                const row = (await settings.boundingBox())!
                expect(row.x - box.x).toBeGreaterThanOrEqual(6)
                expect(box.x + box.width - row.x - row.width).toBeGreaterThanOrEqual(6)
                await expect(menu.locator('.workspace-switch-arrow')).toHaveText('▶')
              }
              await page.keyboard.press('Escape')
              await expect(menu).toBeHidden()
            }
            expect(await page.locator('.sidebar-top-action').evaluateAll(elements => elements.map(element => {
              const box = element.getBoundingClientRect()
              return { x: box.x, width: box.width }
            }))).toEqual(actions)
            if (process.env.FLOW_CI_SHOTS && name === '中文团队') await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/cjk-${width}-${colorScheme}.png` })
          }
        })
      }
    })
  }
}

test('switches real workspaces by click and keyboard, and bounds a long submenu', async ({ page, request, workspaceKey }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'desktop interaction coverage')
  await page.setViewportSize({ width: 1440, height: 900 })
  const created = await request.post('/api/workspaces', { data: { name: '中文团队', urlKey: `${workspaceKey.slice(0, 36)}-other`, region: 'us', tutorial: false } })
  expect(created.status()).toBe(201)
  const second = (await created.json()).workspace
  const secondKey = second.urlKey
  await page.route('**/api/account/bootstrap', async route => {
    const response = await route.fetch()
    const account = await response.json()
    account.workspaces = account.workspaces.filter((item: { workspace: { urlKey: string } }) => [workspaceKey, secondKey].includes(item.workspace.urlKey))
    await route.fulfill({ response, json: account })
  })
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  const trigger = page.locator('.workspace-menu-trigger')
  await trigger.focus()
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('.workspace-primary-menu')).toBeVisible()
  await expect(page.locator('.workspace-primary-menu').getByRole('menuitem').first()).toBeFocused()
  const highlight = await page.locator('html').getAttribute('data-theme') === 'light' ? 'lch(95 0 282)' : 'lch(20.82 1.3 272)'
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('.workspace-primary-menu').getByRole('menuitem', { name: 'Invite and manage members' })).toBeFocused()
  await page.keyboard.press('ArrowUp')
  await expect(page.locator('.workspace-primary-menu').getByRole('menuitem').first()).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('.workspace-primary-menu').getByRole('menuitem', { name: 'Invite and manage members' })).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('[data-workspace-switch-trigger]')).toBeFocused()
  await page.keyboard.press('ArrowRight')
  const submenu = page.locator('.workspace-switch-menu')
  await expect(submenu).toBeVisible()
  await expect(page.locator('[data-workspace-switch-trigger]')).toHaveCSS('background-color', highlight)
  await expect(submenu.locator('.workspace-avatar')).toHaveCount(2)
  await page.keyboard.press('ArrowLeft')
  await expect(submenu).toBeHidden()
  await expect(page.locator('[data-workspace-switch-trigger]')).toBeFocused()
  await page.keyboard.press('ArrowRight')
  await expect(submenu).toBeVisible()
  await initialsFit(submenu.getByRole('menuitem', { name: '中文团队', exact: true }).locator('.workspace-avatar'))
  await submenu.getByRole('menuitem', { name: '中文团队', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/${secondKey}/`))
  await expect(trigger.locator('strong')).toHaveText('中文团队')
  await page.locator('.sidebar').click({ position: { x: 2, y: 50 } })
  await page.keyboard.press('o')
  await page.keyboard.press('w')
  await expect(submenu).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(submenu).toBeHidden()
  await expect(trigger).toBeFocused()

  await page.route('**/api/account/bootstrap', async route => {
    const response = await route.fetch()
    const account = await response.json()
    const membership = account.workspaces.find((item: { workspace: { id: string } }) => item.workspace.id === second.id)
    const original = account.workspaces.find((item: { workspace: { urlKey: string } }) => item.workspace.urlKey === workspaceKey)
    account.workspaces = Array.from({ length: 65 }, (_, index) => ({ ...membership, workspace: { ...second, id: `stress-${index}`, name: `Workspace ${index + 1}` } }))
    account.workspaces.unshift(original)
    account.workspaces.push(membership)
    await route.fulfill({ response, json: account })
  })
  await page.reload()
  await trigger.click()
  await page.locator('[data-workspace-switch-trigger]').hover()
  await expect(submenu).toBeInViewport({ ratio: 1 })
  expect(await submenu.evaluate(element => element.scrollHeight > element.clientHeight)).toBe(true)
  await submenu.getByRole('menuitem', { name: '中文团队', exact: true }).scrollIntoViewIfNeeded()
  await expect(submenu.getByRole('menuitem', { name: '中文团队', exact: true })).toBeInViewport({ ratio: 1 })
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/many-workspaces-desktop.png` })
  await page.keyboard.press('Escape')
  for (const width of [375, 390]) {
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await page.goto(`/${secondKey}/my-issues/assigned`)
    await openDrawer(page, true)
    await trigger.focus()
    await page.keyboard.press('o')
    await page.keyboard.press('w')
    const phone = page.locator('.workspace-phone-menu')
    await expect(phone).toBeInViewport({ ratio: 1 })
    await expect(phone.getByRole('option')).toHaveCount(68)
    const current = phone.getByRole('option', { name: '中文团队', exact: true })
    await current.scrollIntoViewIfNeeded()
    await expect(current).toBeInViewport({ ratio: 1 })
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/many-workspaces-${width}.png` })
    const original = phone.getByRole('option', { name: `Verify ${workspaceKey.slice(-8)}`, exact: true })
    await original.scrollIntoViewIfNeeded()
    await original.click()
    await expect(phone).toBeHidden()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/`))
  }
})

for (const width of [1440, 375, 390]) {
  test(`workspace menu shortcuts and log out work at ${width}px`, async ({ page, workspaceKey }, testInfo) => {
    test.skip(testInfo.project.name !== 'chromium', 'The matrix sets both viewport sizes itself')
    const phone = width !== 1440
    await page.setViewportSize({ width, height: phone ? 844 : 900 })
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    if (!phone) {
      await page.getByRole('separator', { name: 'Resize sidebar', exact: true }).click()
      await expect(page.locator('html')).toHaveAttribute('data-sidebar-mode', 'collapsed')
      await page.locator('[data-sidebar-trigger]').click()
    } else await openDrawer(page, true)
    const trigger = page.locator('.workspace-menu-trigger')
    if (!phone) {
      await trigger.click()
      await expect(page.locator('.workspace-primary-menu')).toBeInViewport({ ratio: 1 })
      await expect(page.locator('.workspace-primary-menu')).toHaveCSS('opacity', '1')
      if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/collapsed-workspace-menu.png`, clip: { x: 0, y: 0, width: 400, height: 300 }, animations: 'disabled' })
      await page.keyboard.press('Escape')
    }
    await trigger.focus()
    await page.keyboard.press('g')
    await page.keyboard.press('s')
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/workspace$`))
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await expect(page.locator('[data-sidebar-surface]')).toBeAttached()
    if (await page.locator('[data-sidebar-surface]').getAttribute('aria-hidden') === 'true') await page.locator('[data-sidebar-trigger]').click()
    await trigger.click()
    const menu = page.locator(phone ? '.workspace-phone-menu' : '.workspace-primary-menu')
    const logout = page.waitForResponse(response => response.url().endsWith('/api/auth/logout') && response.request().method() === 'POST')
    await menu.getByRole(phone ? 'option' : 'menuitem', { name: /^Log out/ }).click()
    expect((await logout).ok()).toBe(true)
    await expect(menu).toBeHidden()
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await expect(page.locator('[data-sidebar-surface]')).toBeAttached()
    if (await page.locator('[data-sidebar-surface]').getAttribute('aria-hidden') === 'true') await page.locator('[data-sidebar-trigger]').click()
    await trigger.focus()
    const shortcut = page.waitForResponse(response => response.url().endsWith('/api/auth/logout') && response.request().method() === 'POST')
    await page.keyboard.press('Alt+Shift+Q')
    expect((await shortcut).ok()).toBe(true)
  })
}

for (const width of [1440, 390]) {
  test(`workspace menu routes and shared avatars work in Chinese at ${width}px`, async ({ page, request, workspaceKey }, testInfo) => {
    test.skip(testInfo.project.name !== 'chromium', 'The matrix sets both viewport sizes itself')
    const phone = width === 390
    await page.setViewportSize({ width, height: phone ? 844 : 900 })
    const headers = { 'X-Workspace-Key': workspaceKey }
    expect((await request.patch(`/api/workspaces/${workspaceKey}`, { headers, data: { name: '中文团队' } })).ok()).toBe(true)
    const settings = await (await request.get('/api/account/settings', { headers })).json()
    expect((await request.patch('/api/account/settings', { headers, data: { ...settings, language: 'zh-CN' } })).ok()).toBe(true)
    await page.addInitScript(() => localStorage.setItem('flow:locale', 'zh-CN'))
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await openDrawer(page, phone)
    const trigger = page.locator('.workspace-menu-trigger')
    await expect(trigger).toHaveAccessibleName('中文团队 工作区菜单')
    await trigger.click()
    const menu = page.locator(phone ? '.workspace-phone-menu' : '.workspace-primary-menu')
    await menu.getByRole(phone ? 'option' : 'menuitem', { name: /^设置/ }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/workspace$`))
    await initialsFit(page.locator('.workspace-logo-initials'))
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/cjk-settings-${width}.png` })

    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await openDrawer(page, phone)
    await trigger.click()
    await menu.getByRole(phone ? 'option' : 'menuitem', { name: '邀请和管理成员', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/members$`))
    await page.getByRole('button', { name: '邀请', exact: true }).click()
    await expect(page.locator('.invite-members-dialog')).toHaveCSS('opacity', '1')
    await initialsFit(page.locator('.invite-members-dialog .workspace-avatar'))
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/cjk-invite-${width}.png`, animations: 'disabled' })
    await page.keyboard.press('Escape')

    const logo = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#208760"/><path d="M8 16l6 6 10-13" fill="none" stroke="white" stroke-width="4"/></svg>')
    expect((await request.post(`/api/workspaces/${workspaceKey}/logo`, { headers, multipart: { file: { name: 'workspace-test.svg', mimeType: 'image/svg+xml', buffer: logo } } })).ok()).toBe(true)
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await openDrawer(page, phone)
    const image = trigger.locator('.workspace-avatar img')
    await expect(image).toBeVisible()
    expect(await image.evaluate(element => (element as HTMLImageElement).naturalWidth)).toBeGreaterThan(0)
    await trigger.click()
    await menu.getByRole(phone ? 'option' : 'menuitem', { name: /^切换工作区/ }).click()
    const switcher = page.locator(phone ? '.workspace-phone-menu' : '.workspace-switch-menu')
    const uploaded = switcher.locator('.workspace-avatar img')
    await expect(uploaded).toBeVisible()
    expect(await uploaded.evaluate(element => (element as HTMLImageElement).naturalWidth)).toBeGreaterThan(0)
    await uploaded.scrollIntoViewIfNeeded()
    await expect(uploaded).toBeInViewport({ ratio: 1 })
    await expect(switcher).toHaveCSS('opacity', '1')
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/uploaded-switch-${width}.png`, animations: 'disabled' })
    await page.keyboard.press('Escape')
    if (!phone) await page.keyboard.press('Escape')
    await page.goto(`/${workspaceKey}/settings/workspace`)
    const settingsImage = page.locator('.workspace-logo-control img')
    await expect(settingsImage).toBeVisible()
    expect(await settingsImage.evaluate(element => (element as HTMLImageElement).naturalWidth)).toBeGreaterThan(0)

    expect((await request.delete(`/api/workspaces/${workspaceKey}/logo`, { headers })).ok()).toBe(true)
    const api = process.env.FLOW_MOBILE_API_URL ?? 'http://127.0.0.1:4180'
    const registered = await request.post(`${api}/oauth/register`, { data: { client_name: 'Workspace menu test', redirect_uris: ['http://127.0.0.1:43119/callback'], token_endpoint_auth_method: 'none' } })
    expect(registered.ok()).toBe(true)
    const client = await registered.json()
    const query = new URLSearchParams({ client_id: client.client_id, redirect_uri: 'http://127.0.0.1:43119/callback', response_type: 'code', scope: 'read', code_challenge: 'a'.repeat(43), code_challenge_method: 'S256', prompt: 'consent' })
    await page.goto(`/oauth/authorize?${query}`)
    await expect(page.locator('.oauth-card')).toBeVisible()
    await initialsFit(page.locator('.oauth-workspace-trigger .workspace-avatar'))
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/cjk-oauth-${width}.png` })
    await page.locator('.oauth-workspace-trigger').click()
    const oauth = page.locator(phone ? '.phone-menu-surface' : '.oauth-workspace-menu')
    await initialsFit(oauth.locator('.is-current .workspace-avatar'))

    expect((await request.patch('/api/account/settings', { headers, data: settings })).ok()).toBe(true)
  })
}
