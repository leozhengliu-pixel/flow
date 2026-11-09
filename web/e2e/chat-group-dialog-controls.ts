import { expect, type APIRequestContext, type Page } from '@playwright/test'
import type { Project } from '../src/types/flow'

// Real mouse and keyboard input on every control of the project's "connect Feishu group" dialog. The dialog is modal, so a control
// whose menu renders under the overlay, or outside the modal's pointer-events, fails here the way it failed for the owner.
// Playwright's click() hit-tests the target and waits until nothing else intercepts it.

const SAVED: unknown[] = []

export async function mockFeishu(page: Page) {
  SAVED.length = 0
  await page.addInitScript(() => { localStorage.setItem('flow:locale', 'en-US') })
  await page.route('**/api/bootstrap', async route => {
    const response = await route.fetch()
    const data = await response.json()
    data.chatPlatform = 'feishu'
    await route.fulfill({ response, json: data })
  })
  await page.route('**/api/integrations/feishu/chat', async route => {
    const bootstrap = await (await page.request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': new URL(page.url()).pathname.split('/')[1] } })).json()
    await route.fulfill({ json: {
      connection: { id: 'chat-review', provider: 'feishu', name: 'Feishu', status: 'connected', config: {}, scopes: [], channels: [] },
      identity: null, bindings: [], bindableTeamIds: bootstrap.teams.map((team: { id: string }) => team.id), credentialsConfigured: true, webhookURL: '', canManage: true,
      permissions: { workspace: { canManage: true }, tenant: { status: 'authorized' }, teams: { manageableTeamIds: bootstrap.teams.map((team: { id: string }) => team.id), privateTeamIds: [] } },
    } })
  })
  await page.route('**/api/integrations/feishu/chat/chats*', route => route.fulfill({ json: { items: [{ id: 'oc_room', name: 'Product launch', external: false }, { id: 'oc_ops', name: 'Operations', external: false }, { id: 'oc_ext', name: 'Partner room', external: true }], hasMore: false } }))
  await page.route('**/api/integrations/feishu/chat/bindings', async route => {
    SAVED.push(route.request().postDataJSON())
    await route.fulfill({ status: 201, json: { id: 'binding-1' } })
  })
}

export async function exerciseChatGroupDialog({ page, request, workspaceKey, isMobile }: { page: Page; request: APIRequestContext; workspaceKey: string; isMobile: boolean }) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const project = await (await request.post('/api/projects', { headers, data: { name: 'Controls project' } })).json() as Project
  await mockFeishu(page)
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
  await page.locator('.project-details-sidebar__property-trigger.is-empty', { hasText: /Feishu/ }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: /Connect Feishu group/ }).click()
  await expect(dialog.getByRole('button', { name: 'Save group binding' })).toBeVisible()

  // Desktop: the Radix select (a combobox) and its menu; phone: the native select.
  const pick = async (label: string, option: string | RegExp) => {
    if (isMobile) {
      await dialog.locator(`select[aria-label="${label}"]`).selectOption(typeof option === 'string' ? { label: option } : { index: 0 })
      return
    }
    await dialog.getByRole('combobox', { name: label }).click()
    const menu = page.getByRole('listbox')
    await expect(menu).toBeVisible()
    await menu.getByRole('option', { name: option }).click()
    await expect(menu).toBeHidden()
  }
  const value = (label: string) => isMobile
    ? dialog.locator(`select[aria-label="${label}"]`).evaluate((select: HTMLSelectElement) => select.selectedOptions[0]?.textContent ?? '')
    : dialog.getByRole('combobox', { name: label }).innerText()

  await pick('Group', 'Product launch')
  expect(await value('Group')).toContain('Product launch')
  await pick('Team', /.+/)
  await pick('Default template', 'No template')
  await pick('Card language', '简体中文')
  expect(await value('Card language')).toContain('简体中文')
  await pick('Card language', 'English')
  expect(await value('Card language')).toContain('English')

  if (!isMobile) {
    // The keyboard: open with Enter, move with the arrows, choose with Enter.
    await dialog.getByRole('combobox', { name: 'Group' }).focus()
    await page.keyboard.press('Enter')
    await expect(page.getByRole('listbox')).toBeVisible()
    await expect(page.getByRole('option', { name: 'Product launch' })).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(page.getByRole('listbox')).toBeHidden()
    expect(await value('Group')).toContain('Operations')
    await pick('Group', 'Product launch')
  } else {
    await dialog.locator('select[aria-label="Group"]').selectOption({ label: 'Product launch' })
  }

  // Checkboxes and switches take real clicks.
  const comment = dialog.getByRole('checkbox', { name: 'Comment added' })
  const commentBefore = await comment.getAttribute('aria-checked')
  await comment.click()
  await expect(comment).not.toHaveAttribute('aria-checked', commentBefore ?? '')
  const create = dialog.getByLabel('Allow creating issues')
  const createBefore = await create.isChecked()
  await create.setChecked(!createBefore)
  expect(await create.isChecked()).toBe(!createBefore)
  await create.setChecked(createBefore)

  // Saving needs the approval switch.
  const save = dialog.getByRole('button', { name: 'Save group binding' })
  await expect(save).toBeDisabled()
  await dialog.getByLabel(/I approve sharing/).setChecked(true)
  await expect(save).toBeEnabled()
  await save.click()
  await expect.poll(() => SAVED.length).toBe(1)
  expect(SAVED[0]).toMatchObject({ chatId: 'oc_room', chatName: 'Product launch', projectIds: [project.id], language: 'en-US', authorize: true })
  expect(errors).toEqual([])
  await page.unrouteAll({ behavior: 'ignoreErrors' })
}
