import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })
test.skip(({ isMobile }) => isMobile, 'desktop keyboard shortcuts')

const dialogRows = [
  'Open command menu', 'Save or submit', 'Send comment', 'Back', 'Open search',
  'Open help menu', 'View keyboard shortcuts', 'Switch workspace', 'Log out', 'Ask Flow',
  'Toggle left sidebar', 'Toggle right sidebar', 'Open issue', 'Open parent issue', 'Go to team', 'Go to inbox',
  'Go to Agent', 'Go to my issues', 'Go to reviews', 'Go to settings', 'Go to triage', 'Go to drafts',
  'Go to active issues', 'Go to backlog', 'Go to cycles', 'Go to projects', 'Go to pulse', 'Go to customers',
  'Open customer…', 'Open document…', 'Open team archive', 'Go to all issues', 'Go to Loops',
  'Go to current cycle', 'Go to upcoming cycle', 'Go to initiatives', 'Go to dashboards',
  'Open a favorite', 'Open a project', 'Open a cycle', 'Open a team', 'Open a user', 'Open view',
  'Open initiative', 'Open link from last toast', 'Dismiss all toasts', 'Show display options',
  'New issue', 'New issue in full screen view', 'Assign issue to user', 'Assign to me', 'Change labels',
  'Change issue status', 'Change priority', 'Change estimate', 'Set due date', 'Rename',
  'Focus issue description input', 'Mark as favorite', 'Remind about issue', 'Move to another team',
  'Delete issue', 'Subscribe to issue', 'Manage issue subscribers', 'Mark as blocked', 'Mark as blocking',
  'Reference related issue', 'Mark as duplicate of another issue', 'Link any URL to issue…',
  'Copy issue id', 'Copy git branch name', 'Copy issue URL', 'Copy issue title',
  'Copy everything as Markdown', 'Copy as prompt', 'Create sub-issue', 'Add to cycle', 'Add to project',
  'Accept issue', 'Decline issue',
  'Mark issue as duplicate of', 'Snooze issue until', 'Next issue', 'Previous issue', 'Back to triage',
  'New project', 'New initiative', 'Delete notification', 'Mark as read/unread', 'Mark all as read',
  'Snooze notification',
]

test('every listed shortcut has a visible row and the shell handlers respond', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  await expect(page.locator('.sidebar')).toBeVisible()

  await page.keyboard.press('ControlOrMeta+/')
  const dialog = page.locator('.sidebar-shortcuts-dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('dt')).toHaveText(dialogRows)
  for (const row of await dialog.locator('dt').all()) await expect(row).toBeVisible()
  await dialog.getByRole('textbox', { name: 'Search shortcuts' }).fill('duplicate')
  await expect(dialog.locator('dt')).toHaveText(['Mark as duplicate of another issue', 'Mark issue as duplicate of'])
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/shortcut-help.png`, animations: 'disabled' })
  await dialog.getByRole('textbox', { name: 'Search shortcuts' }).clear()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()

  await page.keyboard.press('?')
  await expect(page.locator('.sidebar-help-menu')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.sidebar-help-menu')).toBeHidden()
  const detailsButton = page.getByRole('button', { name: /details/ })
  await expect(detailsButton).toBeVisible()
  const initialDetails = await detailsButton.getAttribute('aria-pressed')
  expect(['true', 'false']).toContain(initialDetails)
  await detailsButton.press(']')
  await expect(detailsButton).toHaveAttribute('aria-pressed', String(initialDetails !== 'true'))
  await detailsButton.press(']')
  await expect(detailsButton).toHaveAttribute('aria-pressed', initialDetails!)

  const input = page.locator('.sidebar-shortcuts-dialog input')
  await page.keyboard.press('ControlOrMeta+/')
  await input.focus()
  await page.keyboard.press('?')
  await expect(page.locator('.sidebar-help-menu')).toBeHidden()
})

const navigation: Array<[string, string]> = [
  ['i', '/inbox'], ['j', '/agent'], ['m', '/my-issues/assigned'], ['r', '/reviews'],
  ['s', '/settings/'], ['d', '/drafts'], ['a', '/issues/active'], ['b', '/issues/backlog'],
  ['p', '/projects/all'],
]

for (const [key, suffix] of navigation) {
  test(`G then ${key} navigates to ${suffix}`, async ({ page, workspaceKey }) => {
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.keyboard.press('g')
    await page.keyboard.press(key)
    await expect(page).toHaveURL(new RegExp(`${workspaceKey.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}${suffix.replace('/', '\\/')}`))
  })
}
