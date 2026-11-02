import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({viewport:{width:390,height:844},isMobile:true,hasTouch:true})
test('persists Tuesday and starts the calendar on the saved weekday',async({page,request,workspaceKey})=>{
  const headers={'X-Workspace-Key':workspaceKey}
  const original=await(await request.get('/api/account/settings',{headers})).json() as {firstDay:string;language:string}
  try {
    expect((await request.patch('/api/account/settings',{headers,data:{firstDay:'Sunday',language:'en-US'}})).ok()).toBe(true)
    await page.goto(`/${workspaceKey}/settings/account/preferences`)
    const select=page.getByRole('combobox',{name:'First day of the week',exact:true})
    const saved=page.waitForResponse(r=>r.request().method()==='PATCH'&&r.url().includes('/api/account/settings')&&r.request().postDataJSON()?.firstDay==='Tuesday')
    await select.selectOption('Tuesday')
    expect((await(await saved).json()).firstDay).toBe('Tuesday')
    await expect(select).toHaveValue('Tuesday')
    await page.reload()
    await expect(select).toHaveValue('Tuesday')
    if(process.env.FLOW_UI_SHOTS)await page.locator('.settings-row').filter({has:page.getByText('First day of the week',{exact:true})}).screenshot({path:`${process.env.FLOW_UI_SHOTS}/D3-persisted-real.png`})
    await page.goto(`/${workspaceKey}/search`)
    await page.getByRole('button',{name:'Add filter',exact:true}).click()
    await page.getByRole('option',{name:'Updated date',exact:true}).click()
    await expect(page.locator('.search-date-filter [class*="weekdays"] > span').first()).toHaveText('Tu')
  } finally {
    expect((await request.patch('/api/account/settings',{headers,data:{firstDay:original.firstDay,language:original.language}})).ok()).toBe(true)
  }
})
