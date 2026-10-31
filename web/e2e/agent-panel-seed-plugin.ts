import { readFileSync } from 'node:fs'
import type { Plugin } from 'vite'

type PanelFixture = {
  review: { id: string; issueIds: string[] }
  session: { id: string; issueId?: string }
  task: { id: string; issueId: string; teamId: string }
  activities: unknown[]
}

type SeedBootstrap = {
  issues: { id: string; title: string; team: { id: string } }[]
  reviews: { id: string }[]
  workspaceSettings: { featureFlags: Record<string, boolean> }
}

/** Opt-in local browser fixture; no runtime, database or other workspace is changed. */
export function agentPanelSeedPlugin(apiTarget: string, workspace = process.env.FLOW_E2E_AGENT_PANEL_WORKSPACE ?? 'mobile-agent-agent-panel'): Plugin {
  const fixture = JSON.parse(readFileSync(new URL('./fixtures/mobile-agent-panel.json', import.meta.url), 'utf8')) as PanelFixture
  const load = async () => {
    const response = await fetch(`${apiTarget}/api/bootstrap`, { headers: { 'X-Workspace-Key': workspace } })
    if (!response.ok) throw new Error(`Agent panel fixture workspace: ${response.status}`)
    const bootstrap = await response.json() as SeedBootstrap
    const issue = bootstrap.issues.find(item => item.title === '[mobile] Agent panel fixture issue')
    if (!issue) throw new Error('Create [mobile] Agent panel fixture issue in the seed workspace first')
    const review = { ...fixture.review, issueIds: [issue.id] }
    const session = { ...fixture.session, issueId: issue.id }
    const task = { ...fixture.task, issueId: issue.id, teamId: issue.team.id }
    bootstrap.reviews = [...bootstrap.reviews.filter(item => item.id !== review.id), review]
    bootstrap.workspaceSettings.featureFlags['coding-sessions'] = true
    return { bootstrap, session, task }
  }
  return {
    name: 'flow-mobile-agent-panel-dev-seed',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        if (req.method !== 'GET' || req.headers['x-workspace-key'] !== workspace || !req.url?.startsWith('/api/')) return next()
        const pathname = new URL(req.url, 'http://flow.local').pathname
        if (!['/api/bootstrap', '/api/coding/status', `/api/coding/sessions/${fixture.session.id}`, `/api/agent-tasks/${fixture.task.id}`].includes(pathname)) return next()
        try {
          const { bootstrap, session, task } = await load()
          const body = pathname === '/api/bootstrap' ? bootstrap
            : pathname === '/api/coding/status' ? { enabled: true, provider: 'fake', regions: [], sizes: {} }
            : pathname === `/api/coding/sessions/${session.id}` ? session
            : { session: task, activities: fixture.activities }
          res.setHeader('Content-Type', 'application/json')
          res.end(JSON.stringify(body))
        } catch (error) { next(error as Error) }
      })
    },
  }
}
