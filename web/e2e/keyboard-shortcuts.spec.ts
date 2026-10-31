import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })
test.skip(({ isMobile }) => isMobile, 'desktop keyboard shortcuts')

const dialogRows = [
  'Open command menu', 'Save or submit', 'Send comment', 'Back', 'Open search',
  'Open help menu', 'View keyboard shortcuts', 'Switch workspace', 'Log out', 'Ask Flow',
  'Toggle left sidebar', 'Toggle right sidebar', 'Open parent issue', 'Go to team', 'Go to inbox',
  'Go to Agent', 'Go to my issues', 'Go to reviews', 'Go to settings', 'Go to triage', 'Go to drafts',
  'Go to active issues', 'Go to backlog', 'Go to cycles', 'Go to projects', 'Go to pulse', 'Go to customers',
  'Open customer…', 'Open document…', 'New issue', 'Accept issue', 'Decline issue',
  'Mark issue as duplicate of', 'Snooze issue until', 'Next issue', 'Previous issue', 'Back to triage',
  'New project', 'New initiative',
]

test('every listed shortcut has a visible row and the shell handlers respond', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  await expect(page.locator('.sidebar')).toBeVisible()

  await page.keyboard.press('ControlOrMeta+/')
  const dialog = page.locator('.sidebar-shortcuts-dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('dt')).toHaveText(dialogRows)
  await page.keyboard.press('Escape')

  const navigation: Array<[string, string]> = [
    ['i', '/inbox'], ['j', '/agent'], ['m', '/my-issues/assigned'], ['r', '/reviews'],
    ['s', '/settings/'], ['d', '/drafts'], ['a', '/issues/active'], ['b', '/issues/backlog'],
    ['p', '/projects/all'],
  ]
  for (const [key, suffix] of navigation) {
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.keyboard.press('g')
    await page.keyboard.press(key)
    await expect(page).toHaveURL(new RegExp(`${workspaceKey.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}${suffix.replace('/', '\\/')}`))
  }

  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  await expect(page.locator('.sidebar')).toBeVisible()
  await page.keyboard.press('?')
  await expect(page.locator('.sidebar-help-menu')).toBeVisible()
  await page.keyboard.press('Escape')
  const detailsButton = page.getByRole('button', { name: /details/ })
  await expect(detailsButton).toBeVisible()
  await page.keyboard.press(']')
  await expect(detailsButton).toHaveAttribute('aria-pressed', 'true')
  await page.keyboard.press(']')
  await expect(detailsButton).toHaveAttribute('aria-pressed', 'false')

  const input = page.locator('.sidebar-shortcuts-dialog input')
  await page.keyboard.press('ControlOrMeta+/')
  await input.focus()
  await page.keyboard.press('?')
  await expect(page.locator('.sidebar-help-menu')).toBeHidden()
})
