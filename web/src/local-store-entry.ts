import { startLocalStoreStartup } from './local-store-startup'
import { registerServiceWorker } from './lib/service-worker'
import { installStaleChunkReload, reloadForStaleChunk } from './lib/stale-chunk-reload'
import { initializeTheme } from './lib/theme'

installStaleChunkReload()
registerServiceWorker()
initializeTheme()

const appEntry = import('./app-entry')
const appRoot = document.getElementById('root') ? undefined : new Promise<void>(resolve => {
  document.addEventListener('DOMContentLoaded', () => resolve(), { once: true })
})
void startLocalStoreStartup({
  appReady: Promise.all([appEntry, appRoot]),
  renderApp: prepared => appEntry.then(({ mountApp }) => mountApp(prepared)),
  routePreload: import('./lib/route-preload').then(({ preloadRoute }) =>
    preloadRoute(window.location.pathname + window.location.search)).catch(() => undefined),
}).catch(error => {
  if (!reloadForStaleChunk(error)) throw error
})
