import { test } from './workspace-fixture'
import { expect, type Page } from '@playwright/test'

test.use({ viewport: { width: 1440, height: 900 } })
test.beforeEach(({ isMobile }) => { test.skip(isMobile, 'Desktop SLA settings') })

const ruleCards = (page: Page) => page.locator('.sla-rule')

async function addRule(page: Page, options: { priority?: string; amount?: string; unit?: string; remove?: boolean } = {}) {
  await page.getByRole('button', { name: 'Add rule', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Add rule' })
  if (options.priority) {
    await dialog.getByRole('combobox', { name: 'Priority' }).click()
    await page.getByRole('option', { name: new RegExp(options.priority) }).click()
  }
  if (options.remove) {
    await dialog.getByRole('combobox', { name: 'Action' }).click()
    await page.getByRole('option', { name: 'Remove SLA' }).click()
  } else {
    if (options.amount) await dialog.getByRole('textbox', { name: 'Amount' }).fill(options.amount)
    if (options.unit) {
      await dialog.getByRole('combobox', { name: 'Unit' }).click()
      await page.getByRole('option', { name: options.unit, exact: true }).click()
    }
  }
  await dialog.getByRole('button', { name: 'Add rule' }).click()
  await expect(dialog).toBeHidden()
}

test('SLAs start switched off: the rules are greyed out until the card toggle is on', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/settings/sla`)
  await expect(page.getByRole('heading', { name: 'SLAs', level: 1 })).toBeVisible({ timeout: 30_000 })
  const toggle = page.getByRole('checkbox', { name: 'Enable SLAs' })
  await expect(toggle).not.toBeChecked()
  await expect(page.locator('#automation-rules')).toHaveAttribute('disabled', '')
  await expect(page.locator('#automation-rules')).toHaveCSS('opacity', '0.5')
  await expect(page.getByRole('button', { name: 'Add rule', exact: true })).toBeDisabled()
  await expect(page.getByText('Enable SLAs to add automation rules.')).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Work week' })).toHaveCount(0)

  await toggle.click()
  await expect(toggle).toBeChecked()
  await expect(page.locator('#automation-rules')).not.toHaveAttribute('disabled', '')
  await expect(page.getByText('Add a rule to begin applying SLAs.')).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Work week' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('checkbox', { name: 'Enable SLAs' })).toBeChecked()
})

test('creates, edits, reorders and deletes automation rules', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  expect((await request.put('/api/sla-settings', { headers, data: { enabled: true } })).status()).toBe(200)
  await page.goto(`/${workspaceKey}/settings/sla`)

  await addRule(page, { priority: 'Urgent', amount: '24', unit: 'Hours' })
  await addRule(page, { priority: 'High', amount: '1', unit: 'Weeks' })
  await addRule(page, { priority: 'Medium', remove: true })
  await expect(ruleCards(page)).toHaveCount(3)
  await expect(ruleCards(page).nth(0)).toHaveText(/WhenPriorityisUrgentThenAdd SLAof 24 hours from escalation/)
  await expect(ruleCards(page).nth(1)).toHaveText(/WhenPriorityisHighThenAdd SLAof 1 week from escalation/)
  await expect(ruleCards(page).nth(2)).toHaveText(/WhenPriorityisMediumThenRemove SLA/)

  // The rules the page shows are the rules the server keeps, in the same order.
  const saved = async () => (await (await request.get('/api/bootstrap', { headers })).json()).slaRules as Array<{ name: string; action: string; targetMinutes: number; filters: Record<string, unknown> }>
  expect((await saved()).map(rule => [rule.action, rule.targetMinutes])).toEqual([['add', 1440], ['add', 10080], ['remove', 1440]])

  // Edit: the duration changes, the rule stays where it was.
  await ruleCards(page).nth(0).hover()
  await ruleCards(page).nth(0).getByRole('button', { name: 'Edit configuration' }).click()
  const dialog = page.getByRole('dialog', { name: 'Edit rule' })
  await dialog.getByRole('textbox', { name: 'Amount' }).fill('3')
  await dialog.getByRole('combobox', { name: 'Unit' }).click()
  await page.getByRole('option', { name: 'Days', exact: true }).click()
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(dialog).toBeHidden()
  await expect(ruleCards(page).nth(0)).toHaveText(/Add SLAof 3 days from escalation/)
  expect((await saved())[0].targetMinutes).toBe(4320)

  // Reorder from the keyboard: Alt + arrow moves the focused rule.
  await ruleCards(page).nth(1).focus()
  await page.keyboard.press('Alt+ArrowUp')
  await expect(ruleCards(page).nth(0)).toHaveText(/Priority\s*is\s*High/)
  expect((await saved()).map(rule => rule.filters.priority)).toEqual([2, 1, 3])

  // Delete asks first.
  await ruleCards(page).nth(2).hover()
  await ruleCards(page).nth(2).getByRole('button', { name: 'Delete configuration' }).click()
  const confirm = page.getByRole('dialog').filter({ hasText: 'Delete this SLA rule?' })
  await expect(confirm).toContainText('Delete this SLA rule?')
  await confirm.getByRole('button', { name: 'Cancel' }).click()
  await expect(ruleCards(page)).toHaveCount(3)
  await ruleCards(page).nth(2).hover()
  await ruleCards(page).nth(2).getByRole('button', { name: 'Delete configuration' }).click()
  await page.getByRole('dialog').filter({ hasText: 'Delete this SLA rule?' }).getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(ruleCards(page)).toHaveCount(2)
  expect((await saved()).length).toBe(2)
})

test('rule cards keep the measured desktop geometry', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  await request.put('/api/sla-settings', { headers, data: { enabled: true } })
  for (const [name, priority, action] of [['Priority is Urgent', 1, 'add'], ['Priority is Low', 4, 'remove']] as const) {
    const response = await request.post('/api/sla-rules', { headers, data: { name, filters: { priority }, action, targetMinutes: 1440 } })
    expect(response.status()).toBe(201)
  }
  await page.goto(`/${workspaceKey}/settings/sla`)
  const cards = ruleCards(page)
  await expect(cards).toHaveCount(2)
  const [first, second] = [await cards.nth(0).boundingBox(), await cards.nth(1).boundingBox()]
  expect(first!.width).toBe(640)
  expect(first!.height).toBe(97)
  expect(second!.y - (first!.y + first!.height)).toBe(12)
  await expect(page.getByRole('heading', { name: 'SLAs', level: 1 })).toHaveCSS('font-size', '24px')
  await expect(page.locator('.sla-rule-label').first()).toHaveCSS('width', '46.4375px')
  // The edit and delete buttons stay out of sight until the pointer is on the card.
  const actions = cards.nth(0).locator('.sla-rule-actions')
  await page.mouse.move(5, 5)
  await expect(actions).toHaveCSS('opacity', '0')
  await cards.nth(0).hover()
  await expect(actions).toHaveCSS('opacity', '1')
  expect((await actions.locator('button').first().boundingBox())!.width).toBe(24)
})
