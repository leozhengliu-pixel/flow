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

// Real device scale 3, as on the phone gate: emulated scale would draw 0.5px borders as 1px.
test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=3'] } })

test.describe('agent skills on a phone', () => {
  test('keeps the list and the skill page inside the screen', async ({ page, request, workspaceKey }) => {
    const [first] = await seed(request, workspaceKey)
    await page.goto(`/${workspaceKey}/settings/account/agents`)
    const header = page.locator('.agent-skills-header')
    await expect(header.getByRole('heading', { name: '3 skills' })).toBeVisible({ timeout: 20_000 })
    await page.evaluate(() => document.fonts.ready)
    expect((await header.boundingBox())!.height).toBe(50)
    const plus = (await header.getByRole('link', { name: 'Create skill' }).boundingBox())!
    expect([plus.width, plus.height]).toEqual([28, 28])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect((await page.locator('.agent-skills-row').first().boundingBox())!.height).toBe(68)
    const title = (await page.locator('.agent-skills-row strong').first().boundingBox())!
    const count = (await header.getByRole('heading').boundingBox())!
    expect(Math.abs(title.x - count.x)).toBeLessThan(0.5)

    await page.goto(`/${workspaceKey}/settings/skill/${first.id}`)
    await expect(page.getByRole('textbox', { name: 'Skill instructions' }).locator('h1')).toHaveText('Release notes')
    const crumb = (await page.locator('.settings-crumb').boundingBox())!
    expect([crumb.x, crumb.y, crumb.height]).toEqual([8, 53.5, 32])
    expect((await page.getByRole('button', { name: 'Save' }).boundingBox())!.height).toBe(36)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})
