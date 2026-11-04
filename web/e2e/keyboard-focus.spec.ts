import { expect, type Locator } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { test } from './workspace-fixture'

async function ring(control: Locator, offset: number) {
  await control.page().mouse.move(0, 0)
  await control.focus()
  await expect(control).toBeFocused()
  await expect(control).toHaveCSS('outline-style', 'solid')
  await expect(control).toHaveCSS('outline-width', '1px')
  await expect(control).toHaveCSS('outline-offset', `${offset}px`)
  const colors = await control.evaluate(element => ({
    actual: getComputedStyle(element).outlineColor,
    expected: getComputedStyle(document.documentElement).getPropertyValue('--activity-highlight-color').trim().replaceAll('%', ''),
  }))
  expect(colors.actual).toBe(colors.expected)
}

for (const width of [1440, 390, 375]) {
  for (const theme of ['dark', 'light']) {
    for (const locale of ['en-US', 'zh-CN']) {
      test(`keyboard rings and phone skip link: ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
        const crops: { label: string; image: string; wide: boolean }[] = []
        const capture = async (label: string, element: Locator) => {
          if (!process.env.FLOW_FOCUS_SHOTS) return
          await element.scrollIntoViewIfNeeded()
          const box = await element.boundingBox()
          if (!box) throw new Error(`Missing review control: ${label}`)
          const viewport = page.viewportSize()!
          const x = Math.max(0, box.x - 6), y = Math.max(0, box.y - 6)
          const clip = { x, y, width: Math.min(viewport.width - x, box.width + 12), height: Math.min(viewport.height - y, box.height + 12) }
          const image = await page.screenshot({ clip, animations: 'disabled' })
          crops.push({ label, image: image.toString('base64'), wide: clip.width > 430 })
        }
        await page.setViewportSize({ width, height: width === 1440 ? 900 : width === 390 ? 844 : 667 })
        await page.addInitScript(({ theme, locale }) => {
          localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme }))
          localStorage.setItem('flow:locale', locale)
        }, { theme, locale })
        const headers = { 'X-Workspace-Key': workspaceKey }
        const data = await (await request.get('/api/bootstrap', { headers })).json() as { viewer: { id: string }; teams: { id: string }[] }
        expect((await request.patch('/api/account/settings', { headers, data: { language: locale, interfaceTheme: theme } })).ok()).toBe(true)
        const displayName = locale === 'zh-CN' ? '\u5f20 \u4f1f' : 'Alexandra Montgomery-Wellington'
        const tokenResponse = await request.post('/api/scim/tokens', { headers, data: { name: 'Focus review fixture' } })
        expect(tokenResponse.status()).toBe(201)
        const token = await tokenResponse.json() as { id: string; secret: string }
        const memberResponse = await request.post(`/scim/v2/${workspaceKey}/Users`, {
          headers: { Authorization: `Bearer ${token.secret}` },
          data: { userName: `focus-${workspaceKey}@example.test`, displayName, active: true },
        })
        expect(memberResponse.status()).toBe(201)
        const member = await memberResponse.json() as { id: string }
        expect((await request.delete(`/api/scim/tokens/${token.id}`, { headers })).ok()).toBe(true)
        const projectResponse = await request.post('/api/projects', {
          headers, data: { name: locale === 'zh-CN' ? '\u5f20\u4f1f' : 'Customer reporting platform migration', teamIds: [data.teams[0].id] },
        })
        expect(projectResponse.status()).toBe(201)
        const project = await projectResponse.json() as { id: string }
        const created = await request.post('/api/issues', {
          headers, data: { title: 'Keyboard focus with a long realistic issue title', teamId: data.teams[0].id, assigneeId: member.id, projectId: project.id },
        })
        expect(created.status()).toBe(201)
        await page.goto(`/${workspaceKey}/my-issues/created`)
        await expect(page.locator('.flow-startup')).toHaveCount(0, { timeout: 30_000 })
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
        await expect(page.locator('html')).toHaveAttribute('lang', locale === 'zh-CN' ? 'zh-CN' : 'en')
        const row = page.locator('[data-issue-row-id]').first()
        await expect(row).toBeVisible()
        await capture('Issue row / resting with long text and assignee avatar', row)
        await row.hover()
        await capture('Issue row / hover', row)
        await page.mouse.move(0, 0)
        await page.keyboard.press('Tab')
        const skip = page.locator('.skip-to-content')
        await expect(skip).toBeFocused()
        await ring(skip, 1)
        await capture('Skip link / keyboard focus', skip)
        await page.keyboard.press('Enter')
        await expect(page.locator('main')).toBeFocused()

        const tab = page.locator('[data-my-issues-surface] nav a[aria-current="page"]')
        await capture('View tab / selected, resting', tab)
        await tab.hover()
        await capture('View tab / selected, hover', tab)
        await ring(tab, -1)
        await capture('View tab / selected and focused', tab)
        const otherTab = page.locator('[data-my-issues-surface] nav a:not([aria-current="page"])').first()
        await capture('View tab / unselected, resting', otherTab)
        await otherTab.hover()
        await capture('View tab / unselected, hover', otherTab)
        await ring(otherTab, -1)
        await capture('View tab / unselected, focused', otherTab)
        if (width === 1440) {
          const checkbox = row.getByRole('checkbox')
          await ring(checkbox, 2)
          await capture('Checkbox / focused', checkbox)
          await checkbox.press('Space')
          await expect(checkbox).toHaveAttribute('aria-checked', 'true')
          await ring(checkbox, 2)
          await capture('Checkbox / selected and focused', checkbox)
          await checkbox.press('Space')
          const property = row.locator('button[data-property="priority"]').first()
          await property.hover()
          await capture('Property trigger / hover', property)
          await ring(property, 0)
          await capture('Property trigger / focused', property)
          await property.press('Enter')
          await expect(page.locator('[cmdk-input],.property-command-search input').first()).toBeFocused()
          await capture('Property picker / open', page.locator('.property-command-surface').first())
          await page.keyboard.press('Escape')
          await expect(property).toBeFocused()
          await page.goto(`/${workspaceKey}/projects/all`)
          const projectRow = page.locator('.lp-project-row').first()
          await expect(projectRow).toBeVisible()
          await capture('Project row / resting with long text or two CJK characters', projectRow)
          const header = page.locator('.lp-project-table__header button').first()
          await header.hover()
          await expect(header).toHaveCSS('outline-style', 'none')
          await capture('Project column / hover without keyboard ring', header)
          await ring(header, 0)
          await capture('Project column / keyboard focus', header)
          const icon = projectRow.locator('.lp-project-row__project-icon')
          await ring(icon, 0)
          await capture('Project icon / keyboard focus', icon)
          const health = projectRow.locator('.lp-project-row__health')
          await health.hover()
          await capture('Project health / hover', health)
          await ring(health, 0)
          await capture('Project health / keyboard focus', health)
          const priority = projectRow.locator('.lp-project-property-trigger').first()
          await priority.focus()
          await expect(priority).toHaveCSS('outline-width', '1px')
          await expect(priority).toHaveCSS('outline-offset', '-1px')
          await expect(priority).toHaveCSS('box-shadow', /1px inset$/)
          await capture('Project property / inset keyboard ring', priority)
          const date = projectRow.locator('.lp-project-row__date')
          await ring(date, 0)
          await expect(date).toHaveCSS('box-shadow', 'none')
          await capture('Project target date / flush keyboard ring', date)
        }
        await page.goto(`/${workspaceKey}/settings/account/preferences`)
        const select = page.locator('.settings-select:visible,.personal-select:visible,[role="combobox"]:visible').first()
        await expect(select).toBeVisible()
        await capture('Settings select / resting', select)
        await select.hover()
        await capture('Settings select / hover', select)
        await page.keyboard.press('Tab')
        await ring(select, 0)
        await capture('Settings select / focused', select)
        if (await select.evaluate(element => element.tagName !== 'SELECT')) {
          await select.press('Enter')
          await expect(select).toHaveAttribute('data-state', 'open')
          await capture('Settings select / open', page.locator('[role="listbox"]').last())
          await page.keyboard.press('Escape')
          await expect(select).toBeFocused()
        }
        const toggle = page.locator('input[data-control="toggle"]').first()
        await capture('Toggle / resting', toggle)
        await toggle.hover()
        await capture('Toggle / hover', toggle)
        await ring(toggle, 2)
        await capture('Toggle / focused', toggle)
        await toggle.press('Space')
        await capture('Toggle / changed selection and focused', toggle)
        await toggle.press('Space')
        expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
        await page.goto(`/${workspaceKey}/settings/account/profile`)
        const input = page.locator('.personal-input:visible').first()
        await expect(input).toBeVisible()
        await input.fill(displayName)
        await expect(input).toHaveValue(displayName)
        await page.locator('.personal-page-header h1').click()
        await expect(input).not.toBeFocused()
        await capture('Profile input / resting with long name or CJK name', input)
        await input.hover()
        await capture('Profile input / hover', input)
        await page.keyboard.press('Tab')
        await ring(input, -1)
        await capture('Profile input / focused', input)
        await input.fill('')
        const update = page.locator('.personal-save-bar button.primary')
        await expect(update).toBeDisabled()
        await capture('Update button / disabled', update)
        await update.hover()
        await capture('Update button / disabled and hovered', update)
        await update.focus()
        await expect(update).not.toBeFocused()
        await input.fill('A')
        await expect(update).toBeEnabled()
        await page.mouse.move(0, 0)
        await capture('Update button / resting with shortest valid name', update)
        await update.hover()
        await capture('Update button / hover', update)
        await ring(update, 0)
        await capture('Update button / focused', update)
        await update.hover()
        await page.mouse.down()
        await capture('Update button / active', update)
        await page.mouse.up()
        if (process.env.FLOW_FOCUS_SHOTS) {
          const sheet = await page.context().newPage()
          await sheet.setViewportSize({ width: 1440, height: 900 })
          await sheet.setContent(`<html><style>body{margin:20px;font:14px system-ui;background:${theme === 'dark' ? '#171719' : '#f5f5f6'};color:${theme === 'dark' ? 'white' : 'black'}}main{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}figure{margin:0;padding:12px;border:1px solid gray}figure.wide{grid-column:1/-1}figcaption{margin-bottom:12px}img{max-width:100%;display:block}</style><h1>${width}px / ${theme} / ${locale}</h1><main>${crops.map(crop => `<figure class="${crop.wide ? 'wide' : ''}"><figcaption>${crop.label}</figcaption><img src="data:image/png;base64,${crop.image}"></figure>`).join('')}</main></html>`)
          await mkdir(process.env.FLOW_FOCUS_SHOTS, { recursive: true })
          await sheet.screenshot({ path: `${process.env.FLOW_FOCUS_SHOTS}/controls-${width}-${theme}-${locale}.png`, fullPage: true })
          await sheet.close()
        }
      })
    }
  }
}
