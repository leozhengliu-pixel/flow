import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { Project } from '../src/types/flow'

test.use({isMobile:true,hasTouch:true,viewport:{width:390,height:844}})
test('taps grouped project labels and Back above the property sheet overlay',async({page,request,workspaceKey})=>{
  const headers={'X-Workspace-Key':workspaceKey}
  const projectResponse=await request.post('/api/projects',{headers,data:{name:'Project property group'}})
  expect(projectResponse.status()).toBe(201)
  const project=await projectResponse.json() as Project
  const groupResponse=await request.post('/api/label-groups',{headers,data:{name:'Delivery',resourceType:'project'}})
  const group=await groupResponse.json() as {id:string}
  const labelResponse=await request.post('/api/labels',{headers,data:{name:'Ready',resourceType:'project',groupId:group.id,color:'#5e6ad2'}})
  const label=await labelResponse.json() as {id:string}
  await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
  await page.getByRole('button',{name:'Add label',exact:true}).tap()
  const parent=page.locator('.phone-menu-surface')
  await parent.getByRole('option',{name:'Delivery',exact:true}).tap()
  const child=page.locator('.project-label-submenu')
  await child.getByRole('button',{name:'Back',exact:true}).tap()
  await expect(parent).toBeVisible()
  await expect(parent.locator('input').first()).toBeFocused()
  await parent.getByRole('option',{name:'Delivery',exact:true}).tap()
  const mutation=page.waitForResponse(r=>r.request().method()==='PATCH'&&r.url().includes(`/api/projects/${project.id}`))
  await child.getByRole('option',{name:'Ready',exact:true}).tap()
  expect((await mutation).ok()).toBe(true)
  const saved=await(await request.get(`/api/projects/${project.id}`,{headers})).json() as Project
  expect(saved.labelIds).toContain(label.id)
})
