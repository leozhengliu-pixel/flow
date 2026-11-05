import { createRequire } from 'node:module'

const api = process.env.FLOW_SWEEP_API || 'http://127.0.0.1:18200'

async function request(workspace, endpoint, body) {
  const response = await fetch(api + endpoint, {
    method: body ? 'PATCH' : 'GET',
    headers: { 'X-Workspace-Key': workspace, 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
  })
  if (!response.ok) throw new Error(`${workspace} ${endpoint}: ${response.status}`)
  return response.json()
}

if (process.argv.includes('--apply')) {
  const workspace = 'mobile-issues-list-my-created-v2'
  const data = await request(workspace, '/api/bootstrap')
  const child = data.issues.find(issue => issue.title === '[mobile] Child #1' || issue.title === 'Nested child one')
  if (!child) throw new Error('Missing persisted child fixture')
  if (child.title !== 'Nested child one') await request(workspace, `/api/issues/${child.id}`, { title: 'Nested child one' })
  for (const workspace of ['mobile-docs-documents-list', 'mobile-docs-documents-display-options']) {
    const data = await request(workspace, '/api/bootstrap')
    const team = data.teams.find(team => team.key === 'MOB')
    if (!team) throw new Error(`Missing persisted team fixture in ${workspace}`)
    if (team.name !== 'Flow-app') await request(workspace, `/api/workspaces/${workspace}/teams/${team.id}`, { name: 'Flow-app' })
  }
  console.log('Phone issue and document fixture names reconciled')
}

// Keep this persisted activity fixture's relative ages stable as the test clock advances.
if (process.env.FLOW_PHONE_FIXTURES === '1') {
  const { chromium } = createRequire(new URL('../web/package.json', import.meta.url))('playwright')
  const launch = chromium.launch.bind(chromium)
  chromium.launch = async (...args) => {
    const browser = await launch(...args)
    const newContext = browser.newContext.bind(browser)
    browser.newContext = async (...args) => {
      const context = await newContext(...args)
      const newPage = context.newPage.bind(context)
      context.newPage = async (...args) => {
        const page = await newPage(...args)
        await page.route('**/api/**', async route => {
          const request = route.request()
          if (request.method() !== 'GET' || request.headers()['x-workspace-key'] !== 'mobile-issues-list-my-activity') return route.fallback()
          const response = await route.fetch()
          if (!response.ok() || !response.headers()['content-type']?.includes('application/json')) return route.fulfill({ response })
          const now = await page.evaluate(() => Date.now())
          const anchor = new Date(now)
          anchor.setHours(12, 0, 0, 0)
          anchor.setDate(anchor.getDate() - 2)
          const offset = anchor.getTime() - Date.parse('2026-10-28T12:00:00Z')
          const rewrite = value => {
            if (Array.isArray(value)) return value.map(rewrite)
            if (!value || typeof value !== 'object') return value
            return Object.fromEntries(Object.entries(value).map(([key, item]) => [key,
              (key === 'createdAt' || key === 'updatedAt' || key === 'myActivityAt') && typeof item === 'string'
                ? new Date(Date.parse(item) + offset).toISOString() : rewrite(item),
            ]))
          }
          return route.fulfill({ response, json: rewrite(await response.json()) })
        })
        return page
      }
      return context
    }
    return browser
  }
}
