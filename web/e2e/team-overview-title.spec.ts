import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({isMobile:true,hasTouch:true})
for(const width of [375,390])test(`keeps long team titles above the overview shortcut at ${width}px`,async({page,request,workspaceKey})=>{
  const response=await request.post(`/api/workspaces/${workspaceKey}/teams`,{headers:{'X-Workspace-Key':workspaceKey},data:{name:'Quality assurance and release management',key:'QA'}})
  expect(response.status()).toBe(201)
  await page.setViewportSize({width,height:844})
  await page.goto(`/${workspaceKey}/settings/teams/QA`)
  const title=page.locator('.team-settings-header h1')
  const link=page.getByRole('button',{name:'Team overview',exact:true})
  await expect(title).toContainText('Quality assurance')
  const heading=(await title.boundingBox())!,shortcut=(await link.boundingBox())!
  expect(shortcut.y).toBeGreaterThanOrEqual(heading.y+heading.height)
  await link.tap()
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/team/QA`))
})
