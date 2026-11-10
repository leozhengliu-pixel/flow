import { expect } from '@playwright/test'
import { test as workspaceTest } from './workspace-fixture'

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  workspaceTest.describe(`${viewport.width}px create a team`, () => {
    workspaceTest.use({ viewport, deviceScaleFactor: 2, isMobile: viewport.width < 768, hasTouch: viewport.width < 768, colorScheme: 'dark', workspaceNamePrefix: '[team-create]' })

    workspaceTest('opens inside the settings layout, goes back, and creates a team', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      await page.goto(`/${workspaceKey}/settings/teams`)
      await page.getByRole('button', { name: viewport.width < 768 ? 'Create team' : 'Create new team', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/new-team$`))
      // The form is a settings page: the settings sidebar (desktop) or settings header (phone) is there, with the Back link.
      if (viewport.width >= 768) await expect(page.locator('aside.settings-sidebar')).toBeVisible()
      else await expect(page.locator('.settings-mobile-header')).toBeVisible()
      await expect(page.getByRole('heading', { name: 'Create a new team', exact: true })).toBeVisible()
      await expect(page.getByText('Create a new team to manage separate cycles, workflows, and notifications')).toBeVisible()
      await page.getByRole('button', { name: 'Back', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/teams$`))

      await page.getByRole('button', { name: viewport.width < 768 ? 'Create team' : 'Create new team', exact: true }).click()
      const name = page.getByRole('textbox', { name: 'Icon & Name', exact: true })
      await expect(name).toBeFocused()
      const identifier = page.getByRole('textbox', { name: 'Identifier', exact: true })
      // Submitting an empty form explains what is missing and creates nothing.
      await page.getByRole('button', { name: 'Create team', exact: true }).click()
      await expect(page.getByText('Name must be at least 1 character long')).toBeVisible()
      await name.fill('Create QA Team')
      await expect(identifier).toHaveValue('CRE')
      await identifier.fill('crq')
      await expect(identifier).toHaveValue('CRQ')
      await page.getByRole('button', { name: 'Create team', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/teams/CRQ$`))
      const data = await (await request.get('/api/bootstrap', { headers })).json() as { teams: { name: string; key: string }[] }
      expect(data.teams.find(team => team.key === 'CRQ')?.name).toBe('Create QA Team')
    })

    workspaceTest('a parent team is preselected from the link and fixes the copy source', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const created = await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: 'Parent QA', key: 'PQA' } })
      expect(created.status()).toBe(201)
      const parent = await created.json()
      await page.goto(`/${workspaceKey}/settings/new-team?parentTeamId=${encodeURIComponent(parent.id)}`)
      await expect(page.getByRole('combobox', { name: 'Set parent team', exact: true })).toContainText('Parent QA')
      await expect(page.getByRole('combobox', { name: 'Copy from team', exact: true })).toBeDisabled()
    })
  })
}
