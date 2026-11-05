import { expect, type Locator, type Page } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'
import { test } from './workspace-fixture'

const sizes = [{ width: 1440, height: 900 }, { width: 390, height: 844 }, { width: 375, height: 667 }]
const longName = 'Alexandria Catherine Montgomery Wellington'
const photo = '/test-avatars/uploaded.svg'

async function capture(page: Page, target: Locator, name: string) {
  if (!process.env.FLOW_AVATAR_SHOTS) return
  await fs.mkdir(process.env.FLOW_AVATAR_SHOTS, { recursive: true })
  await target.screenshot({ path: path.join(process.env.FLOW_AVATAR_SHOTS, `${name}.png`), animations: 'disabled' })
}

async function avatarMetrics(avatar: Locator) {
  return avatar.evaluate(element => {
    const style = getComputedStyle(element), rect = element.getBoundingClientRect()
    return { width: rect.width, height: rect.height, fontSize: style.fontSize, fontWeight: style.fontWeight, color: style.color, background: style.backgroundColor }
  })
}

for (const viewport of sizes) for (const colorScheme of ['dark', 'light'] as const) for (const locale of ['en-US', 'zh-CN'] as const) {
  test.describe(`${viewport.width} ${colorScheme} ${locale}`, () => {
    test.use({ viewport, colorScheme, locale, isMobile: viewport.width < 768, hasTouch: viewport.width < 768 })
    test('preserves people and team identity in filter menus and chips', async ({ page, request, workspaceKey }) => {
      test.setTimeout(60_000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      expect((await request.patch('/api/account/settings', { headers, data: { language: locale } })).ok()).toBeTruthy()
      const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
      const team = bootstrap.teams[0]
      expect((await request.patch(`/api/workspaces/${workspaceKey}/teams/${team.id}`, { headers, data: { name: 'A', icon: 'Favorite', color: '#14b8a6' } })).ok()).toBeTruthy()
      expect((await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: '平台工程与开发体验 Platform Engineering and Developer Experience', key: 'LONG', icon: 'Cube', color: '#f59e0b' } })).ok()).toBeTruthy()
      expect((await request.post('/api/issues', { headers, data: { title: 'Avatar identity review', teamId: team.id, stateId: 'state_todo', assigneeId: bootstrap.viewer.id } })).ok()).toBeTruthy()
      await page.route('**/test-avatars/uploaded.svg', route => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#167d93"/><circle cx="16" cy="12" r="6" fill="#fff"/><path d="M5 32v-4a11 11 0 0 1 22 0v4" fill="#fff"/></svg>' }))
      await page.route(/\/api\/(?:bootstrap|issue-records\/bootstrap)(?:\?|$)/, async route => {
        if (route.request().headers()['x-workspace-key'] !== workspaceKey) return route.continue()
        const response = await route.fetch(), data = await response.json()
        const visit = (value: unknown) => {
          if (!value || typeof value !== 'object') return
          const record = value as Record<string, unknown>
          if (record.id === bootstrap.viewer.id && 'displayName' in record) { record.displayName = '林舟'; record.avatarUrl = '' }
          for (const child of Object.values(record)) visit(child)
        }
        visit(data)
        if (data.users) data.users.push({ ...data.viewer, id: 'avatar-person-a', name: longName, displayName: longName, avatarUrl: '' }, { ...data.viewer, id: 'avatar-person-b', name: 'Photo Person', displayName: 'Photo Person', avatarUrl: photo }, { ...data.viewer, id: 'avatar-person-c', name: 'Q', displayName: 'Q', avatarUrl: '' })
        await route.fulfill({ response, json: data })
      })
      await page.addInitScript(value => localStorage.setItem('flow:locale', value), locale)
      const errors: string[] = []
      page.on('pageerror', error => errors.push(error.message))
      await page.goto(`/${workspaceKey}/team/${team.key}/all`)
      await expect(page.locator('[data-issue-explorer]')).toBeVisible({ timeout: 20_000 })
      await expect(page.locator('html')).toHaveAttribute('data-theme', colorScheme)
      await expect(page.locator('html')).toHaveAttribute('data-locale', locale)
      await page.evaluate(() => document.fonts.ready)
      const rowAvatar = page.locator('[data-user-avatar][aria-label="林舟"]').first()
      await expect(rowAvatar).toBeVisible()
      const rowBackground = (await avatarMetrics(rowAvatar)).background
      const add = page.getByRole('button', { name: /^(Add filter|添加筛选|添加筛选条件|添加过滤器)$/ })
      const openField = async (field: string) => {
        const root = page.locator('[data-variant=root]')
        if (!(await root.count()) || await root.getAttribute('data-state') !== 'open') {
          await expect(root).toBeHidden()
          await add.click()
        }
        const fieldRow = root.locator(`[data-value="${field}"]`)
        if (viewport.width < 768) await fieldRow.tap()
        else await fieldRow.hover()
        const menu = page.locator(`[data-field="${field.toLowerCase()}"]:visible`)
        await expect(menu).toBeVisible()
        return menu
      }
      const menu = await openField('Assignee')
      const cjk = menu.locator('[data-user-avatar][aria-label="林舟"]')
      await expect(cjk).toHaveText('林')
      await expect(cjk).toHaveCSS('width', viewport.width < 768 ? '18px' : '16px')
      await expect.poll(() => avatarMetrics(cjk)).toEqual({ width: viewport.width < 768 ? 18 : 16, height: viewport.width < 768 ? 18 : 16, fontSize: '9px', fontWeight: viewport.width < 768 ? '400' : '450', color: 'rgb(255, 255, 255)', background: rowBackground })
      const metrics = await avatarMetrics(cjk)
      expect(metrics).toEqual({ width: viewport.width < 768 ? 18 : 16, height: viewport.width < 768 ? 18 : 16, fontSize: '9px', fontWeight: viewport.width < 768 ? '400' : '450', color: 'rgb(255, 255, 255)', background: rowBackground })
      const inset = await cjk.evaluate(element => {
        const avatar = element.getBoundingClientRect(), row = element.closest('[role=option]')!.getBoundingClientRect()
        return { x: avatar.x - row.x, y: avatar.y - row.y }
      })
      expect(inset).toEqual({ x: viewport.width < 768 ? 40 : 36, y: viewport.width < 768 ? 14 : 8 })
      const latin = menu.locator(`[data-user-avatar][aria-label="${longName}"]`)
      await expect(latin).toHaveText('AC')
      await expect(menu.locator('[data-user-avatar][aria-label="Q"]')).toHaveText('Q')
      const uploaded = menu.locator('[data-user-avatar][aria-label="Photo Person"] img')
      await expect(uploaded).toHaveAttribute('src', photo)
      await expect.poll(() => uploaded.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(32)
      const takeShots = viewport.width === 1440 && colorScheme === 'dark' && locale === 'en-US' || viewport.width === 390 && colorScheme === 'light' && locale === 'zh-CN'
      const prefix = `${viewport.width}-${colorScheme}-${locale}`
      if (process.env.FLOW_AVATAR_SHOTS) await fs.writeFile(path.join(process.env.FLOW_AVATAR_SHOTS, `${prefix}-metrics.json`), JSON.stringify({ avatar: metrics, inset }, null, 2))
      if (takeShots) await capture(page, menu, `${prefix}-people-open`)
      const cjkOption = menu.getByRole('option').filter({ has: page.locator('[data-user-avatar][aria-label="林舟"]') })
      await cjkOption.hover()
      expect(await avatarMetrics(cjk)).toEqual(metrics)
      if (takeShots) await capture(page, cjkOption, `${prefix}-people-hover`)
      await page.keyboard.press('Tab')
      await cjkOption.focus()
      await expect(cjkOption).toBeFocused()
      expect(await cjkOption.evaluate(element => element.matches(':focus-visible'))).toBe(true)
      expect(await avatarMetrics(cjk)).toEqual(metrics)
      if (takeShots) await capture(page, cjkOption, `${prefix}-people-focus`)
      await cjkOption.click()
      await expect(page.locator('[data-variant=root]')).toBeHidden()
      const chip = page.locator('[data-filter-chip]').filter({ has: page.locator('[data-user-avatar][aria-label="林舟"]') })
      await expect(chip).toBeVisible()
      expect((await avatarMetrics(chip.locator('[data-user-avatar]'))).background).toBe(rowBackground)
      const selectedMenu = await openField('Assignee')
      await expect(selectedMenu.getByRole('option').filter({ has: page.locator('[data-user-avatar][aria-label="林舟"]') })).toHaveAttribute('aria-checked', 'true')
      if (takeShots) await capture(page, selectedMenu, `${prefix}-people-selected`)
      await page.keyboard.press('Escape')
      await page.keyboard.press('Escape')
      const teams = await openField('Team')
      const teamIcon = teams.locator('svg').filter({ has: page.locator('use[href="#Favorite"]') })
      await expect(teamIcon).toBeVisible()
      await expect(teamIcon).toHaveCSS('color', 'rgb(20, 184, 166)')
      await expect.poll(() => teamIcon.evaluate(icon => [icon.getBoundingClientRect().width, icon.getBoundingClientRect().height])).toEqual([14, 14])
      if (takeShots) await capture(page, teams, `${prefix}-teams-open`)
      const teamOption = teams.getByRole('option').filter({ has: page.locator('use[href="#Favorite"]') })
      await teamOption.hover()
      await expect(teamIcon).toHaveCSS('color', 'rgb(20, 184, 166)')
      await page.keyboard.press('Tab')
      await teamOption.focus()
      await expect(teamOption).toBeFocused()
      expect(await teamOption.evaluate(element => element.matches(':focus-visible'))).toBe(true)
      await teams.getByRole('option').filter({ has: page.locator('use[href="#Favorite"]') }).click()
      await expect(page.locator('[data-variant=root]')).toBeHidden()
      const teamChip = page.locator('[data-filter-chip]').filter({ has: page.locator('use[href="#Favorite"]') })
      await expect(teamChip.locator('svg').filter({ has: page.locator('use[href="#Favorite"]') })).toHaveCSS('color', 'rgb(20, 184, 166)')
      if (takeShots) {
        await page.mouse.move(1, 1)
        await page.keyboard.press('Escape')
        await expect(page.getByRole('tooltip')).toBeHidden()
        await capture(page, page.locator('[data-filter-chip]').first().locator('..'), `${prefix}-chips`)
      }
      const selectedTeams = await openField('Team')
      await expect(selectedTeams.getByRole('option').filter({ has: page.locator('use[href="#Favorite"]') })).toHaveAttribute('aria-checked', 'true')
      if (takeShots) await capture(page, selectedTeams, `${prefix}-teams-selected`)
      await page.goto(`/${workspaceKey}/inbox`)
      await page.getByRole('button', { name: /^(Add filter|添加筛选|添加筛选条件|添加过滤器)$/ }).click()
      const inboxTeam = page.getByRole('option', { name: /^(Team|团队)(?:\s*▶)?$/ })
      if (viewport.width < 768) await inboxTeam.tap()
      else await inboxTeam.hover()
      const inboxIcon = page.locator('[role=option] use[href="#Favorite"]').locator('..')
      await expect(inboxIcon).toBeVisible()
      await expect(inboxIcon).toHaveCSS('color', 'rgb(20, 184, 166)')
      await expect(inboxIcon).toHaveCSS('width', '14px')
      if (takeShots) await capture(page, inboxIcon.locator('..').locator('..'), `${prefix}-inbox-team`)
      expect((await request.post('/api/projects', { headers, data: { name: 'Identity project', teamIds: [team.id], leadId: bootstrap.viewer.id, memberIds: [bootstrap.viewer.id] } })).ok()).toBeTruthy()
      await page.goto(`/${workspaceKey}/projects/all`)
      await page.getByRole('button', { name: /^(Add filter|添加筛选|添加筛选条件|添加过滤器)$/ }).click()
      await page.locator('[id="project-filter-Lead"]').click()
      const projectPeople = page.locator('.lp-projects-filter__people')
      await expect(projectPeople.locator('[data-user-avatar][aria-label="林舟"]')).toHaveText('林')
      await expect(projectPeople.locator('[data-user-avatar][aria-label="Photo Person"] img')).toHaveAttribute('src', photo)
      await projectPeople.getByRole('option', { name: /林舟/ }).click()
      const leadChip = page.locator('[data-filter-chip]').filter({ has: page.locator('[data-user-avatar][aria-label="林舟"]') })
      await expect(leadChip).toBeVisible()
      expect((await avatarMetrics(leadChip.locator('[data-user-avatar]'))).background).toBe(rowBackground)
      await leadChip.getByRole('button').filter({ has: page.locator('[data-user-avatar]') }).click()
      const projectValues = page.getByRole('listbox')
      await expect(projectValues.locator('[data-user-avatar][aria-label="Photo Person"] img')).toHaveAttribute('src', photo)
      if (takeShots) await capture(page, projectValues, `${prefix}-project-lead-values`)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(viewport.width)
      expect(errors).toEqual([])
    })
  })
}
