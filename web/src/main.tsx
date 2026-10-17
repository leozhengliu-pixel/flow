import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import './styles/foundations.css'
import App from './App.tsx'
import './styles/pill-overrides.css'
import './styles/micro-audit-overrides.css'
import './components/ui/tooltip.css'
import { BrowserRouter } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import { prepareInitialLocale } from './i18n/locale'
import { initializeTheme } from './lib/theme'
import { installStaleChunkReload } from './lib/stale-chunk-reload'
import { preloadInitialRoute } from './lib/route-preload'
import { renderMermaidPreview } from './components/issue/editor/mermaid-preview'
import { ThemedToaster } from './components/ui/themed-toaster'
import { ActionDialogHost } from './components/ui/action-dialogs'
import { LightboxEditorProvider } from './components/editor/lightbox-editor-provider'
import { TooltipProvider } from './components/ui/tooltip'
import { TitleTooltipBridge } from './components/ui/title-tooltip-bridge'
import './i18n/i18n.css'
import { FlowMotionProvider } from './components/ui/motion'
import './styles/motion.css'
import './styles/light-parity.css'
import './styles/picker-parity.css'

installStaleChunkReload()
// Fetch the current route's page chunks now, in parallel with the session/bootstrap requests, instead of after the data lands.
preloadInitialRoute()
initializeTheme()
window.__flowMermaidPreview = renderMermaidPreview

// Await the (lazily loaded) Chinese dictionary when the saved locale needs it so the first paint is never English.
void prepareInitialLocale().then(() => createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <FlowMotionProvider><I18nProvider>
      <BrowserRouter>
        <LightboxEditorProvider>
          <TooltipProvider>
            <App />
          </TooltipProvider>
          <TitleTooltipBridge />
          <ThemedToaster />
          <ActionDialogHost />
        </LightboxEditorProvider>
      </BrowserRouter>
    </I18nProvider></FlowMotionProvider>
  </StrictMode>,
))
