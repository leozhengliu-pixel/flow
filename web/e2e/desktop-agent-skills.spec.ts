import { expect, type APIRequestContext } from '@playwright/test'
import { test } from './workspace-fixture'

type Skill = { id: string; name: string; instructions: string }

const releaseNotes = '# Release notes\n\nWrite **user-facing** notes.\n\n- Group by area\n- Keep lines under `80` chars\n\nSee [the log](https://example.com/log).'
const cjk = '## 代码审查\n\n请**逐条检查**以下内容，这是一段很长的中文说明文字，用来测试窄屏幕上的截断显示，而不会撑破布局。'
const longLine = 'Summarize the incident timeline for the on-call handoff including every alert that fired, every mitigation that was attempted and every customer-visible symptom '.repeat(3)

async function seed(request: APIRequestContext, workspaceKey: string): Promise<Skill[]> {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const created: Skill[] = []
  for (const [name, instructions] of [['Release notes', releaseNotes], ['中文技能：代码审查', cjk], ['Long single line', longLine]]) {
    const response = await request.post('/api/agent/skills', { headers, data: { name, instructions } })
    expect(response.ok()).toBe(true)
    created.push(await response.json() as Skill)
  }
  return created
}

async function skillsOf(request: APIRequestContext, workspaceKey: string): Promise<Skill[]> {
  const response = await request.get('/api/agent/skills', { headers: { 'X-Workspace-Key': workspaceKey } })
  return response.json() as Promise<Skill[]>
}

// Real device scale (not emulated): 0.5px borders only render at their true width with the browser's own scale factor.
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2'] } })

test.describe('agent skills in Agent personalization', () => {
  test('lists skills as name and timestamp rows without raw Markdown and opens a rendered skill page', async ({ page, request, workspaceKey }) => {
    const skills = await seed(request, workspaceKey)
    await page.goto(`/${workspaceKey}/settings/account/agents`)
    const header = page.locator('.agent-skills-header')
    await expect(header.getByRole('heading', { name: '3 skills' })).toBeVisible({ timeout: 20_000 })
    await page.evaluate(() => document.fonts.ready)
    expect((await header.boundingBox())!.height).toBe(60)
    expect((await header.boundingBox())!.width).toBe(640)
    const rows = page.locator('.agent-skills-row')
    await expect(rows).toHaveCount(3)
    // 16px padding around the 15.5px name and the 1px + 14.5px + 2px timestamp block; the separators are overlays and add no height.
    for (let index = 0; index < 3; index++) expect((await rows.nth(index).boundingBox())!.height).toBe(65)
    expect(await rows.first().evaluate(row => { const line = getComputedStyle(row, '::after'); return [line.position, line.borderBottomWidth, line.left, line.right] })).toEqual(['absolute', '0.5px', '16px', '16px'])
    expect(await rows.last().evaluate(row => getComputedStyle(row, '::after').content)).toBe('none')
    await expect(rows.first().locator('strong')).toHaveText('Release notes')
    await expect(rows.first().locator('small')).toHaveText(/^Created /)
    const text = await page.locator('.agent-skills').innerText()
    for (const marker of ['# Release', '**', '`80`', '](https://', '## ', 'Summarize the incident']) expect(text).not.toContain(marker)
    // Long and CJK names and the row menus never push the page sideways.
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

    await rows.first().getByRole('link', { name: 'Release notes' }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/skill/${skills[0].id}$`))
    const editor = page.getByRole('textbox', { name: 'Skill instructions' })
    await expect(editor.locator('h1')).toHaveText('Release notes')
    await expect(editor.locator('strong')).toHaveText('user-facing')
    await expect(editor.locator('li')).toHaveCount(2)
    await expect(editor.locator('code').first()).toHaveText('80')
    await expect(editor.locator('a')).toHaveAttribute('href', 'https://example.com/log')
    expect(await editor.innerText()).not.toContain('**')
    expect((await page.locator('.agent-skill-card').boundingBox())!.width).toBe(640)
    expect((await page.getByRole('button', { name: 'Save' }).boundingBox())!.height).toBe(32)
    await expect(page.getByRole('textbox', { name: 'Skill name' })).toHaveValue('Release notes')
  })

  test('renames a skill and keeps its Markdown line breaks', async ({ page, request, workspaceKey }) => {
    await seed(request, workspaceKey)
    const [first] = await skillsOf(request, workspaceKey)
    await page.goto(`/${workspaceKey}/settings/skill/${first.id}`)
    const title = page.getByRole('textbox', { name: 'Skill name' })
    await title.fill('Release notes v2')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/account/agents$`))
    await expect(page.locator('.agent-skills-row strong', { hasText: 'Release notes v2' })).toBeVisible()
    const saved = (await skillsOf(request, workspaceKey)).find(skill => skill.id === first.id)!
    expect(saved.name).toBe('Release notes v2')
    expect(saved.instructions).toContain('\n\n- Group by area')
    expect(saved.instructions).toContain('**user-facing**')
  })

  test('creates a skill from Markdown typed into the editor', async ({ page, request, workspaceKey }) => {
    await page.goto(`/${workspaceKey}/settings/skill/new`)
    await expect(page.getByRole('textbox', { name: 'Skill name' })).toBeFocused()
    await page.getByRole('textbox', { name: 'Skill name' }).fill('Triage')
    await page.keyboard.press('Enter')
    await page.keyboard.type('## Steps')
    await page.keyboard.press('Enter')
    await page.keyboard.type('- read **carefully**')
    await expect(page.getByRole('textbox', { name: 'Skill instructions' }).locator('h2')).toHaveText('Steps')
    await page.locator('.agent-skill-actions').getByRole('button', { name: 'Create' }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/account/agents$`))
    const [created] = await skillsOf(request, workspaceKey)
    expect(created.name).toBe('Triage')
    expect(created.instructions).toContain('## Steps')
    expect(created.instructions).toContain('- read **carefully**')
  })

  test('asks before discarding edits and deletes from the row menu after confirming', async ({ page, request, workspaceKey }) => {
    await seed(request, workspaceKey)
    const [first] = await skillsOf(request, workspaceKey)
    await page.goto(`/${workspaceKey}/settings/skill/${first.id}`)
    await page.getByRole('textbox', { name: 'Skill name' }).fill('Changed')
    await page.getByRole('button', { name: 'Cancel' }).click()
    const discard = page.getByRole('dialog')
    await expect(discard.getByText('Discard changes?')).toBeVisible()
    await discard.getByRole('button', { name: 'Cancel' }).click()
    await expect(page).toHaveURL(new RegExp(`/settings/skill/${first.id}$`))
    await page.getByRole('button', { name: 'Cancel' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Discard' }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/account/agents$`))

    const row = page.locator('.agent-skills-row', { hasText: 'Long single line' })
    await row.getByRole('button', { name: 'Open menu' }).click()
    await expect(page.getByRole('menuitem')).toHaveText(['Edit', 'Use in chat', 'Delete'])
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await expect(page.getByRole('dialog').getByText('Delete the skill "Long single line"?')).toBeVisible()
    await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page.locator('.agent-skills-row')).toHaveCount(2)
    expect((await skillsOf(request, workspaceKey)).map(skill => skill.name)).not.toContain('Long single line')
  })

  test('shows the empty state with a create button', async ({ page, workspaceKey }) => {
    await page.goto(`/${workspaceKey}/settings/account/agents`)
    const header = page.locator('.agent-skills-header')
    await expect(header.getByRole('heading', { name: 'No skills created' })).toBeVisible()
    expect((await header.boundingBox())!.height).toBe(60)
    const plus = await header.getByRole('link', { name: 'Create skill' }).boundingBox()
    expect([plus!.width, plus!.height]).toEqual([24, 24])
    await header.getByRole('link', { name: 'Create skill' }).click()
    await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/skill/new$`))
  })
})
