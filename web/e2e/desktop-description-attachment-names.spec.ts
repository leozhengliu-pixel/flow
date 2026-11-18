import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'

test('files picked with the paperclip stay file cards whatever their names, through save and reload', async ({ page, request, workspaceKey }) => {
  test.setTimeout(60_000)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
  const created = await request.post('/api/issues', { headers, data: { title: 'Attachment names', description: 'Body copy', teamId: bootstrap.teams[0].id } })
  expect(created.status()).toBe(201)
  const issue = await created.json()
  const names = ['测试报告.docx', '测试 report (1).docx']

  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  const editor = page.locator('.description-editor')
  await expect(editor).toContainText('Body copy', { timeout: 20_000 })
  await page.locator('.document-actions input[type="file"]').setInputFiles(names.map((name, index) => ({ name, mimeType: DOCX, buffer: Buffer.from(`file ${index}`) })))

  const cards = editor.locator('.description-file-node')
  const expectCards = async () => {
    await expect(cards).toHaveCount(2, { timeout: 20_000 })
    await expect(cards.locator('.description-file-name')).toHaveText(names)
    await expect(editor).not.toContainText('::file')
  }
  await expectCards()

  await page.reload()
  await expectCards()

  // Re-edit: the collaborative save writes the Markdown back from the cards.
  await editor.getByText('Body copy').click()
  await page.keyboard.press('End')
  await page.keyboard.type(' edited')
  await expect.poll(async () => (await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json()).description, { timeout: 20_000 }).toContain('Body copy edited')
  await page.reload()
  await expect(editor).toContainText('Body copy edited', { timeout: 20_000 })
  await expectCards()

  const stored = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json()
  const directives = [...stored.description.matchAll(/::file\[([^\]]*)]\(([^)\s]+)\)/g)]
  expect(directives.map(match => match[1])).toEqual(names)
  for (const [index, match] of directives.entries()) {
    const attachment = stored.attachments.find((item: { title: string }) => item.title === names[index])
    expect(attachment.url).toBe(`/uploads/${attachment.id}_${names[index]}`)
    const download = await request.get(match[2], { headers })
    expect(download.status()).toBe(200)
    expect(await download.text()).toBe(`file ${index}`)
  }
  expect(directives[1][2]).toContain('%20report%20%281%29.docx')
})
