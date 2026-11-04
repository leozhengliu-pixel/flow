import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

test.use({isMobile:true,hasTouch:true})
for(const width of [375,390])test(`wraps team label headings beside their actions at ${width}px`,async({page,request,workspaceKey})=>{
  const data=await(await request.get('/api/bootstrap',{headers:{'X-Workspace-Key':workspaceKey}})).json() as BootstrapData
  await page.setViewportSize({width,height:844})
  await page.goto(`/${workspaceKey}/settings/teams/${data.teams[0].key}/issue-labels`)
  const title=page.getByRole('heading',{name:'Team issue labels',exact:true})
  await expect(title).toHaveCSS('white-space','normal')
  expect(await title.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true)
  const heading=(await title.boundingBox())!,button=(await page.getByRole('button',{name:'New group',exact:true}).boundingBox())!
  const header=(await page.locator('.domain-labels-header').boundingBox())!
  expect(heading.y).toBeGreaterThanOrEqual(header.y)
  expect(heading.y+heading.height).toBeLessThanOrEqual(header.y+header.height)
  expect(heading.x+heading.width).toBeLessThanOrEqual(button.x)
})
