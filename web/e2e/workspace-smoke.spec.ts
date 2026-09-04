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
  await expect(page.getByText('Reviewed workspace context.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Result' })).toBeVisible()
  await expect(page.getByText('Streaming').last()).toHaveCSS('font-weight', /600|700/)
  await expect(page.getByText('Verified').last()).toHaveCSS('font-family', /mono/i)
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/agent/.+`))
})

test('opens the project creation assistant and dispatches a suggestion', async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'The project assistant is desktop-only')
  const workspaceKey = `e2e-project-agent-${testInfo.project.name}-${Date.now()}`
  const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Project assistant workspace', urlKey: workspaceKey, region: 'us' },
  })
  expect(response.status()).toBe(201)

  await page.setViewportSize({ width: 1470, height: 706 })
  await page.goto(`/${workspaceKey}/projects/all`)
  await page.getByRole('button', { name: 'New project', exact: true }).first().click()
  await page.getByRole('textbox', { name: 'Project name', exact: true }).fill('Unsaved project')
  await page.getByRole('button', { name: 'Discard project', exact: true }).click()
  const initialDiscard = page.getByRole('alertdialog', { name: 'Discard changes?', exact: true })
  await expect(initialDiscard).toBeVisible()
  await initialDiscard.getByRole('button', { name: 'Cancel', exact: true }).click()
  await page.getByRole('textbox', { name: 'Project name', exact: true }).fill('')
  await page.getByRole('button', { name: 'Create with Agent', exact: true }).click()

  const assistant = page.locator('aside[aria-label="Project creation assistant"]')
  await expect(assistant).toBeVisible()
  await expect(page.getByRole('button', { name: 'Outline the scope', exact: true })).toBeVisible()
  const geometry = await page.evaluate(() => {
    const rect = (selector: string) => {
      const element = document.querySelector<HTMLElement>(selector)
      if (!element) return null
      const value = element.getBoundingClientRect()
      return { x: value.x, y: value.y, width: value.width, height: value.height }
    }
    return {
      panel: rect('.lp-new-project__panel'),
      form: rect('.lp-new-project__form'),
      assistant: rect('.project-creation-agent'),
      composer: rect('.project-creation-agent__composer'),
    }
  })
  expect(geometry.panel?.width).toBeCloseTo(1320, 0)
  expect(geometry.panel?.height).toBeCloseTo(621, 0)
  expect(geometry.form?.width).toBeCloseTo(904, 0)
  expect(geometry.assistant?.width).toBeCloseTo(400, 0)
  expect(geometry.composer?.width).toBeCloseTo(400, 0)
  await page.evaluate(() => { document.documentElement.dataset.theme = 'dark' })
  const darkBackground = await assistant.evaluate(element => getComputedStyle(element).backgroundColor)
  expect(darkBackground).not.toBe('rgba(0, 0, 0, 0)')
  await page.evaluate(() => { document.documentElement.dataset.theme = 'light' })

  const streamRequest = page.waitForRequest(request => request.url().endsWith('/api/agent/sessions/stream') && request.method() === 'POST')
  await page.getByRole('button', { name: 'Outline the scope', exact: true }).click()
  const sent = await streamRequest
  expect(sent.postDataJSON()).toMatchObject({ location: 'page' })
  expect(sent.postDataJSON().message).toContain('Outline the scope')
  await expect(page.getByText('Outline the scope', { exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Project name', exact: true })).toHaveValue('Agent launch')

  await page.getByRole('button', { name: 'Hide agent', exact: true }).click()
  await expect(assistant).toHaveAttribute('data-hidden', 'true')
  await page.getByRole('button', { name: 'Create with Agent', exact: true }).click()
  await expect(assistant).not.toHaveAttribute('data-hidden', 'true')
  await page.getByRole('button', { name: 'Close project creation', exact: true }).click()
  const discard = page.getByRole('alertdialog', { name: 'Discard changes?', exact: true })
  await expect(discard).toBeVisible()
  await discard.getByRole('button', { name: 'Discard', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Create project', exact: true })).toBeHidden()
})
