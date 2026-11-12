import { expect, type Page } from '@playwright/test'
import { test } from './scale-p13-fixture'

async function openSubscribed(page: Page, path: string) {
  const subscribed = page.waitForResponse(response => new URL(response.url()).pathname === '/api/realtime/events' && response.status() === 200)
  await page.goto(path)
  await subscribed
}

async function capture(page: Page, name: string) {
  await page.evaluate(() => document.fonts.ready)
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (process.env.SCALE_P13_SHOTS) await page.screenshot({ path: `${process.env.SCALE_P13_SHOTS}/${name}.png`, animations: 'disabled', scale: 'css' })
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test.describe(`scale-p13 ${viewport.width}`, () => {
    test.use({ viewport, isMobile: viewport.width < 500, hasTouch: viewport.width < 500 })

    test('legacy fallback loads and updates issues, projects, documents, inbox and Agent document links', async ({ page, request, workspaceKey }) => {
      test.setTimeout(90_000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      expect((await request.patch('/api/account/settings', { headers, data: { language: viewport.width < 500 ? 'zh-CN' : 'en-US', interfaceTheme: viewport.width < 500 ? 'Light' : 'Dark' } })).ok()).toBe(true)
      const core = await (await request.get('/api/bootstrap', { headers })).json()
      const issueResponse = await request.post('/api/issues', { headers, data: { title: 'P13 issue', teamId: core.teams[0].id } })
      expect(issueResponse.status()).toBe(201)
      const issue = await issueResponse.json()
      await openSubscribed(page, `/${workspaceKey}/issue/${issue.identifier}`)
      await expect(page.locator('.issue-title-field .title-editor[contenteditable="true"]')).toHaveText('P13 issue')
      expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { title: 'P13 issue updated' } })).ok()).toBe(true)
      await expect(page.locator('.issue-title-field .title-editor[contenteditable="true"]')).toHaveText('P13 issue updated')
      await capture(page, `legacy-issue-${viewport.width}`)

      const projectResponse = await request.post('/api/projects', { headers, data: { name: 'P13 project', teamIds: [core.teams[0].id] } })
      expect(projectResponse.status()).toBe(201)
      const project = await projectResponse.json()
      await openSubscribed(page, `/${workspaceKey}/project/${project.slugId}`)
      await expect(page.locator('main')).toContainText('P13 project')
      expect((await request.patch(`/api/projects/${project.id}`, { headers, data: { name: 'P13 project updated' } })).ok()).toBe(true)
      await expect(page.locator('main')).toContainText('P13 project updated')
      if (viewport.width < 500) {
        const details = page.locator('.project-detail-page__toolbar-button[aria-pressed="true"]').last()
        await details.click()
        await expect(page.locator('.project-details-sidebar')).toBeHidden()
      }
      await capture(page, `legacy-project-${viewport.width}`)

      const docResponse = await request.post('/api/documents', { headers, data: { title: 'P13 document', content: 'P13 saved document body', teamIds: [core.teams[0].id] } })
      expect(docResponse.status()).toBe(201)
      const doc = await docResponse.json()
      await openSubscribed(page, `/${workspaceKey}/document/${doc.slugId}`)
      await expect(page.locator('.document-page')).toContainText('P13 saved document body')
      expect((await request.patch(`/api/documents/${doc.id}`, { headers, data: { title: 'P13 document updated' } })).ok()).toBe(true)
      await expect(page.locator('.document-page')).toContainText('P13 document updated')
      await capture(page, `legacy-document-${viewport.width}`)

      const remind = await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })
      expect(remind.status()).toBe(201)
      await page.goto(`/${workspaceKey}/inbox`)
      await expect(page.locator('.flow-inbox-row').filter({ hasText: issue.identifier })).toBeVisible({ timeout: 15_000 })
      await capture(page, `legacy-inbox-${viewport.width}`)

      const chatResponse = await request.post('/api/agent/sessions', { headers, data: { message: 'Create a document called P13 Agent checklist', location: 'page' } })
      expect(chatResponse.status()).toBe(201)
      const chat = await chatResponse.json()
      await page.goto(`/${workspaceKey}/agent/${chat.slugId}`)
      const link = page.locator('main a[href*="/document/"]').first()
      await expect(link).toBeVisible({ timeout: 30_000 })
      const href = await link.getAttribute('href')
      await link.click()
      await expect(page.locator('.document-page')).toContainText('P13 Agent-created document body.')
      await capture(page, `legacy-agent-document-${viewport.width}`)
      // A new page load exercises direct links with no in-memory entity baseline.
      await page.goto(href!)
      await expect(page.locator('.document-page')).toContainText('P13 Agent-created document body.')
    })

    test('manifest details load on a direct link, refresh after an event, and leave complete-array pages on the legacy path', async ({ page, request, workspaceKey }) => {
      test.setTimeout(90_000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      expect((await request.patch('/api/account/settings', { headers, data: { language: viewport.width < 500 ? 'zh-CN' : 'en-US', interfaceTheme: viewport.width < 500 ? 'Light' : 'Dark' } })).ok()).toBe(true)
      const core = await (await request.get('/api/issue-records/bootstrap', { headers })).json()
      const response = await request.post('/api/documents', { headers, data: { title: 'P13 manifest document', content: 'P13 manifest body', teamIds: [core.teams[0].id] } })
      expect(response.status()).toBe(201)
      const doc = await response.json()
      const issueResponse = await request.post('/api/issues', { headers, data: { title: 'P13 manifest issue', teamId: core.teams[0].id } })
      expect(issueResponse.status()).toBe(201)
      const issue = await issueResponse.json()
      let sequence = 100
      let stamp = ''
      let oldReplica = false
      const batchSizes: number[] = []
      await page.route('**/api/sync/**', async route => {
        const path = new URL(route.request().url()).pathname
        const current = await (await request.get(`/api/documents/${doc.id}`, { headers })).json()
        const currentIssue = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json()
        const source = await (await request.get('/api/realtime/changes', { headers })).json()
        sequence = Math.max(sequence, Number(source.head))
        const updated = JSON.stringify([current.updatedAt, currentIssue.updatedAt])
        if (updated !== stamp) { sequence++; stamp = updated }
        const version = String(sequence)
        if (path.endsWith('/manifest')) {
          const snapshot = { ...core, projects: undefined, documents: undefined, issues: undefined, resourceDetailsOmitted: true }
          return route.fulfill({ json: { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core: snapshot, authzEpoch: 'epoch-1', head: String(source.head), capabilities: ['sync-v2'], collections: { document: { revision: '1', complete: false } } } })
        }
        if (path.endsWith('/batch')) {
          if (oldReplica) return route.fulfill({ status: 404, json: {} })
          const { keys } = route.request().postDataJSON() as { keys: Array<{ kind: string; id: string }> }
          batchSizes.push(keys.length)
          const items = []
          for (const key of keys) {
            const endpoint = key.kind === 'documents' ? 'documents' : 'issue-records'
            const response = await request.get(`/api/${endpoint}/${key.id}`, { headers })
            if (response.ok()) items.push({ key, version, data: await response.json() })
          }
          return route.fulfill({ json: { items, authzEpoch: 'epoch-1' } })
        }
        if (path.endsWith('/changes')) {
          const { content: _content, contentState: _state, revisions: _revisions, ...summary } = current
          const { description: _description, ...issueSummary } = currentIssue
          return route.fulfill({ json: { next: version, head: version, authzEpoch: 'epoch-1', transactions: [{ txId: `tx-${version}`, seq: version, upserts: [{ key: { kind: 'document', id: doc.id }, version, completeness: 'summary', value: summary }, { key: { kind: 'issue', id: issue.id }, version, completeness: 'summary', value: issueSummary }], removals: [], invalidations: [] }], hasMore: false, resyncRequired: false } })
        }
        return route.fulfill({ status: 404, json: {} })
      })
      await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
      await expect(page.locator('.document-page')).toContainText('P13 manifest body')
      expect(batchSizes).toEqual([1])
      expect((await request.patch(`/api/documents/${doc.id}`, { headers, data: { title: 'P13 manifest updated' } })).ok()).toBe(true)
      await expect(page.locator('.document-page')).toContainText('P13 manifest updated')
      await capture(page, `manifest-document-${viewport.width}`)
      await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
      await expect(page.locator('.issue-title-field .title-editor[contenteditable="true"]')).toHaveText('P13 manifest issue')
      expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { title: 'P13 manifest issue updated' } })).ok()).toBe(true)
      await expect(page.locator('.issue-title-field .title-editor[contenteditable="true"]')).toHaveText('P13 manifest issue updated')
      await capture(page, `manifest-issue-${viewport.width}`)
      const chatResponse = await request.post('/api/agent/sessions', { headers, data: { message: 'Create a document called P13 Agent checklist', location: 'page' } })
      expect(chatResponse.status()).toBe(201)
      const chat = await chatResponse.json()
      await page.goto(`/${workspaceKey}/agent/${chat.slugId}`)
      const link = page.locator('main a[href*="/document/"]').first()
      await expect(link).toBeVisible({ timeout: 30_000 })
      const href = (await link.getAttribute('href'))!
      await link.click()
      await expect(page.locator('.document-page')).toContainText('P13 Agent-created document body.')
      const initialized = page.waitForResponse(response => new URL(response.url()).pathname === '/api/sync/manifest' && response.status() === 200)
      await page.goto(href)
      await initialized
      await expect(page.locator('.document-page')).toContainText('P13 Agent-created document body.')
      await capture(page, `manifest-agent-document-${viewport.width}`)
      const workspaceProjects = page.locator(`a[href="/${workspaceKey}/projects"]`).first()
      if (viewport.width >= 500 && await workspaceProjects.isVisible()) {
        await workspaceProjects.click()
        await expect(page.locator('main')).toBeVisible()
      } else {
        await page.goto(`/${workspaceKey}/projects`)
        await expect(page.locator('main')).toBeVisible()
      }
      await expect(page.locator('.document-page')).toHaveCount(0)
      oldReplica = true
      const restored = page.waitForResponse(response => ['/api/bootstrap', '/api/issue-records/bootstrap'].includes(new URL(response.url()).pathname) && response.status() === 200)
      await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
      await restored
      await expect(page.locator('.document-page')).toContainText('P13 manifest body')
      await capture(page, `mixed-replica-document-${viewport.width}`)
    })
  })
}
