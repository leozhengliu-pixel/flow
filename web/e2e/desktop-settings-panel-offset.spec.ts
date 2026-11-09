import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

// The settings panel has a 0.5px hairline border, so page content starts at 8 + 0.5 + 64 = 72.5px. Chromium only keeps a
// 0.5px border when the device scale factor is real; with an emulated one (the context `deviceScaleFactor` option) it rounds
// the border up to 1px and every page measures 0.5px lower (73, skill card 129). That is a measuring artefact, not a layout
// difference, so this file runs at the browser's own scale factor. Text widths need the subpixel text positioning a real Mac
// browser uses (without it the 13px/500 "Back to app" label is 1px wider), so the same text flags are set.
test.use({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
  launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] },
})
test.beforeEach(({ isMobile }) => { test.skip(isMobile, 'Desktop-only settings geometry') })

const rounded = (box: { x: number; y: number; width: number; height: number } | null) => box && Object.fromEntries(Object.entries(box).map(([key, value]) => [key, Math.round(value * 100) / 100]))

async function settle(page: import('@playwright/test').Page) {
  await page.evaluate(() => document.fonts.ready)
  await expect(page.locator('.settings-main')).toHaveCSS('border-top-width', '0.5px')
}

test('desktop settings pages start their content at the same 72.5px under the hairline panel border', async ({ page, request, workspaceKey }) => {
  // Eight full page loads against the dev server: about 28s on a CI runner, so the default 30s limit is a coin flip.
  test.setTimeout(90_000)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await (await request.get('/api/bootstrap', { headers })).json() as { teams: { key: string }[] }
  const pages: [string, string][] = [
    ['account preferences', 'settings/account/preferences'],
    ['account profile', 'settings/account/profile'],
    ['workspace general', 'settings/workspace'],
    ['members', 'settings/members'],
    ['API', 'settings/api'],
    ['teams', 'settings/teams'],
    ['issue labels', 'settings/issue-labels'],
  ]
  if (bootstrap.teams[0]) pages.push(['team hub', `settings/teams/${bootstrap.teams[0].key}`])
  for (const [name, path] of pages) {
    await page.goto(`/${workspaceKey}/${path}`)
    await expect(page.locator('.settings-content, .settings-members-page, .domain-labels-page').first()).toBeVisible({ timeout: 20_000 })
    await settle(page)
    const main = (await page.locator('.settings-main').boundingBox())!
    expect([main.x, main.y], name).toEqual([244, 8])
    const header = (await page.locator('.settings-main .settings-page-header').first().boundingBox())!
    expect(header.y, name).toBe(72.5)
    // The reference centres the 28px "Back to app" link in a 44.5px row 8px down (y 16.25); the search field then sits at 56.5.
    const back = page.getByRole('link', { name: 'Back to app', exact: true })
    expect(rounded(await back.boundingBox()), name).toEqual({ x: 16, y: 16.25, width: 112.23, height: 28 })
    expect(rounded(await back.locator('svg').boundingBox()), name).toEqual({ x: 22.5, y: 23.25, width: 14, height: 14 })
    expect((await page.locator('.settings-search').boundingBox())!.y, name).toBe(56.5)
  }
})

test('the agent skill page card and buttons sit at 128.5px and 424.5px', async ({ page, request, workspaceKey }) => {
  const response = await request.post('/api/agent/skills', { headers: { 'X-Workspace-Key': workspaceKey }, data: { name: 'Release notes', instructions: '# Release notes\n\nWrite **user-facing** notes.' } })
  expect(response.ok()).toBe(true)
  const skill = await response.json() as { id: string }
  await page.goto(`/${workspaceKey}/settings/skill/${skill.id}`)
  await expect(page.getByRole('textbox', { name: 'Skill instructions' })).toBeVisible({ timeout: 20_000 })
  await settle(page)
  expect((await page.locator('.agent-skill-card').boundingBox())!.y).toBe(128.5)
  for (const name of ['Cancel', 'Save']) expect((await page.getByRole('button', { name, exact: true }).boundingBox())!.y, name).toBe(424.5)
})
