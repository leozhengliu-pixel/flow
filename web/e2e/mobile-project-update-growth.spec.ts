import { expect } from '@playwright/test'
import { test, bootstrap, preferences } from './review-findings-fixture'

for (const width of [375, 390]) for (const theme of ['Light', 'Dark'] as const) {
  test(`grows update drafts above the property preview and footer ${width} ${theme}`, async ({ page, request, workspaceKey }) => {
    await preferences(request, workspaceKey, theme)
    const data = await bootstrap(request, workspaceKey)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('/api/projects', { headers, data: { name: 'Update growth', teamIds: [data.teams[0].id] } })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }
    expect((await request.patch(`/api/projects/${project.id}`, { headers, data: { priority: 2, startDate: '2026-09-28' } })).ok()).toBe(true)
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await page.goto(`/${workspaceKey}/project/${project.slugId}/activity`)
    await expect(page.locator('.project-activity__composer')).toBeVisible()
    const close = page.getByRole('button', { name: 'Close project details', exact: true })
    if (await close.isVisible()) await close.click()
    await page.getByRole('tab', { name: 'Update', exact: true }).click()
    const editor = page.getByRole('textbox', { name: 'Project update', exact: true })
    await editor.fill(Array.from({ length: 6 }, (_, index) => `Paragraph ${index + 1}: progress, dependencies and decisions for the international delivery schedule.`).join('\n\n'))
    const composer = page.locator('.project-activity__composer')
    await expect(composer.locator('.project-activity__diff')).toBeVisible()
    const geometry = await composer.evaluate(element => {
      const editor = element.querySelector('.description-editor')!.getBoundingClientRect()
      const diff = element.querySelector('.project-activity__diff')!.getBoundingClientRect()
      const values = [...element.querySelectorAll('.project-activity__diff-label,.project-activity__diff-value')].map(node => node.getBoundingClientRect().bottom)
      const controls = [...element.querySelectorAll('footer button')].map(node => node.getBoundingClientRect().top)
      return { editorBottom: editor.bottom, diffTop: diff.top, textBottom: Math.max(...values), controlsTop: Math.min(...controls), height: element.getBoundingClientRect().height }
    })
    expect(geometry.editorBottom).toBeLessThanOrEqual(geometry.diffTop + 1)
    expect(geometry.textBottom).toBeLessThanOrEqual(geometry.controlsTop + 1)
    expect(geometry.height).toBeGreaterThan(257)
    await composer.locator('footer').scrollIntoViewIfNeeded()
    if (process.env.FLOW_REVIEW_SHOTS) await page.screenshot({ path: `${process.env.FLOW_REVIEW_SHOTS}/flow-project-update-long-${width}-${theme}.png`, animations: 'disabled' })
  })
}
