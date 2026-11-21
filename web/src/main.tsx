import { initializeTheme } from './lib/theme'
import { installStaleChunkReload } from './lib/stale-chunk-reload'
import { registerServiceWorker } from './lib/service-worker'
import { prepareStartupSnapshot } from './lib/local-snapshot'
import { initializeLocalStore } from './lib/local-store-db'
import { localStoreRoute, preloadLocalWorkspace, prepareLocalStartup } from './lib/local-store-resume'
import { beginStartup } from './lib/startup'
import { fetchAccountBootstrap, fetchAuthSession } from './lib/api-startup'
import type { fetchBootstrap } from './lib/api'
import { isProjectListRoute, parseAppRoute } from './lib/app-routes'

// Start authorization and the retained-store read while the UI modules load.
const syncV2 = import.meta.env.VITE_SYNC_V2 === 'true'
const route = parseAppRoute(window.location.pathname, window.location.search)
const localIssues = localStoreRoute(route)
const workspaceKey = 'workspaceSlug' in route ? route.workspaceSlug ?? '' : ''
const startup = !syncV2 || localIssues
  ? beginStartup({
    fetchSession: fetchAuthSession, fetchAccount: fetchAccountBootstrap,
    fetchBootstrap: (...args: Parameters<typeof fetchBootstrap>) => import('./lib/api').then(api => api.fetchBootstrap(...args)),
  }, {
    requestedWorkspaceKey: workspaceKey,
    isRoot: route.kind === 'root',
    projection: isProjectListRoute(route) ? 'project-list' : route.kind === 'issue' ? 'issue-detail' : undefined,
    skipSpeculative: localIssues || undefined,
  }) : undefined
const startupSnapshotRead = prepareStartupSnapshot(window.location.pathname, { enabled: !syncV2 }).catch(() => undefined)
initializeLocalStore()
const localStartup = startup && localIssues && workspaceKey && preloadLocalWorkspace(workspaceKey)
  ? prepareLocalStartup(startup.ready, workspaceKey).catch(() => undefined) : undefined
installStaleChunkReload()
registerServiceWorker()
initializeTheme()

const routePreload = import('./lib/route-preload').then(({ preloadRoute }) =>
  preloadRoute(window.location.pathname + window.location.search)).catch(() => undefined)
void import('./app-entry').then(async ({ mountApp }) => {
  // If authorized data is ready alongside the route, start React directly on
  // that view. A slow session or route still gets the normal startup UI.
  let timer: number | undefined
  try {
    const prepared = localStartup && await Promise.race([
      Promise.all([localStartup, routePreload]).then(([resume]) => resume),
      new Promise<undefined>(resolve => { timer = window.setTimeout(resolve, 100) }),
    ])
    await mountApp(startupSnapshotRead, prepared || undefined)
  } finally { clearTimeout(timer) }
})
