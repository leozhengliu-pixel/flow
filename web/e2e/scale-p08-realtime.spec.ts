import { mkdir } from 'node:fs/promises'
import { expect, type BrowserContext, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

async function recordStreams(context: BrowserContext) {
  await context.addInitScript(() => {
    const state = window as typeof window & { scaleP08LiveStreams: number; scaleP08Events: unknown[] }
    state.scaleP08LiveStreams = 0
    state.scaleP08Events = []
    const remember = (via: string, data: string) => {
      try {
        const event = JSON.parse(data)
        state.scaleP08Events.push({ via, type: event.type, id: event.id, seq: event.seq, snoozedUntil: event.payload?.entity?.snoozedUntil })
        if (state.scaleP08Events.length > 100) state.scaleP08Events.shift()
      } catch { /* Non-event channel traffic. */ }
    }
    window.addEventListener('flow:inbox-activity', event => remember('inbox', JSON.stringify((event as CustomEvent).detail)))
    const Native = window.EventSource
    window.EventSource = class extends Native {
      private stopped = false
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init)
        state.scaleP08LiveStreams++
        this.addEventListener('message', event => remember('stream', event.data))
      }
      close() {
        if (!this.stopped) { this.stopped = true; state.scaleP08LiveStreams-- }
        super.close()
      }
    }
    const NativeChannel = window.BroadcastChannel
    window.BroadcastChannel = class extends NativeChannel {
      constructor(name: string) {
        super(name)
        this.addEventListener('message', event => { if (event.data?.k === 'msg') remember('tab', event.data.data) })
      }
    }
  })
}

async function liveStreams(page: Page) {
  return page.evaluate(() => (window as typeof window & { scaleP08LiveStreams: number }).scaleP08LiveStreams)
}

async function shot(page: Page, name: string, project: string) {
  const directory = `${process.env.SCALE_P08_ARTIFACTS ?? '/tmp/codex-scale-p08'}/shots`
  await mkdir(directory, { recursive: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: `${directory}/${project}-${name}.png`, animations: 'disabled', scale: 'css' })
}

test('scale-p08 issues, projects, documents, inbox and Agent update through the shared stream', async ({ page, context, request, workspaceKey }, info) => {
  await recordStreams(context)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
  const post = async (path: string, data: unknown, status = 201) => {
    const response = await request.post(path, { headers, data })
    expect(response.status(), await response.text()).toBe(status)
    return response.json()
  }
  const patch = async (path: string, data: unknown) => {
    const response = await request.patch(path, { headers, data })
    expect(response.status(), await response.text()).toBe(200)
  }
  const issue = await post('/api/issues', { title: 'P08 original issue', description: 'P08 editable body', teamId: bootstrap.teams[0].id, stateId: 'state_progress' })
  const project = await post('/api/projects', { name: 'P08 original project', teamIds: [bootstrap.teams[0].id] })
  const document = await post('/api/documents', { title: 'P08 original document', content: 'P08 document body' })
  const follower = await context.newPage()
  try {
    const surfaces = [
      { name: 'issue', route: `/issue/${issue.identifier}`, update: () => patch(`/api/issues/${issue.id}`, { title: 'P08 realtime issue' }), assert: async (tab: Page) => { await expect(tab.getByText('P08 realtime issue', { exact: true }).first()).toBeVisible(); await expect(tab.getByText('P08 editable body', { exact: true }).first()).toBeVisible() } },
      { name: 'project', route: `/project/${project.slugId}/overview`, update: () => patch(`/api/projects/${project.id}`, { name: 'P08 realtime project' }), assert: async (tab: Page) => { await expect(tab.getByText('P08 realtime project', { exact: true }).first()).toBeVisible() } },
      { name: 'document', route: `/document/${document.slugId}`, update: () => patch(`/api/documents/${document.id}`, { title: 'P08 realtime document' }), assert: async (tab: Page) => { await expect(tab.getByRole('textbox', { name: 'Document title' })).toHaveValue('P08 realtime document'); await expect(tab.getByRole('textbox', { name: 'Document content' })).toContainText('P08 document body') } },
    ]
    for (const surface of surfaces) {
      await page.goto(`/${workspaceKey}${surface.route}`)
      await follower.goto(`/${workspaceKey}${surface.route}`)
      await expect(page.locator('main').first()).toBeVisible()
      await expect(follower.locator('main').first()).toBeVisible()
      await expect.poll(async () => await liveStreams(page) + await liveStreams(follower)).toBe(1)
      await surface.update()
      await surface.assert(page)
      await surface.assert(follower)
      if (surface.name === 'project' && (follower.viewportSize()?.width ?? 1440) < 600) {
        const closeDetails = follower.getByRole('button', { name: 'Close project details', exact: true })
        if (await closeDetails.isVisible()) await closeDetails.click()
      }
      await shot(follower, surface.name, info.project.name)
    }
    await page.goto(`/${workspaceKey}/inbox`)
    await follower.goto(`/${workspaceKey}/inbox`)
    for (const tab of [page, follower]) await expect(tab.getByText('No notifications', { exact: true })).toBeVisible()
    const reminder = await post(`/api/issues/${issue.id}/reminders`, { remindAt: new Date(Date.now() + 60_000).toISOString() })
    await patch(`/api/notifications/${reminder.id}`, { snoozedUntil: '' })
    for (const tab of [page, follower]) {
      try {
        await expect(tab.locator('.flow-inbox-notification-list').getByText('P08 realtime issue', { exact: true }).first()).toBeVisible({ timeout: 20_000 })
        await expect(tab).toHaveTitle(/Inbox \(1\)/)
      }
      catch (error) {
        console.log('scale-p08 inbox delivery', JSON.stringify(await tab.evaluate(() => (window as typeof window & { scaleP08Events: unknown[] }).scaleP08Events)))
        throw error
      }
    }
    await shot(follower, 'inbox', info.project.name)
    const chat = await post('/api/agent/sessions', { message: 'P08 scripted reply' })
    await page.goto(`/${workspaceKey}/agent/${chat.slugId}`)
    await follower.goto(`/${workspaceKey}/agent/${chat.slugId}`)
    await expect(follower.getByText('Streaming response', { exact: false }).first()).toBeVisible({ timeout: 20_000 })
    await post(`/api/agent/sessions/${chat.id}/messages`, { message: 'P08 second realtime reply' }, 200)
    for (const tab of [page, follower]) {
      await expect(tab.getByText('P08 second realtime reply', { exact: true }).first()).toBeVisible({ timeout: 20_000 })
      await expect(tab.getByRole('heading', { name: 'Result', exact: true })).toHaveCount(2)
    }
    await shot(follower, 'agent', info.project.name)
  } finally { await follower.close() }
})

test('scale-p08 follower takes over after leader tab exits', async ({ page, context, request, workspaceKey }, info) => {
  await recordStreams(context)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.post('/api/documents', { headers, data: { title: 'P08 before crash', content: 'Body survives leader exit' } })
  expect(response.status()).toBe(201)
  const document = await response.json()
  await page.goto(`/${workspaceKey}/document/${document.slugId}`)
  await expect(page.getByRole('textbox', { name: 'Document title' })).toHaveValue('P08 before crash')
  const follower = await context.newPage()
  try {
    await follower.goto(`/${workspaceKey}/document/${document.slugId}`)
    await expect(follower.getByRole('textbox', { name: 'Document title' })).toHaveValue('P08 before crash')
    await expect.poll(async () => await liveStreams(page) + await liveStreams(follower)).toBe(1)
    await page.close()
    await expect.poll(() => liveStreams(follower)).toBe(1)
    expect((await request.patch(`/api/documents/${document.id}`, { headers, data: { title: 'P08 after leader crash' } })).status()).toBe(200)
    await expect(follower.getByRole('textbox', { name: 'Document title' })).toHaveValue('P08 after leader crash', { timeout: 20_000 })
    await shot(follower, 'leader-crash', info.project.name)
  } finally { await follower.close() }
})
