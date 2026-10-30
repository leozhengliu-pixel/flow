import { expect, test } from '@playwright/test'

test('keeps the unchosen document control readable at narrow and wide editor widths', async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name !== 'chromium', 'The geometry assertion uses the desktop editor layout')
  const workspaceKey = `loops-contract-${Date.now()}`
  const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Loops contract geometry', urlKey: workspaceKey, region: 'us' },
  })
  expect(workspaceResponse.status()).toBe(201)
  const loopResponse = await request.post('http://127.0.0.1:4180/api/loops', {
    headers: { 'X-Workspace-Key': workspaceKey },
    data: { name: 'Daily team summary', instructions: '', level: 'workspace', status: 'draft' },
  })
  expect(loopResponse.status()).toBe(201)
  const loop = await loopResponse.json() as { id: string }

  // Keep the test focused on the browser layout instead of asking the model to infer a contract.
  await page.route(`**/api/loops/${loop.id}`, async route => {
    if (route.request().method() !== 'GET') return route.fallback()
    const response = await route.fetch()
    const body = await response.json()
    body.outputContract = {
      source: 'model',
      readOnly: false,
      outputs: [{ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', ambiguous: true, description: 'the team document' }, cardinality: 'onePerRun' }],
    }
    await route.fulfill({ response, json: body })
  })

  await page.addInitScript(() => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: 'dark' }))
    localStorage.setItem('flow:locale', 'en-US')
  })
  for (const viewport of [{ width: 800, height: 600 }, { width: 1280, height: 800 }]) {
    await page.setViewportSize(viewport)
    await page.goto(`/${workspaceKey}/loops/new?draftId=${encodeURIComponent(loop.id)}`)
    const warning = page.locator('.loops-contract-warning').first()
    await expect(warning).toBeVisible()
    const documentButton = warning.getByRole('button', { name: 'Document', exact: true })
    await expect(documentButton).toHaveText('Choose document…')
    const geometry = await documentButton.evaluate(element => {
      const button = element as HTMLElement
      const card = button.closest('.loops-card') as HTMLElement
      const rect = button.getBoundingClientRect()
      const cardRect = card.getBoundingClientRect()
      return { width: rect.width, scrollWidth: button.scrollWidth, right: rect.right, cardRight: cardRect.right }
    })
    expect(geometry.width).toBeGreaterThanOrEqual(geometry.scrollWidth)
    expect(geometry.right).toBeLessThanOrEqual(geometry.cardRight + 0.5)
  }
})
