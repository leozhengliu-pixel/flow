import { expect } from '@playwright/test'
import { test, createReview, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('persists a review reaction without editing the comment draft and uploads composer attachments', async ({ page, request, workspaceKey }) => {
  const id = await createReview(request, workspaceKey)
  await page.goto(`/${workspaceKey}/review/github-1`)
  await page.getByRole('button', { name: 'Add reaction', exact: true }).click()
  await page.getByRole('button', { name: 'thumbs up +1 like' }).first().click()
  await expect(page.locator('.review-document-actions .reaction-pills button')).toHaveText('👍1')
  await expect(page.getByRole('textbox', { name: 'Comment', exact: true })).toHaveValue('')
  await expect(page.getByRole('button', { name: 'Submit comment' })).toBeDisabled()
  await page.reload()
  await expect(page.locator('.review-document-actions .reaction-pills button')).toHaveText('👍1')
  await capture(page.locator('.review-overview-main'), 'flow-review-reaction')
  await page.locator('input[type=file]').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('Review notes') })
  await expect(page.getByRole('textbox', { name: 'Comment', exact: true })).toHaveValue(/\[notes.txt\]\(http.*\/uploads\//)
  const response = await request.get(`/api/reviews/${id}`, { headers: { 'X-Workspace-Key': workspaceKey } })
  expect((await response.json()).commentAttachments).toHaveLength(1)
})
