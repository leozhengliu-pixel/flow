import { expect, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

// The sub-issue composer grows with a multi-line body: the body never runs under the property/action row or out of the
// composer, and the composer pushes the content below it down. Emptying the body brings the composer back to its size.
async function boxes(page: Page) {
  return page.evaluate(() => {
    const form = document.querySelector('.sub-issue-editor')!
    const rect = (element: Element) => { const r = element.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, height: r.height } }
    const body = form.querySelector('.description-editor')!
    return {
      form: rect(form),
      body: rect(body),
      bodyScrollWidth: body.scrollWidth,
      bodyClientWidth: body.clientWidth,
      actions: rect(form.querySelector('.sub-issue-actions')!),
      next: rect(form.nextElementSibling!),
      padding: parseFloat(getComputedStyle(form).paddingRight),
    }
  })
}

// Parity screens are measured at 2x.
test.use({ deviceScaleFactor: 2 })

test('the sub-issue composer grows with a multi-line body and shrinks back', async ({ page, request, workspaceKey: key, isMobile }) => {
  const headers = { 'X-Workspace-Key': key }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const response = await request.post('/api/issues', { headers, data: { title: '[audit] Sub-issue composer body', teamId: data.teams[0].id } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as Issue
  await page.goto(`/${key}/issue/${issue.identifier}`)
  await page.getByText('Add sub-issues', { exact: true }).click()
  const composer = page.locator('.sub-issue-editor')
  await expect(composer).toBeVisible()
  const empty = await boxes(page)
  if (!isMobile) expect(empty.form.height).toBe(107)
  // Nothing sits between the title row and the body (the issue body's phone margin does not apply here).
  await expect(composer.locator('.issue-description-root')).toHaveCSS('margin-top', '0px')

  // Enter in the title moves to the body at once: keys typed straight after it land in the body, not the title.
  await page.keyboard.type('Child')
  await page.keyboard.press('Enter')
  await page.keyboard.type('First paragraph')
  await expect(composer.locator('.title-editor')).toHaveText('Child')
  await expect(composer.locator('.description-editor')).toBeFocused()
  await page.keyboard.press('Enter')
  await page.keyboard.type('Second paragraph')
  await page.keyboard.press('Enter')
  await page.keyboard.type('A third paragraph long enough to wrap across the composer width several times on every layout, with https://example.com/an-unbroken-path-segment-that-must-wrap-inside-the-composer-too')
  const filled = await boxes(page)
  expect(filled.body.height).toBeGreaterThan(empty.body.height + 60)
  expect(filled.body.bottom).toBeLessThanOrEqual(filled.actions.top)
  expect(filled.actions.bottom).toBeLessThanOrEqual(filled.form.bottom)
  expect(filled.form.bottom).toBeLessThanOrEqual(filled.next.top)
  expect(filled.body.right).toBeLessThanOrEqual(filled.form.right - filled.padding)
  expect(filled.bodyScrollWidth).toBeLessThanOrEqual(filled.bodyClientWidth)

  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.press('Backspace')
  await expect.poll(async () => (await boxes(page)).form.height).toBe(empty.form.height)

  // The focused body has no tint in either theme (page-only theme switch; the account setting is untouched).
  for (const theme of ['dark', 'light']) {
    await page.evaluate(value => document.documentElement.setAttribute('data-theme', value), theme)
    await expect(composer.locator('.description-editor')).toBeFocused()
    await expect(composer.locator('.description-editor')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  }
})
