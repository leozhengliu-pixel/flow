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
import { initializeTheme } from './lib/theme'
import { renderMermaidPreview } from './components/issue/editor/mermaid-preview'
import { ThemedToaster } from './components/ui/themed-toaster'
import { ActionDialogHost } from './components/ui/action-dialogs'
import { LightboxEditorProvider } from './components/editor/lightbox-editor-provider'
import { TooltipProvider } from './components/ui/tooltip'
import './i18n/i18n.css'
import { FlowMotionProvider } from './components/ui/motion'
import './styles/motion.css'

initializeTheme()
window.__flowMermaidPreview = renderMermaidPreview

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <FlowMotionProvider><I18nProvider>
      <BrowserRouter>
        <LightboxEditorProvider>
          <TooltipProvider delayDuration={450} skipDelayDuration={300}>
            <App />
          </TooltipProvider>
          <ThemedToaster />
          <ActionDialogHost />
        </LightboxEditorProvider>
      </BrowserRouter>
    </I18nProvider></FlowMotionProvider>
  </StrictMode>,
)
