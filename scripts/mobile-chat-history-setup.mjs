import { createRequire } from 'node:module'
import path from 'node:path'

// Gate-only seed: refresh this fixture's responses, never the shared database.
const args = process.argv
const web = args[args.indexOf('--web') + 1]
if (!args.includes('--web')) throw new Error('Pass --web with the worktree web directory')
const { chromium } = createRequire(path.resolve(web, 'package.json'))('playwright')
const launch = chromium.launch.bind(chromium)
chromium.launch = async (...args) => {
  const browser = await launch(...args)
  const newContext = browser.newContext.bind(browser)
  browser.newContext = async (...args) => {
    const context = await newContext(...args)
    const now = new Date()
    // Monday and Tuesday cannot contain both Yesterday and an earlier This week.
    // Anchor those two gate days to Wednesday without changing the application.
    const weekday = (now.getDay() + 6) % 7
    if (weekday < 2) now.setDate(now.getDate() + 2 - weekday)
    const newPage = context.newPage.bind(context)
    context.newPage = async (...args) => {
      const page = await newPage(...args)
      await page.clock.setFixedTime(now)
      return page
    }
    const sinceMonday = (now.getDay() + 6) % 7
    const ages = [1, Math.min(3, sinceMonday), sinceMonday + 3, sinceMonday + 10, sinceMonday + 10, sinceMonday + 17, sinceMonday + 17, 90]
    await context.route('**/api/**', async route => {
      const request = route.request()
      const pathname = new URL(request.url()).pathname
      if (request.method() !== 'GET' || request.headers()['x-workspace-key'] !== 'mobile-agent-chat-switcher' || !['/api/bootstrap', '/api/agent/sessions'].includes(pathname)) return route.continue()
      const response = await route.fetch()
      if (!response.ok()) return route.fulfill({ response })
      const body = await response.json()
      const sessions = Array.isArray(body) ? body : body.agentSessions
      for (const session of sessions ?? []) {
        const match = /^mobile-chat-switcher-(\d+)$/.exec(session.slugId ?? '')
        if (!match || ages[Number(match[1])] === undefined) continue
        const index = Number(match[1])
        const date = new Date(now)
        date.setDate(date.getDate() - ages[index])
        if (index === 4 || index === 6) date.setMinutes(date.getMinutes() - 1)
        session.createdAt = session.updatedAt = date.toISOString()
      }
      return route.fulfill({ response, json: body })
    })
    return context
  }
  return browser
}
