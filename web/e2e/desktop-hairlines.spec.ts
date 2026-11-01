import { expect, type Locator } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, FlowDocument, Issue, Project } from '../src/types/flow'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

// A computed hairline may round to 1px at the project's emulated scale.
// Read the active authored rule instead of requiring a second browser/context.
async function expectDeclaredCss(locator: Locator, selector: string, property: string, value: RegExp) {
  const declarations = await locator.evaluate((element, { selector, property }) => {
    const values: string[] = []
    const visit = (rules: CSSRuleList) => {
      for (const rule of rules) {
        if (rule instanceof CSSMediaRule && !matchMedia(rule.conditionText).matches) continue
        if (rule instanceof CSSSupportsRule && !CSS.supports(rule.conditionText)) continue
        if (rule instanceof CSSStyleRule && rule.selectorText === selector && element.matches(selector)) {
          const declaration = rule.style.getPropertyValue(property)
          if (declaration) values.push(declaration)
        }
        if ('cssRules' in rule) visit((rule as CSSGroupingRule).cssRules)
      }
    }
    for (const sheet of document.styleSheets) visit(sheet.cssRules)
    return values
  }, { selector, property })
  expect(declarations, `${selector} declares ${property}`).not.toHaveLength(0)
  expect(declarations.at(-1)).toMatch(value)
}

test('keeps the empty agent composer and examples in their content-sized grid tracks', async ({ page, workspaceKey }) => {
  await page.route('**/api/agent/status', route => route.fulfill({ json: { enabled: true, model: 'test' } }))
  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.getByText('Get started with some examples')).toBeVisible()
  expect(await page.evaluate(() => [innerWidth, innerHeight])).toEqual([1440, 900])
  const composer = page.locator('[data-agent-composer]')
  await expect(page.locator('[class*="emptyColumn"]')).toHaveCSS('max-width', '712px')
  await expect(page.locator('[class*="emptyComposerSlot"]')).toHaveCSS('grid-row-start', '2')
  await expect(page.getByRole('region', { name: 'Get started with some examples' })).toHaveCSS('grid-row-start', '3')
  await expect(composer).toHaveCSS('min-height', '104px')
  await expect(composer).toHaveCSS('padding', '12px')
  const skills = composer.getByRole('button', { name: 'Skills', exact: true })
  await expect(skills).toHaveCSS('padding', '0px 6px 0px 4px')
  await expect(skills).toHaveCSS('line-height', 'normal')
  await expect(composer.locator('footer')).toHaveCSS('height', '24px')
  await expect(composer).toHaveCSS('row-gap', '8px')
  await expect(composer).toHaveCSS('border-top-width', '0px')
  const agent = page.locator('.bottom-agent > button').first()
  await expect(agent).toHaveCSS('gap', '6px')
  await expect(agent.locator('span')).toHaveCSS('font-weight', '450')
  await expect(agent).toHaveCSS('padding', '0px 12px 0px 10px')
})

test('sizes document sort targets by their labels and keeps the declared column and group spacing', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const created = await request.post('/api/documents', { headers, data: { title: '[audit] Hairline document', teamIds: [data.teams[0].id] } })
  expect(created.status()).toBe(201)
  const document = await created.json() as FlowDocument
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
  await expect(page.getByText(document.title, { exact: true })).toBeVisible()
  const targets = page.locator('.team-docs-heading')
  await expect(targets).toHaveCount(4)
  for (const target of await targets.all()) {
    await expect(target).toHaveCSS('padding', '0px 6px')
    await expect(target).toHaveCSS('gap', '2px')
    const widths = await target.evaluate(el => ({ button: el.getBoundingClientRect().width, label: el.querySelector('span')!.getBoundingClientRect().width }))
    expect(widths.button).toBeCloseTo(widths.label + 12 + 2 + 12, 1)
  }
  await expect(page.locator('.team-docs-group')).toHaveCSS('padding-left', '2px')
  await expect(page.locator('.team-docs-caret')).toHaveCSS('width', '20px')
  await expect(page.locator('.team-docs-caret')).toHaveCSS('padding', '2px')
  await expect(page.locator('.team-docs-row-inner')).toHaveCSS('padding', '0px 18px')
  await expect(page.locator('.team-docs-date.is-created')).toHaveCSS('flex-basis', '120px')
  const icon = page.getByRole('button', { name: 'Document icon', exact: true })
  await expect(icon).toHaveCSS('width', '24px')
  await expect(icon).toHaveCSS('height', '24px')
  await expect(icon).toHaveCSS('margin', '-4px')
  await icon.click()
  await page.getByRole('tabpanel', { name: 'Icons', exact: true }).getByRole('button', { name: 'BookOpen', exact: true }).click()
  await expect(icon.locator('use')).toHaveAttribute('href', '#BookOpen')
  await expect(page).toHaveURL(new RegExp(`/team/${data.teams[0].key}/documents$`))
  await page.getByRole('button', { name: 'Order by Created', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Order by Created', exact: true })).toHaveAttribute('data-active', 'true')
})

test('uses declared desktop settings padding without inflating the emoticon row', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/settings/account/preferences`)
  const rows = page.locator('.personal-section').first().locator('.settings-row')
  await expect(rows.first()).toBeVisible()
  await expect(rows).toHaveCount(5)
  for (const row of await rows.all()) {
    await expect(row).toHaveCSS('padding', '16px')
    await expect(row).toHaveCSS('gap', '12px')
  }
  await expect(page.locator('.personal-emoticon-emoji')).toHaveCSS('line-height', '14px')
  await expect(page.locator('.personal-emoticon-emoji')).toHaveCSS('vertical-align', 'bottom')
  await expect(page.locator('.settings-content')).toHaveCSS('padding-top', '64px')
  await page.goto(`/${workspaceKey}/settings/workspace`)
  await expect(page.locator('.settings-section:has(> h3) .settings-row').first()).toHaveCSS('padding-top', '16px')
  await page.goto(`/${workspaceKey}/settings/teams`)
  const teamHeaders = page.locator('.settings-members-columns button')
  await expect(teamHeaders).toHaveCount(6)
  for (const header of await teamHeaders.all()) await expect(header.locator('svg')).toBeVisible()
  await expect(teamHeaders.first()).toHaveCSS('margin-left', '-6.5px')
  await expectDeclaredCss(teamHeaders.first(), 'html:root .settings-app .settings-main .settings-teams-page .settings-members-columns button', 'border', /^0?\.5px solid transparent$/)
  await page.goto(`/${workspaceKey}/settings/integrations`)
  await expect(page.locator('.desktop-enabled')).toHaveCSS('margin-top', '48px')
  await expect(page.locator('.desktop-enabled h2')).toHaveCSS('font-size', '11px')
})

test('preserves desktop hairlines, project insets, comment padding and clipboard toast padding', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const created = await request.post('/api/projects', { headers, data: { name: '[audit] Hairline project', teamIds: [data.teams[0].id] } })
  expect(created.status()).toBe(201)
  const project = await created.json() as Project
  await page.goto(`/${workspaceKey}/projects/all`)
  await expect(page.locator('.lp-projects__header')).toHaveCSS('padding-right', '8px')
  await expect(page.locator('.lp-projects__toolbar')).toHaveCSS('padding-left', '8px')
  await expectDeclaredCss(page.locator('.lp-projects__header'), '.lp-projects__header', 'border-bottom', /^0?\.5px solid var\(--lp-border\)$/)
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  await expectDeclaredCss(page.locator('.lp-projects-display'), '.lp-projects-display', 'border', /^0?\.5px solid var\(--theme-border-strong\)$/)
  await expect(page.locator('.lp-projects-display')).toHaveCSS('padding-top', '16px')
  await expect(page.locator('.lp-projects-display__modes')).toHaveCSS('margin', '0px 10px 16px')
  const display = page.getByRole('dialog', { name: 'Display options', exact: true })
  for (const [label, option] of [['Grouping', 'No grouping'], ['Ordering', 'Manual']]) {
    await display.locator('.lp-projects-display-select').filter({ has: page.locator('> span', { hasText: new RegExp(`^${label}$`) }) }).getByRole('combobox').click()
    await page.getByRole('option', { name: option, exact: true }).click()
  }
  await expect(page.locator('.lp-projects-display__options h2')).toHaveCSS('margin', '8px 0px 2px')
  await expect(page.locator('.lp-projects-display__properties h3')).toHaveCSS('margin', '8px 0px 4px')
  await expect(page.locator('.lp-projects-display__properties > div')).toHaveCSS('margin-top', '8px')
  await page.keyboard.press('Escape')
  await page.goto(`/${workspaceKey}/project/${project.slugId}/activity`)
  await expect(page.locator('.project-activity')).toHaveCSS('padding-top', '44px')
  await expect(page.locator('.project-activity__composer > footer')).toHaveCSS('padding-bottom', '12px')
  const response = await request.post('/api/issues', { headers, data: { title: '[audit] Hairline issue', teamId: data.teams[0].id } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as Issue
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/all`)
  await expect(page.locator('[data-issue-explorer]')).toHaveAttribute('data-inset', 'tall')
  await expect(page.locator('[data-issue-explorer]')).toHaveCSS('height', '856px')
  await page.locator(`[data-issue-row-id="${issue.id}"]`).hover()
  await page.getByRole('checkbox', { name: 'Select issue', exact: true }).check()
  await expect(page.locator('[data-my-issues-bulk-bar]')).toHaveCSS('gap', '8px')
  expect((await request.post(`/api/issues/${issue.id}/comments`, { headers, data: { body: '[audit] Comment' } })).ok()).toBeTruthy()
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await expect(page.locator('.comment-thread .comment-card')).toHaveCSS('padding', '16px')
  await page.getByRole('button', { name: 'Copy issue URL', exact: true }).click()
  await expect(page.locator('.flow-toast')).toHaveCSS('padding', '12px')
  await expectDeclaredCss(page.locator('.flow-toast'), '.flow-toast', 'border', /^0?\.5px solid transparent$/)
})
