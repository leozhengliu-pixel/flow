import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData,Issue } from '../src/types/flow'

test.use({viewport:{width:375,height:667},isMobile:true,hasTouch:true})
test('opens releases for an unassigned phone issue without adding a property row',async({page,request,workspaceKey})=>{
  const headers={'X-Workspace-Key':workspaceKey}
  const data=await(await request.get('/api/bootstrap',{headers})).json() as BootstrapData
  const issue=await(await request.post('/api/issues',{headers,data:{teamId:data.teams[0].id,title:'Empty release entry'}})).json() as Issue
  const pipelineResponse=await request.post('/api/release-pipelines',{headers,data:{name:'Phone pipeline',type:'scheduled',teamIds:[data.teams[0].id],stages:['Planned'],stageStatuses:{Planned:'planned'}}})
  expect(pipelineResponse.status()).toBe(201)
  const pipeline=await pipelineResponse.json() as {id:string}
  const releaseResponse=await request.post('/api/releases',{headers,data:{name:'Phone release',version:'1.0',status:'planned',pipelineId:pipeline.id,stage:'Planned'}})
  expect(releaseResponse.status()).toBe(201)
  const release=await releaseResponse.json() as {id:string}
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await expect(page.locator('.issue-mobile-properties .issue-release-property')).toHaveCount(0)
  await page.getByRole('button',{name:'Issue options',exact:true}).click()
  await page.getByRole('option',{name:/^Release/}).click()
  const input=page.getByRole('searchbox',{name:'Add to release…'})
  await expect(input).toBeVisible()
  const picker=page.locator('.issue-release-picker--root')
  const bounds=(await picker.boundingBox())!
  expect(bounds.x).toBeGreaterThanOrEqual(0)
  expect(bounds.x+bounds.width).toBeLessThanOrEqual(375)
  await input.tap()
  await input.fill('Release query')
  await picker.getByRole('button',{name:'Back',exact:true}).tap()
  await expect(page.getByRole('dialog',{name:'Issue options',exact:true})).toBeVisible()

  await page.reload()
  await page.getByRole('button',{name:'Issue options',exact:true}).tap()
  await page.getByRole('option',{name:/^Release/}).tap()
  const selected=page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().includes(`/api/issues/${issue.id}/releases`))
  await page.locator('.issue-release-picker--root .issue-release-option').filter({hasText:'Phone release'}).tap()
  expect((await selected).ok()).toBe(true)
  const savedData=await(await request.get('/api/bootstrap',{headers})).json() as BootstrapData
  expect(savedData.releases.find(item=>item.id===release.id)?.issueIds).toContain(issue.id)
  await page.getByRole('option',{name:'All pipelines…',exact:true}).tap()
  await page.locator('.issue-release-pipelines-menu').getByRole('option',{name:'Phone pipeline',exact:true}).tap()
  await expect(page.locator('.issue-release-pipeline-releases')).toBeVisible()
})
