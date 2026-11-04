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

test('keeps issue properties readable, toggles labels, and searches short and long project lists', async ({ page, request, detail }) => {
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
  await expect(rail).toBeInViewport()
  const titleBox = (await title.boundingBox())!
  const railBox = (await rail.boundingBox())!
  expect(titleBox.x + titleBox.width).toBeLessThanOrEqual(railBox.x)
  await expect(rail.getByRole('combobox', { name: 'Change team', exact: true })).toContainText(issue.team.name)
  const controls = rail.locator('.core-property-trigger')
  const rows = await controls.evaluateAll(els => els.slice(0, 4).map(el => {
    const { top, bottom } = el.getBoundingClientRect()
    return { top, bottom }
  }))
  for (let index = 1; index < rows.length; index++) expect(rows[index].top).toBeGreaterThanOrEqual(rows[index - 1].bottom)
  const estimate = rail.locator('.estimate-picker .label-project-trigger')
  await expect(estimate).toBeInViewport()
  expect((await estimate.boundingBox())!.y).toBeGreaterThanOrEqual(rows[3].bottom)
  await expect(rail.locator(':scope > h3')).toBeVisible()
  for (const name of ['Change status', 'Change priority', 'Change assignee', 'Set estimate']) {
    await rail.getByRole('combobox', { name: new RegExp(`^${name}`) }).click()
    const picker = page.getByRole('dialog')
    const input = picker.getByRole('textbox')
    await expect(input).toBeInViewport()
    await expect(input).toBeFocused()
    const inputBox = (await input.boundingBox())!
    const pickerBox = (await picker.boundingBox())!
    expect(inputBox.x).toBeGreaterThanOrEqual(pickerBox.x)
    expect(inputBox.x + inputBox.width).toBeLessThanOrEqual(pickerBox.x + pickerBox.width)
    for (const keycap of await picker.locator('.property-command-search kbd').all()) {
      await expect(keycap).toBeInViewport()
      expect((await keycap.boundingBox())!.x).toBeGreaterThanOrEqual(inputBox.x + inputBox.width)
    }
    const selected = picker.locator('[role=option][aria-checked=true]')
    await expect(selected).toHaveAttribute('aria-selected', 'true')
    const selectedName = (await selected.locator('.property-command-label').innerText()).trim()
    await input.fill(selectedName)
    await expect(selected).toBeVisible()
    await expect(picker.getByRole('option')).toHaveCount(1)
    await page.keyboard.press('Escape')
  }
  await rail.getByRole('button', { name: 'Add label', exact: true }).click()
  const labels = page.getByRole('dialog', { name: 'Change Labels' })
  await expect(labels).toBeVisible()
  await labels.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  await expect(rail.getByRole('button', { name: 'Add label', exact: true })).toHaveCSS('color', 'lch(61.803 1.2 272)')
  for (const name of await labels.locator('.property-command-label').all()) await expect(name).toHaveCSS('color', 'lch(100 0 272)')
  const labelBox = (await labels.boundingBox())!
  expect(labelBox.x + labelBox.width).toBeLessThan((await rail.boundingBox())!.x + 5)
  const uncheckedLabel = labels.getByRole('option', { name: '[audit] Label', exact: true })
  await expect(uncheckedLabel.locator('.property-command-checkbox')).toBeHidden()
  await expect(uncheckedLabel.locator('.property-command-checkbox')).toHaveCSS('opacity', '0')
  const uncheckedGeometry = await uncheckedLabel.evaluate(option => {
    const label = option.querySelector<HTMLElement>('.property-command-label')!
    const optionBox = option.getBoundingClientRect()
    const labelBox = label.getBoundingClientRect()
    return { labelX: labelBox.x - optionBox.x, labelWidth: labelBox.width }
  })
  expect(uncheckedGeometry.labelX).toBe(60)
  expect(uncheckedGeometry.labelWidth).toBeGreaterThan(100)
  if (process.env.FLOW_CI_SHOTS) await labels.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/issue-labels-desktop-unchecked.png`, animations: 'disabled' })
  await uncheckedLabel.hover()
  await expect(uncheckedLabel.locator('.property-command-checkbox')).toBeVisible()
  await expect(uncheckedLabel.locator('.property-command-checkbox')).toHaveCSS('opacity', '1')
  if (process.env.FLOW_CI_SHOTS) await uncheckedLabel.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/issue-labels-desktop-hover.png`, animations: 'disabled' })
  await uncheckedLabel.click()
  const selectedLabel = labels.getByRole('option', { name: '[audit] Label', exact: true })
  await expect(selectedLabel).toHaveAttribute('aria-checked','true')
  await expect(selectedLabel.locator('.property-command-checkbox')).toBeVisible()
  await expect(selectedLabel.locator('.property-command-checkbox')).toHaveCSS('opacity', '1')
  await expect(selectedLabel.locator('.property-command-checkbox')).toHaveCSS('border-top-width','0px')
  if (process.env.FLOW_CI_SHOTS) await labels.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/issue-labels-desktop-checked.png`, animations: 'disabled' })
  await page.keyboard.press('Escape')
  await expect(rail.locator('.issue-label-chip')).toContainText('[audit] Label')
  await rail.locator('.issue-project-trigger').click()
  const projects = page.getByRole('dialog', { name: 'Change Project' })
  await expect(projects).toBeVisible()
  await expect(rail.locator('.issue-project-trigger')).toHaveCSS('color', 'lch(61.803 1.2 272)')
  await expect(projects.getByRole('option', { name: '[audit] Short project', exact: true })).toBeInViewport()
  await page.keyboard.type('Short')
  await expect(projects.getByRole('textbox')).toBeInViewport()
  await expect(projects.getByRole('textbox')).toHaveValue('Short')
  await projects.getByRole('option', { name: '[audit] Short project', exact: true }).click()
  await expect(rail.locator('.issue-project-trigger')).toContainText('[audit] Short project')
  for (let index=0;index<9;index++) {
    const response = await request.post('/api/projects', { headers, data: { name:`[audit] Extra ${index}`,teamIds:[issue.team.id] } })
    expect(response.status()).toBe(201)
  }
  await page.reload()
  await rail.locator('.issue-project-trigger').click()
  await expect(projects).toBeInViewport({ ratio: 1 })
  await expect(projects.getByRole('textbox')).toBeInViewport()
  const createProject = projects.getByRole('option', { name: 'Create new project…', exact: true })
  await createProject.scrollIntoViewIfNeeded()
  await expect(createProject).toBeInViewport()
  await expect(projects.getByRole('textbox')).toBeInViewport()
  await projects.getByRole('textbox').fill('Extra 8')
  await expect(projects.getByRole('option', { name: '[audit] Extra 8', exact: true })).toBeVisible()
  await expect(projects.getByRole('option', { name: '[audit] Extra 7', exact: true })).toHaveCount(0)
  await projects.getByRole('option', { name: '[audit] Extra 8', exact: true }).click()
  await expect(rail.locator('.issue-project-trigger')).toContainText('[audit] Extra 8')
  await page.reload()
  await expect(rail.locator('.issue-project-trigger')).toContainText('[audit] Extra 8')
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
