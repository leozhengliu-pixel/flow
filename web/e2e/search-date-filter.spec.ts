import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

const base = process.env.FLOW_MOBILE_BASE_URL ?? 'http://127.0.0.1:4173'
const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'

async function seedWorkspace(request: APIRequestContext, workspaceKey: string) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.get(`${base}/api/bootstrap`, { headers })
  expect(response.ok()).toBe(true)
  const workspace = await response.json() as { teams: Array<{ id: string }> }
  const issue = await request.post(`${base}/api/issues`, { headers, data: { title: '[e2e] Dated result', teamId: workspace.teams[0].id } })
  expect(issue.status()).toBe(201)
  const record = await issue.json() as { id: string }
  const recent = await request.post(`${base}/api/recent`, { headers: { 'X-Workspace-Key': workspaceKey }, data: { type: 'issue', id: record.id } })
  expect(recent.ok()).toBe(true)
}

async function applyDateFilter(page: Page, workspaceKey: string, phone = false) {
  const searches: string[] = []
  page.on('request', request => { if (request.url().includes('/api/search?')) searches.push(request.url()) })
  await page.goto(`${base}/${workspaceKey}/search`)
  await expect(page.locator('a[data-result-index]').first()).toBeVisible()
  await page.getByRole('button', { name: 'Add filter', exact: true }).click()
  await page.getByRole(phone ? 'option' : 'menuitem', { name: 'Updated date', exact: true }).click()
  await page.getByRole(phone ? 'option' : 'menuitem', { name: 'Custom date or timeframe…', exact: true }).click()
  await expect(page.locator('.search-date-filter')).toBeVisible()
  await expect(page.getByRole('dialog',{name:'Updated date',exact:true})).toBeVisible()
  await page.keyboard.press('Tab')
  await expect.poll(()=>page.locator('.search-date-filter').evaluate(e=>e.contains(document.activeElement))).toBe(true)
  if(phone){
    await page.getByRole('button',{name:'Apply',exact:true}).focus()
    await page.keyboard.press('Tab')
    await expect.poll(()=>page.locator('.search-date-filter').evaluate(e=>e.contains(document.activeElement))).toBe(true)
  }
  await page.getByRole('button',{name:'Cancel',exact:true}).click()
  await expect(page.locator('.search-date-filter')).toHaveCount(0)
  await expect(page.locator('.workspace-search-filter-chips')).toHaveCount(0)
  await page.getByRole('button',{name:'Add filter',exact:true}).click()
  await page.getByRole(phone?'option':'menuitem',{name:'Updated date',exact:true}).click()
  await page.getByRole(phone?'option':'menuitem',{name:'Custom date or timeframe…',exact:true}).click()
  // Hit-test before clicking: locator clicks can silently scroll a clipped calendar into view.
  await expect.poll(() => page.locator('.search-date-filter [role=gridcell]').evaluateAll(buttons => buttons.every(button => {
    const rect = button.getBoundingClientRect()
    return button.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2))
  }))).toBe(true)
  await expect(page.locator('.search-date-filter [data-outside=true]')).toHaveCount(0)
  await expect(page.locator('.search-date-filter [role=grid]')).toHaveCount(phone?1:2)

  const now = new Date()
  const nextMonth = new Date(now.getFullYear(), now.getMonth() + 1, 1)
  const nextMonthValue = nextMonth.toLocaleDateString('en-CA')
  await page.getByRole('button', { name: 'Next month', exact: true }).click()
  await page.getByRole('gridcell', { name: nextMonth.toLocaleDateString('en-US',{dateStyle:'full'}), exact: true }).click()
  await expect(page.locator('.workspace-search-filter-chips')).toHaveCount(0)
  await page.getByRole('button',{name:'Apply',exact:true}).hover()
  await expect(page.getByRole('button',{name:'Apply',exact:true})).toHaveCSS('color',/^(?:rgb\(255, 255, 255\)|lch\(100(?:%|[ )]))/)
  await page.getByRole('button',{name:'Apply',exact:true}).click()
  await expect(page.locator('.search-date-filter')).toHaveCount(0)

  const chips = page.locator('.workspace-search-filter-chips')
  await expect(chips).toContainText('Updated date')
  await expect(chips).toContainText('after')
  await expect(chips).toContainText(nextMonthValue)
  await expect(page.locator('a[data-result-index]')).toHaveCount(0)
  await expect.poll(() => searches.length).toBeGreaterThan(0)
  const filter = JSON.parse(new URL(searches.at(-1)!).searchParams.get('filter')!)
  const nextDay=new Date(nextMonth.getFullYear(),nextMonth.getMonth(),2)
  expect(filter).toEqual({ and: [{ field: 'updatedAt', operator: 'gte', values: [nextDay.toISOString()] }] })
  expect(page.url()).toContain('filters=')

  await page.getByRole('button',{name:'Add filter',exact:true}).click()
  await page.getByRole(phone?'option':'menuitem',{name:'Updated date',exact:true}).click()
  await page.getByRole(phone?'option':'menuitem',{name:'Custom date or timeframe…',exact:true}).click()
  await page.getByRole('radio',{name:'on',exact:true}).click()
  for (const mode of ['Month','Quarter','Half-year','Year']) {
    await page.getByRole('radio',{name:mode,exact:true}).click()
    await expect(page.locator(`.search-date-filter [data-period-year="${now.getFullYear()}"]`)).toBeInViewport()
  }
  await page.locator('.search-date-filter').getByRole('button',{name:String(now.getFullYear()),exact:true}).click()
  await page.getByRole('button',{name:'Apply',exact:true}).click()
  await expect(chips.locator('strong').last()).toHaveText(String(now.getFullYear()))
  await expect.poll(()=>{
    const value=new URL(searches.at(-1)!).searchParams.get('filter')
    return value?JSON.parse(value).and.length:0
  }).toBe(2)
  const yearFilter=JSON.parse(new URL(searches.at(-1)!).searchParams.get('filter')!)
  expect(yearFilter.and[1]).toEqual({and:[
    {field:'updatedAt',operator:'gte',values:[new Date(now.getFullYear(),0,1).toISOString()]},
    {field:'updatedAt',operator:'before',values:[new Date(now.getFullYear()+1,0,1).toISOString()]},
  ]})
}

test.describe('workspace search date filter', () => {
  test.describe('desktop', () => {
    test.use({ viewport: { width: 1440, height: 900 } })
    for (const width of [1024, 1280]) for (const theme of ['dark', 'light']) {
      test(`keeps the date panel and precision row inside the viewport at ${width}px in ${theme}`, async ({ page, workspaceKey }, testInfo) => {
        test.skip(testInfo.project.name !== 'chromium', 'Desktop geometry runs once')
        await page.setViewportSize({ width, height: 900 })
        await page.goto(`${base}/${workspaceKey}/search`)
        await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme)
        await page.getByRole('button', { name: 'Add filter', exact: true }).click()
        await page.getByRole('menuitem', { name: 'Updated date', exact: true }).click()
        await page.getByRole('menuitem', { name: 'Custom date or timeframe…', exact: true }).click()
        const panel = page.locator('.search-date-filter')
        await expect(panel).toBeVisible()
        await expect.poll(() => panel.evaluate(element => {
          const box = element.getBoundingClientRect()
          const wrapper = element.closest('.search-date-dialog')!.getBoundingClientRect()
          const buttons = Array.from(element.querySelectorAll('.search-date-modes button'), button => button.getBoundingClientRect())
          return Math.abs(box.width-562)<1 && box.left >= 0 && box.right <= innerWidth && wrapper.left <= box.left && wrapper.right >= box.right
            && buttons.length === 5 && buttons.every(button => Math.abs(button.top - buttons[0].top) < 1 && button.right <= box.right)
        })).toBe(true)
      })
    }
    test('picks a date and applies the filter', async ({ page, request, workspaceKey }, testInfo) => {
      test.skip(testInfo.project.name !== 'chromium', 'Desktop geometry runs once')
      await seedWorkspace(request, workspaceKey)
      await applyDateFilter(page, workspaceKey)
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
    test('picks a date and applies the filter', async ({ page, request, workspaceKey }, testInfo) => {
      test.skip(testInfo.project.name !== 'mobile-chromium', 'Phone geometry runs once')
      await seedWorkspace(request, workspaceKey)
      await applyDateFilter(page, workspaceKey, true)
    })
  })
})
