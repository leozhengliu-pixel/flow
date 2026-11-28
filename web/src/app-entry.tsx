import { StrictMode } from 'react'
import { flushSync } from 'react-dom'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import './styles/foundations.css'
import App from './App'
import './styles/pill-overrides.css'
import './styles/micro-audit-overrides.css'
import './components/ui/tooltip.css'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import { prepareInitialLocale } from './i18n/locale'
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
import { RouteError } from './components/layout/route-error'
import type { LocalStartup } from './lib/local-store-resume'
import { onBrowserTelemetryStart } from './lib/browser-telemetry'

export async function mountApp(prepared?: LocalStartup) {
  window.__flowMermaidPreview = renderMermaidPreview
  await prepareInitialLocale()
  const initial = prepared?.consume() ? { current: prepared } : undefined
  const router = createBrowserRouter([{ path: '*', element: (
    <LightboxEditorProvider>
      <TooltipProvider><App prepared={initial} /></TooltipProvider>
      <TitleTooltipBridge />
      <ThemedToaster />
      <ActionDialogHost />
      <DueDateDialogHost />
    </LightboxEditorProvider>
  ), errorElement: <RouteError /> }])
  onBrowserTelemetryStart(client => router.subscribe(state => client.navigate(state.location.pathname)))
  const root = createRoot(document.getElementById('root')!)
  const render = () => root.render(
    <StrictMode>
      <FlowMotionProvider><I18nProvider>
        <RouterProvider router={router} />
      </I18nProvider></FlowMotionProvider>
    </StrictMode>,
  )
  // The retained view and its route are already prepared. Commit the initial
  // frame before starting work for the rest of the workspace.
  if (initial) flushSync(render)
  else render()
}
