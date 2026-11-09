import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

test('desktop shell commands keep commands first, list real shortcuts and open the create dialog', async ({ page, request, workspaceKey }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'desktop-only parity spec')
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap', { headers })
  expect(bootstrap.status()).toBe(200)
  const workspace = await bootstrap.json() as BootstrapData
  const teamId = workspace.teams[0]?.id
  expect(teamId).toBeTruthy()
  const issue = await request.post('/api/issues', {
    headers,
    data: { title: '[audit] Project launch notes', description: 'Quick result for the command menu', teamId, stateId: 'state_todo' },
  })
  expect(issue.status()).toBe(201)

  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.locator('.sidebar')).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Send a message to Flow AI', exact: true })).toBeFocused()
  await page.mouse.click(800, 20)

  // The sidebar search tooltip advertises the shortcut that really opens search.
  await page.getByRole('button', { name: 'Search', exact: true }).hover()
  const searchTooltip = page.getByRole('tooltip').filter({ hasText: 'Search' })
  await expect(searchTooltip).toContainText('/')

  // ⌘K: the Issues group holds all three create commands and Views has Create view.
  await page.keyboard.press('Meta+k')
  const palette = page.locator('[cmdk-root]')
  await expect(palette).toBeVisible()
  await expect(palette.getByRole('option', { name: /^Create new issue…/ })).toBeVisible()
  await expect(palette.getByRole('option', { name: /^Create issue in fullscreen…/ })).toBeVisible()
  await expect(palette.getByRole('option', { name: /^Create new label…/ })).toBeVisible()
  await expect(palette.getByRole('option', { name: /^Create view…/ })).toBeVisible()

  // A command query keeps the command groups before the entity quick results.
  await page.keyboard.type('pro')
  await expect(palette.getByRole('option', { name: /^Create new project…/ })).toBeVisible()
  await expect(palette.locator('[cmdk-group-heading]').filter({ hasText: /Quick results for/ })).toBeVisible({ timeout: 10_000 })
  const headings = await palette.locator('[cmdk-group-heading]').allTextContents()
  const quick = headings.findIndex(heading => heading.startsWith('Quick results for'))
  expect(quick).toBeGreaterThan(-1)
  expect(headings.slice(0, quick)).not.toContain('Search results')
  expect(headings.indexOf('Projects')).toBeLessThan(quick)
  await expect(palette.locator('[cmdk-item]').filter({ hasText: '[audit] Project launch notes' })).toBeVisible()

  // Shortcut help lists the General commands Flow supports.
  await page.keyboard.press('Escape')
  await page.keyboard.press('Meta+/')
  const shortcuts = page.locator('.sidebar-shortcuts-dialog')
  await expect(shortcuts).toBeVisible()
  for (const label of ['Save or submit', 'Send comment', 'Switch workspace', 'Log out']) {
    await expect(shortcuts.getByText(label, { exact: true })).toBeVisible()
  }
  await expect(shortcuts.getByText('Go to reviews', { exact: true })).toBeVisible()
  await expect(shortcuts.getByText('Open customer…', { exact: true })).toBeVisible()
  await expect(shortcuts.getByText('Open help menu', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')

  // The create dialog sits at the reference height, leaves the assignee unassigned and keeps the chip order.
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'Create new issue' }).click()
  const dialog = page.getByRole('dialog', { name: 'New issue' })
  await expect(dialog).toBeVisible()
  await dialog.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  const box = await dialog.boundingBox()
  expect(box?.y).toBeCloseTo(117, 1)
  const radius = await dialog.evaluate(element => getComputedStyle(element).borderRadius)
  expect(radius).toBe('21px')
  const chips = await dialog.locator('.properties [data-property], .properties button[aria-label="More actions"]').evaluateAll(elements => elements.map(element => element.getAttribute('data-property') ?? 'more'))
  expect(chips).toEqual(['status', 'priority', 'assignee', 'project', 'estimate', 'labels', 'cycle', 'more'].filter(chip => chips.includes(chip)))
  await expect(dialog.getByRole('combobox', { name: /^Change assignee/ })).toContainText('Assignee')
  // The cycle chip only exists for a team that has cycles turned on.
  const cycle = dialog.getByRole('combobox', { name: 'Add to cycle' })
  if (chips.includes('cycle')) {
    await expect(cycle).toBeVisible()
    const cycleBox = await cycle.boundingBox()
    expect(cycleBox?.width).toBe(32)
    expect(cycleBox?.height).toBe(24)
  } else await expect(cycle).toHaveCount(0)
})
