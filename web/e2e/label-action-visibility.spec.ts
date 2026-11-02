import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({viewport:{width:390,height:844},isMobile:true,hasTouch:true})
test('reveals phone label actions on hover, keyboard focus and open',async({page,workspaceKey})=>{
  await page.goto(`/${workspaceKey}/settings/labels`)
  const action=page.locator('.domain-label-menu-button').first()
  await action.hover()
  await expect(action).toHaveCSS('opacity','1')
  await page.mouse.move(0,0)
  await page.keyboard.press('Tab')
  await action.focus()
  await expect(action).toHaveCSS('opacity','1')
  await action.tap()
  await expect(action).toHaveAttribute('data-state','open')
  await expect(action).toHaveCSS('opacity','1')
  await expect(page.getByRole('dialog')).toBeVisible()
})
