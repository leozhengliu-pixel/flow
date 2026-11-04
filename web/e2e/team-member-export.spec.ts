import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

test.use({viewport:{width:390,height:844},isMobile:true,hasTouch:true})
test('keeps team CSV export reachable on phones',async({page,request,workspaceKey})=>{
  const data=await(await request.get('/api/bootstrap',{headers:{'X-Workspace-Key':workspaceKey}})).json() as BootstrapData
  await page.goto(`/${workspaceKey}/settings/teams/${data.teams[0].key}/members`)
  const button=page.getByRole('button',{name:'Export CSV',exact:true})
  await expect(button).toBeVisible()
  const download=page.waitForEvent('download')
  await button.tap()
  expect((await download).suggestedFilename()).toMatch(/\.csv$/)
})
