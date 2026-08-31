import { expect, test } from '@playwright/test'

test('loads the workspace shell and core planning route without console errors', async ({ page, request }, testInfo) => {
  const workspaceKey = `e2e-${testInfo.project.name}`
  const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'E2E Workspace', urlKey: workspaceKey, region: 'us' },
  })
  expect(response.status()).toBe(201)
  const errors: string[] = []
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`/${workspaceKey}/projects/all`)
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'New project', exact: true }).first()).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
  expect(overflow).toBe(false)
  expect(errors).toEqual([])
})

test('keeps the onboarding route functional', async ({ page }) => {
  await page.goto('/join')
  await expect(page.getByRole('heading', { name: 'Create a workspace' })).toBeVisible()
  await expect(page.getByLabel('Name')).toBeEditable()
  await expect(page.getByLabel('URL')).toBeEditable()
})

test('renders core workspace workflows without runtime or viewport failures', async ({ page, request }, testInfo) => {
  const device = testInfo.project.name === 'chromium' ? 'desktop' : 'mobile'
  const workspaceKey = `e2e-core-${device}`
  const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Core workflow workspace', urlKey: workspaceKey, region: 'us' },
  })
  expect(response.status()).toBe(201)

  const errors: string[] = []
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
  page.on('pageerror', error => errors.push(error.message))

  const routes = [
    'issues/all',
    'my-issues/assigned',
    'views/issues',
    'projects/all',
    'initiatives',
    'members',
    'teams',
    'settings/issue-labels',
    'settings/project-statuses',
  ]
  for (const route of routes) {
    await page.goto(`/${workspaceKey}/${route}`)
    await expect(page.locator('main.main-panel').first()).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
    expect(overflow, `${route} should not overflow the viewport`).toBe(false)
  }
  expect(errors).toEqual([])
})

test('streams Agent reasoning, tools, and text into the conversation', async ({ page, request }, testInfo) => {
  const device = testInfo.project.name === 'chromium' ? 'desktop' : 'mobile'
  const workspaceKey = `e2e-agent-${device}`
  const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Agent composer workspace', urlKey: workspaceKey, region: 'us' },
  })
  expect(response.status()).toBe(201)
  await page.goto(`/${workspaceKey}/agent`)
  const editor = page.getByRole('textbox', { name: 'Send a message to Flow AI' })
  await expect(editor).toHaveAttribute('contenteditable', 'true')
  await editor.fill('Review the workspace')
  await page.getByRole('button', { name: 'Submit comment' }).click()
  await expect(page.getByText('Looked at issues')).toBeVisible()
  await expect(page.getByText('Streaming response')).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/agent/.+`))
})
