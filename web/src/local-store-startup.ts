import { initializeLocalStore } from './lib/local-store-db'
import { localStoreRoute, preloadLocalWorkspace, prepareLocalStartup, type LocalStartup } from './lib/local-store-resume'
import { beginStartup } from './lib/startup'
import { fetchAccountBootstrap, fetchAuthSession, fetchBootstrap } from './lib/api'
import { isProjectListRoute, parseAppRoute } from './lib/app-routes'

interface LocalStoreStartupOptions {
  appReady: Promise<unknown>
  renderApp: (prepared?: LocalStartup) => void
  routePreload: Promise<unknown>
}

export function startLocalStoreStartup({ appReady, renderApp, routePreload }: LocalStoreStartupOptions) {
  const route = parseAppRoute(window.location.pathname, window.location.search)
  const localIssues = localStoreRoute(route)
  const workspaceKey = 'workspaceSlug' in route ? route.workspaceSlug ?? '' : ''
  const startup = localIssues
    ? beginStartup({ fetchSession: fetchAuthSession, fetchAccount: fetchAccountBootstrap, fetchBootstrap }, {
      requestedWorkspaceKey: workspaceKey,
      isRoot: route.kind === 'root',
      projection: isProjectListRoute(route) ? 'project-list' : route.kind === 'issue' ? 'issue-detail' : undefined,
      skipSpeculative: true,
    })
    : undefined

  initializeLocalStore()
  const localStartup = startup && localIssues && workspaceKey && preloadLocalWorkspace(workspaceKey)
    ? prepareLocalStartup(startup.ready, workspaceKey).catch(() => undefined)
    : undefined

  if (!localStartup) {
    void appReady.then(() => renderApp())
    return
  }

  // A retained local issue workspace can paint directly once its small
  // IndexedDB read is ready. The timeout keeps the normal startup shell in
  // charge when the store or route chunk is slow.
  void Promise.all([
    appReady,
    Promise.race([
      Promise.all([localStartup, routePreload]).then(([prepared]) => prepared),
      new Promise<undefined>(resolve => window.setTimeout(resolve, 100)),
    ]),
  ]).then(([, prepared]) => renderApp(prepared || undefined))
}
