import { initializeLocalStore } from './lib/local-store-db'
import { localStoreRoute, preloadLocalWorkspace, prepareLocalStartup, type LocalStartup } from './lib/local-store-resume'
import { beginStartup } from './lib/startup'
import type { fetchBootstrap } from './lib/api'
import { fetchAccountBootstrap, fetchAuthSession } from './lib/api-startup'
import { isProjectListRoute, parseAppRoute } from './lib/app-routes'

interface LocalStoreStartupOptions {
  appReady: Promise<unknown>
  renderApp: (prepared?: LocalStartup) => void | Promise<void>
  routePreload: Promise<unknown>
}

export function startLocalStoreStartup({ appReady, renderApp, routePreload }: LocalStoreStartupOptions) {
  const route = parseAppRoute(window.location.pathname, window.location.search)
  const localIssues = localStoreRoute(route)
  const workspaceKey = 'workspaceSlug' in route ? route.workspaceSlug ?? '' : ''
  const startup = localIssues || import.meta.env.VITE_SYNC_V2 !== 'true'
    ? beginStartup({
      fetchSession: fetchAuthSession,
      fetchAccount: fetchAccountBootstrap,
      fetchBootstrap: (...args: Parameters<typeof fetchBootstrap>) => import('./lib/api').then(api => api.fetchBootstrap(...args)),
    }, {
      requestedWorkspaceKey: workspaceKey,
      isRoot: route.kind === 'root',
      projection: isProjectListRoute(route) ? 'project-list' : route.kind === 'issue' ? 'issue-detail' : undefined,
      skipSpeculative: localIssues || undefined,
    })
    : undefined

  initializeLocalStore()
  const localStartup = startup && localIssues && workspaceKey && preloadLocalWorkspace(workspaceKey)
    ? prepareLocalStartup(startup.ready, workspaceKey).catch(() => undefined)
    : undefined

  // A retained local issue workspace can paint directly once its small
  // IndexedDB read is ready. The timeout keeps the normal startup shell in
  // charge when the store or route chunk is slow.
  return appReady.then(async () => {
    if (!localStartup) return renderApp()
    let timer: number | undefined
    try {
      return renderApp(await Promise.race([
        Promise.all([localStartup, routePreload]).then(([prepared]) => prepared || undefined),
        new Promise<undefined>(resolve => { timer = window.setTimeout(resolve, 100) }),
      ]))
    } finally {
      window.clearTimeout(timer)
    }
  })
}
