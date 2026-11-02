import { createHmac } from 'node:crypto'
import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import type { BootstrapData } from '../src/types/flow'
import { test as base } from './workspace-fixture'

export const test = base.extend<{ reviewPreferences: void }>({
  reviewPreferences: [async ({ request, workspaceKey }, use) => {
    await preferences(request, workspaceKey, 'Dark')
    await use()
  }, { auto: true }],
})

export async function bootstrap(request: APIRequestContext, workspaceKey: string): Promise<BootstrapData> {
  const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
  expect(response.ok()).toBe(true)
  return response.json()
}

export async function preferences(request: APIRequestContext, workspaceKey: string, theme: 'Light' | 'Dark', language = 'en-US') {
  expect((await request.patch('/api/account/settings', { headers: { 'X-Workspace-Key': workspaceKey }, data: { interfaceTheme: theme, language, pulseWelcomeDismissed: true } })).ok()).toBe(true)
}

export async function createReview(request: APIRequestContext, workspaceKey: string, state: 'open' | 'merged' | 'closed' = 'open') {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const secret = `review-fixture-${workspaceKey}`
  expect((await request.put('/api/integrations/github', { headers, data: { name: 'Offline review fixture', config: { organization: 'offline-provider', webhookSecret: secret, codeAccess: 'true' }, scopes: ['repo'] } })).ok()).toBe(true)
  const number = state === 'merged' ? 2 : state === 'closed' ? 3 : 1
  const pullRequest = { number, id: number + 9800, title: `${state} review`, body: 'A review description.', html_url: `https://github.com/test/repo/pull/${number}`, state: state === 'open' ? 'open' : 'closed', merged: state === 'merged', user: { login: 'flowuser' }, base: { ref: 'main' }, head: { ref: 'review/regression', sha: 'abc' }, additions: 1, deletions: 0, commits: 1 }
  const body = JSON.stringify({ action: state === 'open' ? 'opened' : 'closed', number, pull_request: pullRequest, repository: { full_name: 'test/repo' }, sender: { login: 'flowuser' } })
  const response = await request.post('/api/integrations/github/webhook', { headers: { ...headers, 'Content-Type': 'application/json', 'X-GitHub-Event': 'pull_request', 'X-GitHub-Delivery': `${workspaceKey}-${number}`, 'X-Hub-Signature-256': `sha256=${createHmac('sha256', secret).update(body).digest('hex')}` }, data: body })
  expect(response.status()).toBe(202)
  return (await response.json() as { reviewId: string }).reviewId
}

export async function withinViewport(locator: Locator, page: Page) {
  const box = (await locator.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width)
}

export async function capture(locator: Locator, name: string) {
  if (process.env.FLOW_REVIEW_SHOTS) await locator.screenshot({ path: `${process.env.FLOW_REVIEW_SHOTS}/${name}.png`, animations: 'disabled' })
}
