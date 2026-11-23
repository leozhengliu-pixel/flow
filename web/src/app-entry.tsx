import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import './styles/foundations.css'
import './styles/pill-overrides.css'
import './styles/micro-audit-overrides.css'
import './components/ui/tooltip.css'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import App from './App'
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

export async function mountApp(startupSnapshotRead: Promise<unknown>, prepared?: LocalStartup) {
  window.__flowMermaidPreview = renderMermaidPreview
  await Promise.all([prepareInitialLocale(), startupSnapshotRead])
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
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <FlowMotionProvider><I18nProvider>
        <RouterProvider router={router} />
      </I18nProvider></FlowMotionProvider>
    </StrictMode>,
  )
}
