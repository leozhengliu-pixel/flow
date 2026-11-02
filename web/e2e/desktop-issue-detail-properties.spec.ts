import { expect } from '@playwright/test'
import { test as base } from './workspace-fixture'
import type { BootstrapData, Issue, Team } from '../src/types/flow'

const test = base.extend<{ detail: { key: string; data: BootstrapData; issue: Issue; other: Team } }>({
  detail: async ({ request, workspaceKey: key }, provide) => {
    const headers = { 'X-Workspace-Key': key }
    const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
    const otherResponse = await request.post(`/api/workspaces/${key}/teams`, { headers, data: { name: '[audit] Other team', key: 'OTR' } })
    expect(otherResponse.status()).toBe(201)
    const other = await otherResponse.json() as Team
    const issueResponse = await request.post('/api/issues', { headers, data: { title: '[audit] Desktop properties', teamId: data.teams[0].id, stateId: data.states.find(state=>state.type==='started')!.id } })
    expect(issueResponse.status()).toBe(201)
    const issue = await issueResponse.json() as Issue
    await provide({ key, data, issue, other })
  },
})

test.skip(({ isMobile }) => isMobile, 'Desktop issue property geometry')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2'] } })

test('aligns the issue columns and property rhythm, toggles labels, and keeps short and long project lists searchable', async ({ page, request, detail }) => {
  const { key, data, issue } = detail
  const headers = { 'X-Workspace-Key': key }
  const settings = await request.patch(`/api/teams/${issue.team.id}/settings`, { headers, data: { estimateType: 'fibonacci' } })
  expect(settings.ok()).toBeTruthy()
  const labelResponse = await request.post('/api/labels', { headers, data: { name: '[audit] Label', color: '#4cb782' } })
  expect(labelResponse.status()).toBe(201)
  const projectResponse = await request.post('/api/projects', { headers, data: { name: '[audit] Short project', teamIds: [issue.team.id] } })
  expect(projectResponse.status()).toBe(201)
  await page.goto(`/${key}/issue/${issue.identifier}`)
  const title = page.getByRole('textbox', { name: 'Issue title', exact: true })
  await expect(title).toBeVisible()
  const rail = page.locator('.issue-properties')
  expect(Math.abs((await title.boundingBox())!.width - 653.5)).toBeLessThanOrEqual(1)
  expect(Math.abs((await rail.boundingBox())!.x - 1008)).toBeLessThanOrEqual(1)
  await expect(rail.getByRole('combobox', { name: 'Change team', exact: true })).toContainText(issue.team.name)
  const controls = rail.locator('.core-property-trigger')
  const statusText = controls.first().locator(':scope > span').first()
  expect((await statusText.boundingBox())!.x - (await rail.boundingBox())!.x).toBe(33)
  const ys = await controls.evaluateAll(els=>els.slice(0,4).map(el=>el.getBoundingClientRect().y))
  expect(ys.slice(1).map((y,index)=>y-ys[index])).toEqual([32,32,32])
  const estimate = (await rail.locator('.estimate-picker .label-project-trigger').boundingBox())!
  expect(estimate.y - ys[3]).toBe(32)
  const heading = await rail.locator(':scope > h3').evaluate(el => ({ height:el.getBoundingClientRect().height,color:getComputedStyle(el).color }))
  expect(heading).toEqual({height:15.5,color:'lch(61.803 1.2 272)'})
  for (const name of ['Change status', 'Change priority', 'Change assignee', 'Set estimate']) {
    await rail.getByRole('combobox', { name: new RegExp(`^${name}`) }).click()
    const search = page.locator('.property-command-surface .property-command-search:visible')
    await expect(search).toHaveCSS('gap', '14px')
    await expect(search.getByRole('textbox')).toHaveCSS('width', '148px')
    await expect(page.locator('.property-command-surface [role=option][aria-checked=true]')).toHaveAttribute('aria-selected', 'true')
    for (const keycap of await search.locator('kbd').all()) await expect(keycap).toHaveCSS('padding', '2px')
    await page.keyboard.press('Escape')
  }
  await rail.getByRole('button', { name: 'Add label', exact: true }).click()
  const labels = page.getByRole('dialog', { name: 'Change Labels' })
  await expect(labels).toBeVisible()
  await expect(rail.getByRole('button', { name: 'Add label', exact: true })).toHaveCSS('color', 'lch(61.803 1.2 272)')
  for (const name of await labels.locator('.property-command-label').all()) await expect(name).toHaveCSS('color', 'lch(100 0 272)')
  const labelBox = (await labels.boundingBox())!
  expect(labelBox.x + labelBox.width).toBeLessThan((await rail.boundingBox())!.x + 5)
  await expect(labels.locator('.property-command-checkbox').first()).toBeHidden()
  await labels.getByRole('option', { name: '[audit] Label', exact: true }).click()
  const selectedLabel = labels.getByRole('option', { name: '[audit] Label', exact: true })
  await expect(selectedLabel).toHaveAttribute('aria-checked','true')
  await expect(selectedLabel.locator('.property-command-checkbox')).toHaveCSS('border-top-width','0px')
  await page.keyboard.press('Escape')
  await expect(rail.locator('.issue-label-chip')).toContainText('[audit] Label')
  await rail.locator('.issue-project-trigger').click()
  const projects = page.getByRole('dialog', { name: 'Change Project' })
  await expect(projects).toBeVisible()
  await expect(rail.locator('.issue-project-trigger')).toHaveCSS('color', 'lch(61.803 1.2 272)')
  await expect.poll(async()=>Math.round((await projects.boundingBox())!.width)).toBe(186)
  await page.keyboard.type('Short')
  await expect(projects.locator('.property-command-search')).not.toHaveClass(/is-visually-hidden/)
  await projects.getByRole('option', { name: '[audit] Short project', exact: true }).click()
  await expect(rail.locator('.issue-project-trigger')).toContainText('[audit] Short project')
  for (let index=0;index<9;index++) {
    const response = await request.post('/api/projects', { headers, data: { name:`[audit] Extra ${index}`,teamIds:[issue.team.id] } })
    expect(response.status()).toBe(201)
  }
  await page.reload()
  await rail.locator('.issue-project-trigger').click()
  await expect(projects.locator('.property-command-search')).not.toHaveClass(/is-visually-hidden/)
  await expect.poll(async()=>Math.round((await projects.boundingBox())!.width)).toBe(339)
  await projects.getByRole('textbox').fill('Extra 8')
  await expect(projects.getByRole('option', { name: '[audit] Extra 8', exact: true })).toBeVisible()
  expect(data.viewer.id).toBeTruthy()
})

test('anchors actions to the trigger, loads relation removal on direct navigation, and moves the issue through the API', async ({ page, request, detail }) => {
  const { key, issue, other } = detail
  const headers = { 'X-Workspace-Key': key }
  const related = await request.post('/api/issues', { headers, data: { title: '[audit] Related target',teamId:issue.team.id } })
  const target = await related.json() as Issue
  const relation = await request.post(`/api/issues/${issue.id}/relations`, { headers, data: { type:'related', relatedIssueId:target.id } })
  expect(relation.status()).toBe(201)
  await page.goto(`/${key}/issue/${issue.identifier}`)
  const trigger = page.getByRole('button', { name:'Issue options',exact:true })
  await trigger.click()
  const menu = page.locator('.issue-options-popover')
  await expect(menu).toBeVisible()
  await expect.poll(async()=>Math.abs((await menu.boundingBox())!.x-(await trigger.boundingBox())!.x)).toBeLessThanOrEqual(1)
  await menu.getByRole('option',{name:'Remove',exact:true}).hover()
  const remove = page.getByRole('dialog',{name:'Remove',exact:true})
  await remove.getByRole('option',{name:new RegExp(target.identifier)}).click()
  await expect(page.locator('[data-relation-section="related"]')).toHaveCount(0)
  await trigger.click()
  await expect(menu.getByRole('option',{name:'Remove',exact:true})).toHaveCount(0)
  await menu.getByRole('option',{name:/^Team/}).hover()
  const teams = page.getByRole('dialog',{name:'Team',exact:true})
  await expect(teams).toHaveCSS('width', '242px')
  await expect(teams.locator('.issue-options-icon').first()).toHaveCSS('width', '18px')
  await expect(teams.getByRole('option', { name: other.name, exact: true }).locator('.issue-options-team-key')).toHaveText(other.key)
  await teams.getByRole('option',{name:other.name,exact:true}).click()
  await expect(page.locator('.issue-properties .issue-team-property')).toContainText(other.name)
  await expect(page.locator('.issue-breadcrumb-current')).toContainText('OTR-')
  const record = await request.get(`/api/issue-records/${issue.id}`, { headers })
  const moved = await record.json() as Issue
  expect(moved.team.id).toBe(other.id)
  expect(moved.state.type).toBe(issue.state.type)
  expect(moved.identifier).not.toBe(issue.identifier)
})

test('keeps row actions ordered, subscription visible, and the coding submenu aligned', async ({ page, request, detail }) => {
  const { key, issue } = detail
  const headers = { 'X-Workspace-Key': key }
  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { subscriberIds: [] } })).ok()).toBeTruthy()
  const target = await (await request.post('/api/issues', { headers, data: { title: '[audit] Row relation', teamId: issue.team.id } })).json() as Issue
  expect((await request.post(`/api/issues/${issue.id}/relations`, { headers, data: { type: 'related', relatedIssueId: target.id } })).status()).toBe(201)
  await page.goto(`/${key}/team/${issue.team.key}/active`)
  await page.locator(`[data-issue-row-id="${issue.id}"]`).click({ button: 'right' })
  const menu = page.getByRole('menu').first()
  await expect(menu).toBeVisible()
  await expect(menu).toHaveCSS('padding', '0px')
  await expect(menu.getByText('Subscribe', { exact: true })).toBeVisible()
  const labels = await menu.getByRole('menuitem').evaluateAll(nodes => nodes.map(node => node.querySelector('span:not([aria-hidden])')?.textContent))
  expect(labels.slice(labels.indexOf('Copy'))).toEqual(['Copy', 'Convert to', 'Make a copy…', 'Move', 'Open in', 'Favorite', 'Subscribe', 'Remind me', 'Remove…', 'Delete'])
  for (const shortcut of await menu.locator('kbd').all()) {
    await expect(shortcut).toHaveCSS('padding-left', '0px')
    await expect(shortcut).toHaveCSS('line-height', '12.1px')
  }
  const open = menu.getByRole('menuitem', { name: 'Open in', exact: true })
  await open.hover()
  await expect(open).toHaveCSS('color', 'lch(100 0 272)')
  const configure = page.getByRole('menuitem', { name: 'Configure coding tools…', exact: true })
  await expect(configure.locator('svg')).toBeVisible()
  const submenu = page.getByRole('menu').filter({ has: configure })
  await expect(submenu).toHaveCSS('padding', '0px')
  const rootBox = (await menu.boundingBox())!
  const subBox = (await submenu.boundingBox())!
  expect(Math.abs(subBox.x - (rootBox.x + rootBox.width - 2))).toBeLessThanOrEqual(1)
  expect(await configure.evaluate(node => node.scrollWidth <= node.clientWidth)).toBeTruthy()
})
