import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import './styles/foundations.css'
import App from './App.tsx'
import './styles/pill-overrides.css'
import './styles/micro-audit-overrides.css'
import './components/ui/tooltip.css'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import { prepareInitialLocale } from './i18n/locale'
import { initializeTheme } from './lib/theme'
import { installStaleChunkReload } from './lib/stale-chunk-reload'
import { preloadInitialRoute, preloadRoute } from './lib/route-preload'
import { registerServiceWorker } from './lib/service-worker'
import { renderMermaidPreview } from './components/issue/editor/mermaid-preview'
import { ThemedToaster } from './components/ui/themed-toaster'
import { ActionDialogHost } from './components/ui/action-dialogs'
import { DueDateDialogHost } from './components/issue/due-date-dialog'
import { LightboxEditorProvider } from './components/editor/lightbox-editor-provider'
import { TooltipProvider } from './components/ui/tooltip'
import { TitleTooltipBridge } from './components/ui/title-tooltip-bridge'
import './i18n/i18n.css'
import { FlowMotionProvider } from './components/ui/motion'
import './styles/motion.css'
import './styles/light-parity.css'
import './styles/picker-parity.css'
import './styles/dialog-placement.css'
import './styles/mobile-shell.css'
import './components/ui/menu-highlight.css'
import { prepareStartupSnapshot } from './lib/local-snapshot'
import { syncV2Enabled } from './lib/sync-client'
import type { LocalStartup } from './lib/local-store-resume'
import { beginStartup } from './lib/startup'
import { fetchAccountBootstrap, fetchAuthSession, fetchBootstrap } from './lib/api'
import { isProjectListRoute, parseAppRoute } from './lib/app-routes'
import { RouteError } from './components/layout/route-error'

const syncV2 = syncV2Enabled()
const localStoreBuild = import.meta.env.VITE_LOCAL_STORE === 'true' && syncV2
const route = parseAppRoute(window.location.pathname, window.location.search)

// The session, account and remembered-workspace requests start now instead of after the first render (App takes
// them over), and a repeat visit paints from the local workspace snapshot read alongside them.
if (!syncV2) {
  beginStartup({ fetchSession: fetchAuthSession, fetchAccount: fetchAccountBootstrap, fetchBootstrap }, {
    requestedWorkspaceKey: ('workspaceSlug' in route ? route.workspaceSlug : '') ?? '',
    isRoot: route.kind === 'root',
    projection: isProjectListRoute(route) ? 'project-list' : route.kind === 'issue' ? 'issue-detail' : undefined,
  })
}
const startupSnapshotRead = prepareStartupSnapshot(window.location.pathname, { enabled: !syncV2 }).catch(() => undefined)
installStaleChunkReload()
// Fetch the current route's page chunks now, in parallel with the session/bootstrap requests, instead of after the data lands.
preloadInitialRoute()
registerServiceWorker()
initializeTheme()
window.__flowMermaidPreview = renderMermaidPreview

function renderApp(prepared?: LocalStartup) {
  const app = prepared ? <App prepared={{ current: prepared }} /> : <App />
  const router = createBrowserRouter([{ path: '*', element: (
    <LightboxEditorProvider>
      <TooltipProvider>
        {app}
      </TooltipProvider>
      <TitleTooltipBridge />
      <ThemedToaster />
      <ActionDialogHost />
      <DueDateDialogHost />
    </LightboxEditorProvider>
  ), errorElement: <RouteError /> }])
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <FlowMotionProvider><I18nProvider>
        <RouterProvider router={router} />
      </I18nProvider></FlowMotionProvider>
    </StrictMode>,
  )
}

const appReady = Promise.all([prepareInitialLocale(), startupSnapshotRead])
if (localStoreBuild) {
  void import('./local-store-startup').then(({ startLocalStoreStartup }) => startLocalStoreStartup({
    appReady,
    renderApp,
    routePreload: preloadRoute(window.location.pathname + window.location.search).catch(() => undefined),
  })).catch(() => { void appReady.then(() => renderApp()) })
} else {
  // Await the (lazily loaded) Chinese dictionary when the saved locale needs it so the first paint is never English.
  void appReady.then(() => renderApp())
}
