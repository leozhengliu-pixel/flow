import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

// The reference's issue label picker (measured live, 1440x900, real device scale factor 2): with nothing selected the labels
// already in use list under "Frequently used" (panel 207 x 169.5 for three labels); a selected label leads the list with no
// heading and the rest sit under "Workspace labels" (207 x 187.5); searching drops the headings (113.5 tall with the
// create row). Text widths need the same subpixel text flags the settings geometry specs use.
test.use({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
  launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] },
})
test.beforeEach(({ isMobile }) => { test.skip(isMobile, 'Desktop-only label picker geometry') })

test('the issue label picker headings and panel sizes follow the reference', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const boot = await (await request.get('/api/bootstrap', { headers })).json() as { teams: { id: string }[]; viewer: { id: string }; labels: { id: string; name: string }[] }
  const bug = boot.labels.find(label => label.name === 'Bug')!
  const create = async (title: string, labelIds: string[]) => {
    const response = await request.post('/api/issues', { headers, data: { title, teamId: boot.teams[0].id, stateId: 'state_todo', assigneeId: boot.viewer.id, labelIds } })
    expect(response.status()).toBe(201)
    return await response.json() as { identifier: string }
  }
  const seed = await create('Seed', []), plain = await create('Plain', []), tagged = await create('Tagged', [bug.id])
  const headings = (menu: import('@playwright/test').Locator) => menu.locator('.property-command-group').allTextContents()
  const size = async (menu: import('@playwright/test').Locator) => { const box = (await menu.boundingBox())!; return [box.width, box.height] }
  const menu = page.locator('.issue-labels-menu')
  const open = async (identifier: string) => {
    await page.goto(`/${workspaceKey}/issue/${identifier}`)
    await page.getByRole('button', { name: /Add labels|Change or add labels/ }).first().click()
    await expect(menu).toBeVisible()
    await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  }

  // Before anyone picked a label they all list under "Workspace labels".
  await open(plain.identifier)
  expect(await headings(menu)).toEqual(['Workspace labels'])
  expect(await size(menu)).toEqual([207, 169.5])
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()

  // Picking labels makes them frequently used.
  await open(seed.identifier)
  for (const name of ['Bug', 'Feature', 'Improvement']) await menu.getByRole('option', { name, exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()

  await open(plain.identifier)
  expect(await headings(menu)).toEqual(['Frequently used'])
  expect(await size(menu)).toEqual([207, 169.5])
  await page.keyboard.type('Fea')
  await expect(menu.getByRole('option', { name: /Create new/ })).toBeVisible()
  expect(await headings(menu)).toEqual([])
  expect((await size(menu))[1]).toBe(113.5)
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()

  await open(tagged.identifier)
  expect(await headings(menu)).toEqual(['Workspace labels'])
  expect(await size(menu)).toEqual([207, 187.5])
  expect(await menu.getByRole('option').first().textContent()).toContain('Bug')
})
