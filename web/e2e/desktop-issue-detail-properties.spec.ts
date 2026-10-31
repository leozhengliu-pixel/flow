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
test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: 'dark' })

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
  const ys = await controls.evaluateAll(els=>els.slice(0,4).map(el=>el.getBoundingClientRect().y))
  expect(ys.slice(1).map((y,index)=>y-ys[index])).toEqual([32,32,32])
  const estimate = (await rail.locator('.estimate-picker .label-project-trigger').boundingBox())!
  expect(estimate.y - ys[3]).toBe(32)
  const heading = await rail.locator(':scope > h3').evaluate(el => ({ height:el.getBoundingClientRect().height,color:getComputedStyle(el).color }))
  expect(heading).toEqual({height:15.5,color:'lch(61.803 1.2 272)'})
  await rail.getByRole('button', { name: 'Add label', exact: true }).click()
  const labels = page.getByRole('dialog', { name: 'Change Labels' })
  await expect(labels).toBeVisible()
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
  await page.getByRole('dialog',{name:'Team',exact:true}).getByRole('option',{name:other.name,exact:true}).click()
  await expect(page.locator('.issue-properties .issue-team-property')).toContainText(other.name)
  await expect(page.locator('.issue-breadcrumb-current')).toContainText('OTR-')
  const record = await request.get(`/api/issue-records/${issue.id}`, { headers })
  const moved = await record.json() as Issue
  expect(moved.team.id).toBe(other.id)
  expect(moved.state.type).toBe(issue.state.type)
  expect(moved.identifier).not.toBe(issue.identifier)
})
