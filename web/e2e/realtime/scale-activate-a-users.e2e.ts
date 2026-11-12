import { expect, test, type Page } from '@playwright/test'
import { apiFetch, signIn, signUp, startApi } from '../security/harness'

async function capture(page: Page, name: string) {
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (process.env.SCALE_ACTIVATE_A_SHOTS) await page.screenshot({ path: `${process.env.SCALE_ACTIVATE_A_SHOTS}/${name}.png`, animations: 'disabled', scale: 'css' })
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  for (const theme of ['Light', 'Dark']) {
    test.describe(`scale-activate-a two users ${viewport.width} ${theme}`, () => {
      test.use({ viewport })

      test('an authenticated viewer receives issue and document updates from another context', async ({ browser, context, page }) => {
        test.setTimeout(120_000)
        const api = await startApi({ csp: 'off' })
        const viewerContext = await browser.newContext({ viewport, isMobile: viewport.width < 500, hasTouch: viewport.width < 500 })
        try {
          const suffix = `${viewport.width}-${theme.toLowerCase()}`
          const owner = await signUp(context, api.origin, `activate-owner-${suffix}`)
          await page.goto(`${api.origin}/sw.js`)
          const core = await apiFetch(page, 'GET', '/api/bootstrap', undefined, owner.workspaceKey)
          const team = (core.json as unknown as { teams: Array<{ id: string }> }).teams[0]
          const viewer = await viewerContext.newPage()
          await viewer.goto(`${api.origin}/sw.js`)
          const account = { email: `activate-viewer-${suffix}@example.test`, password: 'correct-horse-battery-1', workspaceKey: owner.workspaceKey }
          const registered = await apiFetch(viewer, 'POST', '/api/auth/register', { name: 'Activation Viewer', email: account.email, password: account.password })
          expect(registered.status).toBe(201)
          expect((await apiFetch(viewer, 'POST', '/api/auth/verify-email', { token: registered.json?.verificationToken })).status).toBe(200)
          await signIn(viewer, account)
          const invitations = await apiFetch(page, 'POST', `/api/workspaces/${owner.workspaceKey}/invitations`, { emails: [account.email], role: 'member', teamIds: [team.id] })
          expect(invitations.status).toBe(201)
          const token = (invitations.json as unknown as Array<{ token: string }>)[0].token
          expect((await apiFetch(viewer, 'POST', '/api/invitations/accept', { token })).status).toBe(200)
          expect((await apiFetch(viewer, 'PATCH', '/api/account/settings', { language: 'en-US', interfaceTheme: theme })).status).toBe(200)
          const issueResponse = await apiFetch(page, 'POST', '/api/issues', { title: 'Two-user issue', teamId: team.id }, owner.workspaceKey)
          expect(issueResponse.status).toBe(201)
          const issue = issueResponse.json as { id: string; identifier: string }
          const subscribed = viewer.waitForResponse(response => new URL(response.url()).pathname === '/api/realtime/events' && response.status() === 200)
          await viewer.goto(`${api.origin}/${owner.workspaceKey}/issue/${issue.identifier}`)
          await subscribed
          const title = viewer.getByRole('textbox', { name: 'Issue title', exact: true })
          await expect(title).toHaveText('Two-user issue')
          expect((await apiFetch(page, 'PATCH', `/api/issues/${issue.id}`, { title: 'Two-user issue updated' }, owner.workspaceKey)).status).toBe(200)
          await expect(title).toHaveText('Two-user issue updated')
          await capture(viewer, `${suffix}-two-user-issue`)
          const documentResponse = await apiFetch(page, 'POST', '/api/documents', { title: 'Two-user document', content: 'Shared document content.', teamIds: [team.id] }, owner.workspaceKey)
          expect(documentResponse.status).toBe(201)
          const document = documentResponse.json as { id: string; slugId: string }
          await viewer.goto(`${api.origin}/${owner.workspaceKey}/document/${document.slugId}`)
          await expect(viewer.locator('.document-page')).toContainText('Shared document content.')
          expect((await apiFetch(page, 'PATCH', `/api/documents/${document.id}`, { title: 'Two-user document updated' }, owner.workspaceKey)).status).toBe(200)
          await expect(viewer.getByRole('textbox', { name: 'Document title' })).toHaveValue('Two-user document updated')
          await capture(viewer, `${suffix}-two-user-document`)
        } finally {
          await viewerContext.close()
          await api.stop()
        }
      })
    })
  }
}
