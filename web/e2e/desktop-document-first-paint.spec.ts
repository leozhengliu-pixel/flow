import { expect, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

const people = Array.from({ length: 12_000 }, (_, index) => ({
  id: `paint-member-${index}`,
  name: `Workspace member ${index}`,
  displayName: `Workspace member ${index}`,
  active: true,
}))

function measureDocumentCreation(page: Page) {
  return page.evaluateHandle(() => {
    const tasks: PerformanceEntry[] = []
    const observer = new PerformanceObserver(list => tasks.push(...list.getEntries()))
    observer.observe({ type: 'longtask' })

    const measured = new Promise<{
      title: number
      editable: number
      longestTaskBeforeTitle: number
      placeholderAtTitle: boolean
      editorMountedAtTitle: boolean
    }>(resolve => {
      document.addEventListener('click', () => {
        const start = performance.now()
        let titleAt: number | undefined
        let placeholderAtTitle = false
        let editorMountedAtTitle = false
        const sample = () => {
          if (titleAt === undefined && document.querySelector('.document-title')?.checkVisibility()) {
            titleAt = performance.now()
            placeholderAtTitle = Boolean(document.querySelector('.document-editor-placeholder')?.checkVisibility())
            editorMountedAtTitle = Boolean(document.querySelector('.collaborative-editor'))
          }
          if (titleAt === undefined || !document.querySelector('.document-editor .ProseMirror[contenteditable="true"]')?.checkVisibility()) {
            requestAnimationFrame(sample)
            return
          }
          const title = titleAt
          const editable = performance.now() - start
          requestAnimationFrame(() => {
            tasks.push(...observer.takeRecords())
            observer.disconnect()
            const beforeTitle = tasks.filter(task => task.startTime < title && task.startTime + task.duration >= start)
            resolve({
              title: title - start,
              editable,
              longestTaskBeforeTitle: Math.max(0, ...beforeTitle.map(task => task.duration)),
              placeholderAtTitle,
              editorMountedAtTitle,
            })
          })
        }
        requestAnimationFrame(sample)
      }, { capture: true, once: true })
    })
    return () => measured
  })
}

test.use({ viewport: { width: 1440, height: 900 } })

test('paints the title before preparing a document with mentions in a large workspace', async ({ page, request, workspaceKey }, testInfo) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap?projection=project-list', { headers })
  expect(bootstrap.ok()).toBeTruthy()
  const { teams: [team] } = await bootstrap.json()
  const warm = await request.post('/api/documents', {
    headers,
    data: { title: 'Warm editor', content: 'Existing document body', teamIds: [team.id] },
  })
  expect(warm.status()).toBe(201)
  const { slugId } = await warm.json()

  // Enlarge the directory the editor sees without thousands of setup writes.
  await page.route(/\/api\/(?:issue-records\/)?bootstrap(?:\?|$)/, async route => {
    const response = await route.fetch()
    if (!response.ok()) return route.fulfill({ response })
    const payload = await response.json()
    const data = payload.bootstrap ?? payload
    if (Array.isArray(data.users)) data.users.push(...people)
    await route.fulfill({ response, json: payload })
  })
  await page.routeWebSocket('**/api/realtime/socket**', socket => socket.close())
  await page.route('**/api/events**', route => route.fulfill({ status: 204 }))
  await page.addInitScript(() => localStorage.setItem('flow:locale', 'en-US'))
  await page.goto(`/${workspaceKey}/document/${slugId}`)
  const editor = page.getByRole('textbox', { name: 'Document content' })
  await expect(editor).toBeEditable()
  await page.getByRole('link', { name: 'Documents', exact: true }).first().click()
  const create = page.getByRole('button', { name: 'New document', exact: true })
  await expect(create).toBeEnabled()

  // Use the real create flow, with a body resembling a document created from a template.
  const contentData = {
    type: 'doc',
    content: people.slice(-240).map(({ id }) => ({
      type: 'paragraph',
      content: [{ type: 'mention', attrs: { id, label: 'Stored name', mentionType: 'user' } }],
    })),
  }
  await page.route('**/api/documents', route => {
    const request = route.request()
    return request.method() === 'POST' ? route.continue({ postData: JSON.stringify({ ...request.postDataJSON(), contentData }) }) : route.continue()
  })
  const measure = await measureDocumentCreation(page)
  await create.click()
  await expect(page).toHaveURL(/\/document\/[^/]+$/)
  await expect(page.getByRole('textbox', { name: 'Document title' })).toBeVisible()
  await expect(editor).toBeEditable()
  const metrics = await measure.evaluate(finish => finish())
  await measure.dispose()
  await testInfo.attach('document first paint', { body: JSON.stringify(metrics, null, 2), contentType: 'application/json' })

  expect(metrics.placeholderAtTitle).toBe(true)
  expect(metrics.editorMountedAtTitle).toBe(false)
  expect(metrics.title).toBeLessThan(1_500)
  expect(metrics.longestTaskBeforeTitle).toBeLessThan(200)
  expect(metrics.editable).toBeGreaterThan(metrics.title)
  await expect(editor.getByText(`@${people.at(-1)!.displayName}`, { exact: true })).toBeVisible()
})
