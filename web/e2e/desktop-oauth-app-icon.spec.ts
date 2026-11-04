import { expect } from '@playwright/test'

import { test } from './workspace-fixture'

// An application icon uploaded on the create page is served back to the
// workspace and shows as the application's logo and its agent's avatar.
const icon = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="48" fill="#5e6ad2"/><circle cx="128" cy="128" r="64" fill="#fff"/></svg>')

test('an uploaded OAuth application icon loads as the app logo and agent avatar', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const upload = await request.post('/api/oauth-applications/icon', { headers, multipart: { file: { name: 'icon.svg', mimeType: 'image/svg+xml', buffer: icon } } })
  expect(upload.status()).toBe(201)
  const { url } = await upload.json() as { url: string }
  expect(url).toMatch(/^\/uploads\/oauth_app_icon_[A-Za-z0-9_-]+\.svg$/)

  const bootstrap = await (await request.get('/api/bootstrap', { headers })).json() as { teams: { id: string }[] }
  const created = await request.post('/api/oauth-applications', { headers, data: { name: 'Icon agent', redirectUris: ['https://client.example.test/callback'], scopes: ['read', 'write'], logoUrl: url } })
  expect(created.status()).toBe(201)
  const app = await created.json() as { id: string; clientId: string; logoUrl: string }
  expect(app.logoUrl).toBe(url)
  const installed = await request.post('/api/application-installations', { headers, data: { clientId: app.clientId, teamIds: [bootstrap.teams[0].id], scopes: ['read', 'write'], active: true } })
  expect(installed.status()).toBe(200)
  const installations = await (await request.get('/api/application-installations', { headers })).json() as { clientId: string; avatarUrl?: string }[]
  expect(installations.find(item => item.clientId === app.clientId)?.avatarUrl).toBe(url)

  const image = await request.get(url)
  expect(image.status()).toBe(200)
  expect(image.headers()['content-type']).toBe('image/svg+xml')
  expect(image.headers()['x-content-type-options']).toBe('nosniff')
  expect(Buffer.compare(await image.body(), icon)).toBe(0)

  const loaded = async (selector: string) => {
    const element = page.locator(selector)
    await expect(element).toBeVisible()
    await expect.poll(() => element.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0 ? img.naturalWidth : 0)).toBe(256)
  }
  // Settings › API lists the application with its logo.
  await page.goto(`/${workspaceKey}/settings/api`)
  await loaded(`.oauth-app-avatar img[src="${url}"]`)
  // The installed application's page shows the logo and its agent (app user) avatar.
  await page.goto(`/${workspaceKey}/settings/applications/${app.clientId}`)
  await loaded(`.app-logo img[src="${url}"]`)
  await loaded(`.oauth-app-member-avatar img[src="${url}"]`)
})

test('an uploaded workspace logo loads in workspace settings', async ({ page, request, workspaceKey }) => {
  const upload = await request.post(`/api/workspaces/${workspaceKey}/logo`, { multipart: { file: { name: 'logo.svg', mimeType: 'image/svg+xml', buffer: icon } } })
  expect(upload.status()).toBe(200)
  const { workspace } = await upload.json() as { workspace: { logoUrl: string } }
  expect(workspace.logoUrl).toMatch(/^\/uploads\/workspace_logo_[A-Za-z0-9_-]+\.svg$/)
  const image = await request.get(workspace.logoUrl)
  expect(image.status()).toBe(200)
  expect(image.headers()['content-type']).toBe('image/svg+xml')
  expect(Buffer.compare(await image.body(), icon)).toBe(0)

  await page.goto(`/${workspaceKey}/settings/workspace`)
  const logo = page.getByRole('img', { name: 'Workspace logo' })
  await expect(logo).toHaveAttribute('src', workspace.logoUrl)
  await expect.poll(() => logo.evaluate((img: HTMLImageElement) => img.complete ? img.naturalWidth : 0)).toBe(256)
})
